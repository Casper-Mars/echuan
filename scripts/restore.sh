#!/usr/bin/env bash
#
# echuan 恢复脚本（宿主机执行）
#
# 流程：校验备份 → compose down → 保留当前数据副本 → 放回库与资料
#       → compose up -d → 轮询 /readyz 直到 200
#
# 用法：
#   scripts/restore.sh /path/to/echuan-backups/echuan-20261027-140000
#   scripts/restore.sh --latest
#
# 可配置环境变量：
#   ECHUAN_DATA_DIR        数据目录，默认 $HOME/echuan-data
#   ECHUAN_BACKUP_ROOT     备份根目录，默认 $HOME/echuan-backups
#   ECHUAN_COMPOSE_FILE    compose 文件，默认 <repo>/docker-compose.yml
#   COMPOSE_PROJECT_NAME   compose 项目名（.env 或环境变量），默认不传 -p，
#                          由 docker compose 按 compose 文件所在目录推导
#   ECHUAN_CONTAINER_NAME  容器名，默认 echuan
#   ECHUAN_PORT            宿主机端口，默认 8720
#   ECHUAN_READY_TIMEOUT   等待 readyz 的秒数，默认 90
#
# 退出码：0 恢复且 readyz=200；非 0 失败（日志会打印最近容器日志）。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# 读取仓库根目录的 .env（compose 使用的同一份配置）。
# 已显式导出的环境变量优先，便于临时覆盖。
load_dotenv() {
  local file="$1" line key value
  [ -f "$file" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in ''|\#*) continue ;; esac
    case "$line" in *=*) ;; *) continue ;; esac
    key="$(printf '%s' "${line%%=*}" | tr -d '[:space:]')"
    value="${line#*=}"
    [ -n "$key" ] || continue
    case "$value" in
      \"*\") value="${value#\"}"; value="${value%\"}" ;;
      \'*\') value="${value#\'}"; value="${value%\'}" ;;
    esac
    if [ -z "${!key+x}" ]; then
      export "$key=$value"
    fi
  done < "$file"
}
load_dotenv "$ROOT_DIR/.env"

DATA_DIR="${ECHUAN_DATA_DIR:-$HOME/echuan-data}"
BACKUP_ROOT="${ECHUAN_BACKUP_ROOT:-$HOME/echuan-backups}"
COMPOSE_FILE="${ECHUAN_COMPOSE_FILE:-$ROOT_DIR/docker-compose.yml}"
CONTAINER="${ECHUAN_CONTAINER_NAME:-echuan}"
PORT="${ECHUAN_PORT:-8720}"
READY_TIMEOUT="${ECHUAN_READY_TIMEOUT:-90}"
HEALTH_URL="http://127.0.0.1:${PORT}"

# 与 compose 手工执行保持同一上下文：
#   - 能读 .env 时显式传 --env-file（脚本从任何 cwd 调用都能解析到同一份变量）
#   - 设置了 COMPOSE_PROJECT_NAME（.env 或 export）时显式传 -p
# 两者都不设时行为与 `docker compose -f <file>` 完全一致，不改变现有部署。
COMPOSE_PROJECT="${COMPOSE_PROJECT_NAME:-}"
COMPOSE_ENV_FILE=""
if [ -f "$ROOT_DIR/.env" ]; then
  COMPOSE_ENV_FILE="$ROOT_DIR/.env"
fi

compose() {
  local args=()
  if [ -n "$COMPOSE_ENV_FILE" ]; then args+=(--env-file "$COMPOSE_ENV_FILE"); fi
  if [ -n "$COMPOSE_PROJECT" ]; then args+=(-p "$COMPOSE_PROJECT"); fi
  # bash 3.2 + set -u：空数组直接展开会报 unbound，必须用 ${a[@]+...}
  docker compose ${args[@]+"${args[@]}"} -f "$COMPOSE_FILE" "$@"
}

START_TS="$(date +%s)"
STAMP="$(date +%Y%m%d-%H%M%S)"

log()  { printf '[restore] %s\n' "$*"; }
fail() {
  printf '[restore] ERROR: %s\n' "$*" >&2
  if [ -f "$COMPOSE_FILE" ]; then
    compose logs --tail=80 2>/dev/null || true
  fi
  exit 1
}

[ -f "$COMPOSE_FILE" ] || fail "找不到 compose 文件：$COMPOSE_FILE"

BACKUP_DIR="${1:-}"
if [ -z "$BACKUP_DIR" ] || [ "$BACKUP_DIR" = "--latest" ]; then
  BACKUP_DIR="$(ls -1dt "$BACKUP_ROOT"/echuan-* 2>/dev/null | head -n 1 || true)"
fi
[ -n "$BACKUP_DIR" ] || fail "未指定备份目录，且 $BACKUP_ROOT 下没有可用备份"
[ -d "$BACKUP_DIR" ] || fail "备份目录不存在：$BACKUP_DIR"
[ -f "$BACKUP_DIR/state.db" ] || fail "备份缺少 state.db：$BACKUP_DIR"

log "备份目录：$BACKUP_DIR"
log "数据目录：$DATA_DIR"

# ---- 1. 校验备份完整性 ------------------------------------------------------
if command -v sqlite3 >/dev/null 2>&1; then
  RESULT="$(sqlite3 "$BACKUP_DIR/state.db" 'PRAGMA integrity_check;')"
  [ "$RESULT" = "ok" ] || fail "备份数据库完整性校验失败：$RESULT"
  log "备份 integrity_check = ok"
else
  log "宿主机无 sqlite3，跳过恢复前完整性校验"
fi

# ---- 2. 停止服务 ------------------------------------------------------------
log "停止 compose 服务"
compose down

# ---- 3. 保留当前数据（可人工回滚） -----------------------------------------
if [ -d "$DATA_DIR" ] && [ -n "$(ls -A "$DATA_DIR" 2>/dev/null || true)" ]; then
  SAFETY="$DATA_DIR.pre-restore-$STAMP"
  mv "$DATA_DIR" "$SAFETY"
  log "当前数据已移动到：$SAFETY"
fi
mkdir -p "$DATA_DIR/objects" "$DATA_DIR/workspaces"

# ---- 4. 放回数据库与资料 ----------------------------------------------------
log "恢复 state.db"
cp -p "$BACKUP_DIR/state.db" "$DATA_DIR/state.db"
# 旧库的 WAL/SHM 必须删除，否则 SQLite 可能拿旧日志覆盖刚恢复的库。
rm -f "$DATA_DIR/state.db-wal" "$DATA_DIR/state.db-shm" "$DATA_DIR/state.db-journal"

log "恢复 objects/"
rsync -a --delete "$BACKUP_DIR/objects/" "$DATA_DIR/objects/"

if [ -d "$BACKUP_DIR/workspaces" ]; then
  log "恢复 workspaces/"
  rsync -a --delete "$BACKUP_DIR/workspaces/" "$DATA_DIR/workspaces/"
else
  log "备份不含 workspaces，保留当前工作区"
fi

# ---- 5. 修正属主 ------------------------------------------------------------
# macOS Docker Desktop：VirtioFS 会把 bind mount 的属主映射给容器运行用户，
# UID 10001 可直接读写，无需（也无法以普通用户身份）chown。
if [ "$(uname -s)" = "Linux" ]; then
  if [ "$(id -u)" -eq 0 ]; then
    chown -R 10001:10001 "$DATA_DIR"
    log "已 chown -R 10001:10001 $DATA_DIR"
  else
    log "警告：Linux 非 root，跳过 chown；请确保数据目录属主与 ECHUAN_UID 一致"
  fi
else
  log "macOS/Docker Desktop：跳过 chown（VirtioFS 自动映射属主）"
fi

# ---- 6. 启动并等待 readyz ---------------------------------------------------
log "启动 compose 服务"
compose up -d

deadline=$(( $(date +%s) + READY_TIMEOUT ))
while :; do
  if curl -fsS --max-time 2 "$HEALTH_URL/readyz" >/dev/null 2>&1; then
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "等待 $HEALTH_URL/readyz 超时（${READY_TIMEOUT}s）"
  fi
  sleep 2
done

DURATION=$(( $(date +%s) - START_TS ))
log "readyz = 200，恢复完成，耗时 ${DURATION}s"
curl -fsS --max-time 3 "$HEALTH_URL/version" || true
echo
echo "RESTORED_FROM=$BACKUP_DIR"
