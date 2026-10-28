# AGENTS.md

> 本文档最近一次与代码核对：2026-10-27 @ `623b8c5`

面向在本仓库工作的 AI 编码代理与开发者。**先读这一份**，再去翻设计文档。

## 1. 这是什么

驿传平台（echuan）：单机协作平台，产品 → 项目 → 任务。一个项目 = 一条需求。
形态是「带事务的队列 + 一份版本化的文档库」。

技术栈（有意保持窄）：

- **Go 1.26**，只用标准库 `net/http`，无第三方 router / ORM / 日志库（日志用 `log/slog`）
- **SQLite**，纯 Go 驱动 `modernc.org/sqlite`，WAL，单写者
- **React 18 + Vite 6 + TypeScript**，构建产物经 `go:embed` 打进二进制
- 交付形态：一个静态二进制，启动即 Web 服务

## 2. 目录结构

| 路径 | 职责 |
|---|---|
| `cmd/echuan/` | `main.go`：配置 → 日志 → 建目录 → 开库并迁移 → 装配服务 → 监听 → 信号优雅退出（10s） |
| `internal/api/` | HTTP 层：路由表、中间件链、错误契约、各端点的编解码 |
| `internal/app/` | 用例编排与**事务边界**：product / project / task / claim / decide / inbox / material |
| `internal/domain/` | 纯逻辑，无 IO：状态机、决策合法性、领域错误 |
| `internal/store/` | SQLite：写池 1 连接、读池 N 连接、迁移执行、就绪检查 |
| `internal/material/` | 内容寻址对象存储：`objects/<sha 前 2 位>/<sha256>` |
| `internal/inbox/` | 待办推导（待评审项目 + 待决策任务） |
| `internal/contract/` | 契约校验：`routeSpecs` 与 `api/openapi.yaml` 的 operation 集合必须一致 |
| `internal/platform/` | 配置加载（TOML + 环境变量）、日志、时钟、版本注入 |
| `internal/web/` | 前端嵌入点（`//go:embed dist`） |
| `web/` | 前端源码；`make web` 构建后同步到 `internal/web/dist` |
| `migrations/` | SQL 迁移，`//go:embed` 打包（README 不打包） |
| `api/openapi.yaml` | 接口契约，与路由表强校验 |
| `configs/echuan.toml` | 配置样例；服务默认读 `./echuan.toml`（`-config` 可指定），文件不存在则全部走默认值 |
| `kit/` | dsh 接入工具包：`dsh-echuan` 插件（TypeScript / Cordis 4，独立 pnpm 包，含自己的 `pnpm-lock.yaml`）。声明 `dsh.bundle.patch` 把自己插进 dsh profile；只依赖平台 HTTP 契约，**不 import 任何 Go 代码**，不参与 Go 构建 |

## 3. 常用命令

```bash
make build      # CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" → ./echuan
make test       # go test ./... -race（全量含竞态检测，首次约 15s）
make lint       # go vet ./...；装了 staticcheck 会一并跑，没装则跳过
make contract   # 只跑契约校验：go test ./internal/contract -count=1
make web        # pnpm --dir web build && 同步产物到 internal/web/dist
make fmt        # gofmt -w .
make clean      # 删除 ./echuan
```

Docker（部署唯一形态，细节见 §10）：

```bash
make docker-build   # 构建镜像；收尾全局清理 build cache + 悬空镜像，并打印镜像 size/id
make docker-up      # docker compose up -d（不重新构建）
make docker-down    # 停容器（数据目录保留）
make docker-logs    # docker compose logs -f --tail=100
make docker-size    # 打印运行镜像体积
make docker-clean   # 删本项目容器/镜像并清构建缓存（不动宿主机数据）
```

本地起服务（开发）：

> **本机 8720 常被已部署的 `echuan` 容器占用**，直接 `./echuan` 会 `address already in use`；
> 且默认数据目录 `~/.echuan` 可能已有线上数据。开发**一律**用独立端口 + 独立数据/工作区目录，
> 三个变量一起给才是完全隔离；不要停线上容器。

```bash
DEV_DIR=$(mktemp -d)
ECHUAN_LISTEN=127.0.0.1:18720 \
ECHUAN_DATA_DIR="$DEV_DIR" \
ECHUAN_WORKSPACE_DIR="$DEV_DIR/workspaces" \
./echuan

curl -s 127.0.0.1:18720/healthz   # 200 即活
curl -s 127.0.0.1:18720/readyz    # 库可写 + 迁移版本匹配
```

容器停掉、8720 空闲时才用下面这条（会读写默认 `~/.echuan`，别对着线上数据用）：

```bash
./echuan -config configs/echuan.toml
```

前端热更新：`cd web && pnpm dev`。**没有 `make dev` 这个目标。** 注意 `web/vite.config.ts:15-18` 把 `/v1`、`/healthz`、`/readyz`、`/version` 的 proxy **硬编码**指向 `127.0.0.1:8720`：要么让后端/容器跑在 8720，要么临时改 proxy 目标（不会自动跟随上面的 18720）。

改完代码的验收顺序固定为：`make build && make test`，涉及路由改动再加 `make contract`。

## 4. 配置

优先级：**环境变量 `ECHUAN_*` > 配置文件 > 内置默认值**。默认读 `./echuan.toml`，文件不存在不报错。

| 键 | 环境变量 | 默认 |
|---|---|---|
| `listen` | `ECHUAN_LISTEN` | `127.0.0.1:8720` |
| `data_dir` | `ECHUAN_DATA_DIR` | `~/.echuan` |
| `workspace_dir` | `ECHUAN_WORKSPACE_DIR` | `~/.echuan/workspaces` |
| `read_pool_size` | `ECHUAN_READ_POOL_SIZE` | `4`（写池恒为 1） |
| `lease_ttl` | `ECHUAN_LEASE_TTL` | `90s` |
| `max_attempts` | `ECHUAN_MAX_ATTEMPTS` | `3` |
| `poll_timeout` | `ECHUAN_POLL_TIMEOUT` | `25s` |
| `review_stale_hours` | `ECHUAN_REVIEW_STALE_HOURS` | `72` |
| `material_max_bytes` | `ECHUAN_MATERIAL_MAX_BYTES` | `20971520`（20 MiB） |
| `log_level` | `ECHUAN_LOG_LEVEL` | `info` |

数据目录布局：`{data_dir}/state.db`、`{data_dir}/objects/`、`{data_dir}/tmp/`；
任务工作区 `{workspace_dir}/{project_id}`。

## 5. 数据不变式（违反即视为 bug，不接受"先合并再修"）

1. **版本链单向往回指。** `materials.previous_material_id` 指向前一版；`materials` 表**没有** `subject_type` / `subject_id`。
2. **归属由持有方声明。** `projects.requirement_material_id`、`projects.spec_material_id`、`tasks.report_material_id`。想让"被引用方记住谁引用我"，就是设计错误。
3. **表关联用自增 `id`，对外用业务编号。** `product_id`=`APP`、`project_id`=`APP-1`、`task_id`=`APP-1-T0001`、`material_id`=ULID 形态 26 位字符串。
4. **枚举落库一律存整数。** 名字只是代码别名（`taskStatusNames`），**永不持久化**。
5. **SQLite 单写者。** 写池固定 1 连接，`BEGIN IMMEDIATE`；读池 `query_only=1`，只读。不要新开写连接，不要在事务外写库。
6. **每张表都有 `id` / `create_time` / `update_time`。** 时间存文本。
7. **状态迁移只有白名单。** 合法边集中在 `internal/domain` 的 `projectTransitions` / `taskDecisionTransitions`，表外组合一律 `409 invalid_state`。

## 6. 状态机

项目：`0 draft` · `1 spec_review` · `2 planning` · `3 plan_review` · `4 developing` · `5 delivery_review` · `6 accepted` · `7 cancelled`
任务：`0 backlog` · `1 ready` · `2 in_progress` · `3 pending_acceptance` · `4 done` · `5 failed` · `6 blocked` · `7 cancelled`

规则：

- 客户端**只能提交意图**（`accept` / `decide` / `cancel`，任务的 `claim` / `report` / `decide`），不能直接写 `status`
- 目标状态由服务端按白名单推导；客户端传来的 `status` 一律忽略或拒绝
- 项目决策二值：`approve` / `reject`；任务决策四值：`accept` / `rework` / `retry` / `abandon`
- `rework` 与 `abandon` 必须带 `reason`

## 7. 接口约定

- 列表与详情**共用同一路径**，`?id=` 选详情。所以一个 `method+path` 只算一个 operation，共 28 个。
- 路径只写资源名（`/v1/projects/specs`），语义参数走 JSON body，标识走 query。
- `X-Actor` 是**身份声明，不是认证**——没有鉴权，别把它当安全边界。
- 响应带 `X-Request-Id`；错误体是四字段 `{code, message, details[], request_id}`。`details` **永远是数组**（无明细为 `[]`），元素形如 `{field?, reason?}`（两个字段都 omitempty）；响应体里的 `request_id` 与 `X-Request-Id` 响应头**是同一个值**，排障和前端错误提示都依赖它。
- 评审类接口（`/decide`）用 `If-Match` 做乐观并发：缺失或格式错 → `400 invalid_argument`，版本已过期 → `409 invalid_version`。
- 中间件顺序固定（外→内）：`Recover → RequestID → UnifiedErrors → AccessLog → Timeout(30s)`。
- **新增端点必须同时改四处**：① `internal/api/router.go` 的 `routeSpecs`；② `api/openapi.yaml`；③ 对应 handler（并在 `NewRouter` 里挂到 `handlers[...]`）；④ 把依赖/handler 传给 `cmd/echuan/main.go:120` 的 `api.NewRouter(...)`。漏 ①↔② 任一侧 → `make contract` 变红；漏 ③（`routeSpecs` 有、`handlers` 无）→ 启动即 panic `api: route ... has no handler`，`make contract` 同样红；漏 ④ → **不 panic**，该路由被 `enabled[service]` 静默跳过、运行时 404，`make contract` 抓不到（它只比对 `routeSpecs` ↔ `openapi.yaml`，不读 `main.go`）。

## 8. 提交与变更

- Conventional Commits，正文中文：`feat(task-09): 任务 CRUD` / `fix(web): 静态资源 404`
- 一个 PR 只做一件事；重构与功能不要混
- **迁移只增不改**：已合并的 `0001` / `0002` 不得编辑。新增迁移版本号 = 当前最大值 + 1，文件名 `NNNN_描述.sql`
- 迁移脚本里**不要写** `PRAGMA user_version`，由执行器统一回写
- 代码注释用英文、只解释 why；文档用中文
- 新增第三方依赖必须在 PR 里写清理由。当前只有 `modernc.org/sqlite` 与 `gopkg.in/yaml.v3`，门槛很高

## 9. 测试地图

| 包 | 覆盖点 |
|---|---|
| `internal/domain` | 状态机、任务决策矩阵、typed errors（无 IO，覆盖率 100.0%） |
| `internal/store` | SQL、迁移与 readyz、**20 goroutine 抢 1 张卡**、租约回收、编号并发（78.7%） |
| `internal/app` | 用例编排与事务边界：claim / decide / plan / spec / inbox / material（74.1%） |
| `internal/api` | handler、中间件、错误契约；`flow_test.go` 用 httptest 跑全链路（90.1%） |
| `internal/contract` | 28 个 operation 与 `api/openapi.yaml` 双向一致 |
| `internal/inbox` / `internal/material` / `internal/web` | 待办推导（100.0%）/ 内容寻址写入（66.4%）/ SPA fallback 与缺失静态 404（92.5%） |
| `migrations` | 嵌入、排序、非法文件名/重复版本拒绝（85.7%） |

```bash
go test ./internal/store -run TestClaimTaskConcurrentSingleCard -v   # 单跑一个用例
go test ./... -race -count=1                                        # 全量 + 竞态
# 总覆盖率靠 coverprofile 汇总（go test ./... -cover 只逐包打印，没有总计）；当前 total: 78.1%
go test ./... -coverprofile=/tmp/cover.out && go tool cover -func=/tmp/cover.out | tail -1
```

**变异纪律**：断言必须能抓住实现退化——把 claim 的 `WHERE t.status = 1` 改坏、放宽状态机白名单、
回收时忽略 `attempt_count >= max_attempts`、`seq_no` 恒返回 1，现有测试都必须变红。
已复现第一条：把 `internal/store/task_claim.go:37` 的 `AND t.status = 1` 改成 `status = 2` 后，
`go test ./internal/store -run TestClaimTaskConcurrentSingleCard -count=1` 报 `winners = 0, want exactly 1`。
新增测试不得为了让实现通过而放宽断言。

新增测试**禁止**联网、占用固定端口、真实 `sleep`；用 `t.TempDir()` 和注入时钟。

## 10. 部署与运维

**Docker 是唯一部署形态**：`make docker-build` → `make docker-up`；完整手册见 `docs/deploy.md`。

| 项 | 事实 |
|---|---|
| 运行镜像 | 三阶段（node 构前端 → golang 编译 embed → alpine 运行）；UID/GID `10001`；内置 `sqlite3` CLI；体积基线 **23,680,349 bytes**（以 `623b8c5` 构建为准，本机现存镜像可能来自更早提交）；不含 go/node/pnpm |
| compose | `restart: unless-stopped`；端口 `127.0.0.1:8720:8720`（容器内 `0.0.0.0:8720`）；`/data` bind mount 到宿主 `~/echuan-data`；`configs/echuan.container.toml` 只读挂到 `/etc/echuan/echuan.toml`；json-file 日志 `max-size 10m / max-file 3`；healthcheck 打 `/readyz` |
| 数据 | 全在宿主 `~/echuan-data`：`state.db`、`objects/`、`workspaces/`；删容器不丢数据 |
| 备份/恢复 | `scripts/backup.sh`（`VACUUM INTO` + rsync，同秒并发自动 `-N` 后缀、上限 99，全量 `integrity_check`）；`scripts/restore.sh`（down → 放回 → up → 等 `/readyz`） |

三个必须知道的副作用/前置：

1. `docker builder prune -f` 是**全局**操作，会清掉本机其他项目的构建缓存（`make docker-build` 收尾就执行它）。
2. `docker image prune -f` 同样是**全局**操作（只删 dangling 镜像，仍会影响本机其他项目）。
3. Linux 宿主需 `sudo chown -R 10001:10001 ~/echuan-data`（本机 macOS Docker Desktop/VirtioFS 未复现该需求，**Linux 未实测**）。

**多 worktree 并行**：`compose` 的 `container_name` 默认固定 `echuan`、宿主端口默认 `8720`，本机两个 worktree
同时 `make docker-up` 会撞名/撞端口。部署第二套前先覆盖这两个变量：
`ECHUAN_CONTAINER_NAME=echuan-2 ECHUAN_PORT=8721 make docker-up`
（先跑 `ECHUAN_CONTAINER_NAME=echuan-2 ECHUAN_PORT=8721 docker compose config | grep published` 确认替换生效）。

```bash
curl -fsS 127.0.0.1:8720/healthz   # 进程活没活
curl -fsS 127.0.0.1:8720/readyz    # 库可写 + 迁移匹配
curl -fsS 127.0.0.1:8720/version
docker logs echuan --tail=100      # 默认容器名；改过 ECHUAN_CONTAINER_NAME 就用新名字
```

备份（只读数据，不影响服务）：

```bash
scripts/backup.sh                  # 产物在 ~/echuan-backups/echuan-<时间戳>/
```

> ⚠️ **恢复会中断服务**：`scripts/restore.sh --latest` 先 `compose down`，再放回库与资料、`up -d` 并等 `/readyz`。
> 只在确认要回滚时执行，且先跑一次 `backup.sh` 留住当前状态。

日志一行一条 JSON：`ts/level/msg/request_id/actor/method/path/status/duration_ms`，业务路由额外带 `subject`，错误额外带 `code`。

## 11. 禁止事项

- ❌ 手工改 `state.db`，或用 `sqlite3` 直接写库排查问题
- ❌ 在 `products` 存 root 路径——工作区由 `workspace_dir` + `project_id` 推导
- ❌ 在 handler 里拼 SQL；SQL 只允许出现在 `internal/store`
- ❌ 引入第三方 router / ORM / 日志库
- ❌ 打开 CGO（`CGO_ENABLED=0` 是交付前提）
- ❌ 把 Go / Node 工具链塞进运行镜像
- ❌ 加指标埋点（已明确不做，无 `/metrics`）

## 12. 文档导航

- 《驿传平台 详细技术方案 v0.19》：https://ecn3asgw8dff.feishu.cn/wiki/QdnowbpahiZKxKk7mROc83wfnEb
- 《驿传平台 顶层设计 v0.6》：https://ecn3asgw8dff.feishu.cn/wiki/Jwc3wYQ0bi6DhBkrJITcesr5npd
- 接口契约：`api/openapi.yaml`（唯一权威，文档与本文件冲突时以它为准）
- 迁移规则：`migrations/README.md`
- 前端说明：`web/README.md`

> 本文件与设计文档冲突时，**代码与 openapi.yaml 优先**，然后回来把这份文件改对。改完代码顺手回来核对本文档（含开头的核对日期与 commit）。别让它变成第二份过期文档。
