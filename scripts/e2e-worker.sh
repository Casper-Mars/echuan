#!/usr/bin/env bash
#
# e2e-worker.sh — 一条命令跑完 Worker 全链路验收。
#
# 覆盖：注册 → 派卡 → 租约续期/停止续期 → 报告 → 重启恢复 → 离线判定 → 保留期清理。
#
# 隔离性：
#   - 每次都新建临时目录，二进制、SQLite、workspace、日志、TOML 全部落在里面；
#   - 端口在 20000-49999 内随机挑选，先用 lsof 探测占用，启动失败自动换端口重试；
#   - 启动前清空 ECHUAN_* 环境变量，避免宿主机配置覆盖临时 TOML；
#   - 绝不触碰 ~/.echuan、仓库内的 data/ 或既有进程。
#
# 退出码：0 全部通过；非 0 表示某一步失败（会打印状态码、响应体、临时目录和服务日志尾部）。
#
# 用法：bash scripts/e2e-worker.sh
# 依赖：bash / curl / jq / go / lsof（macOS 自带）

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN=""
TMP_DIR=""
CONFIG=""
LOG=""
PID=""
PORT=""
BASE=""
CLEANED=0
STEP="startup"

# 响应状态，由 http_post / http_get 填充。
HTTP_CODE=""
HTTP_BODY=""

say()  { printf '%s\n' "$*"; }
pass() { printf 'PASS  %s\n' "$*"; }

fail() {
  printf 'FAIL  [%s] %s\n' "$STEP" "$1" >&2
  [[ -n "$HTTP_CODE" ]] && printf '      status: %s\n' "$HTTP_CODE" >&2
  [[ -n "$HTTP_BODY" ]] && printf '      body:   %s\n' "$HTTP_BODY" >&2
  exit 1
}

cleanup() {
  local code="${1:-0}"
  if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  if [[ "$CLEANED" -eq 0 && -n "$TMP_DIR" && -d "$TMP_DIR" ]]; then
    if [[ "$code" -eq 0 ]]; then
      rm -rf "$TMP_DIR"
    else
      printf '\n临时目录已保留，便于排查：%s\n' "$TMP_DIR" >&2
      if [[ -f "$LOG" ]]; then
        printf '\n---- 服务日志（最后 40 行）----\n' >&2
        tail -40 "$LOG" >&2 || true
        printf '%s\n' '--------------------------------' >&2
      fi
    fi
  fi
}
trap 'cleanup $?' EXIT

# --- 0. 依赖检查 ------------------------------------------------------------
for cmd in curl jq go lsof; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    printf 'ERROR: 缺少依赖 %s，请先安装（macOS: brew install %s）\n' "$cmd" "$cmd" >&2
    exit 2
  fi
done

# 环境变量优先级高于 TOML；清掉宿主机上的 ECHUAN_*，保证只用临时配置。
for var in $(env | sed -n 's/^\(ECHUAN_[A-Za-z0-9_]*\)=.*/\1/p'); do
  unset "$var"
done

# --- HTTP 辅助 --------------------------------------------------------------

http_post() {
  local path="$1" body="$2"
  shift 2
  local raw
  if ! raw=$(curl -sS --max-time 15 -X POST "$BASE$path" \
      -H 'Content-Type: application/json' \
      -H 'X-Actor: e2e' \
      "$@" \
      -d "$body" \
      -w $'\n%{http_code}' 2>&1); then
    HTTP_CODE="000"
    HTTP_BODY="$raw"
    return 0
  fi
  HTTP_CODE="${raw##*$'\n'}"
  HTTP_BODY="${raw%$'\n'*}"
}

http_get() {
  local path="$1"
  local raw
  if ! raw=$(curl -sS --max-time 15 "$BASE$path" -H 'X-Actor: e2e' \
      -w $'\n%{http_code}' 2>&1); then
    HTTP_CODE="000"
    HTTP_BODY="$raw"
    return 0
  fi
  HTTP_CODE="${raw##*$'\n'}"
  HTTP_BODY="${raw%$'\n'*}"
}

require_code() {
  local want="$1" label="$2"
  if [[ "$HTTP_CODE" != "$want" ]]; then
    fail "$label：期望 HTTP $want，实际 $HTTP_CODE"
  fi
}

# heartbeat_busy 发送 state=busy 心跳。租约恰好过期时服务端会返回 409
# lease_lost，这属于时序竞态而非功能错误：最多重试 3 次（间隔 2s），仍失败
# 才判定失败。
heartbeat_busy() {
  local label="$1" attempt
  for attempt in 1 2 3; do
    http_post "/v1/workers/heartbeat" \
      "$(jqr -n --arg wid "$WORKER_ID" '{worker_id:$wid,state:"busy"}')"
    if [[ "$HTTP_CODE" == "200" ]]; then
      return 0
    fi
    if [[ "$HTTP_CODE" == "409" && "$HTTP_BODY" == *lease_lost* ]]; then
      say "$label：第 $attempt 次返回 409 lease_lost，2s 后重试"
      sleep 2
      continue
    fi
    fail "$label：期望 HTTP 200，实际 $HTTP_CODE"
  done
  fail "$label：连续 3 次 409 lease_lost，租约已丢失"
}

jqr() { printf '%s' "$HTTP_BODY" | jq -r "$@"; }

# --- 端口与启动 -------------------------------------------------------------

port_in_use() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

write_config() {
  cat >"$CONFIG" <<EOF
# e2e-worker.sh 生成的临时配置（每次运行都是新目录）
listen = "127.0.0.1:$PORT"
data_dir = "$TMP_DIR/data"
workspace_dir = "$TMP_DIR/workspaces"
read_pool_size = 4
lease_ttl = "90s"
max_attempts = 3
poll_timeout = "2s"
review_stale_hours = 72
material_max_bytes = 20971520
worker_ttl = "45s"
heartbeat_interval = "15s"
worker_metadata_max_bytes = 8192
# 保留期必须大于 worker_ttl，否则 worker 在变为 offline 之前就被删掉，
# 第 8 步就无法先断言 offline 再断言清理。46s 让 offline(45s) 先出现，
# 下一轮 30s sweep 一定会清掉它。
worker_retention = "46s"
log_level = "info"
EOF
}

start_server() {
  local attempt
  for attempt in 1 2 3 4 5 6 7 8; do
    PORT=$((20000 + RANDOM % 30000))
    if port_in_use "$PORT"; then
      continue
    fi
    write_config
    BASE="http://127.0.0.1:$PORT"
    "$BIN" -config "$CONFIG" >>"$LOG" 2>&1 &
    PID=$!
    local i
    for i in $(seq 1 100); do
      if ! kill -0 "$PID" 2>/dev/null; then
        break
      fi
      if curl -fsS --max-time 1 "$BASE/readyz" >/dev/null 2>&1; then
        return 0
      fi
      sleep 0.1
    done
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
    PID=""
    say "端口 $PORT 启动失败，换一个端口重试（第 $attempt 次）" >&2
  done
  fail "连续 8 次都无法启动服务（端口冲突或二进制启动失败）"
}

stop_server() {
  [[ -n "$PID" ]] || return 0
  kill "$PID" 2>/dev/null || true
  local i
  for i in $(seq 1 50); do
    kill -0 "$PID" 2>/dev/null || break
    sleep 0.1
  done
  if kill -0 "$PID" 2>/dev/null; then
    kill -9 "$PID" 2>/dev/null || true
  fi
  wait "$PID" 2>/dev/null || true
  PID=""
}

# --- 1. 临时环境 ------------------------------------------------------------

STEP="1/9 临时环境"
say ""
say "== 1/9 临时环境与进程启动 =="
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/echuan-e2e.XXXXXX")"
CONFIG="$TMP_DIR/echuan.toml"
LOG="$TMP_DIR/echuan.log"
mkdir -p "$TMP_DIR/data" "$TMP_DIR/workspaces"
say "临时目录: $TMP_DIR"

BIN="$TMP_DIR/echuan"
( cd "$ROOT" && go build -o "$BIN" ./cmd/echuan ) || fail "go build 失败"
say "二进制: $BIN"

start_server
say "监听: $BASE"
pass "1/9 临时目录、TOML、随机端口与进程就绪（$BASE）"

# --- 2. 造一张 ready 卡 -----------------------------------------------------
# 项目状态机：requirement_draft → requirement_review → spec_draft → spec_review
# → tasks_draft → tasks_review → pending_done；只有批次审批通过后任务才会从
# backlog 变为 ready（可领取）。

STEP="2/9 建产品/项目/ready 卡"
say ""
say "== 2/9 建产品、项目与一张 ready 卡 =="
PRODUCT_ID="E2E$RANDOM"
http_post "/v1/products" "$(jq -nc \
  --arg id "$PRODUCT_ID" \
  '{product_id:$id,name:"E2E 验收产品",git_url:"https://example.com/e2e.git",description:"e2e-worker.sh"}')"
require_code 201 "创建产品"
say "产品: $PRODUCT_ID"

http_post "/v1/projects" "$(jq -nc \
  --arg pid "$PRODUCT_ID" \
  '{product_id:$pid,title:"E2E 验收项目",requirement_md:"# 需求\n\nWorker 全链路验收。\n"}')"
require_code 201 "创建项目"
PROJECT_ID="$(jqr '.project_id')"
PROJECT_VERSION="$(jqr '.version')"
[[ "$PROJECT_ID" == "$PRODUCT_ID-1" ]] || fail "项目 id 应形如 <product_id>-1，实际 $PROJECT_ID"
say "项目: $PROJECT_ID (version=$PROJECT_VERSION)"

http_post "/v1/projects/submit?id=$PROJECT_ID" '{}'
require_code 200 "提交需求评审"
[[ "$(jqr '.status_name')" == "requirement_review" ]] || fail "提交后应为 requirement_review，实际 $(jqr '.status_name')"
PROJECT_VERSION="$(jqr '.version')"

http_post "/v1/projects/decide?id=$PROJECT_ID" '{"decision":"approve"}' \
  -H "If-Match: $PROJECT_VERSION"
require_code 200 "需求评审通过"
[[ "$(jqr '.status_name')" == "spec_draft" ]] || fail "需求通过后应为 spec_draft，实际 $(jqr '.status_name')"
PROJECT_VERSION="$(jqr '.version')"

http_post "/v1/projects/specs?id=$PROJECT_ID" \
  '{"body_md":"# 技术方案\n\n- 心跳注册\n- 派卡与续租\n- 报告\n"}'
require_code 200 "提交规格"
PROJECT_VERSION="$(jqr '.project_version')"

http_post "/v1/projects/submit?id=$PROJECT_ID" '{}'
require_code 200 "提交方案评审"
[[ "$(jqr '.status_name')" == "spec_review" ]] || fail "方案提交后应为 spec_review，实际 $(jqr '.status_name')"
PROJECT_VERSION="$(jqr '.version')"

http_post "/v1/projects/decide?id=$PROJECT_ID" '{"decision":"approve"}' \
  -H "If-Match: $PROJECT_VERSION"
require_code 200 "方案评审通过"
[[ "$(jqr '.status_name')" == "tasks_draft" ]] || fail "方案通过后应为 tasks_draft，实际 $(jqr '.status_name')"
PROJECT_VERSION="$(jqr '.version')"

http_post "/v1/projects/plans?id=$PROJECT_ID" \
  '{"tasks":[{"title":"E2E 执行卡","body_md":"# 执行\n\n由 e2e-worker.sh 创建。\n","priority":10}]}'
require_code 201 "批量添加任务卡"
TASK_ID="$(jqr '.tasks[0].id')"
[[ -n "$TASK_ID" && "$TASK_ID" != "null" ]] || fail "批量响应缺少任务 id"
PROJECT_VERSION="$(jqr '.project_version')"
[[ "$(jqr '.status_name')" == "tasks_review" ]] || fail "添加第一张卡后应为 tasks_review，实际 $(jqr '.status_name')"

http_post "/v1/tasks/review?project_id=$PROJECT_ID" '{"decision":"approve"}'
require_code 200 "任务批次审批通过"
[[ "$(jqr '.status_name')" == "pending_done" ]] || fail "批次通过后应为 pending_done，实际 $(jqr '.status_name')"

http_get "/v1/tasks?id=$TASK_ID"
require_code 200 "读取任务详情"
[[ "$(jqr '.status_name')" == "ready" ]] || fail "任务应为 ready，实际 $(jqr '.status_name')"
[[ "$(jqr '.worker_id')" == "null" ]] || fail "ready 任务不应有 worker_id"
pass "2/9 产品/项目/任务就绪（$TASK_ID 为 ready）"

# --- 3. 首次心跳注册 --------------------------------------------------------
# 故意带 state=busy：注册的同时不派卡，让第 4 步的 run_task 断言落在第二次心跳上。

STEP="3/9 首次心跳注册"
say ""
say "== 3/9 首次心跳（不带 worker_id）=="
http_post "/v1/workers/heartbeat" \
  '{"state":"busy","metadata":{"host":"e2e","os":"macos","agent_version":"0.0.0"}}'
require_code 200 "首次心跳"
WORKER_ID="$(jqr '.worker.worker_id')"
[[ "$WORKER_ID" == w_* ]] || fail "worker_id 应以 w_ 开头，实际 $WORKER_ID"
[[ "$(jqr '.commands|length')" == "0" ]] || fail "state=busy 的注册心跳不应派卡"
[[ "$(jqr '.worker.presence')" == "active" ]] || fail "注册后 presence 应为 active"
[[ "$(jqr '.worker.heartbeat_interval_ms')" == "15000" ]] || fail "heartbeat_interval_ms 应为 15000"
[[ "$(jqr '.worker.lease_ttl_seconds')" == "90" ]] || fail "lease_ttl_seconds 应为 90"
say "worker_id: $WORKER_ID"
pass "3/9 注册成功并拿到 w_ 前缀 id（$WORKER_ID）"

# --- 4. 第二次心跳派发 run_task ---------------------------------------------

STEP="4/9 派发 run_task"
say ""
say "== 4/9 第二次心跳（state=idle）=="
http_post "/v1/workers/heartbeat" "$(jqr -n --arg wid "$WORKER_ID" '{worker_id:$wid,state:"idle"}')"
require_code 200 "第二次心跳"
[[ "$(jqr '.commands|length')" -ge 1 ]] || fail "第二次心跳应返回 run_task 指令"
[[ "$(jqr '.commands[0].kind')" == "run_task" ]] || fail "commands[0].kind 应为 run_task"
DISPATCHED_TASK="$(jqr '.commands[0].task.id')"
[[ "$DISPATCHED_TASK" == "$TASK_ID" ]] || fail "派发的任务应为 $TASK_ID，实际 $DISPATCHED_TASK"
LEASE_ID="$(jqr '.commands[0].lease.lease_id')"
[[ -n "$LEASE_ID" && "$LEASE_ID" != "null" ]] || fail "派发响应缺少 lease_id"
[[ "$(jqr '.worker.current_task_id')" == "$TASK_ID" ]] || fail "worker.current_task_id 应指向 $TASK_ID"
say "派发任务: $DISPATCHED_TASK 租约: $LEASE_ID"
pass "4/9 心跳返回 commands[0].kind=run_task（$TASK_ID）"

# --- 5. 忙/闲心跳对租约的影响 ----------------------------------------------

STEP="5/9 租约续期"
say ""
say "== 5/9 busy 续租、idle 不续租 =="
lease_until() {
  http_get "/v1/tasks?id=$TASK_ID"
  require_code 200 "读取任务租约"
  jqr '.lease.until'
}

L1="$(lease_until)"
[[ -n "$L1" && "$L1" != "null" ]] || fail "in_progress 任务应带 lease.until"
sleep 1.1
heartbeat_busy "busy 心跳 1"
L2="$(lease_until)"
[[ "$L2" > "$L1" ]] || fail "busy 心跳后 lease_until 应递增：$L1 -> $L2"

sleep 1.1
heartbeat_busy "busy 心跳 2"
L3="$(lease_until)"
[[ "$L3" > "$L2" ]] || fail "第二次 busy 心跳后 lease_until 应继续递增：$L2 -> $L3"

sleep 1.1
http_post "/v1/workers/heartbeat" "$(jqr -n --arg wid "$WORKER_ID" '{worker_id:$wid,state:"idle"}')"
require_code 200 "idle 心跳 1"
sleep 1.1
http_post "/v1/workers/heartbeat" "$(jqr -n --arg wid "$WORKER_ID" '{worker_id:$wid,state:"idle"}')"
require_code 200 "idle 心跳 2"
L4="$(lease_until)"
[[ "$L4" == "$L3" ]] || fail "idle 心跳不应续租：期望保持 $L3，实际 $L4"
[[ "$(jqr '.worker.current_task_id')" == "$TASK_ID" ]] || fail "idle 声明不应丢掉已持有的卡"
say "lease_until: $L1 -> $L2 -> $L3（idle 后仍为 $L4）"
pass "5/9 busy 心跳使 lease_until 递增，idle 心跳保持不变"

# --- 6. 提交报告 ------------------------------------------------------------

STEP="6/9 提交报告"
say ""
say "== 6/9 report 转入 pending_acceptance =="
http_post "/v1/tasks/report?id=$TASK_ID" "$(jq -nc \
  --arg lease "$LEASE_ID" \
  '{lease_id:$lease,summary_md:"# 验收报告\n\n- 注册/派卡/续租/报告 全部通过\n"}')"
require_code 200 "提交报告"
[[ "$(jqr '.status_name')" == "pending_acceptance" ]] || fail "报告后任务状态应为 pending_acceptance，实际 $(jqr '.status_name')"
[[ "$(jqr '.worker_id')" == "null" ]] || fail "报告后任务 worker_id 应为 null"
[[ "$(jqr '.lease')" == "null" ]] || fail "报告后任务 lease 应为 null"

http_get "/v1/tasks?id=$TASK_ID"
require_code 200 "复核任务详情"
[[ "$(jqr '.status_name')" == "pending_acceptance" ]] || fail "复核：任务状态应为 pending_acceptance"
[[ "$(jqr '.worker_id')" == "null" ]] || fail "复核：任务 worker_id 应为 null"
pass "6/9 报告提交成功，任务为 pending_acceptance 且 worker_id=null"

# --- 7. 重启进程后注册表仍在 ------------------------------------------------

STEP="7/9 重启恢复"
say ""
say "== 7/9 重启进程，worker 记录与心跳保持可用 =="
stop_server
start_server
say "已重启，监听: $BASE"

http_get "/v1/workers?limit=200"
require_code 200 "重启后列出 worker"
FOUND="$(jqr --arg wid "$WORKER_ID" '[.items[] | select(.worker_id == $wid)] | length')"
[[ "$FOUND" == "1" ]] || fail "重启后 GET /v1/workers 应仍包含 $WORKER_ID"
[[ "$(jqr --arg wid "$WORKER_ID" '.items[] | select(.worker_id == $wid) | .presence')" == "active" ]] \
  || fail "重启后原 worker 应为 active"

http_post "/v1/workers/heartbeat" "$(jqr -n --arg wid "$WORKER_ID" '{worker_id:$wid,state:"idle"}')"
require_code 200 "重启后原 id 心跳"
[[ "$(jqr '.worker.worker_id')" == "$WORKER_ID" ]] || fail "心跳返回的 worker_id 不一致"
[[ "$(jqr '.commands|length')" == "0" ]] || fail "没有 ready 卡时不应再派卡"
[[ "$(jqr '.worker.current_task_id')" == "null" ]] || fail "报告后 worker 指针应被清理"
pass "7/9 重启后 worker 记录仍在，原 id 心跳 200"

# --- 8. 离线判定与保留期清理 ------------------------------------------------
# worker_retention=46s > worker_ttl=45s：先看到 offline，再等下一轮 30s sweep 删除。

STEP="8/9 离线与清理"
say ""
say "== 8/9 停止心跳：先 offline，再被保留期清理 =="
LAST_BEAT=$(date +%s)
OFFLINE_AT=""
while :; do
  http_get "/v1/workers?id=$WORKER_ID"
  if [[ "$HTTP_CODE" == "200" ]]; then
    if [[ "$(jqr '.presence')" == "offline" ]]; then
      OFFLINE_AT=$(date +%s)
      break
    fi
  elif [[ "$HTTP_CODE" == "404" ]]; then
    fail "worker 在判定 offline 之前就被清理了（worker_retention 必须大于 worker_ttl）"
  else
    fail "轮询 worker 详情失败"
  fi
  if [[ $(( $(date +%s) - LAST_BEAT )) -ge 75 ]]; then
    fail "等待 75s 仍未变为 offline"
  fi
  sleep 2
done
ELAPSED=$(( OFFLINE_AT - LAST_BEAT ))
[[ "$ELAPSED" -ge 40 ]] || fail "offline 出现过早（${ELAPSED}s，应约 45s）"
[[ "$ELAPSED" -le 70 ]] || fail "offline 出现过晚（${ELAPSED}s，应约 45s）"
say "最后心跳后 ${ELAPSED}s 变为 offline"

while :; do
  http_get "/v1/workers?id=$WORKER_ID"
  if [[ "$HTTP_CODE" == "404" ]]; then
    break
  fi
  if [[ "$HTTP_CODE" != "200" ]]; then
    fail "等待清理时轮询失败"
  fi
  if [[ $(( $(date +%s) - OFFLINE_AT )) -ge 40 ]]; then
    fail "offline 后 40s 仍未被清理（下一轮 sweep 应删除无卡离线 worker）"
  fi
  sleep 2
done
[[ "$(jqr '.code')" == "not_found" ]] || fail "清理后详情应返回 404 not_found"
PRUNE_ELAPSED=$(( $(date +%s) - OFFLINE_AT ))
pass "8/9 心跳停止约 ${ELAPSED}s 后 offline，再过 ${PRUNE_ELAPSED}s 被清理（404 not_found）"

# --- 9. 收尾 ----------------------------------------------------------------

STEP="9/9 收尾"
say ""
say "== 9/9 停止进程并删除临时目录 =="
stop_server
rm -rf "$TMP_DIR"
CLEANED=1
pass "9/9 进程已停止，临时目录已删除"

say ""
say "全部 9 步通过。"
exit 0
