# ADR-0003 工作流配置与环节模型（YAML `flow.stages`）

- 状态：已接受（accepted）
- 日期：2026-11-25
- 来源：技术方案 v0.5 §1.1 D1″–D8‴、§8.4、§8.6、§8.7；PRD v0.2；issue #180
- 落位：本文件由配置与流程定义层（`internal/platform`）落实；派生引擎、web、kit 的实现在其他卡里引用同一份模型

## 背景

平台原先的任务环节（需求 / 方案 / 拆解 / 开发）是代码里的枚举：`enum.TaskKind` 写死 4 个值，
审批后派生下一张卡的规则也写在用例层（拆解 → 开发读 plan JSON）。结果是「加一个环节」必须改代码、发版、
同时改执行侧 kit 的 `requireKind` 白名单。本 ADR 记录把流程搬进配置后必须固定的那几条决定。

## 决策 1：配置只认 YAML，错误必须带文件名与行号

- 唯一格式 `.yaml` / `.yml`；`-config` 默认 `./echuan.yaml`；传 `.toml` 直接报错并指向 `configs/echuan.yaml`。
  旧的 TOML 解析器与两份 `configs/*.toml` 样例已删除。
- 解析用 `gopkg.in/yaml.v3` 的 `yaml.Node`：拿得到每个键的行号。
- **`flow` 子树严格**（未知字段 = 启动失败，R14）；其余顶层键沿用「未知键忽略」，
  这样给新版本写的配置文件丢给老二进制不会炸。
- 任何配置问题都以 `<路径>:<行号>: <原因>` 的形式输出，启动直接退出：此时未打开数据库、未 `net.Listen`，
  所以坏配置不会有一个「半启动」的进程对外服务。
- 跨字段校验（如 `worker_retention > worker_ttl`）失败时，行号取自该键在文件里的位置；
  被环境变量覆盖的键不再报文件行号。

## 决策 2：`id` 对内、`name` 对外、`label` 只做显示

| 标识 | 例子 | 谁在用 |
|---|---|---|
| `id`（int，正整数，全表唯一，永不复用） | `2` | **只落库**：`tasks.kind`、`gate_decisions.gate`、`worker_kinds.kind` |
| `name`（trim 后 1–32 字符，全表唯一） | `design` | **唯一对外标识**：kit `presetByKind` 键、HTTP `kind`/`kinds`、审计规则串 `requirement->design` |
| `label`（可选，缺省 = `name`） | `方案` | 看板徽标、建卡下拉文案 |

`name` 与 `label` 共用一个命名空间，**name ∪ label 必须全局唯一**（R4），否则「用名字建卡」会解析到两个环节。
内置 id 1–4 的名字不许改嫁（R15）：`1=requirement`、`2=design`、`3=breakdown`、`4=dev`，
否则历史卡会被悄悄解释成别的环节。

## 决策 3：派生是一张图，每个环节自己声明出边

`derive.mode` 四选一：

| `mode` | 条目从哪来 | 用途 |
|---|---|---|
| `none` | 不派生 | 链尾 / 纯人工环节 |
| `single` | 合成 1 个空条目 | 需求→方案、方案→拆解 |
| `list` | 读本环节最新输出资料里的清单文档（`source: artifact`，`format: json\|yaml`） | 拆解→N 张开发卡 |
| `fanout` | 配置里直接列举条目（`items`） | 一次派多张评审卡 |

目标环节用 `open_cards` 声明同项目同环节最多几张未完成卡；**被 `list`/`fanout` 指向的目标必须
`open_cards: multiple`**（R9），否则第二张起会被幂等闸门静默吞掉。派生图不许有环（R8，DFS 报环上 name 序列）。

## 决策 4：派生卡的任务说明按三级解析，`list` 不参与缺省

取值顺序（`FlowConfig.ResolveDescription` 是唯一入口，返回「模板 + 来源」两段）：

1. 条目正文 `body_md` —— **仅 `list`/`fanout`**；`list` 条目为空就写空，**不回落**（上游 agent 的意图不能被覆盖）；
2. 目标环节的 `description` 模板；
3. 流程级 `flow.default_description`；
4. 空字符串。

`single` 派生的目标必须显式给出描述来源：写了环节 `description`（**可以显式写 `""`**）或配了
`flow.default_description`，否则拒绝启动（R16）。「显式空」与「漏配」的区别就是 R16 的判据——
默认流程正是靠 `description: ""` 保住派生卡 `description_md` 与历史逐字一致。

模板变量白名单（R10/R17）：卡片级 `{上游卡号} {上游卡标题} {项目名} {产品名} {环节名}`；
条目级 `{条目标题} {条目key} {条目序号}`（只在 `list`/`fanout` 场景可用）。未知变量拒绝启动，
错误信息里列出可用变量。

## 决策 5：`fingerprint` 是对生效流程求的 12 位摘要

对 `auto`、`flow.default_description` 与每个环节的
`id|name|label|material|open_cards|closing|title|description|derive` 拼成稳定文本求 `sha256`，取前 12 位十六进制。
「未配置 description」与「显式 `""`」编码不同（`<unset>` vs `<>`），因为两者生成不同的卡。
指纹只用于启动日志与派生事件 detail（哪份配置派生出来的卡），**不落库**：显示名不快照，溯源看事件。

## 决策 6：启动校验 R1–R17

| 编号 | 规则 |
|---|---|
| R1 | `flow.stages` 非空 |
| R2 | `id` 为正整数、全表唯一 |
| R3 | `name` trim 后 1–32 字符、无控制字符、全表唯一 |
| R4 | `label`（若给）trim 后 1–32 字符；name ∪ label 全局唯一 |
| R5 | `material` ∈ {requirement, design, plan, report, context, other} |
| R6 | `derive.mode` ∈ {none, single, list, fanout}；`list` 必须有 `source: artifact` 与 `format ∈ {json, yaml}`；`fanout` 的 `items` 非空且条目 `key` 唯一；`items` 只允许出现在 fanout 上 |
| R7 | `derive.to` 非空、指向存在的 `name`、不是自己；`mode: none` 时不许写 `to` |
| R8 | 派生图无环 |
| R9 | 被 `list`/`fanout` 指向的目标必须 `open_cards: multiple` |
| R10 | 模板变量白名单（卡片级 5 个 + 条目级 3 个，后者仅 list/fanout） |
| R11 | 没有任何上游指向的环节配了 `title`/`description` → 拒（模板永远不会被渲染） |
| R12 | `closing: true` 至多一个环节 |
| R13 | `auto: false` 时仍执行 R1–R12 |
| R14 | `flow` 子树出现未知字段 → 拒 |
| R15 | 内置 id 1–4 的名字不许改嫁 |
| R16 | 会被 `single` 派生的环节必须显式声明描述来源（见决策 4） |
| R17 | `flow.default_description` / 环节 `description` 的模板变量白名单同 R10 |

## 决策 7：内置默认流程 = 现状逐字段复现

删掉 `flow` 段后生效的流程就是 `requirement(需求) → design(方案) → breakdown(拆解) → dev(开发)`：
id 1–4 与库里的存量值逐一对应，`name` 保持英文（审计规则串 `requirement->design` 逐字不变、
kit 的 `presetByKind` 四个键不用改），`label` 承担看板上的中文。`design`/`breakdown` 显式 `description: ""`，
`dev` 是 `open_cards: multiple` + `closing: true`，`breakdown → dev` 是 `list`/`json`。

内置默认流程走的是与配置文件完全相同的解析与 R1–R17 校验路径（不是特例），
因此「默认流程本身合法」由单测守着。

## 迁移

- 配置文件：`configs/echuan.toml` → `configs/echuan.yaml`，`configs/echuan.container.toml` →
  `configs/echuan.container.yaml`；`Dockerfile` 的 `CMD` 与 compose 的只读挂载路径一起改成 `echuan.yaml`；
  飞书的扁平 `im_*` 键改成嵌套 `im:` 段（环境变量仍是 `ECHUAN_IM_*`）。
- 数据库与存量卡：本 ADR 不含任何数据迁移；`kind` 仍为 int，存量 1–4 语义不变。
- 运维纪律：启动后仍有「库里有、配置里没有」的 `kind` 告警作为整数 id 复用/删环节的哨兵（后续卡实现）。

## 本卡实现范围

`internal/platform`：`config.go`（YAML 加载）、`flow.go`（模型 + 指纹 + 描述解析）、`flow_yaml.go`（带行号解析）、
`flow_validate.go`（R1–R17）、`flow_default.go`（内置默认流程）；`cmd/echuan/main.go` 打印 `flow chain loaded`。
派生引擎（写卡、幂等、模板渲染）、`GET /v1/flow`、web 下拉、kit 0.7.0、迁移与 web 徽标在其他卡中实现。
