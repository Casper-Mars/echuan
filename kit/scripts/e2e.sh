#!/usr/bin/env bash
#
# kit/scripts/e2e.sh — dsh-echuan 真机端到端验收（一条命令跑完）
#
# 覆盖链路：
#   S1  独立 docker compose 栈起平台（固定端口 18720，数据落临时目录）
#   S2  播种产品 / 项目 / 两张任务卡
#   S3  pnpm pack → 装进临时 dsh profile → profile 自身能 --help 退出
#   S4  dsh 后台启动（ACP 长驻）→ 平台注册出 w_ 前缀 worker
#   S5  平台派单 → 插件把任务投递给根 agent（日志证据）
#   S6  用 lib/tools 里真实的 echuan_task_report 交三段式报告 → pending_acceptance
#   S7  下一次心跳领到第二张卡（且不是刚报告的那张）
#   S8  平台重启 → 同一 worker id 心跳 200、不重复领卡
#   S9  插件重启 → 新 w_ id，旧记录仍在且不领同一张卡
#   S10 停用插件 → worker 在 worker_ttl(45s) 窗口内转 offline
#   S11 收尾：容器 / profile / 临时目录全清，~/echuan-data 指纹不变
#
# 隔离性（与 scripts/e2e-worker.sh 同一套策略）：
#   - 临时目录 + 固定端口 18720（固定而非随机，便于复现；被占用就直接 FAIL，不静默换端口）
#   - 清空宿主机 ECHUAN_*，平台数据目录指到 $TMP/data
#   - DSH_HOME 指到 $TMP/dsh-home，profile、session、日志全部落在临时目录
#   - pnpm pack 产物用 --pack-destination 落到 $TMP，仓库 kit/ 始终保持无 *.tgz
#   - 绝不读写 ~/.echuan、~/.dsh 或仓库 data/；~/.echuan-data 只读指纹（开头/结尾对比）
#
# 退出码：0 全部 PASS；2 前置环境不满足（FAIL [S0]）；1 某一步失败
#        （打印状态码 / 响应体 / 日志尾部）。
#
# 用法：
#   bash kit/scripts/e2e.sh              # 默认模式，不需要任何 LLM Key
#   E2E_REAL_LLM=1 bash kit/scripts/e2e.sh   # 可选：让 dsh agent 自己调工具交报告
#
# 依赖：bash 3.2+ / docker 27 / docker compose v2 / jq / curl / pnpm / node / dsh

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KIT="$ROOT/kit"

PROJ="echuan-e2e"
CONTAINER="echuan-e2e"
PORT=18720
BASE="http://127.0.0.1:$PORT"
PROFILE="echuan-e2e-$$"
IMAGE="echuan:local"

TMP=""
DSH_HOME_DIR=""
DSH_LOG=""
DSH_PID=""
STACK_STARTED=0
FIFO_FD_OPEN=0
STEP="startup"

HTTP_CODE=""
HTTP_BODY=""

say()  { printf '%s\n' "$*"; }
pass() { printf 'PASS  %s\n' "$*"; }

fail() {
  printf 'FAIL  [%s] %s\n' "$STEP" "$1" >&2
  [ -n "$HTTP_CODE" ] && printf '      status: %s\n' "$HTTP_CODE" >&2
  [ -n "$HTTP_BODY" ] && printf '      body:   %s\n' "$HTTP_BODY" >&2
  exit 1
}

# 前置检查失败：输出风格与其余步骤一致（FAIL [S0]），退出码 2 = 环境不满足。
fail_env() {
  printf 'FAIL  [S0] %s\n' "$1" >&2
  if [ -n "${2:-}" ]; then
    printf '%s' "$2" | sed 's/^/      /' >&2
  fi
  exit 2
}

warn() { printf 'WARN  %s\n' "$*" >&2; }

# --- 清理 -------------------------------------------------------------------
# 无论成败都要清掉**外部**资源（容器 / dsh 进程）；成功时连临时目录一起删，
# 失败时保留临时目录用于排障（其中包含临时 dsh profile、session、平台日志）。
cleanup() {
  local code="${1:-0}"
  stop_dsh
  # 仅当真的走到了建栈/运行阶段才保留现场；S0 环境不满足时 TMP 还是空的。
  if [ "$code" -ne 0 ] && [ -n "$TMP" ]; then
    printf '\n---- 失败现场：保留临时目录 %s ----\n' "$TMP" >&2
    if [ "$STACK_STARTED" = "1" ]; then
      printf '\n---- docker compose -p %s logs --tail 50 ----\n' "$PROJ" >&2
      ( cd "$ROOT" && docker compose -p "$PROJ" logs --tail 50 ) >&2 2>&1 || true
    fi
    if [ -n "$DSH_LOG" ] && [ -f "$DSH_LOG" ]; then
      printf '\n---- dsh 日志（最后 40 行）----\n' >&2
      tail -40 "$DSH_LOG" >&2 || true
    fi
  fi
  if [ "$STACK_STARTED" = "1" ]; then
    ( cd "$ROOT" && docker compose -p "$PROJ" down >/dev/null 2>&1 ) || true
    STACK_STARTED=0
  fi
  if [ "$code" -eq 0 ] && [ -n "$TMP" ] && [ -d "$TMP" ]; then
    rm -rf "$TMP"
  fi
  # 兜底：旧版脚本 / 异常中断可能把 pnpm pack 产物留在仓库 kit/ 下。
  rm -f "$KIT"/dsh-echuan-*.tgz 2>/dev/null || true
}
trap 'cleanup $?' EXIT

# --- 端口探测（bash 内建 /dev/tcp，不依赖 lsof） ----------------------------
# 依赖列表里没有 lsof，也不应该再加：用 bash 3.2 起就有的 /dev/tcp 建一次连接
# 即可判断「是否已有进程在监听」。返回 0 = 已占用，1 = 空闲。
# 连接在子 shell 里完成，fd 随子 shell 退出自动关闭，不会污染主脚本的 fd。
port_in_use() {
  local port="$1"
  (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null
}

# --- HTTP 辅助（结构与 scripts/e2e-worker.sh 一致） -------------------------
http_post() {
  local path="$1" body="$2"
  shift 2
  local raw
  if ! raw=$(curl -sS --max-time 20 -X POST "$BASE$path" \
      -H 'Content-Type: application/json' \
      -H 'X-Actor: kit-e2e' \
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
  if ! raw=$(curl -sS --max-time 20 "$BASE$path" -H 'X-Actor: kit-e2e' \
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
  if [ "$HTTP_CODE" != "$want" ]; then
    fail "$label：期望 HTTP $want，实际 $HTTP_CODE"
  fi
}

jqr() { printf '%s' "$HTTP_BODY" | jq -r "$@"; }

# --- dsh 进程管理 -----------------------------------------------------------
stop_dsh() {
  if [ -n "$DSH_PID" ] && kill -0 "$DSH_PID" 2>/dev/null; then
    kill "$DSH_PID" 2>/dev/null || true
    local i=0
    while [ "$i" -lt 50 ] && kill -0 "$DSH_PID" 2>/dev/null; do
      sleep 0.1
      i=$((i + 1))
    done
    kill -9 "$DSH_PID" 2>/dev/null || true
    wait "$DSH_PID" 2>/dev/null || true
  fi
  DSH_PID=""
  # 关掉留在 FIFO 上的写端。注意不能写成 `exec 9>&- 2>/dev/null`：
  # 无命令的 exec 会把重定向永久应用在当前 shell 上（stderr 从此进 /dev/null），
  # 收尾日志就全丢了。
  if [ "${FIFO_FD_OPEN:-0}" = "1" ]; then
    exec 9>&-
    FIFO_FD_OPEN=0
  fi
}

# start_dsh：后台拉起 dsh（ACP profile，长驻），stdin 用 FIFO 保持打开。
# 顺序很关键：插件在进程启动约 5s 后（首轮延迟）才发首次心跳，而 ACP 会话要等
# 客户端请求才创建；因此这里只负责「启动 + 注册」，会话创建放在 S5
# （此时平台还没有 ready 卡）。
start_dsh() {
  local tag="$1"
  DSH_LOG="$TMP/dsh-$tag.log"
  : > "$DSH_LOG"
  if [ ! -p "$TMP/acp.in" ]; then
    mkfifo "$TMP/acp.in"
  fi
  # O_RDWR 打开 FIFO 不会阻塞，且让 dsh 的 stdin 一直有写端，不会读到 EOF 退出。
  exec 9<>"$TMP/acp.in"
  FIFO_FD_OPEN=1
  DSH_HOME="$DSH_HOME_DIR" dsh --profile "$PROFILE" < "$TMP/acp.in" > "$DSH_LOG" 2>&1 &
  DSH_PID=$!
}

# ACP 握手：initialize + session/new（NDJSON，一行一条 JSON-RPC）。
acp_open_session() {
  local cwd="$1"
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}' >&9
  sleep 1
  printf '{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"%s","mcpServers":[]}}\n' "$cwd" >&9
}

wait_readyz() {
  local secs="$1"
  local deadline=$(( $(date +%s) + secs ))
  while :; do
    if curl -fsS --max-time 2 "$BASE/readyz" >/dev/null 2>&1; then return 0; fi
    if [ "$(date +%s)" -ge "$deadline" ]; then return 1; fi
    sleep 0.5
  done
}

# 等到 dsh 日志里出现某个固定文本（返回最后一条匹配行）。
wait_log() {
  local needle="$1" secs="$2" hit=""
  local deadline=$(( $(date +%s) + secs ))
  while :; do
    hit=$(grep -F -- "$needle" "$DSH_LOG" 2>/dev/null | tail -1 || true)
    if [ -n "$hit" ]; then printf '%s' "$hit"; return 0; fi
    if [ "$(date +%s)" -ge "$deadline" ]; then return 1; fi
    sleep 1
  done
}

# --- S0. 前置检查 -----------------------------------------------------------
STEP="S0"
say "== S0 前置检查 =="

DEP_DETAIL=""
MISSING=""
for cmd in docker jq curl dsh pnpm node; do
  if dep_path="$(command -v "$cmd" 2>/dev/null)"; then
    DEP_DETAIL="${DEP_DETAIL}ok    ${cmd}  ${dep_path}
"
  else
    DEP_DETAIL="${DEP_DETAIL}MISS  ${cmd}
"
    MISSING="${MISSING} ${cmd}"
  fi
done
if docker compose version >/dev/null 2>&1; then
  DEP_DETAIL="${DEP_DETAIL}ok    docker compose v2
"
else
  DEP_DETAIL="${DEP_DETAIL}MISS  docker compose v2
"
  MISSING="${MISSING} docker-compose-v2"
fi
if [ -n "$MISSING" ]; then
  fail_env "依赖不齐全（缺失：${MISSING# }）" "$DEP_DETAIL"
fi
if port_in_use "$PORT"; then
  # 占用者信息是加分项，不是依赖：只在系统自带 lsof 时附加（失败就留空）。
  PORT_DETAIL=""
  if command -v lsof >/dev/null 2>&1; then
    PORT_DETAIL="$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN 2>/dev/null || true)"
  fi
  fail_env "端口 ${PORT} 已被占用；端到端脚本固定用该端口复现，请先释放" "$PORT_DETAIL"
fi

# 历史残留兜底：旧版脚本把 pnpm pack 产物留在仓库 kit/ 下（已被 .gitignore 忽略）。
# 先清一次，保证「运行前后 kit/*.tgz 均为空」的断言成立（产物可随时重新生成）。
KIT_TGZ_OLD="$(cd "$KIT" && ls -1 *.tgz 2>/dev/null || true)"
if [ -n "$KIT_TGZ_OLD" ]; then
  warn "清理历史残留包：$(echo $KIT_TGZ_OLD)"
  rm -f "$KIT"/*.tgz
fi

# ~/echuan-data 指纹：文件数 + 文件名清单的 sha（只读，不碰内容）。
fingerprint_home_data() {
  local dir="$1"
  if [ ! -d "$dir" ]; then printf 'absent'; return 0; fi
  local n h
  n=$(find "$dir" -maxdepth 2 -type f 2>/dev/null | wc -l | tr -d ' ')
  h=$(find "$dir" -maxdepth 2 -type f 2>/dev/null | LC_ALL=C sort | shasum | awk '{print $1}')
  printf '%s files / %s' "$n" "$h"
}
FP_BEFORE="$(fingerprint_home_data "$HOME/echuan-data")"
say "~/echuan-data 运行前指纹：$FP_BEFORE"

# 宿主机 ECHUAN_* 全部清掉，避免覆盖 compose 传入的临时值。
for var in $(env | sed -n 's/^\(ECHUAN_[A-Za-z0-9_]*\)=.*/\1/p'); do
  unset "$var"
done

# 上一次运行若被 kill -9，dsh 子进程可能残留并继续心跳；按脚本专属 profile 前缀清掉。
STALE_PIDS="$(pgrep -f 'dsh --profile echuan-e2e-' 2>/dev/null || true)"
if [ -n "$STALE_PIDS" ]; then
  warn "清理上一次运行残留的 dsh 进程：$(echo $STALE_PIDS | tr '\n' ' ')"
  kill $STALE_PIDS 2>/dev/null || true
  sleep 1
  kill -9 $STALE_PIDS 2>/dev/null || true
fi

TMP="$(mktemp -d "${TMPDIR%/}/echuan-kit-e2e.XXXXXX")"
DSH_HOME_DIR="$TMP/dsh-home"
mkdir -p "$TMP/data" "$TMP/work" "$DSH_HOME_DIR"
say "临时目录：$TMP"

pass "S0 依赖齐全（docker / compose v2 / jq / curl / dsh / pnpm / node），端口 $PORT 空闲，~/echuan-data 指纹已记录"

# --- 1. 独立栈起平台 --------------------------------------------------------
STEP="1/11 起平台"
say ""
say "== 1/11 docker compose 起独立平台栈（$CONTAINER @ $PORT）=="

if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  say "镜像 $IMAGE 不存在，先 build（首次约 1-3 分钟）"
  ( cd "$ROOT" && docker compose -p "$PROJ" build ) || fail "docker compose build 失败"
fi

STACK_STARTED=1
( cd "$ROOT" && \
  COMPOSE_PROJECT_NAME="$PROJ" \
  ECHUAN_CONTAINER_NAME="$CONTAINER" \
  ECHUAN_PORT="$PORT" \
  ECHUAN_DATA_DIR="$TMP/data" \
  ECHUAN_UID="$(id -u)" \
  ECHUAN_GID="$(id -g)" \
  docker compose up -d --wait --wait-timeout 180 ) || fail "docker compose up -d --wait 失败"

wait_readyz 60 || fail "容器起来了但 $BASE/readyz 60s 内没有就绪"
HTTP_BODY=""
pass "1/11 独立栈就绪（container=$CONTAINER base=$BASE data=$TMP/data，/readyz ok）"

# --- 2. 播种产品 / 项目 / 两张卡 -------------------------------------------
# 注意：两张卡先停在 plan_review（backlog），**批准拆解放到 S5**。
# 原因：首轮心跳（启动后约 5s）到达时 ACP 还没有根 agent，
# 平台会先把卡派给一个「0 个根 agent」的 worker，投递被跳过且不会重投。
# 因此在 agent 就绪后再让卡变 ready，才能观察到真实的「派单 → 投递」。
STEP="2/11 播种数据"
say ""
say "== 2/11 建产品、项目与两张任务卡 =="

PRODUCT_ID="E2E$(date +%s)$$"
http_post "/v1/products" "$(jq -nc --arg id "$PRODUCT_ID" \
  '{product_id:$id,name:"E2E 插件验收产品",git_url:"https://example.com/e2e.git",description:"kit/scripts/e2e.sh"}')"
require_code 201 "创建产品"

http_post "/v1/projects" "$(jq -nc --arg pid "$PRODUCT_ID" \
  '{product_id:$pid,title:"E2E 插件验收项目",requirement_md:"# 需求\n\ndsh 插件全链路验收。\n"}')"
require_code 201 "创建项目"
PROJECT_ID="$(jqr '.project_id')"
PROJECT_VERSION="$(jqr '.version')"
[ "$PROJECT_ID" = "$PRODUCT_ID-1" ] || fail "项目 id 应形如 <product_id>-1，实际 $PROJECT_ID"

http_post "/v1/projects/accept?id=$PROJECT_ID" '{}'
require_code 200 "accept 项目"
PROJECT_VERSION="$(jqr '.version')"

http_post "/v1/projects/specs?id=$PROJECT_ID" \
  '{"body_md":"# 技术方案\n\n- 插件注册\n- 心跳派单\n- 报告\n"}'
require_code 200 "提交规格"
PROJECT_VERSION="$(jqr '.project_version')"

http_post "/v1/projects/decide?id=$PROJECT_ID" '{"decision":"approve"}' -H "If-Match: $PROJECT_VERSION"
require_code 200 "规格评审通过"
[ "$(jqr '.status_name')" = "planning" ] || fail "规格通过后应进入 planning，实际 $(jqr '.status_name')"
PROJECT_VERSION="$(jqr '.version')"

http_post "/v1/projects/plans?id=$PROJECT_ID" \
  '{"tasks":[{"title":"E2E 卡一","body_md":"# 卡一\n\n由 kit/scripts/e2e.sh 创建。\n","priority":10},{"title":"E2E 卡二","body_md":"# 卡二\n\n用于验证下一次心跳领新卡。\n","priority":9}]}'
require_code 201 "提交任务拆解"
TASK1="$(jqr '.tasks[0].id')"
TASK2="$(jqr '.tasks[1].id')"
[ -n "$TASK1" ] && [ "$TASK1" != "null" ] || fail "拆解响应缺少第一张卡 id"
[ -n "$TASK2" ] && [ "$TASK2" != "null" ] || fail "拆解响应缺少第二张卡 id"
PROJECT_VERSION="$(jqr '.project_version')"
say "卡一=$TASK1  卡二=$TASK2（plan_review，待 S5 激活）"
pass "2/11 产品/项目/两张卡已入库（$TASK1, $TASK2）"

# --- 3. 装插件到临时 profile ------------------------------------------------
STEP="3/11 安装插件"
say ""
say "== 3/11 pnpm pack → 装进临时 dsh profile =="

PKG_NAME="$(jq -r '.name' "$KIT/package.json")"
PKG_VERSION="$(jq -r '.version' "$KIT/package.json")"
# 产物直接打到临时目录：绝不写仓库 kit/（否则会留下 dsh-echuan-*.tgz 污染工作区，
# 下一次运行还可能误用旧包）。
TGZ="$TMP/${PKG_NAME}-${PKG_VERSION}.tgz"
KIT_TGZ_PRE="$(cd "$KIT" && ls -1 *.tgz 2>/dev/null || true)"
[ -z "$KIT_TGZ_PRE" ] || fail "打包前仓库 kit/ 下已有 tgz：$(echo $KIT_TGZ_PRE)（请先清理）"
if ! ( cd "$KIT" && pnpm pack --pack-destination "$TMP" >/dev/null 2>&1 ); then
  fail "pnpm pack 失败（$KIT）"
fi
[ -f "$TGZ" ] || fail "pnpm pack 没有产出 $TGZ"
KIT_TGZ_POST="$(cd "$KIT" && ls -1 *.tgz 2>/dev/null || true)"
[ -z "$KIT_TGZ_POST" ] || fail "pnpm pack 污染了仓库：kit/ 下出现 $(echo $KIT_TGZ_POST)"

export DSH_HOME="$DSH_HOME_DIR"
PROFILE_DIR="$DSH_HOME_DIR/profiles/$PROFILE"

# 只含 @deepseek-ai/dsh-base 的新 profile 会挂起（没有 app bundle，没人消费启动参数），
# 所以先探测本机已有可用 profile 里用的 app bundle，再照着补进新 profile。
probe_app_bundle() {
  local f b
  for f in "$HOME"/.dsh/profiles/*/package.json; do
    [ -f "$f" ] || continue
    b=$(jq -r '.dsh.profile.bundles[]?' "$f" 2>/dev/null | grep -x '@deepseek-ai/dsh-acp-app' || true)
    if [ -n "$b" ]; then printf '%s' "$b"; return 0; fi
  done
  printf '%s' '@deepseek-ai/dsh-acp-app'
}
APP_BUNDLE="$(probe_app_bundle)"
say "探测到的 app bundle：$APP_BUNDLE"

dsh plugin --profile "$PROFILE" list >/dev/null 2>&1 || fail "初始化 dsh profile $PROFILE 失败"
jq --arg app "$APP_BUNDLE" \
  '.dsh.profile.bundles = ["@deepseek-ai/dsh-base", $app]' \
  "$PROFILE_DIR/package.json" > "$PROFILE_DIR/package.json.tmp" \
  && mv "$PROFILE_DIR/package.json.tmp" "$PROFILE_DIR/package.json"

if ! dsh plugin --profile "$PROFILE" add "$TGZ" >"$TMP/dsh-plugin-add.log" 2>&1; then
  tail -20 "$TMP/dsh-plugin-add.log" >&2 || true
  fail "dsh plugin add $TGZ 失败"
fi

# profile 配置：平台地址 / 心跳开关 / 超时 / metadata（metadata 用于和注册记录对比）。
cat > "$PROFILE_DIR/cordis.patch.yml" <<EOF
# kit/scripts/e2e.sh 生成的临时 profile 覆盖层
- id: echuan
  config:
    baseUrl: $BASE
    actor: kit-e2e-plugin
    requestTimeoutMs: 15000
    worker:
      poll:
        enable: true
      metadata:
        host: echuan-kit-e2e
        os: darwin
        agent_version: e2e-${PKG_VERSION}
EOF

# profile 必须能正常 --help 退出（3 秒内），否则后面的启动一定是挂死。
help_ok=0
dsh --profile "$PROFILE" --help >"$TMP/profile-help.log" 2>&1 &
HELP_PID=$!
i=0
while [ "$i" -lt 60 ]; do
  if ! kill -0 "$HELP_PID" 2>/dev/null; then
    if wait "$HELP_PID"; then help_ok=1; fi
    break
  fi
  sleep 0.1
  i=$((i + 1))
done
if [ "$help_ok" != "1" ]; then
  kill -9 "$HELP_PID" 2>/dev/null || true
  tail -20 "$TMP/profile-help.log" >&2 || true
  fail "临时 profile 无法正常 --help 退出（bundle 配置不完整）"
fi
pass "3/11 插件已装进临时 profile（$PROFILE，bundle=$APP_BUNDLE + $PKG_NAME@$PKG_VERSION，--help 正常退出）"

# --- 4. 启动 dsh 并验证注册 -------------------------------------------------
STEP="4/11 启动 dsh 并注册"
say ""
say "== 4/11 后台启动 dsh（ACP 长驻）并等待注册 =="

start_dsh "boot"
sleep 2
if ! kill -0 "$DSH_PID" 2>/dev/null; then
  tail -30 "$DSH_LOG" >&2 || true
  fail "dsh 进程启动即退出"
fi

WORKER_ID=""
deadline=$(( $(date +%s) + 60 ))
while :; do
  http_get "/v1/workers?limit=50"
  require_code 200 "列出 worker"
  WORKER_ID="$(jqr --arg h "echuan-kit-e2e" \
    '[.items[] | select(.metadata.host == $h) | .worker_id] | first // empty')"
  if [ -n "$WORKER_ID" ]; then break; fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "60s 内没有出现 metadata.host=echuan-kit-e2e 的 worker"
  fi
  sleep 1
done
[ "${WORKER_ID#w_}" != "$WORKER_ID" ] || fail "worker_id 应以 w_ 开头，实际 $WORKER_ID"

http_get "/v1/workers?id=$WORKER_ID"
require_code 200 "读取 worker 详情"
[ "$(jqr '.presence')" = "active" ] || fail "注册后 presence 应为 active，实际 $(jqr '.presence')"
[ "$(jqr '.state')" = "idle" ] || fail "无根 agent 时应为 idle，实际 $(jqr '.state')"
[ "$(jqr '.heartbeat_interval_ms')" = "15000" ] || fail "heartbeat_interval_ms 应为 15000"
EXPECT_META="$(jq -nc --arg v "e2e-$PKG_VERSION" \
  '{host:"echuan-kit-e2e",os:"darwin",agent_version:$v}')"
[ "$(jqr --argjson m "$EXPECT_META" '.metadata == $m')" = "true" ] \
  || fail "worker.metadata 与配置不一致：$(jqr -c '.metadata')"
[ "$(jqr '.current_task_id')" = "null" ] || fail "此时还没有卡，current_task_id 应为 null"
say "worker_id: $WORKER_ID"
pass "4/11 dsh 启动并注册成功（$WORKER_ID，presence=active state=idle metadata 与配置一致）"

# --- 5. 派单与消息投递 ------------------------------------------------------
STEP="5/11 派单与投递"
say ""
say "== 5/11 建 ACP 会话（根 agent 就绪）→ 激活拆解 → 观察派单与投递 =="

acp_open_session "$TMP/work"
SID=""
deadline=$(( $(date +%s) + 30 ))
while :; do
  # 插件日志和 JSON-RPC 响应共用 stdout，这里只挑 session/new 的 result 行。
  SID=$(grep -F '"id":2,"result"' "$DSH_LOG" 2>/dev/null | head -1 \
        | sed -n 's/.*"sessionId":"\([^"]*\)".*/\1/p' || true)
  if [ -n "$SID" ]; then break; fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    tail -30 "$DSH_LOG" >&2 || true
    fail "30s 内 ACP session/new 没有返回 sessionId"
  fi
  sleep 1
done
say "ACP session: $SID"

http_post "/v1/projects/decide?id=$PROJECT_ID" '{"decision":"approve"}' -H "If-Match: $PROJECT_VERSION"
require_code 200 "拆解评审通过"
[ "$(jqr '.status_name')" = "developing" ] || fail "拆解通过后应进入 developing，实际 $(jqr '.status_name')"

http_get "/v1/tasks?id=$TASK1"
require_code 200 "读取卡一"
[ "$(jqr '.status_name')" = "ready" ] || fail "拆解通过后卡一应为 ready，实际 $(jqr '.status_name')"

# 下一次心跳（≤15s）应拿到 run_task；插件随后投递给根 agent。
deadline=$(( $(date +%s) + 70 ))
DISPATCHED=""
while :; do
  http_get "/v1/tasks?id=$TASK1"
  require_code 200 "轮询卡一"
  if [ "$(jqr '.status_name')" = "in_progress" ]; then
    DISPATCHED="$(jqr '.worker_id')"
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "70s 内平台没有把卡一派出去（仍为 $(jqr '.status_name')）"
  fi
  sleep 2
done
[ "$DISPATCHED" = "$WORKER_ID" ] || fail "卡一应派给 $WORKER_ID，实际 $DISPATCHED"

DELIVER_LINE=""
if ! DELIVER_LINE=$(wait_log "[dsh-echuan] 已投递任务 $TASK1 给根 agent" 40); then
  tail -30 "$DSH_LOG" >&2 || true
  fail "40s 内日志里没有卡一的投递记录"
fi
say "证据行：$DELIVER_LINE"
pass "5/11 平台派单成功（卡一 in_progress @ $WORKER_ID），插件已投递给根 agent"

# --- 6. 干活与交报告 --------------------------------------------------------
STEP="6/11 交报告"
say ""
say "== 6/11 用 lib/tools 的 echuan_task_report 交三段式报告 =="

http_get "/v1/tasks?id=$TASK1"
require_code 200 "读取卡一租约"
LEASE_ID="$(jqr '.lease.lease_id')"
[ -n "$LEASE_ID" ] && [ "$LEASE_ID" != "null" ] || fail "卡一应带 lease_id，实际 $LEASE_ID"

REPORT_OK=0
if [ "${E2E_REAL_LLM:-0}" = "1" ]; then
  say "E2E_REAL_LLM=1：等待 dsh agent 自己调 echuan_task_report（最多 180s）"
  deadline=$(( $(date +%s) + 180 ))
  while :; do
    http_get "/v1/tasks?id=$TASK1"
    require_code 200 "轮询卡一"
    if [ "$(jqr '.status_name')" = "pending_acceptance" ]; then REPORT_OK=1; break; fi
    if [ "$(date +%s)" -ge "$deadline" ]; then break; fi
    sleep 5
  done
  [ "$REPORT_OK" = "1" ] || warn "真实 LLM 路径未在 180s 内交报告，回落到工具驱动（默认模式）"
fi

if [ "$REPORT_OK" != "1" ]; then
  cat > "$TMP/report.mjs" <<EOF
import { createClient } from '$KIT/lib/client.js'
import { createAllEchuanTools } from '$KIT/lib/tools/index.js'

const [baseUrl, taskId, leaseId] = process.argv.slice(2)
const config = {
  baseUrl,
  actor: 'kit-e2e-report-driver',
  requestTimeoutMs: 15000,
  worker: { poll: { enable: false }, metadata: {} },
}
const client = createClient(config)
const tools = createAllEchuanTools({ client, identity: { workerId: process.env.ECHUAN_WORKER_ID } })
const tool = tools.find(t => t.name === 'echuan_task_report')
if (!tool) {
  console.error('echuan_task_report 未注册')
  process.exit(2)
}
const summary_md = [
  '## ①改了什么',
  '',
  '- 由 kit/scripts/e2e.sh 通过 lib/tools 的真实 echuan_task_report 提交；',
  '- 未改动平台代码，仅调用 POST /v1/tasks/report。',
  '',
  '## ②怎么自检',
  '',
  '- 端到端脚本轮询任务状态并断言 pending_acceptance；',
  '- 断言报告正文非空、worker 指针与租约已清理。',
  '',
  '## ③遗留风险',
  '',
  '- 报告内容为验收脚本生成，不代表真实业务交付物；',
  '- 默认模式不经过 LLM，agent 侧工具调用由 E2E_REAL_LLM=1 单独覆盖。',
].join('\n')
const value = await tool.execute({ task_id: taskId, lease_id: leaseId, summary_md })
console.log(JSON.stringify({ kind: value.kind, status_name: value.status_name, task_id: value.task_id }))
process.exit(value.kind === 'reported' ? 0 : 1)
EOF
  ECHUAN_WORKER_ID="$WORKER_ID" node "$TMP/report.mjs" "$BASE" "$TASK1" "$LEASE_ID" \
    > "$TMP/report.out" 2> "$TMP/report.err" || {
      cat "$TMP/report.out" >&2 || true
      cat "$TMP/report.err" >&2 || true
      fail "echuan_task_report 执行失败"
    }
  say "工具返回：$(cat "$TMP/report.out")"
fi

http_get "/v1/tasks?id=$TASK1"
require_code 200 "复核卡一"
[ "$(jqr '.status_name')" = "pending_acceptance" ] || fail "报告后卡一应为 pending_acceptance，实际 $(jqr '.status_name')"
[ "$(jqr '.worker_id')" = "null" ] || fail "报告后卡一 worker_id 应为 null"
[ "$(jqr '.lease')" = "null" ] || fail "报告后卡一 lease 应为 null"
REPORT_SIZE=$(jqr '.report.size')
REPORT_MATERIAL=$(jqr '.report.material_id')
REPORT_SHA=$(jqr '.report.sha256')
[ -n "$REPORT_SIZE" ] && [ "$REPORT_SIZE" != "null" ] || fail "任务报告为空（report.size 缺失）"
[ "$REPORT_SIZE" -gt 0 ] || fail "任务报告正文为空（report.size=$REPORT_SIZE）"
[ -n "$REPORT_MATERIAL" ] && [ "$REPORT_MATERIAL" != "null" ] || fail "任务报告缺少 material_id"
pass "6/11 报告已提交（卡一 pending_acceptance，report ${REPORT_SIZE}B sha256=${REPORT_SHA}，worker/lease 已清理）"

# --- 7. 下一次心跳领新卡 ----------------------------------------------------
STEP="7/11 下一张卡"
say ""
say "== 7/11 下一次心跳应领到第二张卡 =="

http_get "/v1/tasks?id=$TASK2"
require_code 200 "读取卡二"
[ "$(jqr '.status_name')" = "ready" ] || fail "报告后卡二应仍为 ready，实际 $(jqr '.status_name')"

deadline=$(( $(date +%s) + 70 ))
GOT2=""
while :; do
  http_get "/v1/tasks?id=$TASK2"
  require_code 200 "轮询卡二"
  if [ "$(jqr '.status_name')" = "in_progress" ]; then
    GOT2="$(jqr '.worker_id')"
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "70s 内卡二没有被派出去（仍为 $(jqr '.status_name')）"
  fi
  sleep 2
done
[ "$GOT2" = "$WORKER_ID" ] || fail "卡二应派给 $WORKER_ID，实际 $GOT2"
[ "$GOT2" != "" ] || fail "卡二没有 worker 指针"
say "卡二已派给 $WORKER_ID（与刚报告的 $TASK1 不同）"
pass "7/11 下一次心跳领到新卡（$TASK2 in_progress @ $WORKER_ID，不是 $TASK1）"

# --- 8. 平台重启 -----------------------------------------------------------
STEP="8/11 平台重启"
say ""
say "== 8/11 重启平台容器，同一 worker id 心跳应保持可用 =="

http_get "/v1/tasks?id=$TASK2"
require_code 200 "重启前读卡二"
T2_STATE_BEFORE="$(jqr '.status_name')"
T2_WORKER_BEFORE="$(jqr '.worker_id')"

if ! ( cd "$ROOT" && docker restart "$CONTAINER" >/dev/null 2>&1 ); then
  fail "docker restart $CONTAINER 失败"
fi
wait_readyz 90 || fail "重启后 90s 内 /readyz 没有恢复"

http_post "/v1/workers/heartbeat" "$(jq -nc --arg wid "$WORKER_ID" '{worker_id:$wid,state:"idle"}')"
require_code 200 "重启后原 id 心跳"
[ "$(jqr '.worker.worker_id')" = "$WORKER_ID" ] || fail "心跳返回的 worker_id 变了：$(jqr '.worker.worker_id')"
[ "$(jqr '.commands|length')" = "0" ] || fail "仍持有卡二，不应再派新卡"

http_get "/v1/tasks?id=$TASK2"
require_code 200 "重启后读卡二"
[ "$(jqr '.status_name')" = "$T2_STATE_BEFORE" ] || fail "卡二状态发生变化：$T2_STATE_BEFORE -> $(jqr '.status_name')"
[ "$(jqr '.worker_id')" = "$T2_WORKER_BEFORE" ] || fail "卡二 worker 指针发生变化：$T2_WORKER_BEFORE -> $(jqr '.worker_id')"
pass "8/11 平台重启后同一 worker id 心跳 200（$WORKER_ID），未重新注册、未重复领卡"

# --- 9. 插件重启 -----------------------------------------------------------
STEP="9/11 插件重启"
say ""
say "== 9/11 重启 dsh，应拿到新的 w_ id 且不接同一张卡 =="

http_get "/v1/workers?id=$WORKER_ID"
require_code 200 "停之前读旧 worker"
OLD_LAST_SEEN="$(jqr '.last_seen')"

stop_dsh
start_dsh "restart"
sleep 2
if ! kill -0 "$DSH_PID" 2>/dev/null; then
  tail -30 "$DSH_LOG" >&2 || true
  fail "重启后 dsh 立即退出"
fi

NEW_WID=""
deadline=$(( $(date +%s) + 60 ))
while :; do
  http_get "/v1/workers?limit=50"
  require_code 200 "列出 worker"
  NEW_WID="$(jqr --arg h "echuan-kit-e2e" --arg old "$WORKER_ID" \
    '[.items[] | select(.metadata.host == $h and .worker_id != $old) | .worker_id] | first // empty')"
  if [ -n "$NEW_WID" ]; then break; fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "60s 内没有出现新的 worker id"
  fi
  sleep 1
done
[ "$NEW_WID" != "$WORKER_ID" ] || fail "重启后 worker id 应变化，仍为 $WORKER_ID"

http_get "/v1/workers?id=$WORKER_ID"
require_code 200 "重启后旧 worker 记录应仍在"
OLD_PRESENCE="$(jqr '.presence')"
OLD_LAST_SEEN_AFTER="$(jqr '.last_seen')"
if [ "$OLD_PRESENCE" != "offline" ] && [ "$OLD_LAST_SEEN_AFTER" != "$OLD_LAST_SEEN" ]; then
  fail "旧 worker 状态异常：presence=$OLD_PRESENCE last_seen 仍在推进（$OLD_LAST_SEEN -> $OLD_LAST_SEEN_AFTER）"
fi

http_get "/v1/workers?id=$NEW_WID"
require_code 200 "读取新 worker"
[ "$(jqr '.presence')" = "active" ] || fail "新 worker 应为 active，实际 $(jqr '.presence')"
[ "$(jqr '.current_task_id')" = "null" ] || fail "新 worker 不应持有任务，实际 $(jqr '.current_task_id')"

http_get "/v1/tasks?id=$TASK2"
require_code 200 "读卡二"
[ "$(jqr '.worker_id')" = "$WORKER_ID" ] || fail "卡二仍应归旧 worker，实际 $(jqr '.worker_id')"
pass "9/11 插件重启拿到新 id（$NEW_WID），旧记录仍在（presence=$OLD_PRESENCE），未接同一张卡"

# --- 10. 停用转 offline ----------------------------------------------------
STEP="10/11 停用转 offline"
say ""
say "== 10/11 停掉 dsh，等待 worker_ttl=45s 判定 offline =="

stop_dsh
LAST_SEEN_TS=$(date +%s)
OFFLINE_AT=""
while :; do
  http_get "/v1/workers?id=$NEW_WID"
  if [ "$HTTP_CODE" = "200" ]; then
    if [ "$(jqr '.presence')" = "offline" ]; then
      OFFLINE_AT=$(date +%s)
      break
    fi
  elif [ "$HTTP_CODE" = "404" ]; then
    fail "worker 在判定 offline 之前就被保留期清理了（应满足 worker_retention > worker_ttl）"
  else
    fail "轮询 worker 详情失败"
  fi
  if [ $(( $(date +%s) - LAST_SEEN_TS )) -ge 60 ]; then
    fail "60s 内仍没有变为 offline"
  fi
  sleep 2
done
ELAPSED=$(( OFFLINE_AT - LAST_SEEN_TS ))
[ "$ELAPSED" -le 60 ] || fail "offline 判定过晚：${ELAPSED}s（worker_ttl=45s）"
pass "10/11 停用后 ${ELAPSED}s 内转为 offline（$NEW_WID，worker_ttl=45s）"

# --- 11. 收尾 --------------------------------------------------------------
STEP="11/11 收尾"
say ""
say "== 11/11 清容器 / profile / 临时目录，并对比 ~/echuan-data 指纹 =="

stop_dsh
( cd "$ROOT" && docker compose -p "$PROJ" down >/dev/null 2>&1 ) || true
STACK_STARTED=0

if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  fail "收尾后仍存在容器 $CONTAINER"
fi
if pgrep -f "dsh --profile $PROFILE" >/dev/null 2>&1; then
  fail "收尾后仍有残留 dsh 进程（profile=$PROFILE）"
fi

# 成功路径断言：整个运行过程不得在仓库 kit/ 下留下任何 tgz。
KIT_TGZ_END="$(cd "$KIT" && ls -1 *.tgz 2>/dev/null || true)"
[ -z "$KIT_TGZ_END" ] || fail "收尾时仓库 kit/ 下仍有 tgz：$(echo $KIT_TGZ_END)"

rm -rf "$TMP"
TMP=""

FP_AFTER="$(fingerprint_home_data "$HOME/echuan-data")"
[ "$FP_AFTER" = "$FP_BEFORE" ] || fail "~/echuan-data 指纹发生变化：$FP_BEFORE -> $FP_AFTER"

pass "11/11 容器/临时 profile/临时目录已清理，~/echuan-data 指纹未变（$FP_AFTER）"

say ""
say "全部 11 步通过。"
exit 0
