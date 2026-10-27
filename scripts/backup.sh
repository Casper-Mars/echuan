#!/usr/bin/env bash
#
# echuan 备份脚本（宿主机执行）
#
# 产物：$ECHUAN_BACKUP_ROOT/echuan-YYYYmmdd-HHMMSS/
#   ├── state.db        SQLite 一致性快照（VACUUM INTO）
#   ├── objects/        素材对象（rsync -a）
#   ├── workspaces/     任务工作区（默认包含，可用 ECHUAN_BACKUP_WORKSPACES=0 排除）
#   └── manifest.txt    时间、版本、校验和、文件数
#
# 可配置环境变量：
#   ECHUAN_DATA_DIR           数据目录，默认 $HOME/echuan-data
#   ECHUAN_BACKUP_ROOT        备份根目录，默认 $HOME/echuan-backups
#   ECHUAN_BACKUP_WORKSPACES  1/0，默认 1
#   ECHUAN_CONTAINER_NAME     容器名，默认 echuan
#   ECHUAN_BASE_URL           用于记录 /version，默认 http://127.0.0.1:8720
#
# 同一秒内重复执行（cron 与手工撞车、脚本被连跑）不会互相踩踏：
# 若 echuan-YYYYmmdd-HHMMSS 已存在，自动改用 -2、-3 … 后缀（最多 99 份），
# 绝不覆盖或删除已有备份；输出里会打印实际使用的目录。
# 并发执行同样安全：抢输的进程会继续尝试下一个后缀，各自得到独立目录。
#
# 退出码：0 成功；非 0 失败（不会留下半成品目录）。

set -euo pipefail
# bash 3.2（macOS 自带）默认不把 ERR trap 传进函数内部，必须开 errtrace，
# 否则函数内的失败只退出、不触发清理，会留下半成品目录。
set -o errtrace

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
INCLUDE_WORKSPACES="${ECHUAN_BACKUP_WORKSPACES:-1}"
CONTAINER="${ECHUAN_CONTAINER_NAME:-echuan}"
BASE_URL="${ECHUAN_BASE_URL:-http://127.0.0.1:${ECHUAN_PORT:-8720}}"

START_TS="$(date +%s)"
STAMP="$(date +%Y%m%d-%H%M%S)"
DEST_BASE="$BACKUP_ROOT/echuan-$STAMP"
DEST=""

log()  { printf '[backup] %s\n' "$*"; }
fail() { printf '[backup] ERROR: %s\n' "$*" >&2; exit 1; }

# 失败但保留产物（完整性校验失败等）：产物是排查依据，不能删。
fail_keep() {
  printf '[backup] ERROR: %s\n' "$*" >&2
  printf '[backup] 已保留本次产物供排查：%s\n' "${DEST:-未创建}" >&2
  trap - ERR INT TERM
  exit 1
}

# CREATED 只在本脚本成功 mkdir 出目录后置 1；清理逻辑据此判断「这个目录
# 是不是本次运行创建的」，避免误删上一次/并发运行留下的备份。
CREATED=0
cleanup_on_error() {
  local rc="${1:-1}"
  trap - ERR INT TERM
  if [ "$CREATED" = "1" ] && [ -n "$DEST" ]; then
    case "$DEST" in
      "$BACKUP_ROOT"/echuan-*)
        rm -rf -- "$DEST"
        log "已清理本次未完成的备份目录 $DEST"
        ;;
      *)
        log "警告：$DEST 不在备份根目录 $BACKUP_ROOT 下，跳过清理"
        ;;
    esac
  fi
  exit "$rc"
}
trap 'cleanup_on_error $?' ERR
trap 'cleanup_on_error 130' INT
trap 'cleanup_on_error 143' TERM

escape_sql() { printf '%s' "$1" | sed "s/'/''/g"; }

have_host_sqlite() { command -v sqlite3 >/dev/null 2>&1; }
container_running() {
  docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -qx true
}

[ -f "$DATA_DIR/state.db" ] || fail "找不到数据库：$DATA_DIR/state.db（可用 ECHUAN_DATA_DIR 指定数据目录）"

mkdir -p "$BACKUP_ROOT"

# 选一个可用的目录名并「原子占位」，探测与创建在同一轮里完成。
# 用 mkdir（非 -p）占位：并发进程可能同时探测到同一个候选名，但 mkdir 的
# 原子性保证只有一个成功；抢输的一方不退出，而是继续尝试下一个后缀
# （-2、-3 … -99），因此并发执行也能各自拿到独立目录。已有目录永不被
# 覆盖或删除；重试之间无需 sleep，mkdir 本身就是原子的。
_n=1
DEST=""
while [ "$_n" -le 99 ]; do
  if [ "$_n" -eq 1 ]; then _cand="$DEST_BASE"; else _cand="$DEST_BASE-$_n"; fi
  if [ ! -e "$_cand" ] && mkdir -- "$_cand" 2>/dev/null; then
    DEST="$_cand"
    break
  fi
  _n=$((_n + 1))
done
if [ -z "$DEST" ]; then
  fail "无法创建备份目录：已有 99 份同秒备份（$DEST_BASE*），或 $BACKUP_ROOT 不可写；未删除任何已有备份，请稍后重试"
fi
CREATED=1
[ "$DEST" = "$DEST_BASE" ] || log "目标目录名已被占用（串行重复或并发执行），本次使用 $DEST（不覆盖、不删除已有备份）"
log "数据目录：$DATA_DIR"
log "备份目录：$DEST"

# ---- 1. SQLite 一致性快照 ---------------------------------------------------
# VACUUM INTO 在只读连接上执行，得到的是事务一致的完整副本；
# 服务运行中也可以安全备份（不需要停容器）。
DB_SRC="$DATA_DIR/state.db"
DB_DST="$DEST/state.db"

if have_host_sqlite; then
  log "使用宿主机 sqlite3 生成快照"
  sqlite3 "file:$DB_SRC?mode=ro" "VACUUM INTO '$(escape_sql "$DB_DST")';"
  [ -s "$DB_DST" ] || fail_keep "快照文件为空：$DB_DST"

  # ---- 2. 校验快照完整性（宿主机路径） ----
  RESULT="$(sqlite3 "$DB_DST" 'PRAGMA integrity_check;')"
  [ "$RESULT" = "ok" ] || fail_keep "快照完整性校验失败：$RESULT"
  log "integrity_check = ok"
else
  container_running || fail "宿主机没有 sqlite3，且容器 $CONTAINER 未运行；无法生成快照"
  log "宿主机无 sqlite3，回退到容器内 sqlite3"
  REMOTE_SNAP="/data/.echuan-backup-$$-$_n.db"
  docker exec "$CONTAINER" sqlite3 /data/state.db "VACUUM INTO '$REMOTE_SNAP';"

  # ---- 2. 校验快照完整性（回退路径：在容器内对临时快照校验） ----
  RESULT="$(docker exec "$CONTAINER" sqlite3 "$REMOTE_SNAP" 'PRAGMA integrity_check;' | tr -d '\r')"
  if [ "$RESULT" != "ok" ]; then
    docker exec "$CONTAINER" rm -f "$REMOTE_SNAP" || true
    fail_keep "容器内快照完整性校验失败：$RESULT"
  fi
  log "integrity_check = ok（容器内）"

  docker cp "$CONTAINER:$REMOTE_SNAP" "$DB_DST"
  docker exec "$CONTAINER" rm -f "$REMOTE_SNAP" || true
  [ -s "$DB_DST" ] || fail_keep "快照文件为空：$DB_DST"
fi

# ---- 3. 素材对象与工作区 ----------------------------------------------------
sync_dir() {
  local src="$1" dst="$2"
  mkdir -p "$dst"
  if [ -d "$src" ]; then
    rsync -a "$src/" "$dst/"
  else
    log "目录不存在，跳过：$src"
  fi
}

log "同步 objects/"
sync_dir "$DATA_DIR/objects" "$DEST/objects"

if [ "$INCLUDE_WORKSPACES" = "1" ]; then
  log "同步 workspaces/"
  sync_dir "$DATA_DIR/workspaces" "$DEST/workspaces"
else
  log "按配置跳过 workspaces（ECHUAN_BACKUP_WORKSPACES=0）"
fi

# ---- 4. 清单 ----------------------------------------------------------------
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

APP_VERSION="unknown"
if command -v curl >/dev/null 2>&1; then
  APP_VERSION="$(curl -fsS --max-time 3 "$BASE_URL/version" 2>/dev/null || echo unknown)"
fi

{
  echo "backup_time=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "host=$(hostname)"
  echo "data_dir=$DATA_DIR"
  echo "include_workspaces=$INCLUDE_WORKSPACES"
  echo "app_version=$APP_VERSION"
  echo "state_db_sha256=$(sha256_of "$DB_DST")"
  echo "state_db_bytes=$(wc -c < "$DB_DST" | tr -d ' ')"
  echo "objects_files=$(find "$DEST/objects" -type f | wc -l | tr -d ' ')"
  if [ -d "$DEST/workspaces" ]; then
    echo "workspaces_files=$(find "$DEST/workspaces" -type f | wc -l | tr -d ' ')"
  fi
} > "$DEST/manifest.txt"

DURATION=$(( $(date +%s) - START_TS ))
SIZE="$(du -sh "$DEST" | awk '{print $1}')"

trap - ERR INT TERM
log "完成：$DEST"
log "体积：$SIZE，耗时：${DURATION}s"
sed 's/^/[backup]   /' "$DEST/manifest.txt"
echo "BACKUP_DIR=$DEST"
