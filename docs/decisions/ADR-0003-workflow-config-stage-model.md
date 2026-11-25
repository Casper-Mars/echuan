# ADR-0003 工作流配置与环节模型（YAML `flow.stages`）

- 状态：已接受（accepted）
- 日期：2026-11-25
- 来源：技术方案 v0.5 §1.1（决策记录 D1″–D8‴ 原样落位）、§6、§8.4、§8.6、§8.7、§9.2；PRD v0.2；issue #180
- 落位包：`internal/platform`。派生引擎、`GET /v1/flow`、web 下拉与徽标、kit 0.7.0、`migrations/0003` 由其他 dev 卡实现，但一律以本文件的决定为准

## 背景

平台原先的任务环节（需求 / 方案 / 拆解 / 开发）是代码里的枚举：`enum.TaskKind` 写死 4 个值，
审批后派生下一张卡的规则也写在用例层（拆解 → 开发读 plan JSON）。结果是「加一个环节」必须改代码、发版、
同时改执行侧 kit 的 `requireKind` 白名单。本 ADR 把「流程搬进配置」之后必须固定的那几条决定记下来。

## 决策记录（D1″–D8‴）

> 编号、选项与结论与技术方案 v0.5 §1.1 一致；每条末尾的「落地」说明本卡（配置与流程定义层）做到哪一步，
> 以及剩余实现归属。

### D1″ 配置只认 YAML

唯一格式 `.yaml` / `.yml`，`-config` 默认 `./echuan.yaml`，解析器用 `gopkg.in/yaml.v3`（`go.mod` 已有，本次进生产二进制）；
传 `.toml` 直接报错并指路 `configs/echuan.yaml`；`flow` 子树按 `KnownFields(true)` 的严格语义处理（未知字段 = 启动失败，R14）
并保留 `yaml.Node` 行号，其他顶层沿用「未知键忽略」；TOML 解析器与两份 toml 样例删除。

**落地**：`internal/platform/config.go` 只读 YAML、`flow_yaml.go` 走 `yaml.Node` 带行号解析；
`configs/echuan.toml`、`configs/echuan.container.toml` 已删，替代为 `configs/echuan.yaml`、`configs/echuan.container.yaml`；
`Dockerfile` 的 `CMD` 与 compose 的只读挂载改为 `/etc/echuan/echuan.yaml`。

### D2‴ name 对外、id 对内：`stage.name` 作唯一对外标识，平台映射成 int

| 选项 | 代价 | 结论 |
|---|---|---|
| **A `name` 作对外标识 + `id` 落库 + 可选 `label` 显示** | 每个环节多一个可选字段（`label`，可省略）；`name` 是自由文本（含中文），因此需要一条「name ∪ label 全局唯一」的校验 | **选** |
| B `key`（ASCII 机读）+ `name`（显示）双字段 | 与「kit 配 name」冲突：agent 配置里写哪个？两个字符串作用重叠，容易写错 | 不选 |
| C 只留 `id` + `name`，显示名也放 name 里 | AC7 要求缺省流程规则串逐字为 `requirement->design`，会直接不满足；kit 现有的 4 个键全要改 | 不选 |
| D kind 改字符串 | 被上一轮意见否决 | 不选 |

细则：

- `id`：正整数，全表唯一，**一旦用过永不复用**（否则历史卡会被解释成新环节）。默认流程 = 1/2/3/4（与存量值逐一对应）
- `name`：trim 后 1–32 字符，禁控制字符，可与 `label` 互为任意语言；**name ∪ label 作为一个集合必须全局唯一**（否则建卡时 name→id 解析有歧义）
- `label`：可选，缺省 = `name`
- DB 弱约束 `CHECK (kind > 0)`（沿用 `enum.go`「0 永不合法」）；词汇由配置裁决
- `enum.TaskKind` **仍是 int**：`Valid()` 从「4 值成员」改为「`> 0`」，`String()` 只对内置 4 值有名（日志用）
- 运维纪律的哨兵：启动时 `SELECT kind, COUNT(*) FROM tasks GROUP BY kind`，**库里有、配置里没有**的 id → warn（含卡数）

退出条件：若出现「同一环节在不同产品下要落成不同身份」的需求，才引入映射层；本期不做。

**落地**：模型与校验在 `flow.go` / `flow_validate.go`（`Stage` 携带 `ID`/`Name`/`Label`，R2/R3/R4/R15 落实上面全部约束）；
`enum.TaskKind` 的放开、`name→id` 映射、启动哨兵在后续卡实现。

### D3″ 派生模型：每个环节声明派生策略（图，不是链）

| mode | 条目从哪来 | 今天对应 | 用途 |
|---|---|---|---|
| `none` | 不派生 | 开发 | 链尾 / 纯人工环节 |
| `single` | 合成 1 个空条目 | 需求→方案、方案→拆解 | — |
| `list` | 读**本环节最新输出资料**里的清单文档（`source: artifact`，`format` 取 json 或 yaml，契约 = 今天的 plan JSON） | 拆解→N 张开发 | 任意环节按交付物清单展开 |
| `fanout` | 配置里直接列举条目 | 无 | 「方案通过后同时派安全/性能/兼容 3 张评审卡」 |

统一：条目 `{key, title, body_md, priority, depends_on[]}`（`key` 是**条目内部**的兄弟标识，与环节无关，允许重名于环节名）；
兄弟依赖按条目 key 解析；幂等 = 事务内按 `(parent_task_id, 目标环节 id)` 计数，>0 整批跳过；启动期环检测。

**落地**：`Derive`/`DeriveItem` 模型、R6/R7/R8/R9 校验已完成；事务内幂等、条目展开与兄弟依赖解析在派生引擎卡实现。

### D4″ 同项目同环节未完成卡上限：一列快照 + 一条部分唯一索引

`tasks.kind_parallel`（0/1，建卡时按配置 `open_cards` 快照）+ 一条部分唯一索引：

```sql
CREATE UNIQUE INDEX ux_tasks_open_per_kind ON tasks(project_id, kind)
  WHERE kind_parallel = 0 AND status IN (1, 2, 3, 4);
```

默认流程下 `dev = 1`、其余 0 ⇒ 与今天逐行等价。不选「只靠代码 COUNT」：那会把 DB 不变量降级为代码检查
（今天 `POST /v1/tasks` 的重复拦截完全靠索引，`store/v3/task.go:105-108` 只负责把唯一冲突翻成 409）。

**落地**：`open_cards: single|multiple` 的解析与 R9（被 list/fanout 指向的目标必须 `multiple`）已完成；
`kind_parallel` 列、部分唯一索引与建卡时快照在 `migrations/0003` 与派生引擎卡实现。

### D5″ 模板：固定 5 变量 + 条目变量；描述归配置，溯源行归平台

卡片级 `{上游卡号}{上游卡标题}{项目名}{产品名}{环节名}`；条目级（仅 `list`/`fanout`）`{条目标题}{条目key}{条目序号}`；
单遍替换；`context_md` 溯源行由平台生成 `由 {上游卡号} 自动派生（{src.name}->{dst.name}）`，**不可配置**
（AC7/AC11 靠它稳定）；标题优先用目标环节 `title` 模板，没配则用条目 `title` 原文。

**落地**：变量白名单与「未使用模板拒绝」在 R10/R17/R11；`context_md` 的生成在派生引擎卡实现，配置里没有也不会有对应字段。

### D6″ 存量语义不迁移（≠ 不做迁移）

迁移是结构性的（放宽 CHECK + 加一列），**不改任何存量值**：`kind` 1–4、标题、描述、上下文、依赖、状态全部原样。
PRD 非目标 5 说的是「不为配置变更改存量卡语义」，严格遵守（AC10）。

**落地**：见下文「迁移」一节——0003 只做结构改动，存量值零转换。

### D7″（新）执行侧包一次性升级：kit 0.6.10 → 0.7.0

| 选项 | 代价 | 结论 |
|---|---|---|
| **A 本需求内一次性升级 kit** | 要发一次包、要更新 agent 配置文档；效果是「此后加环节不再升包」 | **选** |
| B 不碰 kit，只靠平台 | agent 调用 `echuan_work_claim({kind:"安全评审"})` 会被本地 `requireKind` 拒；素材类别对未知 kind 一律落 `report` | 不选 |

kit 的具体改动（7 处，全部是「去掉硬编码」而不是「加新功能」）：

| 文件 | 现状 | 改成什么 |
|---|---|---|
| `kit/src/tools/v3.ts:64-75` | `requireKind` 白名单写死 4 值 | 改为 `requireKindName`：非空字符串、trim 后 ≤32 字符，**词汇由平台裁决**（400 时透传平台的可用清单） |
| `kit/src/tools/v3.ts:193` | 工具 schema 描述 `requirement / design / breakdown / dev` | 改为「平台流程里的环节 name，见 `GET /v1/flow`」 |
| `kit/src/tools/v3.ts:266-268` | 按 kind 本地猜素材类别，未命中落 `report` | 优先用任务 DTO 的 `material_kind`，缺失才回落 `report` |
| `kit/src/v3-types.ts:4` | `V3TaskKind` 是 4 值联合 | 改为 `string`；`V3Task` 增 `kind_id` / `kind_label` / `material_kind` |
| `kit/src/config.ts:32-37,128-133` | `DEFAULT_PRESET_BY_KIND` 键 = 4 个 kind；文案写「kind → preset」 | 键**不变**（默认流程 name 就是这 4 个），文案改「环节 name → preset」，并写明「加环节时在 agent 配置里加 name」 |
| `kit/src/pull-worker.ts:33/42/45` | 类型用 `V3TaskKind` | 随 `string` 放宽（运行时本就无校验） |
| `kit/src/tools/worker-info.ts`、`kit/README`、`kit/package.json` | kinds 文案与版本 | 文案改口径；版本 0.6.10 → **0.7.0**（M2 记一次） |

M2 口径：本需求内 **1 次**（0.6.10 → 0.7.0），此后加环节 0 次。AC6 的「执行侧包版本不变」判定窗口是**演练期间**：
演练开始前把执行侧装到 0.7.0，演练全程版本不变。

混用矩阵（发布窗口与回滚期都要成立）：

| 组合 | 心跳 / 领取 | 工具面 `echuan_work_claim({kind: 新 name})` | 素材类别 | 结论 |
|---|---|---|---|---|
| 旧 kit 0.6.10 + 新平台（默认流程） | 正常（4 个 name 未变） | 正常 | 正常 | 可接受（分阶段发布窗口） |
| 旧 kit 0.6.10 + 新平台（加了新环节） | 正常（pull 循环不校验词汇） | **本地拒绝**（白名单 4 值） | 落 `report`（只影响渲染） | 可用但受限 ⇒ 尽快升 0.7.0 |
| 新 kit 0.7.0 + 新平台 | 正常 | 正常 | 取 `material_kind` | 目标态 |
| 新 kit 0.7.0 + 旧平台（回滚期） | 正常（旧平台只认 4 值，默认流程一致） | 平台 400，错误原样透传 | 回退本地映射 | 安全：放宽只是少一层本地校验，不会伪造成功 |

**落地**：本卡未改动 `kit/`（卡片范围明确排除）；平台侧配套是「HTTP 用 name、平台按配置映射成 id」与 `GET /v1/flow`，后者在接口卡实现。

### D8‴（新）派生卡的任务说明：三级解析，`list` 不参与缺省

| 选项 | 代价 | 结论 |
|---|---|---|
| **A 三级解析：条目正文 → 环节 `description` → 流程级 `default_description`** | 多一个流程级配置项；需要一条校验（R16）把「漏配」变成启动失败；默认流程必须显式写 `description: ""` 才能保住 AC7 | **选** |
| B 只允许每个环节写 `description`（无流程级兜底） | 新增环节时容易只写 title 忘了 description，agent 拿到的卡又没有说明 | 不选 |
| C 只做流程级 `default_description`（无环节级） | 所有派生卡一段通用说明，等于没写清楚要做什么 | 不选 |
| D 给 `list` 也加缺省回落 | 会覆盖上游 agent 写的任务说明（上游明明为每张卡写了 `body_md`） | 不选 |

取值顺序（实现为 `FlowConfig.ResolveDescription`，返回「模板 + 来源」两段）：

1. 条目正文 `body_md` —— **仅 `list`/`fanout`**；`list` 条目为空就写空，**不回落**（上游 agent 的意图不能被覆盖）；
2. 目标环节的 `description` 模板；
3. 流程级 `flow.default_description`；
4. 空字符串。

细则：

- `list`（plan json）路径**不回落**到第 2/3 级，条目 `body_md` 为空就写空（与今天一致）；
- `fanout` 的条目是配置者手写的，条目 body 为空时回落；
- `single` 走第 2 → 3 级；
- R16：会被 `single` 派生、却既没有环节 `description` 也没有 `default_description` 的环节 → 拒绝启动；
  **显式 `description: ""` 视为有意留空**，放行（默认流程就靠这个保住 AC7 的逐字一致）。

**落地**：`ResolveDescription` 与 R16、R17 已完成并有 10 例表驱动测试；模板渲染与写卡在派生引擎卡实现。

## 启动校验 R1–R17

实现于 `flow_validate.go`，全部在 `Load` 内执行；失败即 `启动退出`，输出 `<路径>:<行号>: <原因>`。

| 编号 | 规则 |
|---|---|
| R1 | `flow.stages` 非空 |
| R2 | `id` 为正整数、全表唯一 |
| R3 | `name` trim 后 1–32 字符、无控制字符、全表唯一 |
| R4 | `label`（若给）trim 后 1–32 字符；name ∪ label 全局唯一 |
| R5 | `material` ∈ {requirement, design, plan, report, context, other} |
| R6 | `derive.mode` ∈ {none, single, list, fanout}；`list` 必须有 `source: artifact` 与 `format ∈ {json, yaml}`；`fanout` 的 `items` 非空且条目 `key` 唯一；`items` 只允许出现在 fanout 上 |
| R7 | `derive.to` 非空、指向存在的 `name`、不是自己；`mode: none` 时不许写 `to` |
| R8 | 派生图无环（DFS，报错给出环上 name 序列） |
| R9 | 被 `list`/`fanout` 指向的目标必须 `open_cards: multiple` |
| R10 | 模板变量白名单（卡片级 5 个 + 条目级 3 个，后者仅 list/fanout） |
| R11 | 没有任何上游指向的环节配了 `title`/`description` → 拒（模板永远不会被渲染） |
| R12 | `closing: true` 至多一个环节 |
| R13 | `auto: false` 时仍执行 R1–R12 |
| R14 | `flow` 子树出现未知字段 → 拒（顶层未知字段仍忽略） |
| R15 | 内置 id 1–4 的名字不许改嫁（1=requirement、2=design、3=breakdown、4=dev） |
| R16 | 会被 `single` 派生的环节必须显式声明描述来源（见 D8‴） |
| R17 | `flow.default_description` / 环节 `description` 的模板变量白名单同 R10 |

跨字段校验（如 `worker_retention > worker_ttl`）失败时，行号取自该键在文件里的位置；被环境变量覆盖的键不再报文件行号。

## 指纹（fingerprint）

对 `auto`、`flow.default_description` 与每个环节的
`id|name|label|material|open_cards|closing|title|description|derive` 拼成稳定文本求 `sha256`，取前 12 位十六进制。
「未配置 description」与「显式 `""`」编码不同（`<unset>` vs `<>`），因为两者生成不同的卡。
指纹只用于启动日志（`flow chain loaded` 的 `fingerprint`）与派生事件 detail（哪份配置派生出来的卡），
**不落库**：显示名不快照，溯源看事件。

## 内置默认流程（§8.7，与今天逐字段等价）

删掉 `flow` 段后生效的流程就是 `requirement(需求) → design(方案) → breakdown(拆解) → dev(开发)`：
id 1–4 与库里的存量值逐一对应，`name` 保持英文（审计规则串 `requirement->design` 逐字不变、kit 的 `presetByKind` 四个键不用改），
`label` 承担看板上的中文；`design`/`breakdown` 显式 `description: ""`，`dev` 是 `open_cards: multiple` + `closing: true`，
`breakdown → dev` 是 `list` + `json`。

内置默认流程走与配置文件完全相同的解析与 R1–R17 校验路径（不是特例），因此「默认流程本身合法」由单测守着；
实测三份输入（无 `flow` 段 / `configs/echuan.yaml` / `configs/echuan.container.yaml`）的指纹与环节序列逐项相同。

## 迁移

### 配置文件与部署面

- `configs/echuan.toml` → `configs/echuan.yaml`，`configs/echuan.container.toml` → `configs/echuan.container.yaml`；
  `Dockerfile` 的 `CMD` 与 `docker-compose.yml` 的只读挂载一起改成 `/etc/echuan/echuan.yaml`；
  `scripts/e2e-worker.sh` 生成的临时配置同样改 YAML；
- 飞书的扁平 `im_*` 键改成嵌套 `im:` 段（环境变量仍是 `ECHUAN_IM_*`，优先级不变）。

### 数据库迁移 `migrations/0003_kind_from_config.sql`

结构性迁移，**与「不迁移存量语义」不矛盾**（D6″）：只放宽 CHECK 与加一列，不动任何存量值。

1. `tasks` 重建：`kind` 的 CHECK 从 `IN (1,2,3,4)` 放宽为 `> 0`，新增 `kind_parallel INTEGER NOT NULL DEFAULT 0 CHECK (kind_parallel IN (0,1))`；
2. `gate_decisions.gate`、`worker_kinds.kind` 重建：CHECK 放宽为 `> 0`，值不动；
3. `kind` **原样复制（零值转换）**；`kind_parallel` 按今天的行为快照：`CASE WHEN kind = 4 THEN 1 ELSE 0 END`（今天只有开发卡可并行）；
4. 索引重建：新增 `ux_tasks_open_per_kind`（见 D4″），保留队列/租约/看板索引与 `ux_tasks_worker_lease`，删除旧的 `ux_tasks_active_doc`；
5. 迁移自检（验收点）：三表 COUNT 守恒；`kind` 零转换（`WHERE kind NOT BETWEEN 1 AND 4` 计数为 0）；`kind_parallel` 仅在 `kind = 4` 上为 1；
   runner 的 `foreign_key_check` 覆盖 `task_deps`/`attachments`/`workers.current_task_id`；带 id 复制后 `sqlite_sequence` 落到 `max(id)`；索引齐备且行为等价（开发可并行、其余唯一）。

回滚：**不写 down SQL**（三表重建 + 旧二进制版本守卫，就地降级没有意义且留隐患）。真实回滚 = 恢复升级前的库快照；
即使手工把 `user_version` 降级，旧代码插入开发卡时 `kind_parallel` 取默认 0 会撞 `ux_tasks_open_per_kind`，所以镜像回退必须连同库快照一起做。

### 运维纪律

启动后仍有「库里有、配置里没有」的 `kind` 告警（`flow kind not in config`，含卡数），作为整数 id 复用/删环节的唯一自动哨兵（后续卡实现）。

## 本卡（配置与流程定义层）实现范围

`internal/platform`：`config.go`（YAML 加载）、`flow.go`（模型 + 指纹 + 描述三级解析）、`flow_yaml.go`（带行号解析）、
`flow_validate.go`（R1–R17）、`flow_default.go`（内置默认流程）；`cmd/echuan/main.go` 打印 `flow chain loaded`；
`configs/*.yaml` 与部署/文档同步。

不在本卡：派生引擎（写卡、幂等、模板渲染、`context_md`）、`GET /v1/flow`、`enum.TaskKind` 放宽、启动哨兵、
`migrations/0003`、web 下拉与徽标、kit 0.7.0。

## 修订 R2：总闸下放、`description` 语义回收（配置契约 v2）

- 日期：2026-11-25；来源：issue #188 需求 v0.2 + 技术方案 v0.1（§5 D1–D7、§6.2/§6.3、§7.1、§10）；issue #180 结论不变处不重写。
- 性质：**硬切**。二进制与配置必须成对升级：旧配置的 `flow.auto` 会让新二进制拒绝启动，新配置的
  `task` / `derive.auto` 会让旧二进制拒绝启动（R14）。

### 变更点（覆盖上文的对应条目）

| 上文条目 | 改成 |
|---|---|
| D1″ 之外的隐含前提「`flow.auto` 是全局总闸」 | 删除 `flow.auto`。派生唯一开关是 `stages[].derive.auto`（bool，缺省 `false`）。写了 `to`/`mode` 却忘写 `auto: true` → 静默不派生，由启动日志 `stages=` 的 `auto=on|off` 暴露 |
| D2‴ 字段表：`description` = 任务说明模板 | 拆成两个字段：`description` = **阶段描述**（plain string，给人看，不做模板、不渲染、不进卡正文、R11/R16/R17 都不管它）；`task` = **任务说明模板**（`nil` = 未配置 / `""` = 有意留空 / 文本 = 模板） |
| D8‴ 三级解析第 2 级 | 由「目标环节 `description`」改为「目标环节 `task`」。其余（`list` 不回落、条目正文优先、流程级兜底）不变 |
| R11 | 只查 `title` 与 `task`；`description` 放行（入口环节也能写阶段描述） |
| R13 | **废止**：没有总闸可关，R1–R17 恒在加载时执行 |
| R14 | `flow` 段允许的键去掉 `auto`（写 `flow.auto` 给专门迁移文案）；stage 白名单加 `task`；`derive` 白名单加 `auto` |
| R16 | 被 `single` 派生的环节必须有 `task`（或 `flow.default_description`）；文案指明「`description` 现在只表示阶段描述，不再作为任务说明兜底」 |
| R17 | 模板变量白名单的校验对象由 `description` 换成 `task` |
| 指纹 | 升到 **scheme 2**：digest 首行 `scheme=2`，去掉 `auto` 行，每环节行末的 `description` 换成 `task`，`derive` 串加 `auto=%t`。输出仍是 12 位 hex；`flow.auto_create` 事件结构与 HTTP 响应都不加字段 |
| `GET /v1/flow` | 顶层删 `auto`；每条 stage 增 `task`（未配置为 `""`）；`description` 字段名不变、语义变成阶段描述 |

### 新增的非阻断 warning（W1）

`stages[i].derive.auto: true` 且 `derive.mode` 为 `none`/`""`：**不挡启动**，启动日志每个环节一条
`WARN flow derive switch on without target stage="…" …; nothing will be derived`。

### 内置默认流程

与升级前逐字等价，只是写法换成 v2：`requirement`/`design`/`breakdown` 三处显式 `derive.auto: true`，
`design`/`breakdown` 的 `description: ""` 换成 `task: ""`（仍是有意留空，load-bearing：删掉它派生卡正文会被
`flow.default_description` 填上），四个环节各补一句中文阶段描述（`description`，不进卡）。

### 迁移（运维）

1. 删 `flow.auto`，逐个环节写 `derive.auto: true`；
2. 把原来放在 `description` 里的任务说明文字搬到 `task`；
3. 原来有意写 `description: ""` 的环节改成 `task: ""`；
4. 想给环节写自我介绍，写在 `description`（不再有 R11 例外）；
5. 映射后的指纹与历史事件里的 `config` 值**不可比**（scheme 1 → 2，属预期）。

回滚是成对操作：镜像与配置一起回退，单独回退任一方都启动失败。
