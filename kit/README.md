# dsh-echuan — 驿传平台接入工具包

这是驿传（echuan）的 dsh（Cordis 4）Agent 接入插件：把平台 HTTP 契约接进 Agent。装进一个 profile 后，Agent 能领 work item、续租、交报告，平台也能按 worker 声明的 `kinds`（平台流程里的**环节 name**）把派单投递给它。

## 能力边界（先看这段）

插件**只做三件事**：

1. **定时心跳**：上报 `state`（`busy` = 手里有未交付的 work item，`idle` = 没有）、`kinds`（本 worker 能做的环节 name）与 metadata，并带上平台下发的 `worker_id`（仅存内存）。
2. **投递派单**：心跳只做注册；任务用 `POST /v1/tasks/claim` 拉取。领到后按 `project_id` + `presetByKind` 找/建会话（sessionId = `<project>--<preset>`，分隔符见 `session.ts` 的 `SESSION_ID_SEPARATOR`——宿主 per-record 存储要求键无 `:`；cwd 取会话数最多的 workspace），再投给该会话的 agent。不做 kind → agent 的分流；需要角色 / 模型隔离就拆 worker。
3. **提供 19 个工具**（10 写 + 9 读；由 preset 子路径注册，可用 `exclude` 按 agent 裁剪，见「配置」）：
   - 任务写：`echuan_task_claim`（领卡）、`echuan_task_renew`（续租）、`echuan_task_report`（交交付物，转 `in_review`）、`echuan_task_create`（建卡）、`echuan_task_update`（改未领取的卡）、`echuan_task_attach`（挂附件）
   - 项目与产品写：`echuan_project_create`（建项目容器，不建卡）、`echuan_project_close`（关项目）、`echuan_project_reopen`（重开项目）、`echuan_product_create`（建产品）
   - 读：`echuan_inbox`（待办）、`echuan_product_list`（产品列表 / 详情）、`echuan_project_list`（项目列表）、`echuan_project_get`（项目详情）、`echuan_stage_list`（当前流程的环节清单，只给 id / name / 阶段描述）、`echuan_task_list`（任务列表）、`echuan_task_get`（任务详情）、`echuan_material_get`（资料正文）、`echuan_worker_info`（worker 诊断，零 HTTP）

`echuan_worker_info` 只读进程内状态（心跳开关、声明的环节 name、打码的 `worker_id`、派单目标 agent、inflight 工作项与租约），**不发任何 HTTP 请求、不写盘、不改状态**；`ctx.agents` 缺失时只降级展示。

**写工具契约（v3）**：

| 工具 | 端点 | 关键入参 | 返回 / 下一步 |
|---|---|---|---|
| `echuan_task_claim` | `POST /v1/tasks/claim` | `kind`（环节 name，必填；trim 后 ≤32 字符） | 领到卡回 `task_id` / `lease_id` / 租约到期 / 归属项目 / 输入物料；平台 204 = 当前无活（`no_work`，不当错误重试）。`worker.poll.enable=true` 时本地拒绝（领卡由心跳负责）；worker 尚未注册时本地拒绝（`conflict`） |
| `echuan_task_renew` | `POST /v1/tasks/lease` | `task_id`、`lease_id` | 新 `lease_id` 与 `lease_until`；之后一律用新租约交报告 |
| `echuan_task_report` | `POST /v1/tasks/report`（缺 `material_id` 时先 `POST /v1/materials` 建物料） | `task_id`、`lease_id`，以及 `material_id` 或 `summary_md`（二选一）；可选 `material_title`、`brief_md`（≤600 字决策简报）、`brief_title` | 任务转 `in_review`；`brief_md` 落成 kind=context 物料并挂 `output`，简报失败不回滚交付、只回 `brief_error` |
| `echuan_task_create` | `POST /v1/tasks` | `project_id`、`kind`（环节 name）、`title`（均必填），可选 `task_id`、`parent_task_id`、`description_md`、`context_md`、`priority`、`max_attempts`、`depends_on`（`task_id` 数组） | 新卡；依赖未满足时平台落 `pending` |
| `echuan_task_update` | `PATCH /v1/tasks?id=` | `id`、`title`（必填），可选 `description_md`、`context_md`、`priority` | 更新后的卡；只用于未领取的卡 |
| `echuan_task_attach` | `POST /v1/tasks/attach` | `material_id`、`role`（只接受 `input` / `output`），`project_id` 与 `task_id` 二选一，可选 `sort_no` | 附件记录 |
| `echuan_project_create` | `POST /v1/projects` | `product_id`、`name`（必填），可选 `context_md` | 项目容器；**不会建任何卡** |
| `echuan_project_close` | `POST /v1/projects/close?id=` | `id`，可选 `force`（`true` 会取消未完成任务） | 关闭确认 |
| `echuan_project_reopen` | `POST /v1/projects/reopen?id=` | `id` | 重开确认；已取消的任务不会复活 |
| `echuan_product_create` | `POST /v1/products` | `product_id`、`name`、`git_url`（均必填），可选 `description`、`license` | 产品 |

**环节 name（`kind`）的词汇属于平台**：kit 只校验形态（非空、trim 后 ≤32 字符），不维护枚举；名字写错照发请求，平台 400（含「可用环节: …」清单）原样透出。当前可用环节用 `echuan_stage_list` 查。

`echuan_task_claim` 的 `worker_id` **不在工具参数里**：它只来自心跳循环的内存（平台契约必填）。`worker.poll.enable=false` 或首轮心跳尚未成功时，领卡会在本地拒绝、零请求。

**典型调用顺序**：`echuan_product_create`（建产品）→ `echuan_project_create`（建项目容器）→ `echuan_task_create` 建卡（或由 `breakdown` 卡的报告生成）→ worker 领卡（心跳派单或 `echuan_task_claim`）→ `echuan_task_report` 交付 → 人工审 → `echuan_project_close`。

**交付物形态由卡的 `kind`（环节 name）决定**：所有卡都用 `echuan_task_report` 交付，`summary_md` 就是交付正文（物料类别取平台给这张卡裁定的 `material_kind`）：

| 卡 kind | `summary_md` 写什么 |
|---|---|
| `requirement` / `design` | 需求 / 方案正文（Markdown） |
| `breakdown` | 计划 JSON：`{"items":[{"seq_no":1,"title":"…","body_md":"…","priority":0,"depends_on":[1]}]}`；平台据此生成 N 条 dev 卡 |
| `dev` | 代码交付的四段式报告正文 |

环节清单与默认流程以平台为准（`echuan_stage_list` 查当前 name）；kit 不维护环节词汇，平台加环节不需要升级包。

只读工具（都只发 `GET`，不缓存、不落盘）：

| 工具 | 端点 | 入参 | 返回 |
|---|---|---|---|
| `echuan_inbox` | `GET /v1/inbox` | 无 | 待领取 + 待审核的任务 |
| `echuan_product_list` | `GET /v1/products` | 可选 `id`（带 id 即详情）、`after_id`、`limit` | 产品清单或单个产品 |
| `echuan_project_list` | `GET /v1/projects` | 可选 `product_id`、`status`（只认 `open` / `closed`）、`after_id`、`limit` | 项目清单（按新建倒序，最新在前） |
| `echuan_project_get` | `GET /v1/projects?id=` | `id`（必填） | 项目详情 |
| `echuan_stage_list` | `GET /v1/flow` | 无 | 当前流程的环节清单，每条**只有** id / name / 阶段描述；平台不可达或非 2xx 直接报错，不回落成空清单 |
| `echuan_task_list` | `GET /v1/tasks` | 可选 `project_id`、`kind`（环节 name）、`status`、`limit` | 任务清单 |
| `echuan_task_get` | `GET /v1/tasks?id=` | `id`（必填） | 任务详情 |
| `echuan_material_get` | `GET /v1/materials?id=` | `id`（必填） | 资料正文 |
| `echuan_worker_info` | 无（零 HTTP） | 无 | 进程内 worker 诊断 |

19 个工具对应 23 条 HTTP operation（登记在 `src/client.ts` 的 `TOOL_OPERATIONS`，`echuan_worker_info` 零 HTTP）：项目 / 任务各由一个 `GET` 同时承担列表与按 id 取详情。

写工具在本地先校验：必填字段为空或全空白、`echuan_task_attach` 的 `role` 不是 `input` / `output`、环节 name 超过 32 字符，都直接报 `invalid_argument`、**不发请求**；平台错误（400 / 404 / 409 / 413）原样透出 code 与 message，`details` 的 `field` / `reason` 会附加到错误文本（如 `（title: is required）`），不自动重试。

`exclude` 只影响**本进程注册哪些工具**：不改工具定义、参数或平台 HTTP 契约，也不影响心跳与派单；不写 `exclude` 时按子路径名单全量注册（`tools/leader` 19 个、`tools/requester` 16 个、`tools/readonly` 9 个只读）。

**不提供**：验收、返工、取消、改状态——所有审批类动作一律没有。**不代交报告**：交付必须由 agent 自己调 `echuan_task_report`。**不缓存、不落盘**：`worker_id` 只在内存，进程重启后重新注册（会拿到新的 `w_` id）；工具本身不写本地磁盘（物料与简报都只落在平台侧）。

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
pnpm build && pnpm pack            # 产出 dsh-echuan-0.8.0.tgz（已被 gitignore）
dsh plugin --profile <name> add ./dsh-echuan-0.8.0.tgz
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
    exclude: []                           # 不注册的工具名；默认 [] = 全量 19 个
    worker:
      poll:
        enable: true                      # 默认关闭；打开才会定时心跳
      presetByKind:                       # 环节 name → preset（角色路由在 worker 侧）
        dev: dev-leader
      # 键即本 worker 声明的环节 name（心跳上报的 kinds），不再单独配置 kinds
      metadata:                           # 原样上报，序列化后 ≤8KiB
        host: build-01
        os: darwin
        agent_version: 1.0.0
```

- `worker.presetByKind` 是 **worker 侧的角色路由表**：键是平台流程里的**环节 name**（`GET /v1/flow` 的 `stages[].name`），默认 `requirement=pm, design=cto, breakdown=cto, dev=dev-leader`——就是内置默认流程的四个环节，所以存量配置零改动。平台的派单只带环节 name，不带任何 preset 字段——新增角色只改这里（+ 在 Host 装好 preset），平台零改动。写空对象 `{}` 表示不做映射，会话回落 registry 默认 preset。
- **加环节不需要升包（0.7.0 起）**：平台流程配置里新增一个环节后，在 agent 配置的 `presetByKind` 里加一条 `<环节 name>: <preset>` 即可，心跳、领取、上报全部走名字，kit 不维护环节词汇。名字写错不会在本地被拒，而是把平台的 `400`（含「可用环节: name(label), …」清单）原样透出。唯一保留的本地校验是形态：非空、trim 后 ≤32 字符（与平台 R3 一致）。
- **`worker.presetByKind` 是唯一的角色/能力配置项**：键即本 worker 声明的 `kinds`（能领什么卡），值即拿到卡后挂哪个 preset。心跳上报的 `kinds` 是它的键（派生值），没有独立的 `worker.kinds` 配置——有映射就必然有可接的卡。kind 词汇属于平台，kit 不维护枚举：拼错的 kind 由平台心跳拒绝（错误信息更准），不会因平台新增阶段而编译失败。
- `worker.presetByKind` 为空（`{}`）时**不启动心跳循环**：没有映射就没有可接的 kind，心跳发出去只会每轮 400。此时只 warn 一条，工具仍可正常调用（但 `echuan_task_claim` 因没有 `worker_id` 会在本地拒绝）。
- `worker.poll.enable=false` 是默认值：不建定时器、零请求；**手动领卡需要先注册**（见上一条）。
- `metadata` 序列化后超过 8KiB 时插件**不启动心跳循环**并 warn 一条（不是截断），避免每轮都被平台拒。
- `exclude` 默认 `[]`（不排除，全量 19 个）；工具名写错或含空白项时**插件激活直接失败**（`TypeError`，错误里列出未知项与合法名单），不会静默少注册。
- **一个 worker 一个身份，一个项目按角色拆会话**：派单先按 `kind` 查本机
  `worker.presetByKind` 得到 preset，再按 `project_id` + preset ensureSession，
  会话键 = `项目号--preset`（分隔符见 `session.ts` 的 `SESSION_ID_SEPARATOR`，宿主
  per-record 存储要求键无 `:`；映射未命中时退回「项目号即会话号」的旧键），
  cwd 取会话数最多的 workspace；拿到该会话的 agent 再投递。会话未就绪时只 warn 一条、
  本轮跳过、下一轮重试。**平台不认识 preset 名**：路由表完全属于 worker 侧配置。
- **agentOptions 与 create/resume 同源**：provider/model 取 `agentDefaultModel`，否则恢复出的会话没有模型，首轮 turn 会在 `{{model}}` 插值处失败。
- 真实例子：本机起好平台后，把 `enable` 改成 `true` 并配好 `presetByKind`，`baseUrl` 保持 `http://127.0.0.1:8720`，重启 dsh 即可。

### 按 agent 裁剪工具（exclude）

同一份 kit 可以装进多个 profile：工具由 preset 子路径注册——`dsh-echuan/tools/leader` 19 个（10 写 + 9 读）、`dsh-echuan/tools/requester` 16 个（9 读 + 7 写）、`dsh-echuan/tools/readonly` 9 个只读；再用 `exclude` 在这份名单上继续裁剪。过滤只发生在**注册层**——工具定义、参数校验与平台 HTTP 契约都不变，心跳 / 派单也不受影响。

插件日志只报 `[dsh-echuan] host 服务就绪；工具由 preset 子路径注册`，不再输出「已注册 N 个工具」的条数行。

**builder（全量）**：不写 `exclude` 即可，也可显式写 `exclude: []`：

```yaml
- id: echuan
  config:
    baseUrl: http://127.0.0.1:8720
    exclude: []
```

**worker（只留领卡 / 续租 / 交报告 + 全部只读）**：在 leader 名单上排除其余 7 个写工具，注册 12 个（3 写 + 9 读）：

```yaml
- id: echuan
  config:
    baseUrl: http://127.0.0.1:8720
    worker:
      poll:
        enable: true                      # worker 通常需要派单，打开心跳
      presetByKind:
        dev: dev-leader
    exclude:
      - echuan_task_create
      - echuan_task_update
      - echuan_task_attach
      - echuan_project_create
      - echuan_project_close
      - echuan_project_reopen
      - echuan_product_create
```

> 上面是 **dev-only worker** 的裁剪示例。阶段卡（`requirement` / `design` / `breakdown` / `dev`）都用 `echuan_task_report` 交付、`summary_md` 就是正文，不需要额外写工具；只有会话里要建卡 / 管项目时才需要上面这些写工具，或直接不传 `exclude`（全量 19 个）。

- 名称必须与上文工具表**逐字一致**；写错（拼错一个字母、或写成本进程未声明的名字）会让插件激活直接失败，日志里给出未知项与合法名单。
- 条目会先 `trim` 再去重（保序）；空白项视为配置错误。
- 全部排除也合法（注册 0 个工具），心跳与派单照常（是否需要心跳只由 `worker.poll.enable` 与 `worker.presetByKind` 是否为空决定）。

## 确认它真的在工作

先按 profile 里 app bundle 的正常用法启动 dsh（如 `dsh --profile <name>`；ACP profile 会等待 stdin），保持进程不退出，然后在另一个终端：

```bash
curl -fsS http://127.0.0.1:8720/v1/workers | jq '.items[] | {worker_id, state, presence, kinds, current_work_item_id, last_seen}'
curl -fsS --get --data-urlencode 'id=<worker_id>' http://127.0.0.1:8720/v1/workers | jq .
```

- 看到 `w_` 开头的 `worker_id` 且 `presence=active`：已注册。
- `presence` 由服务端按 `last_seen + worker_ttl` 推导，只有 `active` / `offline` 两个值；客户端不自己算过期。
- `kinds` 与配置一致（都是环节 name）：平台只会派这些环节的卡。
- `current_work_item_id` 非空：正在干活。
- 超过 `worker_ttl`（默认 45s）没有心跳：`presence=offline`。
- `?id=` 详情比列表多 `first_seen` / `heartbeat_interval_ms` / `lease_ttl_seconds` / `metadata`；
  id 不在册是 `404 not_found`，空白 id 是 `400 invalid_argument`（`details[0].field=id`）。

插件日志关键字（启动时 / 卸载时）：

```
[dsh-echuan] 插件加载 ... poll=true kinds=[dev] presets=[dev→dev-leader]
[dsh-echuan] host 服务就绪；工具由 preset 子路径注册
[dsh-echuan] 已注册技能 echuan-task-execution（source=runtime）
[dsh-echuan] worker 心跳已启动（kinds=dev；首轮心跳将在 5000ms 后发出，兜底间隔 15000ms）
[dsh-echuan] worker 心跳已停止（plugin dispose）：定时器已清除，不再产生请求与日志
[dsh-echuan] 已注销技能 echuan-task-execution
```

日志里的 `worker_id` 一律打码（如 `w_01M6****`），不会输出完整 id。

## 常见故障对照

| 现象 | 原因 | 动作 |
|---|---|---|
| 心跳全部失败，日志 `warn` 网络错误 | `baseUrl` 写错 / 平台没起 / 端口不对 | `curl http://127.0.0.1:8720/readyz` 确认平台活着，再改 `baseUrl` |
| 日志反复出现 `409 conflict`（心跳不会回 409，只会出现在 claim） | 该 `worker_id` 未注册（被保留期清理或平台换库） | 插件会自动清 id 重注册；持续出现则检查平台是否被重置 |
| `echuan_task_claim` 报 `409 conflict`，message 含「未注册，请先发一次心跳」 | 平台注册表里没有这个 `worker_id`（还没心跳过，或已被保留期清理） | 等本进程下一轮心跳成功后再领卡；插件每轮心跳会自动重新注册，不要并发重试 claim |
| 日志 `worker.presetByKind 为空，不启动心跳循环` | 没有任何环节 name → preset 映射 | 补至少一条映射，或保持 `poll.enable=false` |
| 派单来了但没人干（日志 `没有可用 agent`） | 本进程还没有 root agent | 确认插件加载在有 agent 的 profile 里；下一轮心跳会自动重试投递 |
| `echuan_task_claim` 本地拒绝 `worker_id 尚未就绪` | 心跳没开 / 尚未注册成功 | 打开 `worker.poll.enable` 并配好 `presetByKind`，等首轮心跳成功后再领 |
| `echuan_task_claim` 报 `409 conflict`，message 含「已持卡」 | 该 worker 已持有卡（一个 worker 同时只持一张） | 先 `echuan_task_report` 交掉当前卡，不要并发抢卡 |
| 续租/报告报 `409 lease_lost` | 租约过期或被回收 | 立即停止该卡，旧结果不可再提交，重新 `echuan_task_claim` |
| `/v1/workers` 显示 `presence=offline` | 插件停了 / 心跳关了 / 超过 45s 没心跳 | 确认 `worker.poll.enable=true`、`presetByKind` 非空、进程在跑、网络通 |
| 报告被拒 `422 material_required` | 非 breakdown 卡既没有 `material_id` 也没有 `summary_md` | 直接传 `summary_md`（平台落报告物料），或传已有物料的 `material_id`；breakdown 卡改用 `plan` 参数 |

## 已知行为（实测）

- 首轮心跳**延迟 5 秒**发出：几秒内就退出的命令（如 `dsh --profile <name> --help`）在 `worker.poll.enable=true` 时不会产生任何请求；若进程存活超过 5s 仍会注册（`--help` 不再是「必然打一次真实 POST」的命令）。
- 心跳成功是 **204 空体**（不返回 worker 对象、不下发指令）：客户端不从心跳回包读任何字段，
  心跳间隔取本进程配置/兜底常量（默认 15s）；平台建议值只由 `GET /v1/workers?id=` 的
  `heartbeat_interval_ms` 发布，供运维核对。
- 每次心跳都上报映射的键（`kinds` = 环节 name），平台据此覆盖本 worker 的声明；`dispatch` 按 `work_id + lease_id` 去重（内存 LRU 200 条），重复下发只投一次。
- `state` 是**占用声明**，不是 agent 的瞬时运行态：inflight 非空（或上一轮回包 `current_work_item_id` 非空，覆盖手动 `echuan_task_claim` 领的卡）期间**每一轮都上报 `busy`**，所以回合间隙不会被平台当成空闲再派一张卡；只有 `echuan_task_report` 成功（`notifyReported`）或租约过期后才会回到 `idle`。一个 worker 同一时间只持一张卡。
- 心跳失败（网络 / 平台 5xx）按 1s→2s→5s→15s 退避重试，不会打爆平台；心跳任何情况都不返回 409。

## 版本兼容性

**声明位置**：`kit/package.json` → `engines.dsh`（当前 `>=0.2.1-rc.2 <0.3.0`）。dsh 安装插件时读它，
不满足直接拒绝安装并打印不满足项——升级 dsh 后装不上，先看这个字段。

仓库根 `.github/workflows/dsh-compat-kit.yml` 在 **kit 变更合并进 main / 每天 09:00 / 手动触发**时，
对 npm 上 dsh 的 `latest`（阻塞）与 `next`（仅提示）做三层校验：

1. **安装准入**：按发布形态 `npm pack` 后装进全新 dsh 环境；
2. **patch 组合**：`dsh --profile <p> --dump-config` 能把 echuan 插件组进配置树；
3. **运行时装配**：冷启动 profile，断言 19 个工具与 1 个技能真的注册。

不兼容时自动开 issue（label `dsh-compat`），恢复后自动关闭。本地跑同一条路径：

```bash
cd kit
pnpm compat                 # 校验 npm latest
pnpm compat 0.2.2-rc.2      # 校验指定版本
```

## 卸载

```bash
dsh plugin --profile <name> remove dsh-echuan
```

卸载后：工具与技能从会话消失、心跳停止，再次启动既无 `[dsh-echuan]` 日志也无请求。

## 给维护者：测试与验收

```bash
# 单测：typecheck + 全量用例（在 kit/ 下）
cd kit && pnpm verify

# 真机端到端：独立 compose 栈 18720，约 90s，进/出不动 ~/echuan-data（在仓库根目录）
bash kit/scripts/e2e.sh
```
