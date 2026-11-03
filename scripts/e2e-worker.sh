#!/usr/bin/env bash
#
# scripts/e2e-worker.sh — 平台 v2（work item）全链路验收
#
# 覆盖：
#   1  临时目录 / TOML / 随机端口 / 空数据目录一键启动（全新库自动迁移）
#   2  产品 / 项目 / 需求 / 方案 / 批量拆解 / 驳回 + 审批；旧 /v1/tasks 端点 404
#   3  首次心跳注册（kinds 必填，state=busy 不派单）
#   4  第二次心跳 dispatch 派单；单 worker 单租约并发（并发 claim 全部 409 worker_busy）
#   5  租约：busy 心跳续租、显式 lease 续租、持卡 idle 仍续租（契约）
#   6  报告（material_id + lease_id）→ submitted；验收 → accepted，项目自动 completed
#   7  平台重启后 worker 注册表与心跳仍可用
#   8  停止心跳 → offline（worker_ttl）→ 保留期清理（worker_retention）
#   9  收尾
#
# 依赖：bash 3.2+ / go / jq / curl / lsof
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STEP="startup"

TMP_DIR=""
CONFIG=""
LOG=""
PID=""
CLEANED=0
PORT=""
BASE=""

HTTP_CODE=""
HTTP_BODY=""

say()  { printf '%s\n' "$*"; }
pass() { printf 'PASS  %s\n' "$*"; }

fail() {
  printf 'FAIL  [%s] %s\n' "$STEP" "$1" >&2
  [ -n "$HTTP_CODE" ] && printf '      status: %s\n' "$HTTP_CODE" >&2
  [ -n "$HTTP_BODY" ] && printf '      body:   %s\n' "$HTTP_BODY" >&2
  if [ -n "$LOG" ] && [ -f "$LOG" ]; then
    printf '\n---- 服务日志（最后 40 行）----\n' >&2
    tail -40 "$LOG" >&2 || true
  fi
  exit 1
}

cleanup() {
  local code="${1:-0}"
  if [ "${CLEANED:-0}" != "1" ]; then
    stop_server 2>/dev/null || true
    if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ] && [ "$code" -ne 0 ]; then
      printf '\n---- 失败现场：保留临时目录 %s ----\n' "$TMP_DIR" >&2
    fi
    if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ] && [ "$code" -eq 0 ]; then
      rm -rf "$TMP_DIR"
    fi
  fi
}
trap 'cleanup $?' EXIT

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

# heartbeat_busy 发送 state=busy 心跳（kinds 每次覆盖，必须携带）。租约恰好
# 过期时服务端会返回 409 lease_lost，这属于时序竞态而非功能错误：最多重试
# 3 次（间隔 2s），仍失败才判定失败。
heartbeat_busy() {
  local label="$1" attempt
  for attempt in 1 2 3; do
    http_post "/v1/workers/heartbeat" \
      "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,state:"busy",kinds:["requirement","design","breakdown","dev"]}')"
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

# dispatch_next 发一次 idle 心跳并要求平台派卡，结果写入 NEXT_WORK /
# NEXT_KIND / NEXT_LEASE。阶段流水线按优先级（requirement 990 / design 980 /
# breakdown 970 / dev）派发，调用方负责断言派到的是哪一张。
dispatch_next() {
  local label="$1"
  http_post "/v1/workers/heartbeat" \
    "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,state:"idle",kinds:["requirement","design","breakdown","dev"]}')"
  require_code 200 "$label"
  [[ "$(jqr '.commands|length')" -ge 1 ]] || fail "$label：应返回 dispatch 指令"
  [[ "$(jqr '.commands[0].kind')" == "dispatch" ]] || fail "$label：commands[0].kind 应为 dispatch"
  NEXT_WORK="$(jqr '.commands[0].dispatch.work_id')"
  NEXT_KIND="$(jqr '.commands[0].dispatch.kind')"
  NEXT_LEASE="$(jqr '.commands[0].dispatch.lease.lease_id')"
  [[ -n "$NEXT_WORK" && "$NEXT_WORK" != "null" ]] || fail "$label：派发响应缺少 work_id"
  [[ -n "$NEXT_LEASE" && "$NEXT_LEASE" != "null" ]] || fail "$label：派发响应缺少 lease_id"
}

# --- 端口与启动 -------------------------------------------------------------

port_in_use() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

write_config() {
  cat >"$CONFIG" <<EOF
# e2e-worker.sh 生成的临时配置（每次运行都是新目录）
listen = "127.0.0.1:$PORT"
data_dir = "$TMP_DIR/data"
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
say "== 1/9 临时环境、全新库一键启动 =="
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/echuan-e2e.XXXXXX")"
CONFIG="$TMP_DIR/echuan.toml"
LOG="$TMP_DIR/echuan.log"
mkdir -p "$TMP_DIR/data"
say "临时目录: $TMP_DIR"
[[ ! -f "$TMP_DIR/data/state.db" ]] || fail "启动前数据目录不应已有 state.db"

BIN="$TMP_DIR/echuan"
( cd "$ROOT" && go build -o "$BIN" ./cmd/echuan ) || fail "go build 失败"
say "二进制: $BIN"

start_server
say "监听: $BASE"
[[ -f "$TMP_DIR/data/state.db" ]] || fail "全新数据目录启动后未生成 state.db（迁移未执行）"
curl -fsS --max-time 2 "$BASE/healthz" >/dev/null 2>&1 || fail "全新库启动后 /healthz 不可用"
pass "1/9 空数据目录一键启动成功（state.db 自动创建，/readyz 与 /healthz 正常）"

# --- 2. 阶段工作项自动入队 --------------------------------------------------
# 项目一落地就带一条 queued 的 requirement 阶段项（优先 990），0/2/4 不再
# 是空转等待：谁写需求/方案/拆解，由工作项表达。

STEP="2/9 建产品/项目/阶段项"
say ""
say "== 2/9 建产品、项目与阶段工作项（自动入队）=="
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
REQ_MATERIAL_ID="$(jqr '.requirement.material_id')"
[[ "$PROJECT_ID" == "$PRODUCT_ID-1" ]] || fail "项目 id 应形如 <product_id>-1，实际 $PROJECT_ID"
[[ "$(jqr '.status_name')" == "requirement_draft" ]] || fail "新建项目应为 requirement_draft，实际 $(jqr '.status_name')"
[[ -n "$REQ_MATERIAL_ID" && "$REQ_MATERIAL_ID" != "null" ]] || fail "创建响应缺少 requirement.material_id"
say "项目: $PROJECT_ID (version=$PROJECT_VERSION) 需求物料: $REQ_MATERIAL_ID"

# 验收 1：状态 0，且恰有一条 kind=requirement 的 queued 活跃项。
http_get "/v1/work-items?project_id=$PROJECT_ID&kind=requirement"
require_code 200 "列出需求阶段项"
[[ "$(jqr '.items|length')" == "1" ]] || fail "requirement 阶段项应恰有 1 条，实际 $(jqr '.items|length')"
WORK_ID="$(jqr '.items[0].work_id')"
[[ "$(jqr '.items[0].state')" == "queued" ]] || fail "需求阶段项应为 queued，实际 $(jqr '.items[0].state')"
[[ "$(jqr '.items[0].priority')" == "990" ]] || fail "需求阶段项 priority 应为 990，实际 $(jqr '.items[0].priority')"
[[ "$(jqr '.items[0].title')" == "需求撰写：E2E 验收项目" ]] || fail "需求阶段项标题不符：$(jqr '.items[0].title')"
say "需求阶段项: $WORK_ID (queued, priority 990)"

# v2 只保留 /v1/work-items；旧 /v1/tasks 必须已经不存在。
http_get "/v1/tasks"
require_code 404 "旧 /v1/tasks 端点应 404"
http_post "/v1/tasks/review?project_id=$PROJECT_ID" '{"decision":"approve"}'
require_code 404 "旧 /v1/tasks/review 端点应 404"
http_post "/v1/tasks/report?id=x" '{}'
require_code 404 "旧 /v1/tasks/report 端点应 404"
pass "2/9 项目为 requirement_draft，自动带一条 queued 需求阶段项（$WORK_ID）；旧 /v1/tasks 全 404"

# --- 3. 首次心跳注册 --------------------------------------------------------
# 故意带 state=busy：注册的同时不派卡，让第 4 步的派单断言落在第二次心跳上。

STEP="3/9 首次心跳注册"
say ""
say "== 3/9 首次心跳（不带 worker_id，kinds 必填）=="
http_post "/v1/workers/heartbeat" \
  '{"state":"busy","kinds":["requirement","design","breakdown","dev"],"metadata":{"host":"e2e","os":"macos","agent_version":"0.0.0"}}'
require_code 200 "首次心跳"
WORKER_ID="$(jqr '.worker.worker_id')"
[[ "$WORKER_ID" == w_* ]] || fail "worker_id 应以 w_ 开头，实际 $WORKER_ID"
[[ "$(jqr '.commands|length')" == "0" ]] || fail "state=busy 的注册心跳不应派卡"
[[ "$(jqr '.worker.presence')" == "active" ]] || fail "注册后 presence 应为 active"
[[ "$(jqr '.worker.state')" == "busy" ]] || fail "worker.state 应为 busy"
[[ "$(jqr '.worker.heartbeat_interval_ms')" == "15000" ]] || fail "heartbeat_interval_ms 应为 15000"
[[ "$(jqr '.worker.lease_ttl_seconds')" == "90" ]] || fail "lease_ttl_seconds 应为 90"
[[ "$(jqr '.worker.current_work_item_id')" == "null" ]] || fail "注册时不应持有卡"
say "worker_id: $WORKER_ID"
pass "3/9 注册成功并拿到 w_ 前缀 id（$WORKER_ID）"

# --- 4. 第二次心跳派发 dispatch ---------------------------------------------

STEP="4/9 派发 dispatch"
say ""
say "== 4/9 第二次心跳（state=idle）→ dispatch 需求阶段项 =="
dispatch_next "第二次心跳"
[[ "$NEXT_WORK" == "$WORK_ID" ]] || fail "应派发需求阶段项 $WORK_ID，实际 $NEXT_WORK"
[[ "$NEXT_KIND" == "requirement" ]] || fail "dispatch.kind 应为 requirement，实际 $NEXT_KIND"
[[ "$(jqr '.commands[0].dispatch.protocol')" == "v2" ]] || fail "dispatch.protocol 应为 v2"
[[ "$(jqr '.commands[0].dispatch.lease.ttl_seconds')" == "90" ]] || fail "dispatch lease ttl_seconds 应为 90"
[[ "$(jqr '.worker.current_work_item_id')" == "$WORK_ID" ]] || fail "worker.current_work_item_id 应指向 $WORK_ID"
# 需求阶段还没有方案：spec_version 为 0 是预期值（dev 派发时再断言 ≥1）。
[[ "$(jqr '.commands[0].dispatch.context.spec_version')" == "0" ]] \
  || fail "需求阶段 spec_version 应为 0，实际 $(jqr '.commands[0].dispatch.context.spec_version')"
DISPATCHED="$NEXT_WORK"
LEASE_ID="$NEXT_LEASE"
say "派发工作项: $DISPATCHED 租约: $LEASE_ID"

# 单 worker 单租约并发：同一 worker 同时发两个 claim，必须都 409 worker_busy，
# 不能出现第二张卡或第二个租约。
CLAIM_A="$TMP_DIR/claim-a.json"
CLAIM_B="$TMP_DIR/claim-b.json"
( curl -sS --max-time 15 -X POST "$BASE/v1/work-items/claim" \
    -H 'Content-Type: application/json' -H 'X-Actor: e2e' \
    -d "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid}')" \
    -w '\n%{http_code}' >"$CLAIM_A" 2>&1 ) &
CA=$!
( curl -sS --max-time 15 -X POST "$BASE/v1/work-items/claim" \
    -H 'Content-Type: application/json' -H 'X-Actor: e2e' \
    -d "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid}')" \
    -w '\n%{http_code}' >"$CLAIM_B" 2>&1 ) &
CB=$!
wait "$CA" "$CB"
CODE_A="$(tail -1 "$CLAIM_A")"
CODE_B="$(tail -1 "$CLAIM_B")"
[[ "$CODE_A" == "409" && "$CODE_B" == "409" ]] || fail "并发 claim 应双 409 worker_busy，实际 $CODE_A / $CODE_B"
grep -q 'worker_busy' "$CLAIM_A" || fail "并发 claim A 未返回 worker_busy"
grep -q 'worker_busy' "$CLAIM_B" || fail "并发 claim B 未返回 worker_busy"
http_get "/v1/work-items?id=$WORK_ID"
require_code 200 "并发 claim 后复核工作项"
[[ "$(jqr '.state')" == "leased" ]] || fail "并发 claim 后工作项应仍为 leased，实际 $(jqr '.state')"
[[ "$(jqr '.lease.lease_id')" == "$LEASE_ID" ]] || fail "并发 claim 不应更换租约"
say "并发 claim：409 + 409（worker_busy），租约未变"
pass "4/9 心跳返回 dispatch（$WORK_ID，requirement），单 worker 单租约并发守卫生效"

# --- 5. 租约续期 ------------------------------------------------------------

STEP="5/9 租约续期"
say ""
say "== 5/9 busy 心跳续租、显式 lease 续租、持卡 idle 仍续租（契约） =="
lease_until() {
  http_get "/v1/work-items?id=$WORK_ID"
  require_code 200 "读取工作项租约"
  jqr '.lease.until'
}

L1="$(lease_until)"
[[ -n "$L1" && "$L1" != "null" ]] || fail "leased 工作项应带 lease.until"
sleep 1.1
heartbeat_busy "busy 心跳 1"
L2="$(lease_until)"
[[ "$L2" > "$L1" ]] || fail "busy 心跳后 lease_until 应递增：$L1 -> $L2"

sleep 1.1
http_post "/v1/work-items/lease" \
  "$(jq -nc --arg wid "$WORK_ID" --arg lid "$LEASE_ID" '{work_id:$wid,lease_id:$lid}')"
require_code 200 "显式续租"
RENEWED_UNTIL="$(jqr '.until')"
NEW_LEASE="$(jqr '.lease_id')"
[[ "$NEW_LEASE" == "$LEASE_ID" ]] || fail "显式续租应保持 lease_id 不变"
[[ "$RENEWED_UNTIL" > "$L2" ]] || fail "显式续租后 until 应递增：$L2 -> $RENEWED_UNTIL"
L3="$(lease_until)"
[[ "$L3" == "$RENEWED_UNTIL" ]] || fail "显式续租响应与详情租约不一致：$RENEWED_UNTIL / $L3"

# 契约：占用只看指针，与 state 无关；持卡发 idle 也必须续租。
# lease_until 是固定宽度的毫秒 UTC 时间戳（.000Z），用「首轮 idle 心跳前的
# now + TTL - 2s」做硬断言，2s 余量用于吸收秒级取整与请求耗时。
LEASE_TTL_SECONDS=90  # 与 write_config 的 lease_ttl 保持一致
lease_epoch() {
  # .000Z 截到秒后交给 jq 的 fromdateiso8601，避免依赖 GNU/BSD date 差异。
  jq -nr --arg ts "${1%%.*}Z" '$ts | fromdateiso8601'
}

IDLE_T0="$(date -u +%s)"
sleep 1.1
http_post "/v1/workers/heartbeat" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,state:"idle",kinds:["requirement","design","breakdown","dev"]}')"
require_code 200 "idle 心跳 1"
sleep 1.1
http_post "/v1/workers/heartbeat" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,state:"idle",kinds:["requirement","design","breakdown","dev"]}')"
require_code 200 "idle 心跳 2"
L4="$(lease_until)"
[[ "$(jqr '.worker.current_work_item_id')" == "$WORK_ID" ]] || fail "idle 声明不应丢掉已持有的卡"
[[ "$L4" > "$L3" ]] || fail "持卡 idle 心跳必须续租（lease_until 应递增）：$L3 -> $L4"
L4_EPOCH="$(lease_epoch "$L4")"
IDLE_MIN=$((IDLE_T0 + LEASE_TTL_SECONDS - 2))
[[ "$L4_EPOCH" -ge "$IDLE_MIN" ]] || fail "持卡 idle 心跳未按契约续租：lease_until=$L4（epoch $L4_EPOCH）早于 now+TTL-2s（$IDLE_MIN）"
say "lease_until: $L1 -> $L2 -> $L3（idle 后 $L4，仍续租，契约满足）"
pass "5/9 busy 心跳与显式 lease 均续租，持卡 idle 心跳亦续租（契约）"

# --- 6. 阶段推进 ------------------------------------------------------------

STEP="6/9 阶段推进"
say ""
say "== 6/9 需求报告 → 1 → 审批 2 + 方案项 → … → 拆解 → 6 → completed =="

# 6.1 需求阶段报告：0 → 1（验收 2）。
http_post "/v1/work-items/report" "$(jq -nc \
  --arg wid "$WORK_ID" --arg mid "$REQ_MATERIAL_ID" --arg lease "$LEASE_ID" \
  '{work_id:$wid,material_id:$mid,lease_id:$lease}')"
require_code 200 "提交需求报告"
[[ "$(jqr '.state')" == "submitted" ]] || fail "报告后工作项状态应为 submitted，实际 $(jqr '.state')"
[[ "$(jqr '.worker_id')" == "null" ]] || fail "报告后工作项 worker_id 应为 null"
[[ "$(jqr '.lease')" == "null" ]] || fail "报告后工作项 lease 应为 null"
[[ "$(jqr '.result_material_id')" == "$REQ_MATERIAL_ID" ]] || fail "报告后 result_material_id 应指向提交的物料"

http_get "/v1/projects?id=$PROJECT_ID"
require_code 200 "报告后读取项目"
[[ "$(jqr '.status_name')" == "requirement_review" ]] || fail "需求报告后项目应为 requirement_review，实际 $(jqr '.status_name')"
PROJECT_VERSION="$(jqr '.version')"
REQ_REVIEW_VERSION="$PROJECT_VERSION"

# 6.2 需求评审通过：1 → 2，并自动出现 design 活跃项（验收 3）。
http_post "/v1/projects/decide?id=$PROJECT_ID" '{"decision":"approve"}' \
  -H "If-Match: $PROJECT_VERSION"
require_code 200 "需求评审通过"
[[ "$(jqr '.status_name')" == "spec_draft" ]] || fail "需求通过后应为 spec_draft，实际 $(jqr '.status_name')"
[[ "$(jqr '.material_id')" == "$REQ_MATERIAL_ID" ]] || fail "需求审批响应 material_id 应指向被审需求版本，实际 $(jqr '.material_id')"
PROJECT_VERSION="$(jqr '.version')"

http_get "/v1/work-items?project_id=$PROJECT_ID&kind=design"
require_code 200 "列出方案阶段项"
[[ "$(jqr '.items|length')" == "1" ]] || fail "批准后应恰有 1 条 design 活跃项，实际 $(jqr '.items|length')"
DESIGN_WORK_ID="$(jqr '.items[0].work_id')"
[[ "$(jqr '.items[0].state')" == "queued" ]] || fail "design 阶段项应为 queued，实际 $(jqr '.items[0].state')"
[[ "$(jqr '.items[0].priority')" == "980" ]] || fail "design 阶段项 priority 应为 980，实际 $(jqr '.items[0].priority')"
say "方案阶段项: $DESIGN_WORK_ID (queued, priority 980)"

# 验收 4：旧 version 重复批准 → 409 invalid_version，且不新增活跃阶段项。
http_post "/v1/projects/decide?id=$PROJECT_ID" '{"decision":"approve"}' \
  -H "If-Match: $REQ_REVIEW_VERSION"
require_code 409 "重复批准"
[[ "$HTTP_BODY" == *invalid_version* ]] || fail "重复批准应返回 invalid_version，实际 $HTTP_BODY"
http_get "/v1/work-items?project_id=$PROJECT_ID&kind=design"
require_code 200 "重复批准后复核方案阶段项"
[[ "$(jqr '.items|length')" == "1" ]] || fail "重复批准不应新增 design 阶段项，实际 $(jqr '.items|length')"
say "重复批准：409 invalid_version，design 阶段项仍为 1 条"

# 6.3 方案阶段报告：2 → 3。
dispatch_next "心跳派发方案卡"
[[ "$NEXT_WORK" == "$DESIGN_WORK_ID" ]] || fail "应派发方案阶段项 $DESIGN_WORK_ID，实际 $NEXT_WORK"
[[ "$NEXT_KIND" == "design" ]] || fail "dispatch.kind 应为 design，实际 $NEXT_KIND"
DESIGN_LEASE="$NEXT_LEASE"

http_post "/v1/projects/specs?id=$PROJECT_ID" \
  '{"body_md":"# 技术方案\n\n- 心跳注册\n- 派卡与续租\n- 报告\n"}'
require_code 200 "提交规格"
PROJECT_VERSION="$(jqr '.project_version')"
SPEC_MATERIAL_ID="$(jqr '.spec.material_id')"
[[ -n "$SPEC_MATERIAL_ID" && "$SPEC_MATERIAL_ID" != "null" ]] || fail "方案响应缺少 material_id"
say "方案物料: $SPEC_MATERIAL_ID"

http_post "/v1/work-items/report" "$(jq -nc \
  --arg wid "$DESIGN_WORK_ID" --arg mid "$SPEC_MATERIAL_ID" --arg lease "$DESIGN_LEASE" \
  '{work_id:$wid,material_id:$mid,lease_id:$lease}')"
require_code 200 "提交方案报告"
[[ "$(jqr '.state')" == "submitted" ]] || fail "方案报告后应为 submitted，实际 $(jqr '.state')"
http_get "/v1/projects?id=$PROJECT_ID"
require_code 200 "方案报告后读取项目"
[[ "$(jqr '.status_name')" == "spec_review" ]] || fail "方案报告后项目应为 spec_review，实际 $(jqr '.status_name')"
PROJECT_VERSION="$(jqr '.version')"

# 6.4 方案评审通过：3 → 4，并自动出现 breakdown 活跃项。
http_post "/v1/projects/decide?id=$PROJECT_ID" '{"decision":"approve"}' \
  -H "If-Match: $PROJECT_VERSION"
require_code 200 "方案评审通过"
[[ "$(jqr '.status_name')" == "tasks_draft" ]] || fail "方案通过后应为 tasks_draft，实际 $(jqr '.status_name')"
[[ "$(jqr '.material_id')" == "$SPEC_MATERIAL_ID" ]] || fail "方案审批响应 material_id 应指向被审方案版本，实际 $(jqr '.material_id')"
PROJECT_VERSION="$(jqr '.version')"

http_get "/v1/work-items?project_id=$PROJECT_ID&kind=breakdown"
require_code 200 "列出拆解阶段项"
[[ "$(jqr '.items|length')" == "1" ]] || fail "批准后应恰有 1 条 breakdown 活跃项，实际 $(jqr '.items|length')"
BREAKDOWN_WORK_ID="$(jqr '.items[0].work_id')"
[[ "$(jqr '.items[0].state')" == "queued" ]] || fail "breakdown 阶段项应为 queued，实际 $(jqr '.items[0].state')"
[[ "$(jqr '.items[0].priority')" == "970" ]] || fail "breakdown 阶段项 priority 应为 970，实际 $(jqr '.items[0].priority')"
say "拆解阶段项: $BREAKDOWN_WORK_ID (queued, priority 970)"

# 6.5 拆解阶段报告：4 → 5。
dispatch_next "心跳派发拆解卡"
[[ "$NEXT_WORK" == "$BREAKDOWN_WORK_ID" ]] || fail "应派发拆解阶段项 $BREAKDOWN_WORK_ID，实际 $NEXT_WORK"
[[ "$NEXT_KIND" == "breakdown" ]] || fail "dispatch.kind 应为 breakdown，实际 $NEXT_KIND"
BREAKDOWN_LEASE="$NEXT_LEASE"
# 拆解报告按新契约携带 plan（可省略 material_id）：平台上同一事务生成
# 1 条 blocked dev 子项，项目 4 → 5。
http_post "/v1/work-items/report" "$(jq -nc \
  --arg wid "$BREAKDOWN_WORK_ID" --arg pid "$PROJECT_ID" --arg lease "$BREAKDOWN_LEASE" \
  '{work_id:$wid,lease_id:$lease,plan:{items:[{seq_no:1,project_id:$pid,title:"E2E 拆分卡",body_md:"# 执行\n\n由拆解计划生成。\n",priority:5}]}}')"
require_code 200 "提交拆解报告"
[[ "$(jqr '.state')" == "submitted" ]] || fail "拆解报告后应为 submitted，实际 $(jqr '.state')"
# 平台把 plan 落成 kind=4 的计划物料（task-0010），并回填 result_material_id。
PLAN_MATERIAL_ID="$(jqr '.result_material_id')"
[[ -n "$PLAN_MATERIAL_ID" && "$PLAN_MATERIAL_ID" != "null" ]] || fail "拆解计划应生成 kind=4 计划物料，实际 $(jqr '.result_material_id')"
[[ "$(jqr '.result.kind')" == "4" ]] || fail "拆解计划物料 kind 应为 4，实际 $(jqr '.result.kind')"
http_get "/v1/projects?id=$PROJECT_ID"
require_code 200 "拆解报告后读取项目"
[[ "$(jqr '.status_name')" == "tasks_review" ]] || fail "拆解报告后项目应为 tasks_review，实际 $(jqr '.status_name')"
PROJECT_VERSION="$(jqr '.version')"

# 计划生成的 dev 子项在批准前为 blocked，parent_id 指向拆解卡。
http_get "/v1/work-items?project_id=$PROJECT_ID&kind=dev"
require_code 200 "列出拆解生成的 dev 子项"
[[ "$(jqr '.items|length')" == "1" ]] || fail "拆解计划应生成 1 条 dev，实际 $(jqr '.items|length')"
[[ "$(jqr '.items[0].state')" == "blocked" ]] || fail "批准前 dev 子项应为 blocked，实际 $(jqr '.items[0].state')"
[[ "$(jqr '.items[0].parent_id')" == "$BREAKDOWN_WORK_ID" ]] || fail "dev 子项 parent_id 应指向拆解卡，实际 $(jqr '.items[0].parent_id')"

# 6.6 /v1/projects/plans 手工批量建卡入口保留：dev 卡在 tasks_review 落为
# blocked，与拆解计划生成的子项并存，且同样只能经受计划门批准后 claim。
http_post "/v1/projects/plans?id=$PROJECT_ID" \
  '{"work_items":[{"title":"E2E 执行卡","body_md":"# 执行\n\n由 e2e-worker.sh 创建。\n","priority":10}]}'
require_code 201 "批量添加工作项"
DEV_WORK_ID="$(jqr '.work_items[0].work_id')"
[[ -n "$DEV_WORK_ID" && "$DEV_WORK_ID" != "null" ]] || fail "批量响应缺少 work_id"
[[ "$(jqr '.work_items[0].kind')" == "dev" ]] || fail "计划卡 kind 应为 dev"
[[ "$(jqr '.work_items[0].state')" == "blocked" ]] || fail "计划卡 state 应为 blocked"
PROJECT_VERSION="$(jqr '.project_version')"
[[ "$(jqr '.status_name')" == "tasks_review" ]] || fail "建卡后应仍在 tasks_review，实际 $(jqr '.status_name')"

# 驳回只写 tasks_review_reason/tasks_review_rejected_at，卡与项目状态不变。
http_post "/v1/work-items/review" \
  "$(jq -nc --arg pid "$PROJECT_ID" '{project_id:$pid,decision:"reject",reason:"E2E 先驳回一次"}')"
require_code 200 "批次驳回"
[[ "$(jqr '.status')" == "5" ]] || fail "驳回后项目应仍在 tasks_review(5)，实际 $(jqr '.status')"
http_get "/v1/projects?id=$PROJECT_ID"
require_code 200 "读取驳回字段"
[[ "$(jqr '.tasks_review_reason')" == "E2E 先驳回一次" ]] || fail "驳回原因未落到项目"
[[ "$(jqr '.tasks_review_rejected_at')" != "null" ]] || fail "驳回时间未落到项目"

http_post "/v1/work-items/review" \
  "$(jq -nc --arg pid "$PROJECT_ID" '{project_id:$pid,decision:"approve"}')"
require_code 200 "批次审批通过"
[[ "$(jqr '.status_name')" == "pending_done" ]] || fail "批次通过后应为 pending_done，实际 $(jqr '.status_name')"
[[ "$(jqr '.ready_work_items')" == "2" ]] || fail "批次通过后应有 2 张卡转 queued，实际 $(jqr '.ready_work_items')"
[[ "$(jqr '.reason // ""')" == "" ]] || fail "审批通过后驳回原因应被清空"

# 6.7 开发卡：报告 → submitted，验收 → accepted，项目自动 completed。
dispatch_next "心跳派发开发卡"
[[ "$NEXT_WORK" == "$DEV_WORK_ID" ]] || fail "应派发开发卡 $DEV_WORK_ID，实际 $NEXT_WORK"
[[ "$NEXT_KIND" == "dev" ]] || fail "dispatch.kind 应为 dev，实际 $NEXT_KIND"
[[ "$(jqr '.commands[0].dispatch.context.spec_version')" -ge 1 ]] || fail "dispatch.context.spec_version 应为正数"
DEV_LEASE="$NEXT_LEASE"

http_post "/v1/work-items/report" "$(jq -nc \
  --arg wid "$DEV_WORK_ID" --arg mid "$SPEC_MATERIAL_ID" --arg lease "$DEV_LEASE" \
  '{work_id:$wid,material_id:$mid,lease_id:$lease}')"
require_code 200 "提交开发报告"
[[ "$(jqr '.state')" == "submitted" ]] || fail "报告后工作项状态应为 submitted，实际 $(jqr '.state')"
[[ "$(jqr '.worker_id')" == "null" ]] || fail "报告后工作项 worker_id 应为 null"
[[ "$(jqr '.lease')" == "null" ]] || fail "报告后工作项 lease 应为 null"

http_get "/v1/work-items?id=$DEV_WORK_ID"
require_code 200 "复核工作项详情"
[[ "$(jqr '.result.size')" -gt 0 ]] || fail "复核：结果物料大小应为正数"

http_post "/v1/work-items/decide" \
  "$(jq -nc --arg wid "$DEV_WORK_ID" '{work_id:$wid,decision:"approve"}')"
require_code 200 "验收工作项"
[[ "$(jqr '.state')" == "accepted" ]] || fail "验收后工作项应为 accepted，实际 $(jqr '.state')"
[[ "$(jqr '.material_id')" == "$SPEC_MATERIAL_ID" ]] || fail "验收响应 material_id 应指向被审结果资料，实际 $(jqr '.material_id')"

# 拆解计划生成的 dev 子项同样可被 claim / 报告 / 验收；结清后项目 completed。
dispatch_next "心跳派发拆解生成的 dev 子项"
[[ "$NEXT_KIND" == "dev" ]] || fail "dispatch.kind 应为 dev，实际 $NEXT_KIND"
[[ "$NEXT_WORK" != "$DEV_WORK_ID" ]] || fail "第二张 dev 不应是已验收的 $DEV_WORK_ID"
PLAN_DEV_LEASE="$NEXT_LEASE"
http_post "/v1/work-items/report" "$(jq -nc \
  --arg wid "$NEXT_WORK" --arg mid "$SPEC_MATERIAL_ID" --arg lease "$PLAN_DEV_LEASE" \
  '{work_id:$wid,material_id:$mid,lease_id:$lease}')"
require_code 200 "提交拆解生成的 dev 报告"
http_post "/v1/work-items/decide" \
  "$(jq -nc --arg wid "$NEXT_WORK" '{work_id:$wid,decision:"approve"}')"
require_code 200 "验收拆解生成的 dev"
[[ "$(jqr '.state')" == "accepted" ]] || fail "验收后 dev 应为 accepted，实际 $(jqr '.state')"

http_get "/v1/projects?id=$PROJECT_ID"
require_code 200 "读取项目"
[[ "$(jqr '.status_name')" == "completed" ]] || fail "最后一张卡结清后项目应为 completed，实际 $(jqr '.status_name')"

# 6.8 门禁历史：项目级 requirement/spec/plan + 两条工作项验收，共 6 条；
# 版本绑定不随资料升级改变，计划批次绑定 kind=4 计划物料。
http_get "/v1/gate-decisions?project_id=$PROJECT_ID&limit=100"
require_code 200 "查询门禁历史"
[[ "$(jqr '.items|length')" == "6" ]] || fail "门禁历史应恰有 6 条，实际 $(jqr '.items|length')"
[[ "$(jqr '.items[0].subject_type')" == "work_item" ]] || fail "门禁历史应按 id 倒序，首条应为工作项，实际 $(jqr '.items[0].subject_type')"
[[ "$(jqr '[.items[] | select(.gate=="requirement")] | length')" == "1" ]] || fail "需求门禁应恰有 1 条"
[[ "$(jqr '[.items[] | select(.gate=="spec")] | length')" == "1" ]] || fail "方案门禁应恰有 1 条"
[[ "$(jqr '[.items[] | select(.gate=="plan")] | length')" == "2" ]] || fail "计划批次应恰有 2 条（驳回 + 通过）"
[[ "$(jqr '[.items[] | select(.gate=="plan") | .material_id] | all(. == "'"$PLAN_MATERIAL_ID"'")')" == "true" ]] || fail "计划审批 material_id 应指向 kind=4 计划物料"
[[ "$(jqr '[.items[] | select(.gate=="dev")] | length')" == "2" ]] || fail "工作项验收应恰有 2 条"
say "门禁历史: 6 条（requirement/spec/plan×2/dev×2），版本绑定正确"

# 阶段卡在门禁批准时已结清：requirement/design accepted，breakdown accepted，
# 不会把项目卡在 pending_done。
for kind in requirement design breakdown; do
  http_get "/v1/work-items?project_id=$PROJECT_ID&kind=$kind"
  require_code 200 "复核 $kind 阶段项"
  [[ "$(jqr '.items|length')" == "1" ]] || fail "$kind 阶段项历史应恰有 1 条，实际 $(jqr '.items|length')"
  [[ "$(jqr '.items[0].state')" == "accepted" ]] || fail "$kind 阶段项应已 accepted，实际 $(jqr '.items[0].state')"
done
pass "6/9 三阶段卡驱动 0→1→2→3→4→5，门禁结清阶段项；开发卡验收后项目 completed"

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

http_post "/v1/workers/heartbeat" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,state:"idle",kinds:["requirement","design","breakdown","dev"]}')"
require_code 200 "重启后原 id 心跳"
[[ "$(jqr '.worker.worker_id')" == "$WORKER_ID" ]] || fail "心跳返回的 worker_id 不一致"
[[ "$(jqr '.commands|length')" == "0" ]] || fail "没有 queued 卡时不应再派卡"
[[ "$(jqr '.worker.current_work_item_id')" == "null" ]] || fail "报告后 worker 指针应被清理"
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
