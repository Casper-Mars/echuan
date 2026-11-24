#!/usr/bin/env bash
#
# scripts/e2e-worker.sh — 平台 V3（tasks + worker 注册表）全链路验收
#
# 覆盖（契约见 api/openapi.yaml 与 docs/worker-ops.md）：
#   1  临时目录 / TOML / 随机端口 / 空数据目录一键启动（全新库自动迁移）
#   2  产品 / 项目容器语义（建项不建卡）；未注册的 /v1 路径统一 404
#   3  Worker 注册（worker_id 由客户端生成，心跳 204 空体）与注册表读取
#   4  建卡 → claim 派单信封（task / project / product / inputs / upstream）
#   5  租约显式续期、错误 lease 409 lease lost、心跳不归还占用
#   6  报告 → 驳回 → 重新领取（attempt 递增）→ 验收 → 派生下一阶段卡（依赖）
#   7  拆解计划落成 dev 卡 → 派发 → 报告 → 验收；项目关闭 / 重开
#   8  重启后注册表与任务仍在；项目 force 关闭取消持卡任务并当场归还指针（卡 A）；
#      无卡 worker 只注册不领卡（#164 防回归前置）
#   9  保留期清理：离线未到保留期不删，超过 worker_retention 且一轮 sweep 后行消失
#      （卡 A 后半 + #164 防回归）
#
# 未覆盖（等卡 B / issue #165 落地后再补断言，脚本内以 TODO(issue #165) 标注）：
#   - 列表响应里的 presence 字段推导；
#   - GET /v1/workers?id= 详情端点（404 语义）。
#
# 依赖：bash 3.2+ / go / jq / curl
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

# 与 write_config 写进 TOML 的值保持一致；第 9 步的等待窗口按这几个常量算。
# lease_ttl 已接线到任务服务（claim / 续租都按它写 lease_until）：本脚本断言
# 「详情端点发布的 lease_ttl_seconds 等于配置值」与「续租后 lease_until 递增」，
# 但不解析时间戳硬算租期长度——镜像里的 date 未必支持 -d 解析毫秒定宽 UTC 串，
# 硬算会把断言变成平台相关的。
LEASE_TTL_SECONDS=90
WORKER_TTL_SECONDS=45
WORKER_RETENTION_SECONDS=46
# 服务端维护循环的固定周期（cmd/echuan/main.go: maintenanceInterval）。
SWEEP_INTERVAL_SECONDS=30
# 保留期到点后最多再等一轮 sweep，留 30s 余量让失败信息停在超时而不是竞态上。
PRUNE_TIMEOUT_SECONDS=$((WORKER_RETENTION_SECONDS + 2 * SWEEP_INTERVAL_SECONDS + 30))

say()  { printf '%s\n' "$*"; }
pass() { printf 'PASS  %s\n' "$*"; }

fail() {
  printf 'FAIL  [%s] %s\n' "$STEP" "$1" >&2
  [ -n "${TMP_DIR:-}" ] && printf '      临时目录: %s\n' "$TMP_DIR" >&2
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

# http_request 发一次请求，把响应体写进 HTTP_BODY、状态码写进 HTTP_CODE。
# 连不上时状态码记 000，交给调用方的 require_code 统一报错。
http_request() {
  local method="$1" path="$2"
  shift 2
  local raw
  if ! raw=$(curl -sS --max-time 15 -X "$method" "$BASE$path" \
      -H 'Content-Type: application/json' \
      -H 'X-Actor: e2e' \
      "$@" \
      -w $'\n%{http_code}' 2>&1); then
    HTTP_CODE="000"
    HTTP_BODY="$raw"
    return 0
  fi
  HTTP_CODE="${raw##*$'\n'}"
  HTTP_BODY="${raw%$'\n'*}"
}

# http_post 发 POST（第二个参数是 JSON 请求体），http_get / http_delete 只带路径。
http_post() {
  local path="$1" body="${2-}"
  if [ "$#" -ge 2 ]; then shift 2; else shift; fi
  http_request POST "$path" -d "$body" "$@"
}
http_get()    { http_request GET "$@"; }
http_delete() { http_request DELETE "$@"; }

require_code() {
  local want="$1" label="$2"
  if [[ "$HTTP_CODE" != "$want" ]]; then
    fail "$label：期望 HTTP $want，实际 $HTTP_CODE"
  fi
}

# require_body 断言响应体包含某个片段（错误码、规则名等）。
require_body() {
  local want="$1" label="$2"
  if [[ "$HTTP_BODY" != *"$want"* ]]; then
    fail "$label：响应体应包含 $want"
  fi
}

# require_empty 断言空响应体（V3 的 204 端点不带 body）。
require_empty() {
  local label="$1"
  if [[ -n "$HTTP_BODY" ]]; then
    fail "$label：204 响应体应为空"
  fi
}

# jqr 用 jq 表达式从上次响应体取标量。
jqr() { printf '%s' "$HTTP_BODY" | jq -r "$@"; }

# --- 端口与启动 -------------------------------------------------------------

# port_in_use 用 bash 的 /dev/tcp 探一下有没有进程在监听。
# 不用 lsof：该镜像里的 lsof 是 busybox 兼容层，忽略 -iTCP/-sTCP 过滤参数、
# 恒返回 0，会让「端口空闲」永远判定为占用。
port_in_use() {
  if (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; then
    exec 3<&- 3>&- 2>/dev/null || true
    return 0
  fi
  return 1
}

write_config() {
  cat >"$CONFIG" <<EOF
# e2e-worker.sh 生成的临时配置（每次运行都是新目录）
listen = "127.0.0.1:$PORT"
data_dir = "$TMP_DIR/data"
read_pool_size = 4
lease_ttl = "${LEASE_TTL_SECONDS}s"
max_attempts = 3
poll_timeout = "2s"
review_stale_hours = 72
material_max_bytes = 20971520
worker_ttl = "${WORKER_TTL_SECONDS}s"
heartbeat_interval = "15s"
worker_metadata_max_bytes = 8192
# 保留期必须严格大于 worker_ttl（配置校验），第 9 步才能在「先不删」之后
# 再观察到清理。46s 让离线判定的窗口和清理窗口几乎重合，等待时间最短。
worker_retention = "${WORKER_RETENTION_SECONDS}s"
log_level = "info"
EOF
}

# start_server 选一个空闲高位端口起服务，等到 /readyz 可用。
# 端口被别的进程占用时二进制会自己退出（kill -0 失败），换端口重试。
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
http_get "/healthz"
require_code 200 "/healthz"
[[ "$(jqr '.status')" == "ok" ]] || fail "/healthz status 应为 ok，实际 $(jqr '.status')"
http_get "/readyz"
require_code 200 "/readyz"
http_get "/version"
require_code 200 "/version"
[[ -n "$(jqr '.version')" ]] || fail "/version 缺少 version 字段"
pass "1/9 空数据目录一键启动成功（state.db 自动创建，/readyz、/healthz、/version 正常）"

# --- 2. 产品 / 项目容器 ------------------------------------------------------

STEP="2/9 产品与项目"
say ""
say "== 2/9 建产品、项目（项目只是容器，建项不建卡）；未注册路径 404 =="
PRODUCT_ID="E2E$RANDOM"
http_post "/v1/products" "$(jq -nc \
  --arg id "$PRODUCT_ID" \
  '{product_id:$id,name:"E2E 验收产品",git_url:"https://example.com/e2e.git",description:"e2e-worker.sh"}')"
require_code 201 "创建产品"
[[ "$(jqr '.product_id')" == "$PRODUCT_ID" ]] || fail "创建产品响应 product_id 不符"
say "产品: $PRODUCT_ID"

http_post "/v1/projects" "$(jq -nc \
  --arg pid "$PRODUCT_ID" \
  '{product_id:$pid,name:"E2E 验收项目",context_md:"# 背景\n\nWorker 全链路验收。\n"}')"
require_code 201 "创建项目"
PROJECT_ID="$(jqr '.project_id')"
[[ "$PROJECT_ID" == "$PRODUCT_ID-1" ]] || fail "项目 id 应形如 <product_id>-1，实际 $PROJECT_ID"
[[ "$(jqr '.status')" == "open" ]] || fail "新建项目 status 应为 open，实际 $(jqr '.status')"
[[ "$(jqr '.context_md')" == *"Worker 全链路验收"* ]] || fail "建项响应未回显 context_md"
say "项目: $PROJECT_ID (status=open)"

http_get "/v1/projects?id=$PROJECT_ID"
require_code 200 "读取项目"
[[ "$(jqr '.project_id')" == "$PROJECT_ID" ]] || fail "项目详情 project_id 不符"

http_get "/v1/projects?product_id=$PRODUCT_ID"
require_code 200 "按产品过滤项目"
[[ "$(jqr '.items|length')" == "1" ]] || fail "按产品过滤应返回 1 条项目，实际 $(jqr '.items|length')"

# 建项不建卡：项目内暂时没有任何任务。
http_get "/v1/tasks?project_id=$PROJECT_ID"
require_code 200 "列出项目任务"
[[ "$(jqr '.items|length')" == "0" ]] || fail "建项不应产生任何任务，实际 $(jqr '.items|length')"

# 旧 v2 工作项路由已删除：任何未注册的 /v1 路径都由 fallback 答 404 not_found。
http_get "/v1/retired-v2-path"
require_code 404 "未注册的 /v1 路径"
require_body "not_found" "未注册的 /v1 路径应返回 not_found"

# 请求体字段错误走统一 400（严格解码，未知字段也不放过）。
http_post "/v1/projects" '{"name":"缺少 product_id"}'
require_code 400 "缺少 product_id 的建项"
require_body "invalid_argument" "建项参数错误应返回 invalid_argument"
pass "2/9 建产品 / 项目成功且建项不建卡；未注册 /v1 路径 404 not_found；参数错误 400 invalid_argument"

# --- 3. Worker 注册 ----------------------------------------------------------

STEP="3/9 Worker 注册"
say ""
say "== 3/9 首次心跳（worker_id 由客户端生成，kinds 必填，响应 204 空体）=="
WORKER_ID="w_e2e_flow_$(date -u +%s)_$RANDOM"
http_post "/v1/workers/heartbeat" "$(jq -nc \
  --arg wid "$WORKER_ID" \
  '{worker_id:$wid,state:"idle",kinds:["requirement","design","breakdown","dev"],metadata:{host:"e2e",os:"linux"}}')"
require_code 204 "首次心跳"
require_empty "首次心跳"
say "worker_id: $WORKER_ID（客户端生成，服务端不再下发）"

http_get "/v1/workers"
require_code 200 "读取注册表"
[[ "$(jqr --arg w "$WORKER_ID" '[.items[]|select(.worker_id==$w)]|length')" == "1" ]] \
  || fail "注册后注册表里应有 $WORKER_ID"
WORKER_JSON="$(printf '%s' "$HTTP_BODY" | jq -c --arg w "$WORKER_ID" '.items[]|select(.worker_id==$w)')"
[[ "$(jq -r '.state' <<<"$WORKER_JSON")" == "idle" ]] || fail "注册后 state 应为 idle，实际 $(jq -r '.state' <<<"$WORKER_JSON")"
# kinds 按 kind 数值序返回（requirement 1 < design 2 < breakdown 3 < dev 4）。
[[ "$(jq -c '.kinds' <<<"$WORKER_JSON")" == '["requirement","design","breakdown","dev"]' ]] \
  || fail "kinds 应按数值序返回，实际 $(jq -c '.kinds' <<<"$WORKER_JSON")"
# 未持卡时契约要求显式 null（前端契约 string|null），而不是缺键。
[[ "$(jq -r 'has("current_work_item_id")' <<<"$WORKER_JSON")" == "true" ]] \
  || fail "worker 对象缺少 current_work_item_id 键"
[[ "$(jq -r '.current_work_item_id' <<<"$WORKER_JSON")" == "null" ]] \
  || fail "未持卡时 current_work_item_id 应为 null"
[[ "$(jq -r '.current_task_id // "null"' <<<"$WORKER_JSON")" == "null" ]] \
  || fail "未持卡时不应回传 current_task_id"
FIRST_SEEN="$(jq -r '.last_seen' <<<"$WORKER_JSON")"
say "注册表: state=idle kinds=$(jq -c '.kinds' <<<"$WORKER_JSON") last_seen=$FIRST_SEEN"

# 再次心跳只刷新 last_seen（V3 心跳不派卡、不改占用）。
sleep 0.2
http_post "/v1/workers/heartbeat" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,kinds:["requirement","design","breakdown","dev"]}')"
require_code 204 "重复心跳"
http_get "/v1/workers"
require_code 200 "重复心跳后读取注册表"
LAST_SEEN="$(jqr --arg w "$WORKER_ID" '.items[]|select(.worker_id==$w)|.last_seen')"
[[ ! "$LAST_SEEN" < "$FIRST_SEEN" ]] || fail "心跳后 last_seen 不应倒退：$FIRST_SEEN -> $LAST_SEEN"
say "last_seen: $FIRST_SEEN -> $LAST_SEEN"

# TODO(issue #165)：卡 B 落地后在此处补两条断言 ——
#   1) 列表响应的 presence 字段（active / offline 由 last_seen 与 worker_ttl 推导）；
#   2) GET /v1/workers?id=<worker_id> 的详情语义（当前该路由只按列表处理）。
# 这两条现在写死会必然失败，所以只留 TODO，不写会红的断言。
pass "3/9 注册成功（$WORKER_ID），心跳 204 空体，注册表 state/kinds/last_seen 与契约一致；presence 与详情端点见 TODO(issue #165)"

# --- 4. 建卡与 claim 派单信封 ------------------------------------------------

STEP="4/9 建卡与 claim"
say ""
say "== 4/9 建需求卡 → claim 拿到派单信封（task / project / product / inputs / upstream）=="
http_post "/v1/tasks" "$(jq -nc \
  --arg pid "$PROJECT_ID" \
  '{project_id:$pid,kind:"requirement",title:"需求：Worker 全链路验收",description_md:"# 需求\n\n覆盖注册 / 派卡 / 租约 / 报告 / 清理。\n",priority:10}')"
require_code 201 "建需求卡"
REQ_TASK="$(jqr '.task_id')"
[[ "$REQ_TASK" == t_* ]] || fail "task_id 应以 t_ 开头，实际 $REQ_TASK"
[[ "$(jqr '.status')" == "ready" ]] || fail "新卡 status 应为 ready（无未满足依赖），实际 $(jqr '.status')"
[[ "$(jqr '.attempt')" == "0" ]] || fail "新卡 attempt 应为 0，实际 $(jqr '.attempt')"
[[ "$(jqr '.max_attempts')" == "3" ]] \
  || fail "max_attempts 应取自配置（3），实际 $(jqr '.max_attempts')"
say "需求卡: $REQ_TASK (ready)"

http_post "/v1/tasks/claim" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,kind:"requirement"}')"
require_code 200 "claim 需求卡"
CLAIMED_TASK="$(jqr '.task.task_id')"
[[ "$CLAIMED_TASK" == "$REQ_TASK" ]] || fail "claim 到的卡应为 $REQ_TASK，实际 $CLAIMED_TASK"
[[ "$(jqr '.task.status')" == "in_progress" ]] || fail "claim 后 task.status 应为 in_progress"
[[ "$(jqr '.task.attempt')" == "1" ]] || fail "首次 claim 后 attempt 应为 1，实际 $(jqr '.task.attempt')"
[[ "$(jqr '.task.worker_id')" == "$WORKER_ID" ]] || fail "claim 后 task.worker_id 应为 $WORKER_ID"
[[ "$(jqr '.task.lease_id')" == l_* ]] || fail "lease_id 应以 l_ 开头，实际 $(jqr '.task.lease_id')"
[[ "$(jqr '.task.lease_until')" == *Z ]] || fail "lease_until 应为毫秒定宽 UTC 时间戳"
REQ_LEASE="$(jqr '.task.lease_id')"
# 派单信封：卡 + 项目 + 产品 + 输入/上游（此刻都为空）。
[[ "$(jqr '.project.project_id')" == "$PROJECT_ID" ]] || fail "信封 project.project_id 不符"
[[ "$(jqr '.project.context_md')" == *"Worker 全链路验收"* ]] || fail "信封未带项目背景 context_md"
[[ "$(jqr '.product.product_id')" == "$PRODUCT_ID" ]] || fail "信封 product.product_id 不符"
[[ "$(jqr '.product.git_url')" == "https://example.com/e2e.git" ]] || fail "信封未带产品 git_url"
[[ "$(jqr '.inputs|length')" == "0" ]] || fail "首张卡不应有输入物料，实际 $(jqr '.inputs|length')"
[[ "$(jqr '.upstream|length')" == "0" ]] || fail "首张卡不应有上游卡，实际 $(jqr '.upstream|length')"
[[ "$(jqr 'has("has_downstream_dev")')" == "true" ]] || fail "信封缺少 has_downstream_dev 字段"
say "claim: $CLAIMED_TASK lease=$REQ_LEASE lease_until=$(jqr '.task.lease_until')"

http_get "/v1/workers"
require_code 200 "持卡后读取注册表"
WORKER_JSON="$(printf '%s' "$HTTP_BODY" | jq -c --arg w "$WORKER_ID" '.items[]|select(.worker_id==$w)')"
[[ "$(jq -r '.state' <<<"$WORKER_JSON")" == "busy" ]] || fail "持卡后 state 应为 busy"
[[ "$(jq -r '.current_work_item_id' <<<"$WORKER_JSON")" == "$REQ_TASK" ]] \
  || fail "持卡后 current_work_item_id 应指向 $REQ_TASK"
[[ "$(jq -r '.current_lease_id' <<<"$WORKER_JSON")" == "$REQ_LEASE" ]] \
  || fail "持卡后 current_lease_id 应为 $REQ_LEASE"
say "注册表: state=busy current_work_item_id=$REQ_TASK"

# 队列空时 claim 是 204（不是错误，也不是 body）。
http_post "/v1/tasks/claim" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,kind:"requirement"}')"
require_code 204 "无卡可领的 claim"
require_empty "无卡可领的 claim"
http_post "/v1/tasks/claim" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,kind:"design"}')"
require_code 204 "无 design 卡可领的 claim"
pass "4/9 建卡 → claim 拿到 in_progress + 租约 + 派单信封；持卡后注册表 busy；队列空 claim 204"

# --- 5. 租约 -----------------------------------------------------------------

STEP="5/9 租约续期"
say ""
say "== 5/9 显式续租递增、错误 lease 409、心跳不归还占用 =="
http_get "/v1/tasks?id=$REQ_TASK"
require_code 200 "读取卡租约"
L1="$(jqr '.lease_until')"
[[ -n "$L1" && "$L1" != "null" ]] || fail "in_progress 卡应带 lease_until"

sleep 1.1
http_post "/v1/tasks/lease" "$(jq -nc --arg tid "$REQ_TASK" --arg lid "$REQ_LEASE" '{task_id:$tid,lease_id:$lid}')"
require_code 200 "显式续租"
RENEWED="$(jqr '.lease_until')"
[[ "$(jqr '.lease_id')" == "$REQ_LEASE" ]] || fail "显式续租应保持 lease_id 不变"
[[ "$RENEWED" > "$L1" ]] || fail "续租后 lease_until 应递增：$L1 -> $RENEWED"
http_get "/v1/tasks?id=$REQ_TASK"
require_code 200 "续租后复核租约"
[[ "$(jqr '.lease_until')" == "$RENEWED" ]] || fail "续租响应与详情租约不一致：$RENEWED / $(jqr '.lease_until')"
say "lease_until: $L1 -> $RENEWED"

# 用不存在的 lease 续租：租约已丢失/不匹配统一 409 conflict（message 带 lease lost）。
http_post "/v1/tasks/lease" "$(jq -nc --arg tid "$REQ_TASK" '{task_id:$tid,lease_id:"l_e2e_bogus"}')"
require_code 409 "错误 lease 续租"
require_body "conflict" "错误 lease 续租应返回 409 conflict"
require_body "lease lost" "错误 lease 续租的 message 应指出 lease lost"
http_get "/v1/tasks?id=$REQ_TASK"
require_code 200 "失败续租后复核租约"
[[ "$(jqr '.lease_until')" == "$RENEWED" ]] || fail "失败的续租不应改动 lease_until"

# V3 心跳只刷新 last_seen：持卡心跳不会归还占用，也不会换租约。
http_post "/v1/workers/heartbeat" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,kinds:["requirement","design","breakdown","dev"]}')"
require_code 204 "持卡心跳"
http_get "/v1/workers"
require_code 200 "持卡心跳后读取注册表"
WORKER_JSON="$(printf '%s' "$HTTP_BODY" | jq -c --arg w "$WORKER_ID" '.items[]|select(.worker_id==$w)')"
[[ "$(jq -r '.current_work_item_id' <<<"$WORKER_JSON")" == "$REQ_TASK" ]] \
  || fail "心跳不应释放已持有的卡"
[[ "$(jq -r '.current_lease_id' <<<"$WORKER_JSON")" == "$REQ_LEASE" ]] \
  || fail "心跳不应改动 current_lease_id"

# lease_ttl 接线（issue #165 D-2）：详情端点发布的租期必须等于配置值，也就是
# claim 刚才实际用的那个时长（两处同源于 platform.Config.LeaseTTL）。
http_get "/v1/workers?id=$WORKER_ID"
require_code 200 "读取持卡 worker 详情"
[[ "$(jqr '.lease_ttl_seconds')" == "$LEASE_TTL_SECONDS" ]] \
  || fail "详情 lease_ttl_seconds 应为配置的 ${LEASE_TTL_SECONDS}s，实际 $(jqr '.lease_ttl_seconds')"
pass "5/9 显式续租 lease_until 递增且 lease_id 不变；错误 lease 409 lease lost；持卡心跳不归还占用；详情 lease_ttl_seconds=${LEASE_TTL_SECONDS}s 与配置同源"

# --- 6. 报告 / 驳回 / 验收 ---------------------------------------------------

STEP="6/9 报告与验收"
say ""
say "== 6/9 报告 → 归还占用 → 驳回回到 ready → 重新领取 → 验收 → 派生方案卡 =="
http_post "/v1/materials" '{"kind":"requirement","title":"需求正文 v1","body_md":"# 需求\n\nWorker 全链路验收 v1。\n"}'
require_code 201 "创建需求物料"
REQ_MATERIAL="$(jqr '.material_id')"
[[ "$(jqr '.kind')" == "requirement" ]] || fail "物料 kind 应为 requirement"
[[ "$(jqr '.version')" == "1" ]] || fail "首个物料 version 应为 1，实际 $(jqr '.version')"

http_post "/v1/tasks/report" "$(jq -nc \
  --arg tid "$REQ_TASK" --arg lid "$REQ_LEASE" --arg mid "$REQ_MATERIAL" \
  '{task_id:$tid,lease_id:$lid,material_id:$mid}')"
require_code 200 "提交需求报告"
[[ "$(jqr '.status')" == "in_review" ]] || fail "报告后 status 应为 in_review，实际 $(jqr '.status')"
[[ "$(jqr '.lease_id // "null"')" == "null" ]] || fail "报告后应释放 lease_id"
[[ "$(jqr '.worker_id // "null"')" == "null" ]] || fail "报告后应释放 worker_id"

http_get "/v1/workers"
require_code 200 "报告后读取注册表"
WORKER_JSON="$(printf '%s' "$HTTP_BODY" | jq -c --arg w "$WORKER_ID" '.items[]|select(.worker_id==$w)')"
[[ "$(jq -r '.state' <<<"$WORKER_JSON")" == "idle" ]] || fail "报告后 worker 应回到 idle"
[[ "$(jq -r '.current_work_item_id' <<<"$WORKER_JSON")" == "null" ]] || fail "报告后应归还 current_work_item_id"

http_get "/v1/tasks/attachments?task_id=$REQ_TASK"
require_code 200 "读取任务附件"
[[ "$(jqr '.items|length')" == "1" ]] || fail "报告后应有 1 条附件，实际 $(jqr '.items|length')"
[[ "$(jqr '.items[0].material_id')" == "$REQ_MATERIAL" ]] || fail "附件 material_id 应为 $REQ_MATERIAL"
[[ "$(jqr '.items[0].role')" == "output" ]] || fail "报告写入的附件 role 应为 output"
say "报告: in_review，附件 $REQ_MATERIAL (output)，worker 占用已归还"

# 驳回必须带 reason（缺 reason 是 400，不是静默通过）。
http_post "/v1/tasks/decide" "$(jq -nc --arg tid "$REQ_TASK" '{task_id:$tid,decision:"reject",decided_by:"e2e"}')"
require_code 400 "无 reason 的驳回"
require_body "invalid_argument" "无 reason 的驳回应返回 invalid_argument"

http_post "/v1/tasks/decide" "$(jq -nc \
  --arg tid "$REQ_TASK" \
  '{task_id:$tid,decision:"reject",decided_by:"e2e",reason:"需求缺少验收标准"}')"
require_code 200 "驳回需求"
[[ "$(jqr '.status')" == "ready" ]] || fail "驳回后 status 应回到 ready，实际 $(jqr '.status')"
[[ "$(jqr '.reason')" == "需求缺少验收标准" ]] || fail "驳回原因未落到卡上"
http_get "/v1/decisions?task_id=$REQ_TASK"
require_code 200 "读取评审决策"
[[ "$(jqr '.items|length')" == "1" ]] || fail "应有 1 条评审决策，实际 $(jqr '.items|length')"
[[ "$(jqr '.items[0].decision')" == "reject" ]] || fail "决策记录应为 reject"
[[ "$(jqr '.items[0].gate')" == "requirement" ]] || fail "决策 gate 应为 requirement"

# 重新领取：attempt 递增，换新的 lease。
http_post "/v1/tasks/claim" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,kind:"requirement"}')"
require_code 200 "重新领取需求卡"
[[ "$(jqr '.task.task_id')" == "$REQ_TASK" ]] || fail "重新领取的应是 $REQ_TASK"
[[ "$(jqr '.task.attempt')" == "2" ]] || fail "重新领取后 attempt 应为 2，实际 $(jqr '.task.attempt')"
REQ_LEASE="$(jqr '.task.lease_id')"
say "重新领取: attempt=2 lease=$REQ_LEASE"

http_post "/v1/materials" '{"kind":"requirement","title":"需求正文 v2","body_md":"# 需求\n\n补上验收标准。\n"}'
require_code 201 "创建需求物料 v2"
REQ_MATERIAL="$(jqr '.material_id')"
[[ -n "$REQ_MATERIAL" && "$REQ_MATERIAL" != "null" ]] || fail "创建需求物料 v2 未返回 material_id"
http_post "/v1/tasks/report" "$(jq -nc \
  --arg tid "$REQ_TASK" --arg lid "$REQ_LEASE" --arg mid "$REQ_MATERIAL" \
  '{task_id:$tid,lease_id:$lid,material_id:$mid}')"
require_code 200 "提交需求报告 v2"
http_post "/v1/tasks/decide" "$(jq -nc \
  --arg tid "$REQ_TASK" --arg mid "$REQ_MATERIAL" \
  '{task_id:$tid,decision:"approve",material_id:$mid,decided_by:"e2e"}')"
require_code 200 "验收需求卡"
[[ "$(jqr '.status')" == "done" ]] || fail "验收后 status 应为 done，实际 $(jqr '.status')"
[[ "$(jqr '.reason // "null"')" == "null" ]] || fail "验收通过后驳回原因应被清空"

# 派生规则：requirement 验收 → 同项目派生 1 张 design 卡（依赖已验收的卡）。
http_get "/v1/tasks?project_id=$PROJECT_ID&kind=design"
require_code 200 "列出派生方案卡"
[[ "$(jqr '.items|length')" == "1" ]] || fail "需求验收后应派生 1 张方案卡，实际 $(jqr '.items|length')"
DESIGN_TASK="$(jqr '.items[0].task_id')"
[[ "$(jqr '.items[0].status')" == "ready" ]] || fail "派生方案卡 status 应为 ready，实际 $(jqr '.items[0].status')"
[[ "$(jqr '.items[0].parent_task_id')" == "$REQ_TASK" ]] || fail "派生方案卡的 parent_task_id 应指向上游卡"
http_get "/v1/tasks/dependencies?task_id=$DESIGN_TASK"
require_code 200 "读取派生卡依赖"
[[ "$(jqr '.items|length')" == "1" ]] || fail "派生卡应有 1 条依赖，实际 $(jqr '.items|length')"
[[ "$(jqr '.items[0]')" == "$REQ_TASK" ]] || fail "派生卡依赖应指向 $REQ_TASK"
say "派生方案卡: $DESIGN_TASK (ready, 依赖 $REQ_TASK)"

# 领派生卡：信封里的 upstream 带上游卡与它验收过的交付物。
http_post "/v1/tasks/claim" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,kind:"design"}')"
require_code 200 "claim 派生方案卡"
[[ "$(jqr '.task.task_id')" == "$DESIGN_TASK" ]] || fail "claim 到的应是派生方案卡"
DESIGN_LEASE="$(jqr '.task.lease_id')"
[[ "$(jqr '.upstream|length')" == "1" ]] || fail "方案卡信封应有 1 条上游，实际 $(jqr '.upstream|length')"
[[ "$(jqr '.upstream[0].task_id')" == "$REQ_TASK" ]] || fail "上游卡应为 $REQ_TASK"
[[ "$(jqr '.upstream[0].material_id')" == "$REQ_MATERIAL" ]] || fail "上游物料应为已验收的 $REQ_MATERIAL"
say "方案卡信封: upstream=$(jqr -c '.upstream')"
pass "6/9 报告归还占用、驳回回到 ready 且决策落库、重新领取 attempt 递增、验收后派生方案卡并带上游交付"

# --- 7. 拆解计划 → dev ------------------------------------------------------

STEP="7/9 拆解与执行"
say ""
say "== 7/9 方案验收 → 派生拆解卡 → 计划落成 dev 卡 → 派发 → 报告 → 验收 =="
http_post "/v1/materials" '{"kind":"design","title":"技术方案","body_md":"# 技术方案\n\n- 注册表\n- 派卡与租约\n- 报告与验收\n"}'
require_code 201 "创建方案物料"
DESIGN_MATERIAL="$(jqr '.material_id')"
http_post "/v1/tasks/report" "$(jq -nc \
  --arg tid "$DESIGN_TASK" --arg lid "$DESIGN_LEASE" --arg mid "$DESIGN_MATERIAL" \
  '{task_id:$tid,lease_id:$lid,material_id:$mid}')"
require_code 200 "提交方案报告"
http_post "/v1/tasks/decide" "$(jq -nc \
  --arg tid "$DESIGN_TASK" --arg mid "$DESIGN_MATERIAL" \
  '{task_id:$tid,decision:"approve",material_id:$mid,decided_by:"e2e"}')"
require_code 200 "验收方案卡"
[[ "$(jqr '.status')" == "done" ]] || fail "方案验收后 status 应为 done，实际 $(jqr '.status')"

http_get "/v1/tasks?project_id=$PROJECT_ID&kind=breakdown"
require_code 200 "列出派生拆解卡"
[[ "$(jqr '.items|length')" == "1" ]] || fail "方案验收后应派生 1 张拆解卡，实际 $(jqr '.items|length')"
BREAKDOWN_TASK="$(jqr '.items[0].task_id')"
http_post "/v1/tasks/claim" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,kind:"breakdown"}')"
require_code 200 "claim 拆解卡"
[[ "$(jqr '.task.task_id')" == "$BREAKDOWN_TASK" ]] || fail "claim 到的应是拆解卡"
BREAKDOWN_LEASE="$(jqr '.task.lease_id')"
say "拆解卡: $BREAKDOWN_TASK"

# 拆解报告携带 plan（JSON 文档），验收时平台把它落成 dev 卡。
PLAN_BODY="$(jq -nc --arg pid "$PROJECT_ID" \
  '{items:[{seq_no:1,project_id:$pid,title:"E2E 执行卡",body_md:"# 执行\n\n由拆解计划生成。\n",priority:5}]}')"
http_post "/v1/materials" "$(jq -nc --arg body "$PLAN_BODY" '{kind:"plan",title:"拆解计划",body_md:$body}')"
require_code 201 "创建拆解计划物料"
PLAN_MATERIAL="$(jqr '.material_id')"
http_post "/v1/tasks/report" "$(jq -nc \
  --arg tid "$BREAKDOWN_TASK" --arg lid "$BREAKDOWN_LEASE" --arg mid "$PLAN_MATERIAL" \
  '{task_id:$tid,lease_id:$lid,material_id:$mid}')"
require_code 200 "提交拆解报告"
http_post "/v1/tasks/decide" "$(jq -nc \
  --arg tid "$BREAKDOWN_TASK" --arg mid "$PLAN_MATERIAL" \
  '{task_id:$tid,decision:"approve",material_id:$mid,decided_by:"e2e"}')"
require_code 200 "验收拆解卡"
[[ "$(jqr '.status')" == "done" ]] || fail "拆解验收后 status 应为 done，实际 $(jqr '.status')"

http_get "/v1/tasks?project_id=$PROJECT_ID&kind=dev"
require_code 200 "列出计划生成的 dev 卡"
[[ "$(jqr '.items|length')" == "1" ]] || fail "拆解计划应生成 1 张 dev 卡，实际 $(jqr '.items|length')"
DEV_TASK="$(jqr '.items[0].task_id')"
[[ "$(jqr '.items[0].status')" == "ready" ]] || fail "计划生成的 dev 卡应为 ready，实际 $(jqr '.items[0].status')"
[[ "$(jqr '.items[0].parent_task_id')" == "$BREAKDOWN_TASK" ]] || fail "dev 卡 parent_task_id 应指向拆解卡"
[[ "$(jqr '.items[0].priority')" == "5" ]] || fail "dev 卡 priority 应来自计划（5），实际 $(jqr '.items[0].priority')"
say "计划生成的 dev 卡: $DEV_TASK (ready, priority 5)"

http_post "/v1/tasks/claim" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,kind:"dev"}')"
require_code 200 "claim dev 卡"
[[ "$(jqr '.task.task_id')" == "$DEV_TASK" ]] || fail "claim 到的应是 dev 卡"
DEV_LEASE="$(jqr '.task.lease_id')"
http_post "/v1/materials" '{"kind":"report","title":"执行结果","body_md":"# 执行结果\n\n验收脚本跑通。\n"}'
require_code 201 "创建执行结果物料"
DEV_MATERIAL="$(jqr '.material_id')"
http_post "/v1/tasks/report" "$(jq -nc \
  --arg tid "$DEV_TASK" --arg lid "$DEV_LEASE" --arg mid "$DEV_MATERIAL" \
  '{task_id:$tid,lease_id:$lid,material_id:$mid}')"
require_code 200 "提交 dev 报告"
http_post "/v1/tasks/decide" "$(jq -nc \
  --arg tid "$DEV_TASK" --arg mid "$DEV_MATERIAL" \
  '{task_id:$tid,decision:"approve",material_id:$mid,decided_by:"e2e"}')"
require_code 200 "验收 dev 卡"
[[ "$(jqr '.status')" == "done" ]] || fail "dev 验收后 status 应为 done，实际 $(jqr '.status')"

# 项目材料视图由任务附件派生：每张卡的交付物都带 producing task_id，驳回前
# 报过一版需求也仍留在视图里（附件只追加）。
http_get "/v1/projects/materials?id=$PROJECT_ID"
require_code 200 "读取项目材料视图"
[[ "$(jqr '.items|length')" == "5" ]] || fail "项目材料应有 5 条（需求 v1/v2、方案、计划、执行结果），实际 $(jqr '.items|length')"
[[ "$(jqr --arg t "$DEV_TASK" '[.items[]|select(.task_id==$t)]|length')" == "1" ]] \
  || fail "项目材料视图应带上 dev 卡的交付物"

# 全部卡片已结清：待办视图为空。
http_get "/v1/inbox"
require_code 200 "读取待办"
[[ "$(jqr '.items|length')" == "0" ]] || fail "所有卡结清后待办应为空，实际 $(jqr '.items|length')"

# 没有活跃卡时关闭项目不需要 force；关闭后再重开。
http_post "/v1/projects/close?id=$PROJECT_ID" ''
require_code 204 "关闭空项目"
require_empty "关闭空项目"
http_get "/v1/projects?id=$PROJECT_ID"
require_code 200 "读取关闭后的项目"
[[ "$(jqr '.status')" == "closed" ]] || fail "关闭后 status 应为 closed，实际 $(jqr '.status')"
[[ "$(jqr '.closed_at')" != "null" ]] || fail "关闭后应写 closed_at"
http_post "/v1/projects/reopen?id=$PROJECT_ID" ''
require_code 204 "重开项目"
http_get "/v1/projects?id=$PROJECT_ID"
require_code 200 "读取重开后的项目"
[[ "$(jqr '.status')" == "open" ]] || fail "重开后 status 应为 open，实际 $(jqr '.status')"
pass "7/9 方案验收派生拆解卡，计划落成 dev 卡并走完报告 / 验收；项目材料与待办一致；关闭 / 重开可用"

# --- 8. 重启恢复 + 取消归还 + 无卡 worker ------------------------------------

STEP="8/9 重启与取消归还"
say ""
say "== 8/9 重启后注册表 / 任务仍在；项目 force 关闭取消持卡任务并当场归还指针 =="
OLD_PORT="$PORT"
stop_server
start_server
say "重启: $OLD_PORT -> $PORT（同一数据目录）"
http_get "/v1/tasks?id=$REQ_TASK"
require_code 200 "重启后读取需求卡"
[[ "$(jqr '.status')" == "done" ]] || fail "重启后需求卡应仍为 done，实际 $(jqr '.status')"
http_get "/v1/workers"
require_code 200 "重启后读取注册表"
[[ "$(jqr --arg w "$WORKER_ID" '[.items[]|select(.worker_id==$w)]|length')" == "1" ]] \
  || fail "重启后注册表里应仍有 $WORKER_ID"
say "重启后：需求卡 done、$WORKER_ID 仍在注册表"

# 卡 A：持卡 → 取消（本构建没有单任务取消/失败路由，取消只能经项目 force 关闭）
# → 指针当场清空 → 超过保留期且一轮 sweep 后整行消失。
WORKER_HOLDER="w_e2e_holder_$(date -u +%s)_$RANDOM"
http_post "/v1/workers/heartbeat" "$(jq -nc --arg wid "$WORKER_HOLDER" '{worker_id:$wid,kinds:["dev"]}')"
require_code 204 "持卡 worker 注册"
http_post "/v1/tasks" "$(jq -nc \
  --arg pid "$PROJECT_ID" \
  '{project_id:$pid,kind:"dev",title:"E2E 待取消的执行卡",description_md:"# 执行\n\n会被项目关闭取消。\n"}')"
require_code 201 "建待取消的 dev 卡"
CANCEL_TASK="$(jqr '.task_id')"
http_post "/v1/tasks/claim" "$(jq -nc --arg wid "$WORKER_HOLDER" '{worker_id:$wid,kind:"dev"}')"
require_code 200 "持卡 worker 领取 dev 卡"
[[ "$(jqr '.task.task_id')" == "$CANCEL_TASK" ]] || fail "持卡 worker 应领到 $CANCEL_TASK"
CANCEL_LEASE="$(jqr '.task.lease_id')"
http_get "/v1/workers"
require_code 200 "持卡后读取注册表"
[[ "$(jqr --arg w "$WORKER_HOLDER" '.items[]|select(.worker_id==$w)|.current_work_item_id')" == "$CANCEL_TASK" ]] \
  || fail "$WORKER_HOLDER 持卡后 current_work_item_id 应为 $CANCEL_TASK"

# 持卡再 claim（issue #165 D-1 防回归）：队列里还有一张同类卡时，持卡 Worker
# 再 claim 必须是 409 conflict + 「已持卡」，而不是唯一索引撞出来的 500。
http_post "/v1/tasks" "$(jq -nc \
  --arg pid "$PROJECT_ID" \
  '{project_id:$pid,kind:"dev",title:"E2E 排队中的第二张执行卡",description_md:"# 执行\n\n用于验证持卡再 claim 的 409。\n"}')"
require_code 201 "建排队中的第二张卡"
QUEUED_TASK="$(jqr '.task_id')"
http_post "/v1/tasks/claim" "$(jq -nc --arg wid "$WORKER_HOLDER" '{worker_id:$wid,kind:"dev"}')"
require_code 409 "持卡 Worker 再 claim（队列里有卡）"
require_body "conflict" "持卡再 claim 应返回 409 conflict"
require_body "已持卡" "持卡再 claim 的 message 应指出已持卡"
# 被拒的 claim 整笔回滚：排队的卡仍是 ready、无属主。
http_get "/v1/tasks?id=$QUEUED_TASK"
require_code 200 "读取排队中的第二张卡"
[[ "$(jqr '.status')" == "ready" ]] || fail "被拒的 claim 不应改动排队卡状态，实际 $(jqr '.status')"
[[ "$(jqr '.worker_id // "null"')" == "null" ]] || fail "被拒的 claim 不应写排队卡 worker_id"
say "持卡再 claim：409 conflict（已持卡），$QUEUED_TASK 仍为 ready"

# 单任务没有取消/失败路由：DELETE /v1/tasks 只允许 pending/ready（硬删），
# 对 in_progress 的卡是 409 invalid transition。
http_delete "/v1/tasks?id=$CANCEL_TASK"
require_code 409 "硬删 in_progress 卡"
require_body "invalid task transition" "硬删 in_progress 卡应被拒绝"
# 未结清项目的关闭必须显式 force，否则 409。
http_post "/v1/projects/close?id=$PROJECT_ID" ''
require_code 409 "关闭未结清项目"
require_body "unfinished" "未结清项目的关闭应返回 unfinished"

http_post "/v1/projects/close?id=$PROJECT_ID&force=1" ''
require_code 204 "force 关闭项目"
require_empty "force 关闭项目"
http_get "/v1/tasks?id=$CANCEL_TASK"
require_code 200 "读取被取消的卡"
[[ "$(jqr '.status')" == "cancelled" ]] || fail "force 关闭后卡应为 cancelled，实际 $(jqr '.status')"
[[ "$(jqr '.lease_id // "null"')" == "null" ]] || fail "取消后应释放 lease_id（原 $CANCEL_LEASE）"
[[ "$(jqr '.worker_id // "null"')" == "null" ]] || fail "取消后应释放 worker_id"
http_get "/v1/projects?id=$PROJECT_ID"
require_code 200 "读取 force 关闭后的项目"
[[ "$(jqr '.status')" == "closed" ]] || fail "force 关闭后项目 status 应为 closed"

# 卡 A 的关键断言：取消当场归还占用（不必等 sweep）。
http_get "/v1/workers"
require_code 200 "取消后读取注册表"
HOLDER_JSON="$(printf '%s' "$HTTP_BODY" | jq -c --arg w "$WORKER_HOLDER" '.items[]|select(.worker_id==$w)')"
[[ -n "$HOLDER_JSON" ]] || fail "取消后 $WORKER_HOLDER 不应立刻从注册表消失（未过保留期）"
[[ "$(jq -r '.state' <<<"$HOLDER_JSON")" == "idle" ]] || fail "取消后 $WORKER_HOLDER state 应回到 idle"
[[ "$(jq -r '.current_work_item_id' <<<"$HOLDER_JSON")" == "null" ]] \
  || fail "取消后 $WORKER_HOLDER 的 current_work_item_id 应被清空"
[[ "$(jq -r '.current_lease_id // "null"' <<<"$HOLDER_JSON")" == "null" ]] \
  || fail "取消后 $WORKER_HOLDER 的 current_lease_id 应被清空"
say "取消后：$CANCEL_TASK=cancelled，$WORKER_HOLDER 指针已清空、行仍在"

# #164 前置：只注册、从不领卡的 worker 也是注册表里的正常一行。
WORKER_IDLE="w_e2e_idle_$(date -u +%s)_$RANDOM"
http_post "/v1/workers/heartbeat" "$(jq -nc --arg wid "$WORKER_IDLE" '{worker_id:$wid,kinds:["dev"]}')"
require_code 204 "无卡 worker 注册"
http_get "/v1/workers"
require_code 200 "读取含无卡 worker 的注册表"
[[ "$(jqr --arg w "$WORKER_IDLE" '[.items[]|select(.worker_id==$w)]|length')" == "1" ]] \
  || fail "无卡 worker $WORKER_IDLE 应出现在注册表里"
[[ "$(jqr --arg w "$WORKER_IDLE" '.items[]|select(.worker_id==$w)|.current_work_item_id')" == "null" ]] \
  || fail "无卡 worker 的 current_work_item_id 应为 null"
pass "8/9 重启后注册表与任务仍在；持卡再 claim 409 conflict 且队列卡未被改动；force 关闭取消持卡卡并当场归还指针；无卡 worker 正常在册"

# --- 9. 保留期清理 ----------------------------------------------------------

STEP="9/9 保留期清理"
say ""
say "== 9/9 离线未到保留期不删；超过 worker_retention 且一轮 sweep 后行消失 =="
WAIT_START="$(date -u +%s)"
# 保留期前的检查点：这一轮 sweep 可能刚跑过，两行都必须还在（#164 防回归：
# 无卡离线 worker 不能在保留期之前被删）。
sleep 8
http_get "/v1/workers"
require_code 200 "保留期前读取注册表"
for wid in "$WORKER_HOLDER" "$WORKER_IDLE"; do
  [[ "$(jqr --arg w "$wid" '[.items[]|select(.worker_id==$w)]|length')" == "1" ]] \
    || fail "保留期未到，$wid 不应被删除"
done
say "保留期前检查点（+8s）：两个离线 worker 仍在注册表"

# 等保留期到点后的下一轮 sweep。轮询到两行都消失，上限 PRUNE_TIMEOUT_SECONDS。
say "等待保留期 ${WORKER_RETENTION_SECONDS}s + 一轮 sweep（${SWEEP_INTERVAL_SECONDS}s），上限 ${PRUNE_TIMEOUT_SECONDS}s ..."
PRUNED=0
while :; do
  ELAPSED=$(( $(date -u +%s) - WAIT_START ))
  http_get "/v1/workers"
  require_code 200 "轮询注册表"
  HOLDER_ROWS="$(jqr --arg w "$WORKER_HOLDER" '[.items[]|select(.worker_id==$w)]|length')"
  IDLE_ROWS="$(jqr --arg w "$WORKER_IDLE" '[.items[]|select(.worker_id==$w)]|length')"
  if [[ "$HOLDER_ROWS" == "0" && "$IDLE_ROWS" == "0" ]]; then
    PRUNED=1
    break
  fi
  if [[ "$ELAPSED" -ge "$PRUNE_TIMEOUT_SECONDS" ]]; then
    fail "等待 ${ELAPSED}s 后仍未清理（${WORKER_HOLDER}=${HOLDER_ROWS} 行，${WORKER_IDLE}=${IDLE_ROWS} 行）"
  fi
  sleep 3
done
[[ "$PRUNED" == "1" ]] || fail "保留期清理未发生"
say "清理耗时: ${ELAPSED}s"

# sweep 真的跑过：日志里应有清理行（顺带证明删行来自维护循环而非别的路径）。
if ! grep -q 'pruned stale workers' "$LOG"; then
  fail "服务日志里没有 pruned stale workers，清理来源存疑"
fi
say "服务日志: $(grep 'pruned stale workers' "$LOG" | tail -1)"

# TODO(issue #165)：卡 B 落地后在此处补 —— 行被删后 GET /v1/workers?id=<worker_id>
# 应返回 404 not_found；当前该路由只有列表语义，写死断言会必然失败。
pass "9/9 保留期前不删、超过 worker_retention 且一轮 sweep 后两行都从注册表消失（卡 A / #164）"

say ""
say "全部 9 步通过。"
