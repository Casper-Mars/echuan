# 验收卡按附件文件名列多链接 + 卡片头改任务名 技术方案 v0.2

> **实现说明（入库时补记，2026-11-23）**：本方案由两张验收卡落地。本文档为评审通过的 v0.2 原文，与实现有一处经需求方确认的偏差：
> - **§8.4 体积预检、§7.5「体积超限」行、D5 一并作废**：验收卡明确「不做体积预检与截断」，因此实现里没有 `maxCardBytes` 与 `im card links dropped for size` 事件；卡片按链接行原样渲染（元素数恒为链接块 1 个 + 表单 + 辅助信息行 1 个）。
> - 门禁卡（需求/方案/计划评审）不在本次改动范围：只删掉它对 `Options.DocURL` 的用法，header/正文保持原样。
> - 生产接线：`internal/im/v3_source.go` 实现 `TaskAttachments` / `TaskMeta`（`*V3TaskSource`），poller 通过 `V3ReviewSource` 类型断言取用；`SetMaterials(LatestOutputMaterialID)` 保留为单行兜底。
>
> 其余章节（链接集合与顺序、逐份降级、尝试上限、指纹含集合摘要、header/正文/终态卡、日志口径、部署与回滚）与实现一致，测试见 `internal/im/card/*_test.go`、`internal/im/v3_test.go` 与 `internal/im/card/testdata/*.json`。

- 状态：待评审（返工版）
- 作者：CTO
- 日期：2026-11-23
- 输入：PRD v0.1 `AGRASTSZCHWXH57SJKUZZCIUJI`（级别 `m`，AC1–AC20）；需求方 2026-11-23 返工意见（卡片头不要裸露任务号，卡片样式与内容要更用户友好）；代码 `Casper-Mars/echuan`，`origin/main` = `9849e15c604a213b343b89dcb25aa979268f7af4`（所有代码引用在该 commit 的 worktree `/workspace/wt-design-154` 只读核对）；飞书卡片能力与限制见 §7.3.1 与 §8.4

## 0 返工说明（只回应被驳回的那一条）

驳回原因（原文）：**「飞书消息卡片的内容也要优化下，任务id不要出现在卡片头，现在卡片头是一串字符，很难看，都看不出这个是什么任务。你自己想想怎么的消息卡片样式、内容会比较用户友好的」**

v0.1 漏了什么：v0.1 只按 PRD §4.1 把「链接行」塞进卡片，没动卡片头与摘要行。而现状卡片头是 `t_01M84N0TTJA4BK5HEM3T8YF6E · 交付验收 · 已等 2 小时`（`internal/im/card/card.go:122`，`s.ID` 即任务业务号），正文第一行又是 `**APP-1 · 项目名 · 任务标题**`（`v3.go:127` 拼 `ProjectID` + `v3_source.go:45` 拼项目名）——审批人先看到一串字符，再看到三个字段挤在一行，确实读不出「这是什么任务」。

v0.2 改了什么（只动呈现层）：

| # | 改动 | 落点 |
|---|---|---|
| 1 | 卡片头主标题 = **任务标题**；副标题 = `<项目名> · 交付验收`；任务号彻底移出 header | §7.3.1 |
| 2 | 正文删掉与卡片头重复的 `**项目码 · 项目名 · 任务标题**` 行；摘要行改成语义化中文状态行 | §7.3.2 |
| 3 | 任务号 + 报告号下移到正文末尾的**辅助信息行**（`text_size=notation`，12px 灰字），保留运维可查可复制 | §7.3.2 |
| 4 | 终态卡同样以任务标题为头（原实现 `v3.go:178` 把标题写成任务号，返工后必须修，否则「已处理」卡仍是那串字符） | §7.3.3 / §7.4 |
| 5 | 标题转义从「反斜杠」改为飞书官方文档给出的 **HTML 实体**（`&#91;` 等），并说明理由 | §7.3.4 / AC15 |
| 6 | 失败裁决卡用语义色（orange），终态灰（grey），待验收蓝（blue） | §7.3.1 |

v0.2 **没有**改（返工不外溢）：链接集合与顺序（仍走 `currentver` R-a…R-h）、单份失败降级、每卡每材料 2 次上限、28 KB 体积预检、指纹含集合摘要、日志口径、部署与回滚、数据库与接口——全部与 v0.1 一字未改。§1–§11 的其余内容保持 v0.1 结论。

## 1 结论

把验收卡从「一个 `看全文` 按钮 + 一串任务号标题」改成**一张能一眼读懂任务的卡**：卡片头写任务名、副标题写项目与卡类型；正文一行一个 `[文件名](URL)`；任务号与报告号降级到卡片末尾的辅助信息行。

链接集合 = 该任务在 Web 附件区可见的当前版本集合（同一套 R-a…R-h 规则在 Go 侧新增一份实现，靠共享夹具锁死跨语言一致），顺序同 Web；每份材料的云文档独立导入，失败只降级该行为纯文本，不阻塞审批；卡片指纹加入附件集合摘要，附件变了卡片就跟着变。

改动范围：4 个 Go 包（`internal/currentver` 新增、`internal/im/card`、`internal/im`、`internal/im/v3_source.go` 适配层）、1 份运维文档（`docs/im-runbook.md`）、Web 侧 1 个测试/门禁脚本 + `package.json` 一行（不改页面）、0 个 HTTP 接口、0 张表、0 次迁移、0 个新依赖、0 个环境变量。

最大风险：跨语言「当前版本」判定漂移（§11 R1）——它决定卡片和 Web 是不是同一组文件，漂移后审计会指向别的版本。处置见 §7.1.2 的三道锁与 §10 的日志信号。
第二风险：把任务号从卡片头移走，会让「照卡片头搜索任务」这个习惯失效（§11 R12）——处置是辅助信息行原样保留任务号 + runbook 写清新的排障路径。

八个关键决定（每条都写了「不选什么」）：

| # | 决定 | 不选 / 代价 |
|---|---|---|
| D1 | 「当前版本」判定实现为 Go 包 `internal/currentver`，逐条镜像 `web/src/lib/current-versions.ts` 的 R-a…R-h；两边用**同一份夹具 JSON** + 「Go 测试解析 TS 源码常量」双向锁死 | 不选「服务端另立一套口径而不加锁」：PRD U2 已判定那是未达 AC6。也不选「把判定搬到服务端、Web 改调接口」：会改 Web 行为代码，撞 PRD 非目标「不改 Web 附件区」，回归面大于收益；记为后续收敛项（§11 未决 U5） |
| D2 | 卡片契约 `card.Options{DocURL}` → `card.Options{Links []card.Link}`（有序、每项带 `URL` 可空）；链接行并进**已有的正文 markdown 元素**，不新增按钮 | 不选「每份一条 markdown 元素」：元素数随附件数增长（飞书单卡上限 200 元素），且正文会被切成 N 段，视觉上出现组件间距。代价：一行的 markdown 语法错误会污染整块正文，故转义做成单点函数 + 夹具（§7.3.4） |
| D3 | 导入尝试按「卡 × 材料」限 2 次，计数**只在进程内**（`map[taskID+materialID]int`），集合变化（指纹变）时清零 | 不选「持久化到 `im_card` 新列」：要加迁移，而本仓 `MigrateFS` 在 DB 版本高于二进制时直接拒绝启动（`internal/store/migrate.go:95-101`），加迁移等于给回滚上一个必须手改 `PRAGMA user_version` 的门槛，为一个重试计数器不值。代价：进程重启后该卡可再各尝试 2 次（§11 R4） |
| D4 | 卡片指纹加入**附件集合摘要**（材料号 + role + kind + 标题 + 顺序） | 不选「沿用 `taskID\|status\|updateTime\|reason`」：`Attach` 不写 `tasks.update_time`（`internal/store/v3/attachment.go:75-125` 只 insert `attachments`），重新交付/补挂附件时指纹不变 ⇒ 卡片永不刷新，直接违反 AC12/AC17 |
| D5 | 发送前做体积预检：卡片 JSON 超 28,672 字节则**整卡去掉链接块**仍照常发出（审批可用），并记 `im card links dropped for size` 升级 | 不选「按 N 份截断/折叠」：PRD §2 非目标明令禁止。也不选「不预检、让飞书 230025 拒绝」：那样连审批表单都发不出去，审批直接停摆。代价：这是产品可见的降级，按 PRD §4.6 属 U4 升级范围，本卡只给默认动作，需需求方确认（§11 U4） |
| D6 | 云文档名改为材料的**显示名**（`title`，空则 `material_id`），仅新导入生效 | 不选「回溯改名历史文档」：每份一次改名 API 调用，收益只是历史一致性；PRD §4.5 已接受该代价 |
| D7 | **卡片头**：主标题 = 任务标题（`plain_text`），副标题 = `<项目名> · <卡类型>`，颜色按语义（待验收 blue / 失败裁决 orange / 终态 grey）；任务号不进 header | 不选「主标题写成 `任务标题 · 交付验收`」：卡类型已在副标题，重复占位会把长标题挤到客户端 4 行省略。也不选「主标题用 `lark_md` 支持加粗/表情」：标题里的 `* _ [ ]` 会被当语法解析，得再引一层转义（§7.3.4），不如 `plain_text` 零解析 |
| D8 | 任务号/报告号下移到正文末的**辅助信息行**（`text_size=notation`，12px），并以「任务 / 报告」中文前缀标注；终态卡的标题改为查真实任务标题（`TaskMeta`）而不是沿用任务号 | 不选「任务号彻底从卡片移除」：`docs/im-runbook.md` 的排障流程靠任务号定位，移除后值班无法从群消息反查任务。也不选「卡片重发时保留旧标题」：终态卡不查标题的话，它会重新出现一串字符，等于驳回意见没解决 |

## 2 目标与非目标

目标（抄 PRD §2，指标口径见 PRD §7）：

| 目标 | 指标 | 基线（`9849e15`） | 目标值 |
|---|---|---|---|
| 卡片上能直接读出本次验收的文件名 | 链接文字 = Web 附件区显示名的行 ÷ 全部链接行 | 0%（唯一链接文字是硬编码「看全文」，`internal/im/card/card.go:105`） | 100% |
| 卡片覆盖当前版本附件全集 | 卡片链接行数 ÷ 当前版本附件数 | ≤1 行/任务且只覆盖最新 output（`internal/im/v3.go:134-137`、`internal/im/contract.go:59-63`） | 100%（1:1） |
| 缺链可定位 | 缺链接材料中日志四项（任务号 / 材料号 / 材料标题 / 原因）齐全的比例 | 现日志有 subject/material/error，缺材料标题与尝试次数（`internal/im/v3.go:249`） | 100% |
| **卡片头能读出「这是什么任务」**（返工新增） | 卡片 header 中出现原始业务号（`t_`/`r_` 前缀）的卡 ÷ 全部卡 | 100%（header 现在就是任务号，`card.go:122`） | 0% |

非目标（本期明确不做）：

- 不加在线预览 / 正文直读：预览是 Web 已建成能力（`web/src/features/task-detail/attachment-preview.tsx`），本卡只给云文档链接。
- 不给每份加版本号、版本下拉、新旧对比：需求方已定「历史版本不上卡」。
- 不重排成「交付物优先」、不按 kind 分组：一重排，AC6 的「卡片 vs Web 逐行对」退化成人工映射。
- 不改 Web 任务详情 / 项目详情的附件区与排序：判定沿用 Web 现有规则，本期不动 Web 运行时代码。
- 不改审批动作、状态机与门禁卡（需求/方案/计划评审卡）：决策仍打「最新 output」（`internal/im/v3_decider.go:35-50`）；项目门禁卡运行期无产出路径。
- 不做折叠 / 只列前 N 份 / 失败提示文案 / 「3/4 已就绪」计数。
- **不新增「超时变红 / 积压提醒」一类的状态语义**：那要引入新的时间阈值与产品规则，本期只把既有 `Stale`→red 分支（V3 从未置位）原样留着（§11 未决 U10）。
- 不新增 HTTP 接口、不加表、不加迁移、不加依赖。

## 3 AC 覆盖表

`来源`：原文 = PRD 原句；返工新增 = 需求方 2026-11-23 驳回意见（PRD 未覆盖，需 PM 同步进 PRD v0.2）；推断 = 本方案补齐的可判定形式。

| AC | 原文（节选） | 来源 | 模块 | 接口 | 数据 | 验证方式 |
|---|---|---|---|---|---|---|
| AC1 | 5 份附件 → 5 行 markdown 链接 | 原文 | `card` 渲染 | `card.Options{Links}` | 读 `attachments` 投影 | 卡片 JSON 数 `](` 行数 = 5；`go test -run TestRenderLinks` |
| AC2 | 链接文字 = Web 显示名（`title`，空则 `material_id`） | 原文 | `currentver` + `card` | 同上 | `materials.title` | 双端逐行比对（卡片截图 vs Web 附件区截图）；单测夹具覆盖 title 空 |
| AC3 | 顺序 = Web 附件区顺序（input 先、同 role 按加入顺序） | 原文 | `currentver`（保序）+ `store/v3.ListTaskAttachmentRefs` 排序 | 无（沿用 SQL 排序） | `attachments.role/sort_no/id` | 夹具断言输出顺序；双端逐行比对位置 |
| AC4 | 卡片 JSON 不含 `open_doc`，正文不含「看全文」 | 原文 | `card` | 无 | 无 | `grep -c '"name":"open_doc"'` = 0；`grep -c 看全文` = 0（golden + 实卡） |
| AC5 | v1/v2 两版，卡片只列 v2 | 原文 | `currentver` R-b/R-d | 无 | 同上 | 夹具：同 kind 两条 output → 只留最后一条；实卡 grep v1 的 `material_id` 0 命中 |
| AC6 | 含 input + output + brief 时与 Web 逐项一致 | 原文 | `currentver`（含 R-e 简报跟随） | 无 | `materials.source_material_id` | 共享夹具 `current-versions-cases.json`；双端截图逐项比对 |
| AC7 | 3 份中 1 份导入失败，其余 2 行可点 | 原文 | 导入填充（`fill`） | `DocProvider.Ensure` | `im_doc`（成功才写） | 单测：fake importer 第 2 份返回 error，断言 2 行带 `](`、1 行纯文本 |
| AC8 | 失败行保留文件名、无任何提示文案 | 原文 | `card` 渲染 | 无 | 无 | 单测断言该行等于转义后的标题、不含「生成中/失败/无链接/暂无」类文案；golden |
| AC9 | 导入失败时审批按钮可用 | 原文 | `card.Render`（表单与链接互不依赖） | 无 | 无 | 单测：全失败时卡片仍含 `approve`/`reject` 与 `form` |
| AC10 | 日志含任务号 / 材料号 / 材料标题 / 失败原因 | 原文 | 填充日志 | 无 | 无 | 单测捕获 slog 输出断言 4 个 key 非空 |
| AC11 | 同一张卡上同一材料导入尝试 ≤2 | 原文 | 尝试计数（§7.4） | 无 | 进程内 `attempts` | 单测：让 Ensure 恒失败，连续 5 轮 tick 后计数 = 2 |
| AC12 | 重新交付 v2 后一个轮询内只列 v2 | 原文 | 指纹含集合摘要（D4） | 无 | `attachments` | 单测：改附件集合后同一 tick 触发 patch，且旧行消失 |
| AC13 | 集合稳定时两轮渲染行数/文字/顺序一致 | 原文 | 渲染纯函数 + patch 跳过 | 无 | 无 | 单测：两轮 body 字节相等；patch 次数不增（`lastBody` 比较） |
| AC14 | `title` ≥60 字符 → 逐字显示、无省略号 | 原文 | `card`（不截断） | 无 | `materials.title` | 夹具：60 字标题断言渲染含全串、不含 `…` |
| AC15 | `title` 含 `[ ] ( ) * _` → 原文显示且可点 | 原文 | `card` 转义（§7.3.4） | 无 | 无 | 夹具断言实体转义（`&#91;`/`&#93;`/`&#42;`/`&#95;`…），URL 段不受影响；实卡显示原文 |
| AC16 | 无附件 → 无链接行、无占位文案 | 原文 | `card`（空列表不渲染块） | 无 | 无 | golden `work_item_no_links.json` |
| AC17 | 终态卡同形态列当前版本附件 | 原文 | `card.RenderClosed` | 无 | `attachments` | 单测：终态卡与验收卡正文链接块逐行相等 |
| AC18 | 刚进入 `in_review` 且均未导入 → 发出时无这些链接行 | 原文 | 先发后补（§8.2） | 无 | `im_doc` 未命中 | 单测：首帧 body 不含 material_id、含 form |
| AC19 | 补行前后 message_id 相同 | 原文 | 同 message_id patch | `Messenger.PatchCard` | `im_card.message_id` | 单测：`sent[0]` 与后续 `patched` 的 message id 相同；实卡对比 |
| AC20 | 新导入文档名 = 卡片上的文件名 | 原文 | `fill` 传 `DocProvider.Ensure` 的 docName | `Ensure(ctx, materialID, docName)` | `im_doc` | 单测断言 Ensure 收到的 docName = 显示名；实卡点开比对 |
| **AC21** | 卡片头不得出现原始业务号（任务号 / 报告号） | 返工新增 | `card` header | 无 | 无 | 取卡片 JSON 的 `header` 子树，`t_` / `r_` 前缀 0 命中；`header.title` = 任务标题（AC22） |
| **AC22** | 主标题 = 任务标题逐字一致；副标题 = `<项目名> · 交付验收` | 返工新增 | `card` header | 无 | `projects.name`、`tasks.title` | 逐字比对 `header.title.content` 与 `tasks.title`；副标题断言两种形态（项目名空 → 只有卡类型） |
| **AC23** | 正文不再重复任务标题 / 项目码；状态行语义化 | 返工新增 | `card` body | 无 | 无 | golden：body 中 `**` 开头的标题行 0 命中；首行为 `已等待 X · <worker> 第 n/m 次提交` |
| **AC24** | 任务号 / 报告号只出现在末尾辅助信息行，且字号为辅助级 | 返工新增 | `card` body（footer 元素） | 无 | `tasks.task_id`、`reports` | 卡片 JSON 中 `t_` / `r_` 仅出现在最后一个 markdown 元素内，且该元素 `text_size` = `notation` |
| **AC25** | 终态卡主标题也是任务标题，不得退化成任务号 | 返工新增 | `card.RenderClosed` + `V3ReviewSource.TaskMeta` | `TaskMeta(ctx, taskID)` | `tasks.title` | 单测：关单后 patch 的卡片 `header.title` = 任务标题；解析失败 → 卡类型文字 + 日志 `im card close meta missing` |
| **AC26** | 标题含 markdown 特殊字符 / 换行时按原文显示；超长由客户端省略，服务端不截断 | 返工新增 | `card` header（`plain_text`） | 无 | 无 | 夹具：标题含 `[ ] * _ ~ #` 与换行 → header.content 逐字等于输入；正文链接文字走实体转义（AC15） |

## 4 现状与改动点

| 文件 / 模块 | 现状（`9849e15`） | 改成什么 | 影响面 |
|---|---|---|---|
| `internal/currentver`（**新增**） | 无 | `Current(refs []Ref) []Ref`：镜像 R-a…R-h 的任务详情投影；纯函数、无 IO | 新增唯一判定处（Go 侧）；被 `im` 调用 |
| `internal/currentver/testdata/current-versions-cases.json`（**新增**） | 无 | 共享夹具：规则名 + 输入 + 期望输出（material_id 序列），Go 与 Web 两侧都读它 | 跨语言一致性证据；改规则必须两侧同批改夹具 |
| `internal/im/contract.go` | `TaskMaterialSource.LatestOutputMaterialID` 单 id（`:59-63`）；`InboxEntry`/`SubjectSource` 供门禁卡用 | 换成 `TaskAttachments(ctx, taskID) ([]MaterialRef, error)`；`MaterialRef{MaterialID,Title,Kind,Role,SourceMaterialID}`；新增 `TaskMeta(ctx, taskID) (TaskMeta, error)` 供终态卡取标题 | 只影响 `im` 内部；`v3_decider` 用自己的 `LatestOutputMaterialID`（app 层），不动 |
| `internal/im/v3_source.go` | `V3Review.Title` = `项目名 + " · " + 任务标题`（`:45`），标题里项目名与 `ProjectID` 双份 | `V3Review` 拆成 `ProjectName` / `TaskTitle`；新增 `TaskMeta(ctx, taskID)`（`appv3.GetTask` + `ProjectService.GetProject`，`service.go:96`） | `v3.go` 标题拼接、终态卡标题来源 |
| `internal/im/card/card.go` | header = `s.ID · 卡类型 · 已等 X`（`:122`）；`RenderClosed` header = `s.ID · 卡类型`（`:146`）；正文首行 `**标题**` + `worker/尝试/报告` 行 | header = 任务标题 + 副标题（项目名 · 卡类型）+ 语义色；正文 = 状态行 +（打回原因）+ 链接行 + 辅助信息行；终态卡 = `处理结果：` + 链接行 + 辅助信息行 | 卡片 JSON 契约 + golden 夹具 + 实卡观感（**返工主体**） |
| `internal/im/card/elements.go` | `cardHeader{Title, Template}`；`markdownElement{Tag, Content}` | `cardHeader` 增 `Subtitle *text`；`markdownElement` 增 `TextSize string`（`text_size`，取值 `notation`） | 卡片元素结构；`validateNames` 不受影响 |
| `internal/im/card/render_links.go`（**新增**） | 无 | `renderLinks(links []Link) string`：逐行 `[escapedTitle](url)`，`url==""` → 纯文本行；实体转义、换行归一都在这一处 | 转义错误的唯一风险点，集中 + 夹具 |
| `internal/im/v3.go` | 单材料解析（`:136-137`）、单 doc 填充（`:220-263`）、指纹不含附件（`:265-269`）、关单时 `Title: id`（`:178`） | 解析附件集合 → 投影 → 渲染多链接；指纹纳入集合摘要；按卡一个填充任务；关单时用 `cardState` 缓存或 `TaskMeta` 取真实标题 | 轮询行为、导入量、日志、终态卡标题 |
| `internal/im/docs.go` | `Ensure(ctx, materialID, title)`，`title` = 卡片标题（`v3.go:247`） | 调用方改传材料显示名；`sanitizeDocName`（`:146-155`）逻辑不动 | 新文档名；历史文档不改 |
| `internal/im/card/testdata/*.json` | 5 份 golden | 更新 5 份（header 去掉任务号、改标题+副标题+语义色；验收卡删按钮、正文改链接行）、新增 3 份（5 行混合成功与失败降级、60 字标题含特殊字符、终态卡） | 纯测试夹具 |
| `docs/im-runbook.md` | 「看全文」按钮不出现（`:69-84`）；排障按卡片头任务号定位 | 改写为「链接行缺失/无链接」+「卡片头现在是任务标题，任务号在卡片末尾辅助信息行」；补充 `im card close meta missing` | 值班口径（**不写会让值班找不到任务**） |

本次不改：

- `web/src/lib/current-versions.ts` 与 Web 运行时代码：本期只在**测试与构建门禁层**加一个读同一份夹具的脚本，并把 `node scripts/current-versions-parity.mjs` 加进 `web/package.json` 的 `build` 链（现为 `tsc --noEmit && vite build && node scripts/check-size.mjs`，`web/package.json:8`）；页面与组件零改动，产物字节不变（§7.1.2 第 3 条）。
- `internal/im/docs.go` 的缓存与并发去重（一份材料一次导入、`inflight` 去重）：语义正确，沿用。
- `internal/im/actions.go`、状态机、`v3_decider`、`api/openapi.yaml`、数据库 schema：不动。
- 项目门禁卡分支（`card.go:187-217`）：运行期没有产出路径（v2 轮询层已删），本次只删它对 `DocURL` 的用法；golden 里两份项目卡的按钮变化属测试夹具更新，不构成行为变更。

## 5 总体设计

```text
V3Poller.tick (30s)
  └─ PendingReviews ──> in_review 任务列表（ProjectName / TaskTitle 已拆开）
       ├─ TaskAttachments(taskID) ──> attachments 投影（role, sort_no, id 排序）
       ├─ currentver.Current(refs) ──> 当前版本集合（保序）
       ├─ 指纹 = sha256(task|status|updateTime|reason|集合摘要)
       ├─ Docs.Cached(materialID) ──> 已有 URL
       ├─ card.Render / RenderClosed(Links) ──> 卡片 JSON（体积预检）
       └─ Messenger.SendCard / PatchCard
              └─ fill（每卡一个后台任务，全局并发 ≤2）
                    ├─ Docs.Ensure(materialID, 显示名) ──> im_doc（成功落缓存）
                    ├─ 成功后重建 Links → patch 同一 message_id
                    └─ 失败 → attempts++ → 记四项日志 → 该行降级为纯文本
关单：TaskMeta(taskID) 或进程内缓存 ──> RenderClosed(标题/项目名/链接)
```

| 组件 | 职责 | 同步/异步 | 失败后的行为 |
|---|---|---|---|
| `currentver` | 纯函数：全量附件 → 当前版本集合，保序 | 同步、无 IO、无时钟 | 输入异常（空串 material_id）按原样保留并记 `im currentver suspect ref`（宁多不藏） |
| `TaskAttachments`（适配器） | 读 `attachments` 投影 | 同步 | 返回 error → 本轮跳过该任务（不渲染、不发送），日志 `im attachments resolve failed` |
| `TaskMeta`（适配器） | 关单时取 `项目名 + 任务标题` | 同步 | 失败 → 用进程内缓存；都缺 → 卡片头用卡类型文字，日志 `im card close meta missing`（**不退化回任务号**） |
| `card.Render(Closed)` | 纯函数：标题/项目/状态/链接 → 卡片 JSON | 同步 | JSON 超体积预检 → 去掉链接块重渲染；渲染错误 → 跳过本轮 |
| `Docs.Cached` | 读 `im_doc` 缓存 | 同步 | 读失败按「无缓存」处理（记 warn），不阻塞 |
| `Docs.Ensure` | 逐份导入，成功写 `im_doc` | 异步（卡已发出后再补） | 失败不写缓存；由填充计数决定是否再试 |
| `Messenger` | 发送 / patch | 同步 | patch 失败 → 记 warn，下一轮按指纹重试（内容幂等） |

## 6 数据模型

### 6.1 结论：本期无 schema 变更

- **无新增表、无新增字段、无迁移、无存量回填。** 所以没有 `up`/`down` SQL，也没有 bin/migration 顺序问题——这对回滚是硬收益：旧二进制可以直接接新数据（§9 回滚）。
- 理由：PRD 需要的新状态只有两类——「材料 → 云文档 URL」（已有 `im_doc`，`migrations/0001_init.sql:204-209`）和「本卡对某材料的导入尝试次数」（短生命周期、可重算）。前者继续用 `im_doc`；后者放进程内（D3）。返工加出来的「卡片头要任务标题」也只读现有列（`tasks.title`、`projects.name`），不需要新字段。
- 明确否决：给 `im_card` 加 `doc_attempts` JSON 列（`migrations/0001_init.sql:181-194`）。收益是重启后仍守着 2 次上限；代价是一次迁移 + `internal/store/v3_schema_test.go:115-146` 与 `internal/store/im_card.go:44-108` 同批改 + 旧二进制回滚被 `SchemaVersionError` 拦住（`internal/store/migrate.go:95-101`）。代价大于收益，进 §11 未决 U6，除非出现真实的配额事故。
- 同样否决：把「发送时的标题/项目名」存到 `im_card` 供终态卡复用（省一次查询）。收益是一次 DB 查询；代价是同一次迁移门槛。改用进程内缓存 + 关单兜底查询（D8）。

### 6.2 读的投影（不改，只约束读法）

`internal/store/v3/context.go:125-137` `ListTaskAttachmentRefs` 的列与排序即卡片的数据源，**必须原样复用，不得在 Go 侧重排**：

| 列 | 用途 | 约束 |
|---|---|---|
| `m.material_id` | 卡片行的身份 + `im_doc` 缓存的键（决策仍走 `LatestOutput`，与此无关） | 非空（`materials.material_id` 唯一约束保证） |
| `m.title` | 链接文字 / 云文档名 | `TEXT NOT NULL`（`0001_init.sql:33`）但允许空串 → 空串回退到 `material_id`（AC2） |
| `m.kind` | R-b 参与去重的 kinds 1–4 | 枚举 `internal/domain/enum/enum.go:155-160` |
| `a.role` | 排序主键（input=1 先于 output=2）与 R-a/R-b 判据 | `enum.Input=1 / enum.Output=2`（`enum.go:201-207`） |
| `src.material_id` | 简报跟随其 source（R-e） | 可空 |
| 排序 | `ORDER BY a.role ASC, a.sort_no ASC, a.id ASC` | 与 Web 同序（AC3） |

查询走 `ix_attachments_task ON attachments(task_id, role, id)`（`migrations/0001_init.sql:122`）：`task_id` 等值 + `role` 已在索引前缀，排序只需再排 `sort_no`（未进索引，但单任务附件量为个位数，排序成本可忽略）。本期**不新增索引**——没有新的查询形状，换来的只是多一张索引的写放大。

返工新增的读取（只读现有列）：`tasks.task_id / tasks.title`（`appv3.GetTask`，`internal/app/v3/service.go:96`）、`projects.name`（`ProjectService.GetProject`，`internal/app/v3/project.go:59`）。走主键查询，无新增索引需求。

### 6.3 进程内状态（不落库）

| 键 | 值 | 生命周期 | 说明 |
|---|---|---|---|
| `taskID`（`cardState`） | `links map[materialID]string`、`attempts map[materialID]int`、`lastBody []byte`、`filling bool`、`messageID string`、`title/projectName string` | 卡片未关闭期间；进程重启后重建 | 按任务索引，发送成功后填 `messageID`；`links` 是「已导入成功」的镜像（真身在 `im_doc`）；`title/projectName` 供关单时免查询 |

失效规则：指纹变化（附件集合变、任务状态变、驳回原因变）→ 清空该卡的 `attempts` 与 `links`，按新集合重建（PRD §4.3「直到附件集合变化触发重建」）；`title/projectName` 随新一次渲染刷新。

## 7 接口契约

HTTP API 无变更：`api/openapi.yaml` 不动，Web 不感知本卡实现。

### 7.1 内部契约：`internal/currentver`

```go
// Ref 是投影的最小形状，与 web/src/lib/current-versions.ts 的 VersionRef 字段同名同义。
type Ref struct {
    MaterialID       string
    Title            string
    Kind             string // requirement|design|plan|report|context|other
    Role             string // input|output
    SourceMaterialID string // 空串表示没有 source；简报用
}

// Current 返回任务详情「附件」区可见的集合，保序（rule 语义见 §7.1.1）。
func Current(refs []Ref) []Ref
```

- 鉴权：无（进程内纯函数）。
- 幂等：纯函数，同一输入必得同一输出（AC13 的基础）。
- 限流：无。
- 输入约束：`refs` 必须是 `ListTaskAttachmentRefs` 的原序；调用方不得排序。
- 输出约束：只做删减，不新增、不重排；长度 `0 ≤ len(Current) ≤ len(refs)`。
- 错误：不返回 error。缺字段（空 `material_id`）不丢弃，保持 R-f「宁多不藏」的既有取向，并记 `im currentver suspect ref`。

#### 7.1.1 规则表（逐条对齐 `web/src/lib/current-versions.ts:4-12`）

| 规则 | Go 实现 | 对应 PRD AC |
|---|---|---|
| R-a `role=input` 永不隐藏 | 分组键返回 `nil`，永不被丢弃 | AC1/AC6 |
| R-b 只有 `role=output` 且 `kind ∈ {requirement,design,plan,report}` 参与去重 | `isVersionedBody` 常量集合 | AC5 |
| R-c 分组键 = kind（任务详情口径） | `groupKeyOf = kind` | AC3/AC6 |
| R-d 组内保留**最后一条**，输出保持原顺序 | 按**下标**记账丢弃（不是按 material_id），见下 | AC5/AC13 |
| R-e 简报（`kind=context` 且有 source）在 source 被丢弃时同隐 | 迭代到不动点（兜住「简报的 source 又是简报」） | AC6 |
| R-f 真孤儿简报与无 source 的 context 一律保留 | 不参与丢弃集合 | AC6/AC16 |
| R-g 缺 `task_id` 各成一版 | **不适用**（任务视图不使用 `task_id` 分组）；项目视图仍在 Web 侧 | 非目标 |
| R-h `reviewTargetId` | **不在本包**：决策仍用 `appv3.LatestOutput`（`v3_decider.go:35-50`） | 非目标 |

按**下标**记账这条必须写进实现注释：同一 `material_id` 可以同时是 input 与 output 两行（把旧版正文补挂成输入附件），按 id 记会把那条 input 行一起抹掉，直接违反 R-a（对照 `current-versions.ts:49-51`）。

#### 7.1.2 防漂移的三道锁（PRD U2 的「自带防漂移证据」）

1. **共享夹具**：`internal/currentver/testdata/current-versions-cases.json`，每条 `{name, refs, want_material_ids}`；Go 测试 `TestCurrentFixtures` 覆盖 R-a…R-f 全部路径（含 PRD AC5/AC6 的真实形状）。
2. **常量解析断言**：`TestKindConstantMatchesWeb` 读 `../../web/src/lib/current-versions.ts`，抽出 `VERSIONED_KINDS` 的字符串集合，断言与 Go 的 `versionedKinds` 完全相等。改任一侧而不同批改另一侧 ⇒ `make test` 红。
3. **Web 侧同夹具**：新增 `web/scripts/current-versions-parity.mjs`（沿用 `web/scripts/ui-imports-check.mjs` 的 esbuild 打包法），读同一份 JSON 夹具跑 `currentTaskVersions`，断言与夹具期望一致；并把 `node scripts/current-versions-parity.mjs` 加进 `web/package.json` 的 `build` 链（现为 `tsc --noEmit && vite build && node scripts/check-size.mjs`，`web/package.json:8`）。**改动只落在测试与构建门禁，页面与组件零改动，产物字节不变。**

任一道锁连续两次在评审里被绕过（例如改夹具期望值来「修绿」），按 §11 R1 的退出条件处理：把判定整体搬到服务端并让 Web 调接口（U5）。

### 7.2 内部契约：`internal/im`（`contract.go`）

```go
// MaterialRef 是卡片需要的一条附件投影。
type MaterialRef struct {
    MaterialID       string
    Title            string
    Kind             string
    Role             string
    SourceMaterialID string // 空串 = 无 source
}

// TaskMaterialSource 返回某任务的全部附件，顺序即 Web 附件区顺序。
type TaskMaterialSource interface {
    TaskAttachments(ctx context.Context, taskID string) ([]MaterialRef, error)
}

// TaskMeta 是终态卡要的「这是什么任务」。
type TaskMeta struct {
    ProjectID   string
    ProjectName string
    TaskTitle   string
}

// V3ReviewSource 现在还要能按任务号补标题（关单路径）。
type V3ReviewSource interface {
    PendingReviews(ctx context.Context, limit int) ([]V3Review, error)
    TaskMeta(ctx context.Context, taskID string) (TaskMeta, error) // 返工新增
}

// V3Review 拆开项目名与任务标题，不再拼成 "项目名 · 标题"。
type V3Review struct {
    TaskID      string
    ProjectID   string
    ProjectName string
    TaskTitle   string
    Kind        string
    Status      string
    Reason      string
    WorkerID    string
    Attempt     int
    MaxAttempts int
    UpdateTime  string
}
```

- 鉴权 / 限流 / 幂等：进程内接口，无；`taskID` 来自 `PendingReviews`，非外部输入。
- 错误：`attachments` 查询失败 → 返回 error；调用方本轮跳过该任务（不发送、不 patch、保留旧卡），日志 `im attachments resolve failed` 带 `task`/`error`。
- `TaskMeta` 失败：调用方先用 `cardState` 缓存，仍缺则 header 主标题用卡类型文字（`交付验收`），并记 `im card close meta missing`；**不把任务号写回标题**（否则违反 AC21/AC25）。
- 空集：`([]MaterialRef{}, nil)`，卡片渲染为无链接块（AC16），**不是**错误。
- 未接线：`p.materials == nil`（仅测试/降级接线）按空集处理，渲染无链接块、不记 error，与现状「`materials == nil` → 卡片不带按钮」（`internal/im/v3.go:195-205`）同语义。
- `DocProvider` 签名不变：`Cached(ctx, materialID) string`；`Ensure(ctx, materialID, docName string) (string, error)`。语义变化只有 `docName` 从「卡片标题」变为「材料显示名」（AC20）。
- `CardStore` / `Messenger` 签名不变。

### 7.3 内部契约：`internal/im/card`

```go
// Subject 现在把「任务标题」「项目名」「任务号」三件事分开：标题进 header，
// 项目名进 subtitle，任务号只进正文末尾的辅助信息行。
type Subject struct {
    Kind        string // work_item | project
    ID          string // 任务业务号：只用于辅助信息行与日志，不进 header
    State       string
    Title       string // 任务标题（card 的 header 主标题）
    ProjectName string // 项目名（subtitle 前缀）
    Age         time.Duration
    Stale       bool
    Reason      string
}

// Link 是一行链接。
type Link struct {
    MaterialID string // 仅用于日志与去重，不渲染
    Title      string // 未转义的显示名（title 非空则 title，否则 material_id）
    URL        string // 空 = 该份降级为纯文本行（导入失败/关闭/超时）
}

type Options struct {
    Links      []Link
    StaleHours int
}
```

#### 7.3.1 卡片头（返工主体）

| 卡类型（`gate.label`） | `header.title`（`plain_text`） | `header.subtitle`（`plain_text`） | `template` |
|---|---|---|---|
| 交付验收 | 任务标题；空 → 项目名；再空 → `待验收任务` | `<项目名> · 交付验收`；项目名空 → `交付验收` | `blue` |
| 失败裁决 | 同上 | `<项目名> · 失败裁决` | `orange` |
| 终态（已处理） | 任务标题（同验收卡）；取不到 → `交付验收` | `<项目名> · 交付验收`；项目名取不到 → `交付验收` | `grey` |
| 门禁卡（需求/方案/计划评审，运行期无产出路径） | `Subject.Title` 原样（现状就是人能读的标题，`card.go:187-217`） | `<项目名> · <卡类型>`（无项目名则不挂副标题） | `blue` |

- 为什么不用 `lark_md` 做标题：任务标题里常出现 `*`、`_`、`[`、`]`、`#`，`lark_md` 会把它们当语法，必须在标题上再做一层转义；`plain_text` 零解析，最稳（AC26）。
- 为什么不让 header 出现任务号：这正是本次驳回意见；header 的信息位只留「是什么任务」。
- 长度：`header.title` 飞书限**最多 4 行**、`header.subtitle` 限**最多 1 行**，超出由客户端省略（官方标题组件文档「字段说明」）。服务端不截断，超过 60 字符时记 `im card title long`（`title_len`），便于观察是否频繁省略。
- 颜色：待验收 `blue`（需要人处理）、失败裁决 `orange`（异常）、终态 `grey`（已失效），取自官方建议的语义色。既有 `Stale → red` 分支保留但 V3 从不置位（`v3.go:125-129` 未设 `Stale`），即实际不会出现红色卡片；**不新增**「超时变红」规则（§11 未决 U10）。

#### 7.3.2 卡片正文

正文 = **一个** `markdown` 元素（`text_size=normal`）+ 表单 + **一个** `markdown` 辅助信息元素（`text_size=notation`）。自上而下：

| 顺序 | 内容 | 形态 |
|---|---|---|
| 1 | 状态行：`已等待 2 小时 · dev-1 第 2/3 次提交` | 正文；失败卡为 `… 第 3/3 次失败`；无上限时 `… dev-1 提交`；无 worker 时省略 worker 段 |
| 2 | 上次打回 / 失败原因（有才出现）：`上次打回：<原因>` / `失败原因：<原因>` | 正文，沿用现有 80 字截断（`card.go:296-302`） |
| 3 | 链接行：每份一行 `[文件名](URL)`，失败行只留文件名 | PRD §4.1 定义，位置不变 |
| 4 | 辅助信息行：`任务 t_01M84N0TTJA4BK5HEM3T8YF6E · 报告 r0099aab`（无报告号时只留任务号） | **独立元素**，`text_size=notation`（12px），`<font color='grey'>` 包裹 |
| 5 | 审批表单：驳回理由输入框 + `验收通过` / `打回` | 位置与行为不变 |

- 正文**不再**出现 `**项目码 · 项目名 · 任务标题**` 粗体行（与 header 重复，且正是「三个字段挤一行」的来源）。
- 链接块仍不加标题行、不加计数、不加分隔线、不加序号、不加 kind 前缀（PRD §4.1 原样）。
- 输入框占位文案由 `驳回理由（通过可留空）` 改为 `打回理由（通过可留空）`，与按钮「打回」一致（纯文案）。
- 失败卡的按钮仍只有「打回重做」（`rejectOnly`），不变。

#### 7.3.3 终态卡

```text
header.title    = 任务标题（取不到 → 交付验收）
header.subtitle = <项目名> · 交付验收
header.template = grey
body            = 处理结果：已处理
                  [文件名](URL) × N
                  任务 t_xxx · 报告 r_yyy   （notation）
```

- 现状 `RenderClosed` 的标题是 `s.ID`（`card.go:146`），关单路径传的 `Subject.Title` 也是任务号（`v3.go:178`）——返工必须同时改这两处，否则「已处理」卡仍是一串字符（AC25）。

#### 7.3.4 渲染规则与转义

| 情形 | 输出 |
|---|---|
| `len(Links) == 0` | 正文不追加任何行；无占位文案（AC16） |
| `URL != ""` | `[<escaped title>](<url>)`，一行一份 |
| `URL == ""` | `<escaped title>`，一行一份，纯文本（AC8） |
| 标题含换行 | `\r\n`/`\n`/`\r` 归一为一个空格（保证「一行一份」） |
| 标题含 `& < > [ ] ( ) * _ ~ \` ` | 按飞书官方「特殊字符转义说明」用 HTML 实体：`&amp;` `&lt;` `&gt;` `&#91;` `&#93;` `&#40;` `&#41;` `&#42;` `&#95;` `&sim;` `&#96;` `&#92;`；URL 段不转义（AC15） |

- **转义方式从 v0.1 的反斜杠改为 HTML 实体**：飞书富文本组件文档把「特殊字符转义」明确指向 HTML 实体表（`[`→`&#91;`、`]`→`&#93;`、`*`→`&#42;`、`_`→`&#95;` 等），并要求转义后才能正常展示；反斜杠属 CommonMark 便携写法，但在该文档里没有承诺。两相权衡取官方路径。
- 转义只发生在**链接文字/纯文本行**；`header` 用 `plain_text`，不解析、不转义（AC26）。
- 不截断标题、不加省略号（AC14）；长标题交给客户端折行，header 超 4 行由客户端省略（§7.3.1）。
- `card.Options` 不再有 `DocURL`；删除 `buttonElement` 的 `open_doc` 分支后，`validateNames`（`card/validate.go:13-35`）继续保证组件名唯一（`gate_form`/`reason`/`approve`/`reject`）。

### 7.4 内部契约：`internal/im` 填充与计数（行为契约）

| 项 | 契约 |
|---|---|
| 单卡填充并发 | 每卡最多 1 个后台填充任务（`filling` 标志）；进程内总并发 ≤ 2（信号量 `chan struct{}` cap 2） |
| 每材料尝试上限 | 同一张卡（同一 `message_id`）内 2 次；`attempts[materialID] >= 2` 后不再尝试 |
| 尝试计数清零 | 指纹变化时清零（附件集合/状态/驳回原因变化） |
| 成功后 | 写 `im_doc`（沿用 `Docs.Ensure`）→ 重建 Links → patch 同一 `message_id` |
| 失败后 | 该行保持纯文本；不写 `im_doc`；记一条四项日志 |
| 幂等 | patch 前比较 `lastBody`，字节相同则跳过 patch（AC13） |
| 关闭卡 | 任务离开 `in_review` → 取 `cardState.title/projectName`，缺失则 `TaskMeta` → `RenderClosed(标题/项目名/Links)` → patch → `CardStore.Close` |

### 7.5 错误语义表（内部契约，无 HTTP 业务码）

本期不新增 HTTP 接口，所以「错误码」体现为「错误来源 → 用户可见结果 → 日志 → 是否重试」四列。**没有任何一种错误会让审批按钮不可用，也没有一种错误会让卡片消失**（`SendCard` 本身失败属既有故障域，仍是「无卡」）。

| 错误来源 | 触发条件 | 用户可见结果 | 日志 | 是否重试 |
|---|---|---|---|---|
| `attachments` 查询失败 | DB 不可读 / 任务不存在 | 本轮不发送、不更新，旧卡保持 | `im attachments resolve failed`（`task`,`error`） | 下一轮 tick 自动重试 |
| 渲染失败 | JSON 编码 / 组件名重复 | 旧卡保持 | `im v3: render card`（沿用） | 下一轮重试 |
| 体积超限 | `len(body) > 28672` | 卡片无链接行，审批表单在 | `im card links dropped for size` | 不重试（重试也不会变小），等集合变化 |
| 关单时标题取不到 | `cardState` 缺且 `TaskMeta` 失败 | 卡片头显示 `交付验收`（**不回落任务号**） | `im card close meta missing`（`task`,`error`） | 不重试；下次 tick 若卡片仍未关闭会重试 |
| 导入失败（配置/权限/网络/素材为空） | `Ensure` 返回 error | 该行纯文本，其余照常 | `im doc import failed` + `im doc import attempt`（`attempt`=1/2） | 同卡该材料最多 2 次 |
| 导入超时 | 单次 HTTP 30s 超时 / ctx 取消 | 同上 | 同上（`error` 含 timeout） | 计入 2 次上限 |
| 文档功能关闭 | `docs == nil`（folder token 空） | 全部行纯文本，无任何文案 | `im doc fill skipped`（reason=docs_disabled） | 不重试 |
| 缓存写失败 | `im_doc` 写失败 | 链接照常显示 | `im doc cache write failed`（沿用） | 下次渲染重新导入一次（既有行为） |
| patch 失败 | 飞书返回非 0 / 网络 | 卡片停在上一版 | `im patch with doc failed` / `im v3: patch card` | 下一轮按指纹重试（内容幂等） |
| `SendCard` 失败 | 飞书限频/权限/群不存在 | 无卡 | `im v3: send card`（沿用） | 下一轮重试 |

## 8 关键流程

### 8.1 正常路径（首次发送）

1. `tick` → `PendingReviews(limit=MaxCards=10)` 拿到 `in_review` 任务（含 `ProjectName` / `TaskTitle`）。
2. `TaskAttachments(taskID)` → 原序附件投影；`currentver.Current(refs)` → 当前版本集合（保序，AC3）。
3. 对集合内每份：`Docs.Cached(materialID)` → 有 URL 的带上，没有的 `URL=""`。
4. 计算指纹：`sha256(taskID|status|updateTime|reason|Σ(role,kind,materialID,title))`（D4）；同时把 `ProjectName`/`TaskTitle` 写进 `cardState`。
5. `card.Render(subject, nil, &item, Options{Links})` → JSON；体积预检（§8.4）。
6. `SendCard(chatID, body)` → `messageID`；`CardStore.Put(record)`；记 `im card rendered`（action=send）；`title_len > 60` 时补一条 `im card title long`。
7. 若集合内有缺 URL 且未达上限的材料 → 启动该卡的填充任务（§8.2）。

### 8.2 正常路径（补链，AC18/AC19）

1. 填充任务按集合顺序逐份 `Docs.Ensure(ctx, materialID, docName)`。
2. 成功 → 写 `im_doc` → 更新 `cardState.links` → 重建 Links（已导入在前一次的基础上全部带 URL）→ patch **同一 `message_id`**。
3. 失败 → `attempts[materialID]++` → 记日志 → 继续下一份。
4. 全部处理完 → 记 `im doc fill finished`（set_size / links_missing / attempts）。

### 8.3 失败与降级路径

| 场景 | 检测方式 | 处理 | 最终状态 |
|---|---|---|---|
| 单份导入失败 | `Ensure` 返回 error | 该行 `URL=""`；`attempts++`；其余照常 | 一行纯文本，无提示文案（AC8） |
| 单份两次失败 | `attempts >= 2` | 不再尝试；记 `im doc fill skipped`（reason=attempt_cap） | 该卡该行永久纯文本，直到指纹变化（AC11） |
| 全部失败 | 所有行 `URL=""` | 卡片照常发出/更新，审批可用 | 头/状态行/辅助信息行 + N 行文件名 + 表单（AC9） |
| 文档功能关闭（`ECHUAN_IM_DOC_FOLDER` 空） | `p.docs == nil` | 不启动填充；全部行纯文本 | 与现状一致：无占位文案 |
| 导入超时 | `feishu.Importer` 的 `http.Client{Timeout: 30s}`（`internal/im/feishu/importer.go:33-38`）或 ctx 取消 | 按失败处理，计入 2 次上限 | 一行纯文本 |
| 填充期间又到一轮轮询 | `filling == true` | 不重复启动；本轮只做渲染/指纹比较 | 行数不变（AC13） |
| 填充期间指纹变化（新增版本） | 指纹不等 | 清空 `links`/`attempts`，按新集合重建 | 旧行消失（AC12） |
| 填充成功但材料已不在集合 | 重建时按集合取交集 | 丢弃该 URL 不回卡（仍留 `im_doc` 缓存，供别的卡复用） | 卡片不出现多余行 |
| patch 失败 | `Messenger.PatchCard` 返回 error | 记 warn；不改 `CardRecord.Fingerprint`（下一轮重试） | 卡片暂时停在上一版，内容幂等 |
| `attachments` 查询失败 | `TaskAttachments` 返回 error | 本轮跳过该任务，不发送/patch | 旧卡保持；下轮重试 |
| **关单时任务标题取不到** | `cardState` 缺 + `TaskMeta` 失败 | header 主标题用 `交付验收`，副标题省略项目名；辅助信息行仍带任务号 | 无裸 id 标题；日志可查（AC25） |
| 审批人在填充中点击 | 决策链路与卡片渲染无耦合 | 与现状一致：决策打「最新 output」 | 任务状态按决定变化 |

### 8.4 体积预检（U4 的实现侧处置）

- 触发：`len(body) > maxCardBytes`（常量 `maxCardBytes = 28672`，28 KB）。
- 依据（已核实，见 §11 R2）：[发送消息](https://open.feishu.cn/document/server-docs/im-v1/message/create.md) 明确「卡片消息、富文本消息请求体最大不能超过 30 KB」，超限错误码 `230025`；[卡片 JSON 2.0 结构](https://open.feishu.cn/document/feishu-cards/card-json-v2-structure) 明确「一张卡片最多支持 200 个元素或组件」，超限错误 `11310 element exceeds the limit`；`PATCH` 与 `POST` 同量级。第三方实测（[happyclaw 单卡容量验收](https://github.com/riba2534/happyclaw/blob/9db2ab3f06d338d6e86ecc21c6b5dca21e51c9cf/docs/feishu-card-capacity-2026-09-13.md)，非权威，仅作交叉验证）：全量更新 300,439 字节成功、307,439 字节返回 `200860 卡片过大`。
- 元素数不受影响：链接行全部塞进**已有的 1 个正文 markdown 元素**；返工后多一个辅助信息元素，整卡元素数恒为 9（正文 markdown ×1、form ×1、input ×1、column_set ×1、column ×2、button ×2、辅助 markdown ×1），与附件数无关。**只有字节数会触顶。**
- 处置：`Render` 返回 `body` 后再渲一次 `Options{Links: nil}`，用「无链接块」的 body 发送，并记 `im card links dropped for size`（`task` / `message_id` / `set_size` / `card_bytes` / `limit`）。审批表单保留，审批不阻塞。
- 不启动填充任务（无行可补），避免白发导入配额。
- 已用编号估算（非实测，实测留开发卡）：v0.1 基线卡片 1,034 字节含一枚按钮 JSON（`internal/im/card/testdata/work_item_submitted.json` 实测 `wc -c`）；返工后去掉按钮、加副标题与辅助信息行，骨架约 980 字节；每行 ≈ `title 字节 + URL 字节 + 7`。取 URL 70 字节、标题 60 汉字 180 字节 → 每行 257 字节 → 28,672/257 ≈ **108 行触顶**；标题 20 汉字（60 字节）→ 137 字节/行 → ≈ **204 行触顶**。即：正常任务（≤20 份）离上限有一个数量级。

### 8.5 云文档命名（AC20）

- `fill` 调 `Ensure(ctx, materialID, displayName)`，`displayName = title`，`title` 为空则 `material_id`（与卡片链接文字同源，保证「点开就是这个名字」）。
- `Docs.Ensure` 内部 `sanitizeDocName(title, name)`（`docs.go:146-155`）保持：`title` 非空即用；空前缀回退到上传文件名。
- 历史文档（`im_doc` 命中）不回溯改名；卡片会出现「新文档名=文件名、旧文档名=任务标题」并存，PRD §4.5 已接受。
- 注意：返工后卡片头不再是「任务标题 · 项目 · 卡类型」，而云文档名仍取材料名——这是 PRD U1 的既定口径，不受本次卡片改版影响。

## 9 部署与配置

- 新增依赖：**无**（不引入包、不加服务）。
- 环境变量 / 配置项：**无新增**。沿用 `im_poll_interval`（默认 30s，`internal/platform/config.go:409-424`）、`im_max_cards`（默认 10）、`im_doc_folder_token`（空 = 关闭导入）。
- 迁移：**无**。启动时 `Migrate` 返回 0（§6.1），因此没有 bin/DB 顺序约束。
- 发布顺序：1) `make docker-build`（构建；收尾的 builder/image prune 是全局操作，见 `docs/deploy.md:150-164`）→ 2) `scripts/backup.sh` 备份 → 3) `docker compose up -d`（单实例，短暂不可用）→ 4) `curl -fsS http://127.0.0.1:8721/readyz` 与 `/version` 校验 → 5) 观察首张验收卡（§10 告警窗口）。原因：无 schema 变更、无接口变更，不需要先动 Web 或先动数据。
- 灰度：单实例 + 单一白名单群（`im_allowed_chat_ids`，`docker-compose.yml:18` 的单容器），没有按比例灰度的载体。替代做法是**按任务灰度**：先让 1 个 `in_review` 任务走一圈（发送 → 补链 → 点击 → 终审），再放开其余任务；不满足则回滚。
- 回滚（沿用 `docs/deploy.md:445-456` 的既有流程，不新增脚本）：

```bash
# 0) 部署前先给当前镜像留退路：用错版本不必重新构建
docker tag echuan-v2:local echuan-v2:rollback

# 1) 镜像回滚（无迁移 ⇒ 不需要恢复数据，这是本方案不加迁移换来的直接收益）
docker tag echuan-v2:rollback echuan-v2:local
docker compose up -d
curl -fsS http://127.0.0.1:8721/readyz

# 2) 仅出现数据异常时才需要（本期预计不触发）
scripts/restore.sh "$HOME/echuan-v2-backups/echuan-<升级前时间>"
```

  回滚后行为：已发出的多链接卡在下一次指纹变化时会被旧代码 patch 回「看全文」形态，且卡片头回到任务号（旧渲染路径）；未变化的卡保持新形态（旧代码只在自身渲染路径覆盖）。新导入的文档名不会改回任务标题（`im_doc` 命中，不重导入）。
- 回滚的退出条件：出现「审批卡发不出」（`SendCard` 连续失败）或「链接指向错材料」两类事件之一，立即回滚，不等待定位。

## 10 可观测性

本仓**没有指标系统**（`internal/` 无 metrics/prometheus 依赖，已核对），所以观测口径 = 结构化日志 + 日志派生计数。如实说明：本节不引入 Prometheus，也不声称有指标看板。

### 10.1 日志字段

| 事件（`msg`） | 字段 | 说明 |
|---|---|---|
| `im card rendered` | `task`, `message_id`, `action`(send/patch/degrade), `set_size`, `links_total`, `links_missing`, `links_missing_ids`, `card_bytes`, `fingerprint`, `title_len` | 每次发送或更新一条（PRD §4.3 第二类）；`title_len` 供观察 header 4 行省略 |
| `im card title long` | `task`, `title_len` | 任务标题 > 60 字符时补一条（AC21–AC26 的观察信号） |
| `im card close meta missing` | `task`, `error` | 关单取不到标题，退回卡类型文字（AC25 的兜底证据） |
| `im doc import attempt` | `task`, `material`, `material_title`, `attempt`, `result`(ok/failed), `duration_ms`, `error` | 每份每次尝试一条（AC10/AC11） |
| `im doc import failed` | `task`, `material`, `material_title`, `attempt`, `error` | 保留原 `msg` 名（`docs/im-runbook.md` 按它排障），补 3 个字段 |
| `im doc fill skipped` | `task`, `material`, `attempt`, `reason`(attempt_cap / docs_disabled / not_in_set) | 解释「为什么这行没链接」 |
| `im doc fill finished` | `task`, `set_size`, `links_total`, `links_missing`, `attempts_used` | 一轮填充收尾 |
| `im card links dropped for size` | `task`, `message_id`, `set_size`, `card_bytes`, `limit` | 体积降级（U4 触发信号） |
| `im currentver suspect ref` | `task`, `material`, `role`, `kind` | 投影输入异常（宁多不藏） |

示例（单行 JSON）：

```json
{"level":"INFO","msg":"im card rendered","task":"t_01M84","message_id":"om_x","action":"send","set_size":3,"links_total":2,"links_missing":1,"links_missing_ids":"m_01ABC","card_bytes":1180,"title_len":18}
```

### 10.2 日志派生指标（对应 PRD §7 三条 + 返工一条）

| 指标 | 派生方式 | 达标线 |
|---|---|---|
| 文件名行占比 | 每张卡人工核对卡片截图与链接文字；日志侧自洽校验 `links_total + links_missing == set_size` | 前 10 张卡 100% |
| 集合覆盖率 | `im card rendered` 的 `links_total / set_size` | 前 10 张卡 100% |
| 缺链可定位率 | `im doc import failed` 中 `material_title` 非空占比 | 首月 100% |
| **卡片头无裸 id**（返工） | 每张卡人工看 header；或取 `im card rendered` 对应 message 的卡片 JSON 核对 header 子树 | 上线后前 10 张卡 100% |

### 10.3 告警（无告警系统，落到值班手册 `docs/im-runbook.md` 的检查项）

| 条件 | 持续 | 动作 |
|---|---|---|
| `im card links dropped for size` 出现任意一条 | 立即 | 值班停手截断类改动，回需求方按 U4 重定上限；附 `card_bytes`、`set_size` |
| `im card close meta missing` 出现 | 立即 | 查任务是否被删；卡片头此时只有卡类型文字，需人工在群里补充任务名 |
| `im card title long` 频发（>20% 的卡） | 一天 | 与需求方确认任务标题命名规范；header 4 行省略是客户端行为，服务端不截断 |
| 同一 `material` 在 24h 内出现 ≥4 条 `im doc import failed` | 4 条 | 按 runbook 查 app_secret / 文件夹权限 / 断网；此类故障是配置型，重试无意义 |
| `im card rendered` 的 `links_missing > 0` 且 `action=send` 超过 30 分钟 | 30 分钟 | 检查 `im_doc` 缓存与导入配额 |
| `im attachments resolve failed` 连续 3 轮 | 3 轮 | 查 DB 可读性；期间卡片停留旧版，审批不受影响 |

## 11 风险与未决

### 11.1 风险

| # | 风险 | 触发信号 | 影响 | 缓解 | 回滚 |
|---|---|---|---|---|---|
| R1 | Go 侧「当前版本」与 `web/src/lib/current-versions.ts` 漂移 | `TestKindConstantMatchesWeb` / 共享夹具红；或 AC6 双端比对出现差异 | 卡片与 Web 附件区不一致，审计指向别的版本 | 三道锁（§7.1.2）；AC6 双端逐行比对作为放行门槛 | 改一处常量/夹具，单 PR 回滚；漂移两次以上转 U5 |
| R2 | 卡片体积触达飞书 30 KB 请求体上限（错误 `230025`）或 200 元素上限（`11310`） | `im card links dropped for size`；或 `SendCard` 返回 230025 | 链接块整体缺失；极端情况下卡片发不出（若不预检） | 预检 28 KB 整卡降级（§8.4），保留审批；元素数恒 9，不受附件数影响 | 关掉预检 = 回到「发不出」；不可取。按 U4 回需求方 |
| R3 | 不设上限 ⇒ 附件多的任务成批导入，吃飞书日配额（每份 3 次 API 调用，`docs.go:91-95`） | `im doc import attempt` 计数上升；配额 429/错误码 | 其他任务导入失败（只降级链接，不阻塞审批） | 全局导入并发 ≤2、每卡每材料 2 次、成功即缓存不再导入 | 调低并发/上限常量即可，无需回滚 |
| R4 | 进程重启导致尝试计数与 `title/projectName` 缓存清零 | 重启后 `attempts_used` 重新从 1 起；关单缓存为空时改走 `TaskMeta` 兜底 | 该卡再各试 2 次；多一次 DB 查询 | 成功缓存使重启后多数材料不再导入；关单有 `TaskMeta` 兜底 | 无需回滚；若成为事故转 U6 |
| R5 | 标题转义错误导致断链或错字 | AC15 夹具红；实卡上链接不可点 | 该行显示为原文但不可点，或吞掉相邻行 | 转义单点函数 + 夹具（含 `[ ] ( ) * _ ~ < > &` 与换行）+ golden | 单 PR 回滚 |
| R6 | 同一材料既挂 input 又挂 output → 卡片两行同名（不去重） | AC6 双端比对出现两行同名 | 审批人看到两条同名链接（与 Web 一致） | 按 PRD 不去重；命名规范属数据侧 | 不改 |
| R7 | 旧文档名 = 任务标题，与新文档名 = 材料名并存 | 同一卡上点开两份名字风格不同 | 观感不一致，PRD 已接受 | 不回溯改名（D6）；runbook 说明 | 不改 |
| R8 | 填充任务被 patch 频率放大（每成功一份一次 patch） | 飞书 5 QPS 群限频（官方 create 文档「使用限制」） | 极端情况下 patch 被限频 | 每卡顺序导入、并发 ≤2 ⇒ 单群 patch 峰值 ≤2 QPS，低于 5 QPS | 改为批量 patch（合并 N 份一次）——代价是链接出现更慢 |
| R9 | 关单标题走 `TaskMeta` 兜底查询 | 任务被删 → 查不到 | 卡片头只有卡类型文字 | 日志 `im card close meta missing`；不回落任务号 | 不改 |
| R10 | `docs/im-runbook.md` 未同批更新 | 排障段仍讲「看全文」与「按头找任务号」 | 值班按旧步骤排查，误判 | 随实现同批改（§4） | 改文档 |
| R11 | 任务标题超过 4 行被客户端省略，审批人只看到前 4 行 | `im card title long` 频发；实卡标题末尾出现 `...` | 标题信息损失（但不丢任务） | 服务端不截断；`title_len` 可观测；引导在 Web 或任务标题命名上收敛 | 若要完整显示，需改成「标题用短名 + 正文首行重复全名」（产品口径，另议） |
| R12 | 任务号从卡片头移走，值班「看头找任务」的习惯失效 | 值班反馈在群里找不到任务号 | 排障变慢 | 任务号在正文末辅助信息行原样保留（可复制）；`docs/im-runbook.md` 同批写明「任务号在卡片最后一行」 | 改文档；不建议把任务号搬回 header |
| R13 | PRD §4.1 的卡片结构（粗体标题 → 摘要行 → …）与 v0.2 不一致 | 评审时对不上 PRD 原文 | 验收口径两套 | 本方案 §0 列出差异；PRD 需出 v0.2 同步（U9） | PRD 更新前按本方案实现并在评审记录中标注 |

### 11.2 未决项

| # | 问题 | 谁定 | 何时 | 未定前默认动作 |
|---|---|---|---|---|
| U4 | 卡片触达 30 KB 时是否接受「整卡去链接块、审批照发」的降级（而不是截断/不出卡） | 需求方（PM 转达） | 方案评审通过前 | 按默认动作实现（§8.4）。若否决：需给出替代形态（例如改为「附件清单另发一条消息」），否则本项阻塞上线 |
| U5 | 「当前版本」判定是否整体搬到服务端、Web 改调接口（彻底消掉双实现） | CTO | 下一轮技术债排序 | 保持双实现 + 三道锁；漂移事故两次以上即立项 |
| U6 | 导入尝试次数是否需要持久化（`im_card.doc_attempts` 列） | CTO | 出现真实配额事故后 | 进程内计数（D3）；事故阈值：单日因重试多耗的导入 ≥ 20 次 |
| **U9** | PRD §4.1 卡片结构（粗体标题 + 摘要行）需按返工口径更新为「header 任务名 + 副标题 + 状态行 + 链接行 + 辅助信息行」 | PM（承接需求方意见） | PRD v0.2 评审时 | 按本方案实现；PRD 未更新不阻塞实现，但验收以本方案 §7.3 为准 |
| U10 | 是否给「等待超时」加视觉提醒（如 >24h 变红或副标题加提示） | 需求方 + PM | 下一轮卡片改版 | 不新增（V3 从未置位 `Stale`，现状不会出现红色卡片）；本期只保留既有分支 |
| U7（已关闭） | 终态卡标题用任务 ID | — | — | 返工已修（AC25），不再是遗留项 |
| U8 | 每份材料导入失败时是否需要给审批人「缺件」提示（PRD 已列下期候选） | PM | 下期需求 | 不做交互提示，只保留文件名 + 日志可查 |

### 11.3 假设清单（未逐条实证，按最低风险取值）

| 假设 | 依据 | 若被推翻 |
|---|---|---|
| A1 一行的 markdown 链接语法在飞书卡片 2.0 的 `markdown` 元素里可用（含实体转义后的 `[...]`） | 现有 `RenderClosed` 已用 `[看全文](url)` 且线上可用 | 改为 `plain_text` + 按钮行；AC2/AC4 需重定 |
| A2 `attachments` 投影的 `title` 即 Web 附件区显示名 | `sections.tsx:114` 同口径（`ref.title \|\| ref.material_id`） | 若 Web 后续改显示规则，同批改本方案与夹具 |
| A3 30 KB 上限作用于 `content` 序列化字符串（而非解码后长度） | 官方 create 文档「卡片消息请求体最大不能超过 30 KB」+ 错误码 230025 说明 | 预检阈值下调（常量一处） |
| A4 飞书导入单次的正常耗时在秒级、`http.Client` 30s 超时足以覆盖 | `internal/im/feishu/importer.go:33-38` 现状 | 超时按失败处理，不阻塞审批 |
| A5 进程内 `cardState` 在重启后可由 `im_doc` + 附件集合 + `TaskMeta` 完整重建 | `im_doc` 是成功导入的唯一真身；标题可从库查 | 若出现「重启后链接丢失」，查 `im doc cache write failed`；标题缺失走 `TaskMeta` |
| A6 `header.title` / `header.subtitle` 的 `plain_text` 不解析 markdown（含 `* _ [ ] #`） | 官方标题组件文档：`tag` 取 `plain_text` 为普通文本内容 | 若被解析，标题需做一次实体转义（AC26 改判定方式） |
| A7 `<font color='grey'>` 在 2.0 富文本中可用，颜色枚举含 `grey` | 官方富文本文档「彩色文本样式」列出 `<font>` 与颜色枚举 | 退回只用 `text_size=notation`（12px），不依赖颜色 |
| A8 `text_size=notation`（12px）在客户端稳定生效 | 官方富文本文档 `text_size` 枚举含 `notation：辅助信息（12px）` | 辅助信息行改用普通字号 + 灰色，视觉层级略弱 |
