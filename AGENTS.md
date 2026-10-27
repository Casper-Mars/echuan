# AGENTS.md

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
| `configs/echuan.toml` | 配置样例（默认不读取，需显式 `-config`） |

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

本地起服务（开发）：

```bash
./echuan -config configs/echuan.toml
curl -s 127.0.0.1:8720/healthz   # 200 即活
curl -s 127.0.0.1:8720/readyz    # 库可写 + 迁移版本匹配
```

前端热更新：`cd web && pnpm dev`（Vite 已把 `/v1`、`/healthz`、`/readyz`、`/version` 代理到 8720）。**没有 `make dev` 这个目标。**

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
- 响应带 `X-Request-Id`；错误体 `{code, message, details[]}`，`details` **永远是数组**（无明细为 `[]`）。
- 评审类接口（`/decide`）用 `If-Match` 做乐观并发：缺失或格式错 → `400 invalid_argument`，版本已过期 → `409 invalid_version`。
- 中间件顺序固定（外→内）：`Recover → RequestID → UnifiedErrors → AccessLog → Timeout(30s)`。
- **新增端点必须同时改三处**：`internal/api/router.go` 的 `routeSpecs`、`api/openapi.yaml`、对应 handler。漏掉前两个，`make contract` 会红。

## 8. 提交与变更

- Conventional Commits，正文中文：`feat(task-09): 任务 CRUD` / `fix(web): 静态资源 404`
- 一个 PR 只做一件事；重构与功能不要混
- **迁移只增不改**：已合并的 `0001` / `0002` 不得编辑。新增迁移版本号 = 当前最大值 + 1，文件名 `NNNN_描述.sql`
- 迁移脚本里**不要写** `PRAGMA user_version`，由执行器统一回写
- 代码注释用英文、只解释 why；文档用中文
- 新增第三方依赖必须在 PR 里写清理由。当前只有 `modernc.org/sqlite` 与 `gopkg.in/yaml.v3`，门槛很高

## 9. 禁止事项

- ❌ 手工改 `state.db`，或用 `sqlite3` 直接写库排查问题
- ❌ 在 `products` 存 root 路径——工作区由 `workspace_dir` + `project_id` 推导
- ❌ 在 handler 里拼 SQL；SQL 只允许出现在 `internal/store`
- ❌ 引入第三方 router / ORM / 日志库
- ❌ 打开 CGO（`CGO_ENABLED=0` 是交付前提）
- ❌ 把 Go / Node 工具链塞进运行镜像
- ❌ 加指标埋点（已明确不做，无 `/metrics`）

## 10. 文档导航

- 《驿传平台 详细技术方案 v0.19》：https://ecn3asgw8dff.feishu.cn/wiki/QdnowbpahiZKxKk7mROc83wfnEb
- 《驿传平台 顶层设计 v0.6》：https://ecn3asgw8dff.feishu.cn/wiki/Jwc3wYQ0bi6DhBkrJITcesr5npd
- 接口契约：`api/openapi.yaml`（唯一权威，文档与本文件冲突时以它为准）
- 迁移规则：`migrations/README.md`
- 前端说明：`web/README.md`

> 本文件与设计文档冲突时，**代码与 openapi.yaml 优先**，然后回来把这份文件改对。别让它变成第二份过期文档。
