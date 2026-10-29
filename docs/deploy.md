# echuan 部署与运维手册

本文档覆盖 echuan 的 Docker 化部署：构建、首次上线、配置、日志、备份恢复、升级与排障。
所有命令在仓库根目录执行。

---

## 1. 架构与目录布局

```
 浏览器 / Worker / curl
        │
        │ 127.0.0.1:8720（仅本机）
        ▼
┌────────────────────────── 宿主机 ──────────────────────────┐
│                                                            │
│  ~/echuan-data/  ◄── bind mount ──┐                        │
│    ├── state.db                   │                        │
│    ├── objects/<sha[:2]>/<sha>    │                        │
│    └── workspaces/<task>/         │                        │
│                                   │                        │
│  configs/echuan.container.toml ───┼── 只读挂载             │
│                                   ▼                        │
│                        ┌──────────────────────────────┐    │
│                        │ docker 容器 echuan            │    │
│                        │ uid/gid 10001                │    │
│                        │ /usr/local/bin/echuan        │    │
│                        │ /data（bind mount）           │    │
│                        │ /etc/echuan/echuan.toml (ro) │    │
│                        │ 0.0.0.0:8720                 │    │
│                        └──────────────────────────────┘    │
└────────────────────────────────────────────────────────────┘
```

设计要点：

- **单进程单二进制**：前端构建产物通过 `go:embed` 打进 Go 二进制，运行镜像没有 Node/Go 工具链。
- **数据全部外置**：SQLite、素材对象、任务工作区都落在宿主机目录，删容器 / 换镜像不丢数据。
- **只监听本机**：compose 端口映射为 `127.0.0.1:8720:8720`；对外服务请在前面放反向代理（Nginx/Caddy）。
- **非 root**：容器以 UID/GID `10001` 运行。
- **无 systemd / launchd**：容器由 Docker 的 `restart: unless-stopped` 托管；仓库不再提供服务文件。

数据目录内容：

| 路径 | 内容 | 是否可重建 |
|------|------|-----------|
| `state.db` | 产品、项目、任务、材料元数据、租约 | 否，必须备份 |
| `objects/<sha[:2]>/<sha>` | 需求/方案/报告等素材的原始字节（内容寻址） | 否，必须备份 |
| `workspaces/<task-id>/` | Worker 的任务工作区 | 可由任务重新生成，默认一并备份 |
| `state.db-wal` / `state.db-shm` | SQLite 运行期文件 | 会自动重建 |

---

## 2. 前置条件

- Docker 24+（含 Compose v2）。验证：`docker compose version`
- 可用端口 `8720`（可用 `.env` 的 `ECHUAN_PORT` 覆盖）
- 备份/恢复脚本依赖：`bash`、`rsync`、`curl`、`sqlite3`（macOS 自带；宿主机没有时备份脚本会回退到容器内 `sqlite3`）
- 首次构建需要访问 Docker Hub、npm registry、Go module proxy（国内网络建议先看第 3.5 节）

---

## 3. 首次部署

### 3.1 准备数据目录

默认使用 `$HOME/echuan-data`。自定义时：

```bash
cp .env.example .env
# 编辑 .env，取消注释并设置 ECHUAN_DATA_DIR=/绝对路径/echuan-data

# 当前 shell 也要读到同一份配置，否则下面（以及 chown）会退回默认目录：
set -a; [ -f .env ] && . ./.env; set +a
echo "ECHUAN_DATA_DIR=${ECHUAN_DATA_DIR:-$HOME/echuan-data}"   # 确认路径
mkdir -p "${ECHUAN_DATA_DIR:-$HOME/echuan-data}"
```

> `set -a` 让 `. ./.env` 里的变量自动 `export`，这样 `mkdir`、`chown`、`docker compose`
> 和 `scripts/*.sh` 看到的是同一个目录。已经 `export ECHUAN_DATA_DIR=...` 时可跳过 sourcing。

**属主说明（已实测）**：

- **macOS + Docker Desktop（VirtioFS）**：不需要 `chown`。实测宿主机目录属主为 `501:20`、权限 `700` 时，容器内 UID 10001 仍可创建/写入文件；容器内看到的属主被映射为 `10001:10001`，宿主机侧文件仍归当前用户。**以普通用户执行 `chown 10001:10001` 会失败（macOS 不允许非 root 改属主），也不需要执行。**
- **Linux 宿主机**：需要让数据目录属主与容器用户一致：

  ```bash
  sudo chown -R 10001:10001 "${ECHUAN_DATA_DIR:-$HOME/echuan-data}"
  ```

  或者修改 `.env`：`ECHUAN_UID=$(id -u)`、`ECHUAN_GID=$(id -g)`，让容器以数据目录属主运行。
- **替代方案**：若宿主机文件系统无法满足属主要求（如 NFS root squash），可把 compose 的 `/data` 改为 named volume：

  ```yaml
  volumes:
    - echuan-data:/data
  volumes:
    echuan-data:
  ```

  named volume 首次创建时会继承镜像内 `/data` 的 `10001:10001` 属主。代价是数据不再直接暴露在宿主机目录，备份需改为 `docker run --rm -v ...` 方式。

### 3.2 构建并启动

```bash
make docker-build     # 构建镜像，收尾自动清理 build cache / 悬空镜像（全局操作，见 3.4）
make docker-up        # docker compose up -d
```

等价命令：

```bash
docker compose build
docker compose up -d
```

首次启动会自动创建 `state.db` 并执行迁移。

### 3.3 验证

```bash
curl -fsS http://127.0.0.1:8720/healthz    # {"status":"ok"}
curl -fsS http://127.0.0.1:8720/readyz     # {"status":"ok","checks":{"db":"ok","migration":"ok"},...}
curl -fsS http://127.0.0.1:8720/version
docker inspect -f '{{.State.Status}} {{.Config.User}}' echuan
docker exec echuan id                      # uid=10001 gid=10001
```

`docker compose ps` 的 `STATUS` 应显示 `Up (healthy)`。

> **代理环境说明**：Docker Desktop 会把 `~/.docker/config.json` 里的 `proxies` 注入**每个容器**
> （`HTTP_PROXY/HTTPS_PROXY/ALL_PROXY` 及小写变体，本机还会自动附带一份含 `127.0.0.1` 的 `NO_PROXY`）。
> busybox 的 `wget` 会读 `http_proxy` 并把 `127.0.0.1` 的请求也发给代理，且**完全不认 `NO_PROXY`**，
> 结果代理返回 `502 Bad Gateway`，探针连败直到 `FailingStreak` 超限，容器长期显示 `unhealthy`——
> 而应用本身是好的。镜像 `HEALTHCHECK` 与 compose `healthcheck.test` 都已改为在执行前用
> `env -u` 剥离六个代理变量，正常情况下不会再复现。
>
> 若**仍然**看到 `unhealthy`，按下面顺序自查（不要先怀疑应用）：
>
> ```bash
> docker inspect echuan --format '{{json .State.Health.Log}}' | jq '.[-1]'   # 探针的真实报错
> docker exec echuan sh -c 'wget -q -O /dev/null http://127.0.0.1:8720/readyz; echo $?'  # 带代理（应为 1/502）
> docker exec echuan sh -c 'env -u http_proxy -u https_proxy -u all_proxy \
>   -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY wget -q -O /dev/null http://127.0.0.1:8720/readyz; echo $?'  # 剥离后（应为 0）
> ```
>
> 剥离后仍失败，才是应用/数据目录/迁移的问题（看 `/readyz` 的 `checks` 与容器日志）。
> 另外若宿主用第三方工具以非标准变量名注入代理、或有别的 compose override 覆盖了 `healthcheck`，
> 也可能绕过上面的处理；以 `docker inspect` 里的 `Config.Healthcheck` 为准。

### 3.4 构建清理

`make docker-build` 在构建成功后自动执行：

```bash
docker builder prune -f    # 清空 BuildKit 构建缓存
docker image prune -f      # 清空悬空镜像（Docker 会保留仍被容器引用的镜像）
```

> ⚠️ 这两条都是**全局**操作，影响同一台机器上的**所有**项目，不只 echuan：
> - `docker builder prune -f` 清空的是整个 Docker daemon 的 BuildKit 缓存，其他项目的构建缓存一并消失；
> - `docker image prune -f` 删除所有项目的悬空镜像（仍被容器引用的镜像会被保留，所以正在跑的服务不受影响）。
>
> 因此**日常开发不要跑 `make docker-build`**，用 `docker compose build`（保留缓存）即可；
> 只有发版/交付需要“干净体积/干净缓存”时再执行。构建缓存被清后下一次构建会明显变慢（Go module、npm 包需重新下载）。

构建期 Go module 默认走 `goproxy.cn`（`go.sum` 仍校验完整性），可用 `.env` 的 `DOCKER_BUILD_GOPROXY` 覆盖。

### 3.5 国内网络构建（依赖源加速）

镜像构建会从四个地方下载：基础镜像（Docker Hub）、npm 包、apk 包、Go module。
默认值已经指向国内镜像，无需任何配置即可加速；全部源都可以用 build-arg 覆盖。

| 依赖 | Dockerfile ARG | `.env` 变量 | 默认值 |
|------|----------------|-------------|--------|
| npm / pnpm registry | `NPM_REGISTRY` | `DOCKER_BUILD_NPM_REGISTRY` | `https://registry.npmmirror.com` |
| apk 包 | `APK_MIRROR` | `DOCKER_BUILD_APK_MIRROR` | `mirrors.aliyun.com` |
| Go module | `GOPROXY` | `DOCKER_BUILD_GOPROXY` | `https://goproxy.cn,https://proxy.golang.org,direct` |
| 基础镜像 | `IMAGE_MIRROR` | `DOCKER_BUILD_IMAGE_MIRROR` | 空（Docker Hub 官方） |

实现要点：

- `NPM_REGISTRY` 作用于 `npm install -g pnpm`（`--registry`）、写入 `/root/.npmrc`
  （`npm config set registry` 写入），并作为 `pnpm install --registry` 的显式参数。
  实测 pnpm 11 不再读 `npm_config_registry` 环境变量，所以不能只靠 `ENV`。
- `APK_MIRROR` 在 `apk add` 之前用 `sed` 改写 `/etc/apk/repositories`；改写后仍残留
  `dl-cdn.alpinelinux.org` 会直接报错中止构建，不会静默回落到慢源。
- `IMAGE_MIRROR` 是仓库前缀，镜像名变为 `<前缀><镜像名>`。默认留空是有意为之：
  镜像名不带域名时才能命中 Docker daemon 的 `registry-mirrors` 加速器；显式带域名后加速器不再参与。
- pnpm store、go module、go build 三处使用 BuildKit cache mount，重复构建不会重新下载/编译。

常用组合：

```bash
# 日常：直接用 compose 构建（保留构建缓存，默认已经是国内镜像）
docker compose build

# 发版/交付：make 目标会在构建后清空 build cache（下一次回到冷启动）
make docker-build

# 显式指定
DOCKER_BUILD_NPM_REGISTRY=https://registry.npmmirror.com \
DOCKER_BUILD_APK_MIRROR=mirrors.tuna.tsinghua.edu.cn \
docker compose build

# 基础镜像也换镜像站（注意 library/ 前缀）
docker build --build-arg IMAGE_MIRROR=dockerproxy.net/library/ -t echuan:local .
```

**切回官方源**（排查“是不是镜像站导致的问题”时用）：

> ⚠️ **前提：该组合需要国际网络，或已给构建配好代理。**
> 国内直连 `registry.npmjs.org` 实测约 30–45s 后连接被重置（busybox wget 报 `connection closed prematurely`，退出码 1）——这只能说明“官方源在这里不通”，
> 不能证明镜像站有问题，反而会让排查变量变多。没有国际网络时不要整组切。
>
> 整组切换前先确认代理链路真的通（返回 0 再继续）：
>
> ```bash
> docker run --rm alpine:3.20 sh -c \
>   'wget -T 20 -q -O /dev/null https://registry.npmjs.org/pnpm; echo "exit=$?"'
> ```
>
> **只想验证“故障与某个镜像站无关”时，更可行的是单变量替换**：一次只动一项，其余保持默认。
>
> ```bash
> # 例 1：只把 apk 源切回官方源（apk 包小、官方源在国内通常还能通）
> docker build --build-arg APK_MIRROR= -t echuan:local .
>
> # 例 2：只换 apk 镜像站（阿里云 → 清华），验证是不是某一家镜像站的问题
> docker build --build-arg APK_MIRROR=mirrors.tuna.tsinghua.edu.cn -t echuan:local .
>
> # 例 3：只换 Go 代理，验证 goproxy.cn 是否异常
> docker build --build-arg GOPROXY=https://goproxy.io,direct -t echuan:local .
> ```
>
> 有国际网络/代理时，才用下面的整组切换：

```bash
docker build \
  --build-arg NPM_REGISTRY=https://registry.npmjs.org \
  --build-arg APK_MIRROR= \
  --build-arg GOPROXY=https://proxy.golang.org,direct \
  --build-arg IMAGE_MIRROR= \
  -t echuan:local .
```

或写进 `.env`（compose 会自动透传）：

```ini
DOCKER_BUILD_NPM_REGISTRY=https://registry.npmjs.org
DOCKER_BUILD_APK_MIRROR=
DOCKER_BUILD_GOPROXY=https://proxy.golang.org,direct
DOCKER_BUILD_IMAGE_MIRROR=
```

> `APK_MIRROR` 传空即保持基础镜像自带的 `dl-cdn.alpinelinux.org`。
> 国内的 tuna（`mirrors.tuna.tsinghua.edu.cn`）可替换 `mirrors.aliyun.com`。
> 注意：`make docker-build` 构建成功后会 `docker builder prune -f`，这是**全局**操作，
> 会清掉同机所有项目的构建缓存，下一次构建回到冷启动速度。只是本机重跑、想保留缓存时请直接 `docker compose build`。

### 3.6 构建耗时与「冷/热」缓存

`--no-cache` 与 `docker builder prune` 不是一回事，别混用：

| 操作 | 效果 |
|------|------|
| `--no-cache` / 改 `--build-arg` | **只让层失效重跑**；pnpm store / `go mod` / `go build` 三处 **cache mount 仍然命中** |
| `docker builder prune -f` | 连 cache mount 一起清空，但**全局生效**（同机其他项目遭殃） |

实测参考（macOS arm64 / Docker 27.4，本仓库）：

| 场景 | 耗时 | 说明 |
|------|------|------|
| 全缓存命中（仅改 `VERSION`/`COMMIT`） | 数秒 | 只重跑 COPY / build 层 |
| `--no-cache`（cache mount 仍在） | **约 26s** | 属**半冷**：`go mod download` 仅 1.8s，因为 modcache 是热的 |
| 真冷启动（空 pnpm store + 空 modcache + 空 go-build） | 明显更久 | 需重新下载全部 npm 包与 Go module，通常是分钟级 |

> 所以「`--no-cache` 只花了 26s」并不代表冷启动很快，**它压根没清 cache mount**。

需要测**真冷启动**又不想影响同机其他项目时，用一个独立的 buildx builder（它有自己的缓存存储，
删掉它不影响默认 daemon 的缓存）：

```bash
docker buildx create --name echuan-cold --driver docker-container --use
docker buildx build -t echuan:cold --load .     # 这个 builder 首次构建 ≈ 真冷启动
docker buildx rm echuan-cold                    # 只清掉这个 builder 的缓存
```

> 不要为了「测冷启动」去跑 `docker builder prune -f`——它清的是全局缓存，会影响同机其他项目。

---

## 4. 配置

容器读取只读挂载的 `/etc/echuan/echuan.toml`（`configs/echuan.container.toml`）。
优先级：**环境变量 `ECHUAN_*` > TOML > 内置默认值**。

| 键 | 容器值 | 说明 |
|----|--------|------|
| `listen` | `0.0.0.0:8720` | 容器内必须监听 0.0.0.0 |
| `data_dir` | `/data` | 由 compose bind mount 到宿主机 |
| `workspace_dir` | `/data/workspaces` | 任务工作区 |
| `read_pool_size` | `4` | SQLite 读连接池 |
| `lease_ttl` | `90s` | 任务租约有效期 |
| `max_attempts` | `3` | 单任务最大尝试次数 |
| `poll_timeout` | `25s` | claim 长轮询超时 |
| `review_stale_hours` | `72` | 评审 stale 阈值 |
| `material_max_bytes` | `20971520` | 单素材上限（20 MiB） |
| `log_level` | `info` | `debug/info/warn/error` |

常用环境变量覆盖（写入 `.env` 后 `docker compose up -d` 生效）：

```bash
ECHUAN_LOG_LEVEL=debug              # 覆盖日志级别
ECHUAN_DATA_DIR=$HOME/echuan-data   # 宿主路径；容器内始终是 /data（不要写 /data/...）
ECHUAN_PORT=8720
COMPOSE_PROJECT_NAME=echuan         # 可选：自定 compose 项目名（脚本会按它加 -p）；已有部署不要中途改，否则 compose 会当成另一个项目
```

> 修改 `configs/echuan.container.toml` 后需要重启容器：`docker compose restart echuan`。

---

## 5. 日志

- 应用日志输出到 **stdout**，由 Docker `json-file` 驱动落盘。
- 轮转：`max-size: 10m`、`max-file: 3`（最多约 30 MB）。
- 每条访问日志为一行 JSON，字段：

  `ts`、`level`、`msg`、`request_id`、`actor`、`method`、`path`、`status`、`duration_ms`，业务路由额外带 `subject`（产品/项目/任务 ID）。

查看：

```bash
docker compose logs -f --tail=100
docker logs echuan --since 10m
docker inspect echuan --format '{{json .HostConfig.LogConfig}}'
```

启动 banner 会打印 `version / commit / listen / data_dir / workspace_dir / log_level`，可用来确认配置是否生效。

---

## 6. 备份与恢复

脚本会自动读取仓库根目录的 `.env`（与 compose 使用同一份配置）；命令行显式导出的环境变量优先。

### 6.1 备份

```bash
scripts/backup.sh
# 或指定数据目录
ECHUAN_DATA_DIR=~/echuan-data scripts/backup.sh
```

行为：

1. `sqlite3 "VACUUM INTO"` 在只读连接上生成 **事务一致** 的 `state.db` 快照（服务运行中可备份）；
2. `PRAGMA integrity_check` 校验快照；
3. `rsync -a` 复制 `objects/`；
4. 默认同时复制 `workspaces/`（`ECHUAN_BACKUP_WORKSPACES=0` 可排除）；
5. 生成 `manifest.txt`（时间、版本、SHA256、文件数）。

产物：`$ECHUAN_BACKUP_ROOT/echuan-YYYYmmdd-HHMMSS/`，默认根目录 `$HOME/echuan-backups`。

同一秒内重复执行（cron 与手工撞车、脚本被连跑）不会互相踩踏：若同名目录已存在，
自动改用 `-2`、`-3` … 后缀（最多 99 份），**绝不覆盖或删除已有备份**，输出里会打印实际目录。
并发执行也各自产出独立备份（自动 `-N` 后缀，上限 99），不会有进程因撞名而失败。
失败时只清理本次运行创建的目录（完整性校验失败除外：产物会保留并打印路径供排查）。

建议用 cron 每日执行，并保留至少 7 份：

```cron
15 3 * * * cd /path/to/echuan && ECHUAN_DATA_DIR=$HOME/echuan-data scripts/backup.sh >> $HOME/echuan-backups/backup.log 2>&1
```

### 6.2 恢复

```bash
scripts/restore.sh --latest
# 或指定备份
scripts/restore.sh "$HOME/echuan-backups/echuan-20261027-140000"
```

流程：校验备份 → `docker compose down` → 把当前数据移动到 `<data>.pre-restore-<时间>`（可人工回滚）→ 放回 `state.db` + `objects/` + `workspaces/` → `docker compose up -d` → 轮询 `/readyz` 直到 200。

注意：

- 脚本会先 `--env-file <repo>/.env`；若设置了 `COMPOSE_PROJECT_NAME`（`.env` 或 `export`），
  会再传 `-p <名称>`；两者都未设置时与 `docker compose -f docker-compose.yml` 默认行为一致。
  **从仓库以外的目录执行、或机器上有多个 compose 项目时，建议在 `.env` 里显式写 `COMPOSE_PROJECT_NAME`。**
- 恢复会把数据回退到备份点，**备份之后产生的数据会丢失**（原数据目录保留为 `.pre-restore-*`）。
- 恢复前建议再跑一次 `scripts/backup.sh`。
- 脚本 `set -euo pipefail`，任一步失败都会非零退出并打印最近容器日志。

---

## 7. 升级流程

```bash
# 1. 拉取/切换代码后重新构建（版本号来自 git describe）
#    注意：make docker-build 收尾的 builder/image prune 是全局操作，见 3.4
make docker-build

# 2. 先备份
scripts/backup.sh

# 3. 滚动重启（单实例，短暂不可用）
docker compose up -d

# 4. 验证
curl -fsS http://127.0.0.1:8720/readyz
curl -fsS http://127.0.0.1:8720/version
```

回滚：

```bash
# 镜像回滚（如果旧镜像仍在）
docker tag echuan:local echuan:rollback
# 重新构建上一个 commit 后 up -d

# 数据回滚
scripts/restore.sh "$HOME/echuan-backups/echuan-<升级前时间>"
```

数据库迁移只向前执行；跨版本回滚镜像前请先确认迁移兼容性，最稳妥的方式是同时恢复升级前的备份。

---

## 8. 排障

| 现象 | 排查 |
|------|------|
| `curl: connection refused` | `docker compose ps`、`docker compose logs --tail=100`；确认端口未被占用：`lsof -nP -iTCP:8720 -sTCP:LISTEN` |
| `/healthz` 200、`/readyz` 503 | 看 `checks` 字段与日志：`db` 失败多为权限/磁盘；`migration` 失败为 schema 版本不匹配，通常是镜像与数据目录版本不一致 |
| 容器启动即退出 | `docker compose logs echuan`；常见原因：数据目录不可写、`state.db` 损坏、端口冲突、配置语法错误 |
| `database is locked` | 确认没有多个实例共用同一数据目录；SQLite 写池固定为 1，正常不会出现 |
| 磁盘增长 | `du -sh ~/echuan-data/*`；`objects/` 为内容寻址、只增不减，清理需先确认无材料引用 |
| 时区不对 | 镜像已装 `tzdata`，通过 `.env` 的 `TZ` 控制；API 时间字段始终是 RFC3339 UTC |

常用诊断命令：

```bash
docker compose ps
docker inspect echuan --format 'restart={{.HostConfig.RestartPolicy.Name}} user={{.Config.User}}'
docker exec echuan id
docker exec echuan sh -c 'ls -ld /data; df -h /data'
docker exec echuan sqlite3 /data/state.db 'PRAGMA integrity_check;'
docker stats --no-stream echuan
```

---

## 9. 首次上线手册（curl 全流程）

以下命令在宿主机执行，依赖 `curl`、`jq`。把 `BASE` 换成实际地址。

> 解析 JSON 时统一用 `printf '%s\n' "$VAR" | jq ...`，**不要用 `echo "$VAR" | jq`**：
> zsh / POSIX sh 的 `echo` 会解释参数里的 `\n`（`body_md`/`summary_md` 常带 `\n`），
> 把一条 JSON 拆成多行，jq 报 control characters 错误，变量取值就变空了。

```bash
BASE=http://127.0.0.1:8720
JSON='Content-Type: application/json'
```

### 9.1 建产品

```bash
curl -fsS -X POST "$BASE/v1/products" \
  -H "$JSON" -H 'X-Actor: pm' \
  -d '{"product_id":"APP","name":"驿传","git_url":"git@example.com:APP.git"}' | jq
```

### 9.2 建项目并写需求（需求作为第一份材料）

```bash
curl -fsS -X POST "$BASE/v1/projects" \
  -H "$JSON" -H 'X-Actor: pm' \
  -d '{
        "product_id":"APP",
        "title":"登录页改版",
        "requirement_md":"# 需求\n\n- 登录页支持验证码\n- 保留原有账号密码登录\n"
      }' | jq
```

返回中 `project_id` 为 `APP-1`（产品内自增）。项目状态 `draft`，版本 `1`。

追加需求版本（可选）：

```bash
curl -fsS -X POST "$BASE/v1/projects/requirement?id=APP-1" \
  -H "$JSON" -H 'X-Actor: pm' \
  -d '{"body_md":"# 需求 v2\n\n- 验证码 60 秒倒计时\n"}' | jq
```

### 9.3 提交需求评审

```bash
curl -fsS -X POST "$BASE/v1/projects/accept?id=APP-1" \
  -H 'X-Actor: pm' | jq '.status_name'    # spec_review
```

### 9.4 提交技术方案

```bash
SPEC=$(curl -fsS -X POST "$BASE/v1/projects/specs?id=APP-1" \
  -H "$JSON" -H 'X-Actor: agent:cto' \
  -d '{"body_md":"# 方案\n\n1. 新增验证码组件\n2. 增加接口限流\n"}')
printf '%s\n' "$SPEC" | jq
SPEC_VER=$(printf '%s\n' "$SPEC" | jq -r '.project_version')
```

### 9.5 方案通过 → 进入规划

```bash
curl -fsS -X POST "$BASE/v1/projects/decide?id=APP-1" \
  -H "$JSON" -H 'X-Actor: pm' -H "If-Match: $SPEC_VER" \
  -d '{"decision":"approve"}' | jq '.status_name'    # planning
```

### 9.6 提交任务清单

```bash
PLAN=$(curl -fsS -X POST "$BASE/v1/projects/plans?id=APP-1" \
  -H "$JSON" -H 'X-Actor: agent:cto' \
  -d '{"tasks":[
        {"title":"实现验证码登录","body_md":"# 实现\n\n- 前后端联调\n","priority":10},
        {"title":"回归验证","body_md":"# 验证\n\n- 跑全量测试\n","priority":5,"depends_on":[1]}
      ]}')
printf '%s\n' "$PLAN" | jq
PLAN_VER=$(printf '%s\n' "$PLAN" | jq -r '.project_version')
```

`depends_on` 使用清单内的序号（从 1 开始）。

### 9.7 清单通过 → 进入开发

```bash
curl -fsS -X POST "$BASE/v1/projects/decide?id=APP-1" \
  -H "$JSON" -H 'X-Actor: pm' -H "If-Match: $PLAN_VER" \
  -d '{"decision":"approve"}' | jq '.status_name'    # developing
```

任务自动变为 `ready`，等待 Worker 领取。

### 9.8 领取任务

```bash
CLAIM=$(curl -fsS -X POST "$BASE/v1/tasks/claim" \
  -H "$JSON" -H 'X-Actor: agent:worker' \
  -d '{"product":"APP"}')
printf '%s\n' "$CLAIM" | jq
TASK_ID=$(printf '%s\n' "$CLAIM" | jq -r '.task.id')
LEASE_ID=$(printf '%s\n' "$CLAIM" | jq -r '.lease.lease_id')
```

无任务时返回 `204`（空响应体）。

### 9.9 提交报告

```bash
REPORT=$(curl -fsS -X POST "$BASE/v1/tasks/report?id=$TASK_ID" \
  -H "$JSON" -H 'X-Actor: agent:worker' \
  -d "{\"lease_id\":\"$LEASE_ID\",\"summary_md\":\"# 完成\n\n- 已实现并自测\n\"}")
printf '%s\n' "$REPORT" | jq '.status_name, .version'
TASK_VER=$(printf '%s\n' "$REPORT" | jq -r '.version')
```

### 9.10 验收任务

```bash
curl -fsS -X POST "$BASE/v1/tasks/decide?id=$TASK_ID" \
  -H "$JSON" -H 'X-Actor: pm' -H "If-Match: $TASK_VER" \
  -d '{"decision":"accept"}' | jq '.status_name'    # done
```

重复 9.8–9.10 处理剩余任务。最后一张卡验收后，项目自动进入 `delivery_review`。

### 9.11 项目交付验收

```bash
PROJECT=$(curl -fsS "$BASE/v1/projects?id=APP-1")
printf '%s\n' "$PROJECT" | jq '.status_name, .version'       # delivery_review
PROJECT_VER=$(printf '%s\n' "$PROJECT" | jq -r '.version')

curl -fsS -X POST "$BASE/v1/projects/decide?id=APP-1" \
  -H "$JSON" -H 'X-Actor: pm' -H "If-Match: $PROJECT_VER" \
  -d '{"decision":"approve"}' | jq '.status_name'    # accepted
```

### 9.12 查看整体状态

```bash
curl -fsS "$BASE/v1/inbox" | jq
curl -fsS "$BASE/v1/projects?product_id=APP" | jq
curl -fsS "$BASE/v1/tasks?project_id=APP-1" | jq
```

---

## 10. 镜像基线

<!-- IMAGE_BASELINE_START -->
构建环境：macOS arm64 / Docker 27.4 / BuildKit v0.17.3，`echuan:local` 镜像：

| 项 | 数值 |
|----|------|
| 镜像体积 | **23.7 MB**（23,680,349 bytes） |
| 基础层 alpine:3.20 | 8.82 MB |
| apk（ca-certificates + tzdata + sqlite） | 3.65 MB |
| echuan 二进制 | 11.2 MB（11,206,818 bytes，`-s -w`） |
| 构建上下文 | 约 1.2 MB（`.dockerignore` 排除 node_modules/.git/data/dist） |

```
$ make docker-size
echuan:local: 23680349 bytes
```
<!-- IMAGE_BASELINE_END -->

体积组成（层大小）：alpine 基础层 8.82 MB + apk 运行时依赖 3.65 MB + Go 单二进制 11.2 MB。
运行镜像内**不含** go / node / pnpm：

```bash
$ docker run --rm echuan:local sh -c 'command -v go; command -v node; command -v pnpm'
# 无输出
```

回归方式：

```bash
make docker-size
docker image inspect echuan:local --format '{{.Size}} bytes'
```

若体积明显上涨，检查是否误把构建工具链或前端 `node_modules` 带进运行镜像：

```bash
docker run --rm echuan:local sh -c 'command -v go; command -v node; command -v pnpm'   # 应为空
```
