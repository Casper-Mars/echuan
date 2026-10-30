# dsh-echuan — 驿传平台接入工具包

这是驿传（echuan）的 dsh（Cordis 4）Agent 接入插件：把平台 HTTP 契约接进 Agent。装进一个 profile 后，Agent 能领卡、续租、交报告，平台也能把派单投递给它。

## 能力边界（先看这段）

插件**只做三件事**：

1. **定时心跳**：上报 agent 状态与 metadata，并带上平台下发的 `worker_id`（仅存内存）。
2. **投递派单**：把平台的 `run_task` 交给当前唯一的根 agent。
3. **提供 22 个工具**（默认全量；可用 `exclude` 按 agent 裁剪，见「配置」）：
   - 生命周期写：`echuan_project_create`（建项目）、`echuan_project_requirement_write`（写需求）、`echuan_project_submit`（提交评审）、`echuan_spec_create`（建方案）、`echuan_spec_revise`（修订方案）、`echuan_plan_submit`（批量建卡）
   - 任务写：`echuan_task_claim`（领卡）、`echuan_task_renew`（续租）、`echuan_task_report`（交报告）
   - 辅助写：`echuan_task_create`（单卡新增）、`echuan_task_update`（改卡）、`echuan_task_delete`（删卡）、`echuan_project_update`（改项目标题）、`echuan_product_create`（建产品）、`echuan_project_package`（下载资料包）
   - 读：`echuan_inbox`（待办）、`echuan_product_list`（产品列表）、`echuan_project_list`（项目列表）、`echuan_task_list`（任务列表）、`echuan_project_get`（项目详情）、`echuan_task_get`（任务详情）、`echuan_material_get`（资料）

生命周期写工具的参数与副作用：

| 工具 | 端点 | 关键入参 | 返回 / 下一步 |
|---|---|---|---|
| `echuan_project_create` | `POST /v1/projects` | `product_id`、`title`、可选 `requirement_md` | `project_id`、状态、`version` |
| `echuan_project_requirement_write` | `POST /v1/projects/requirement?id=` | `project_id`、`body_md` | 需求物料 `material_id` / 版本 / 字节数 |
| `echuan_project_submit` | `POST /v1/projects/submit?id=` | `project_id`（无 body） | 新状态；缺需求时平台 409「请先上传需求文档」原样透出 |
| `echuan_spec_create` | `POST /v1/specs` | `project_id`、`title`、`body_md`（均必填） | `spec_id`、`version` |
| `echuan_spec_revise` | `PATCH /v1/specs?id=` | `spec_id`、`title`、`body_md`（均必填）、必填 `version`（走 `If-Match` 头） | 新 `version`；错版本 409 `invalid_version` 透出 |
| `echuan_plan_submit` | `POST /v1/projects/plans?id=` | `project_id`、`tasks[]`（`title`/`body_md`，可选 `priority`、`depends_on` 为 1-based 批内序号） | 项目状态 + 新卡列表（含 `task_id`） |

辅助写工具（任务被驳回后自行改卡/删卡，或补建产品/下载资料）：

| 工具 | 端点 | 关键入参 | 返回 / 下一步 |
|---|---|---|---|
| `echuan_task_create` | `POST /v1/tasks?project_id=` | `project_id`、`title`、`body_md`，可选 `priority`、`depends_on`（任务 id 数组） | `task_id`、`seq_no`、状态；依赖以任务 id 表示 |
| `echuan_task_update` | `PATCH /v1/tasks?id=` | `task_id` + 至少一个 `title` / `body_md` / `priority`（空白不算有效修改） | 空 patch 本地拒绝、零请求；驳回提示随写入清空 |
| `echuan_task_delete` | `DELETE /v1/tasks?id=` | `task_id` | 204 成功；已领取/不可删时 409 原样透出 |
| `echuan_project_update` | `PATCH /v1/projects?id=` | `project_id`、`title` | 只改标题，状态与需求/方案指针不变 |
| `echuan_product_create` | `POST /v1/products` | `product_id`、`name`、`git_url`，可选 `description`、`license` | 产品 id；可立即用 `echuan_project_create` 建项目 |
| `echuan_project_package` | `GET /v1/projects/package?id=` | `project_id` | zip 写入 `os.tmpdir()`，返回 `saved_path` / `bytes` / `entries`；同名不覆盖 |

**典型调用顺序**：`echuan_project_create` → `echuan_project_requirement_write` → `echuan_project_submit`（需求评审）→ 人工评审通过 → `echuan_spec_create` → `echuan_project_submit`（方案评审）→ 人工评审通过 → `echuan_plan_submit`（批量建卡，项目进入任务评审）。

列表工具的参数（都只发 GET，不缓存、不落盘）：

| 工具 | 筛选参数 | 分页 |
|---|---|---|
| `echuan_product_list` | 无 | `limit` 1–200、`cursor` |
| `echuan_project_list` | `product_id`、`status`（单个整数） | `limit` 1–200、`cursor` |
| `echuan_task_list` | `project_id`、`status`（可多个，按重复 key 传） | `limit` 1–200、`cursor` |
`limit` 非整数或越界时本地拒绝、不发请求；空列表返回「当前没有…」而不是报错；回包带 `next_cursor` 时末尾提示 `cursor=…`。22 个工具共用 20 个 HTTP operation：项目/任务各由一个 `GET` 同时承担列表与按 `id` 取详情。

写工具在本地先校验：缺字段（含 `echuan_spec_create` / `echuan_spec_revise` 的 `title`）、空白正文、空 `tasks`、缺失或非正的 `version` 都直接报 `invalid_argument`，**不发请求**；平台错误（400 / 409 / 413）原样透出 code 与 message，`details` 中的 `field` / `reason` 也会附加到错误文本（如 `（title: is required）`），不自动重试。

`exclude` 只影响**本进程注册哪些工具**：不改工具定义、参数或平台 HTTP 契约，也不影响心跳与派单；不配置时默认全量 22 个。

**不提供**：验收、返工、取消、改状态——所有审批类动作一律没有。**不代交报告**：报告必须由 agent 自己调 `echuan_task_report`。**不缓存、不落盘**：`worker_id` 只在内存，进程重启后重新注册（会拿到新的 `w_` id）；唯一的落盘动作是 `echuan_project_package` 把 zip 写到 `os.tmpdir()`（同名不覆盖、失败清理半成品，绝不写仓库目录）。

## 与平台的关系：同仓

`kit/` 与平台在同一个仓库。平台改 `api/openapi.yaml` 契约时，**kit 的客户端与用例必须在同一个 PR 里同步更新**——`pnpm test` 里的契约断言会直接变红，不靠人肉比对。

## 安装

下文 `<name>` 替换成你的 profile 名，例如 `work`。

### 方式一：本地目录（开发时推荐）

在仓库根目录执行（`./kit` 指本仓库的工具包；换机器只需把仓库放到任意路径）：

```bash
# 1) 初始化 profile（已存在可跳过），会创建 ~/.dsh/profiles/<name>/
dsh plugin --profile <name> list

# 2) 装插件：--profile 必须在 add 之前
dsh plugin --profile <name> add ./kit
```

⚠️ `--profile` 位置错了（如 `dsh plugin add ... --profile <name>`）会报 `required option '--profile <name>' not specified`。

⚠️ 全新 profile 只含 `@deepseek-ai/dsh-base` 时，`dsh --profile <name> --help` 会挂起（没有 app bundle 消费启动参数）。在 `~/.dsh/profiles/<name>/package.json` 里补齐 `bundles`。

> `add` 会把 `dsh-echuan` 自动追加进 `bundles`。**手改时要在现有数组上追加，不要整段覆盖**，否则插件会静默不加载（表现为 0 条 `[dsh-echuan]` 日志、0 心跳）。

```json
"dsh": {
  "profile": {
    "bundles": ["@deepseek-ai/dsh-base", "@deepseek-ai/dsh-acp-app", "dsh-echuan"]
  }
}
```

改完 `dsh --profile <name> --help` 应在数秒内正常退出。

### 方式二：tgz 包

在仓库根目录执行：

```bash
cd kit
pnpm build && pnpm pack            # 产出 dsh-echuan-0.1.0.tgz（已被 gitignore）
dsh plugin --profile <name> add ./dsh-echuan-0.1.0.tgz
```

同样受上面两条 ⚠️ 约束：`--profile` 在前；手工补 `bundles` 时要保留 `dsh-echuan`。

## 配置

编辑 `~/.dsh/profiles/<name>/cordis.patch.yml`（已有其他条目就追加这一条；重启 dsh 生效）：

```yaml
- id: echuan
  config:
    baseUrl: http://127.0.0.1:8720        # 平台地址，结尾斜杠会被去掉
    actor: agent:echuan                   # X-Actor 声明式身份，不是 worker_id
    requestTimeoutMs: 35000               # 单请求超时
    exclude: []                           # 不注册的工具名；默认 [] = 全量 22 个
    worker:
      poll:
        enable: false                     # 默认关闭；打开才会定时心跳
      metadata:                           # 原样上报，序列化后 ≤8KiB
        host: build-01
        os: darwin
        agent_version: 0.1.0
```

- `worker.poll.enable=false` 是默认值：不建定时器、零请求；**手动领卡照常可用**（工具与心跳开关无关）。
- `metadata` 序列化后超过 8KiB 时插件**不启动心跳循环**并 warn 一条（不是截断），避免每轮都被平台拒。
- `exclude` 默认 `[]`（不排除，全量 22 个）；工具名写错或含空白项时**插件激活直接失败**（`TypeError`，错误里列出未知项与合法名单），不会静默少注册。
- 真实例子：本机起好平台后，把上面的 `enable` 改成 `true`，`baseUrl` 保持 `http://127.0.0.1:8720`，重启 dsh 即可。

### 按 agent 裁剪工具（exclude）

同一份 kit 可以装进多个 profile：用 `exclude` 让不同 agent 只看到自己该用的工具。过滤只发生在**注册层**——工具定义、参数校验与平台 HTTP 契约都不变，心跳/派单也不受影响。

**builder（全量）**：不写 `exclude` 即可，或不显式写 `exclude: []`：

```yaml
- id: echuan
  config:
    baseUrl: http://127.0.0.1:8720
    exclude: []
```

启动日志：`[dsh-echuan] 已注册 22 个工具（15 写 + 7 读）：...`

**worker（只留领卡 / 续租 / 交报告 + 全部只读）**：排除其余 12 个写工具：

```yaml
- id: echuan
  config:
    baseUrl: http://127.0.0.1:8720
    worker:
      poll:
        enable: true                      # worker 通常需要派单，打开心跳
    exclude:
      - echuan_project_create
      - echuan_project_requirement_write
      - echuan_project_submit
      - echuan_spec_create
      - echuan_spec_revise
      - echuan_plan_submit
      - echuan_task_create
      - echuan_task_update
      - echuan_task_delete
      - echuan_project_update
      - echuan_product_create
      - echuan_project_package
```

启动日志：`[dsh-echuan] 已注册 10 个工具（3 写 + 7 读；已排除 12 个：...）：...`

- 名称必须与上文工具表**逐字一致**；写错（如 `echuan_plan_submmit`）会让插件激活直接失败，日志里给出未知项与合法名单。
- 条目会先 `trim` 再去重（保序）；空白项视为配置错误。
- 全部排除也合法：`已注册 0 个工具（已排除 22 个：...）`，心跳与派单照常（是否需要心跳只由 `worker.poll.enable` 决定）。

## 确认它真的在工作

先按 profile 里 app bundle 的正常用法启动 dsh（如 `dsh --profile <name>`；ACP profile 会等待 stdin），保持进程不退出，然后在另一个终端：

```bash
curl -fsS http://127.0.0.1:8720/v1/workers | jq '.items[] | {worker_id, state, presence, current_task_id, last_seen}'
```

- 看到 `w_` 开头的 `worker_id` 且 `presence=active`：已注册。
- `current_task_id` 非空：正在干活。
- 超过 `worker_ttl`（默认 45s）没有心跳：`presence=offline`。

插件日志关键字（启动时 / 卸载时）：

```
[dsh-echuan] 插件加载 ... poll=true
[dsh-echuan] 已注册 22 个工具（15 写 + 7 读）：...
[dsh-echuan] 已注册技能 echuan-task-execution（source=runtime）
[dsh-echuan] worker 心跳已停止（plugin dispose）：定时器已清除，不再产生请求与日志
[dsh-echuan] 已注销技能 echuan-task-execution
```

日志里的 `worker_id` 一律打码（如 `w_01M6****`），不会输出完整 id。

## 常见故障对照

| 现象 | 原因 | 动作 |
|---|---|---|
| 心跳全部失败，日志 `warn` 网络错误 | `baseUrl` 写错 / 平台没起 / 端口不对 | `curl http://127.0.0.1:8720/readyz` 确认平台活着，再改 `baseUrl` |
| 日志反复出现 `409 worker_unknown` | 平台库里没有这个 id（被保留期清理或换库） | 插件会自动清 id 重注册；持续出现则检查平台是否被重置 |
| `echuan_task_claim` 报 `409 worker_busy` | 该 worker 已持有卡 | 先 `echuan_task_report` 交掉当前卡，不要并发抢卡 |
| 续租/报告报 `409 lease_lost` | 租约过期或被回收 | 立即停止该卡，旧结果不可再提交，重新 `echuan_task_claim` |
| `/v1/workers` 显示 `presence=offline` | 插件停了 / 心跳关了 / 超过 45s 没心跳 | 确认 `worker.poll.enable=true`、进程在跑、网络通 |
| 报告被拒 `422 material_required` | `summary_md` 为空 | 按「改了什么 / 怎么自检 / 遗留风险」三段重写 |

## 已知行为（实测）

- 首轮心跳**延迟 5 秒**发出：几秒内就退出的命令（如 `dsh --profile <name> --help`）在 `worker.poll.enable=true` 时不会产生任何请求；若进程存活超过 5s 仍会注册（`--help` 不再是「必然打一次真实 POST」的命令）。
- 心跳间隔只认平台回包 `heartbeat_interval_ms`（默认 15s），客户端夹紧在 [500ms, 24h]。
- 连续 `worker_unknown` 会退避 1s→2s→5s→15s，不会打爆平台。

## 卸载

```bash
dsh plugin --profile <name> remove dsh-echuan
```

卸载后：工具与技能从会话消失、心跳停止，再次启动既无 `[dsh-echuan]` 日志也无请求。

## 给维护者：测试与验收

```bash
# 单测：typecheck + 297 条用例（在 kit/ 下）
cd kit && pnpm verify

# 真机端到端：独立 compose 栈 18720，约 90s，进/出不动 ~/echuan-data（在仓库根目录）
bash kit/scripts/e2e.sh
```
