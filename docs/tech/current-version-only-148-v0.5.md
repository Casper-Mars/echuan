# 资料只留当前版本 + 评审区简化 技术方案 v0.5

- 状态：待评审（v0.5：按第 3 次驳回意见，删掉设计交付表与全部 D 编号——UI 设计已改完并合并，本方案不再列设计卡）
- 作者：CTO
- 日期：2026-11-23
- 输入：PRD v0.3 `AGRARAZETWXJCZVPPDI4G2MKVI`（级别 `m`）；仓库 `Casper-Mars/echuan`，`origin/main` = `8c525f62c8328809bb9a73d1fee7b9945c57542f`（= `388f4a1` + 设计契约 PR #149 合并）。**代码路径引用按 `388f4a1` 读取**（本方案不涉及代码改动，PR #149 只动 `design/`，代码在该范围两 commit 间逐字节未变）；`design/` 契约引用按 `8c525f6` 读取。工作区另有他人 23 项已暂存改动，未触碰
- 关联：issue #148；本卡 `t_01M8488H230SGNM5EMTP8WJG0R`（design，返工 3/3）

## 变更记录（逐条回应驳回意见）

| 驳回意见 | 上一版写法 | 本版改成 |
|---|---|---|
| 1、需要拍板的第 1 条「按默认的」 | 列为未决 1（是否允许给项目资料接口加 `task_id`） | **已拍板：加**（F 方案）。§1.2 F 行改为已裁决，§11 移入「已拍板」，R2 风险降级为「已放行」 |
| 2、需要拍板的第 2 条「按默认的」 | 列为未决 2（评审区是否显示「结论记在哪份产出物」） | **已拍板：不显示**（R11 只许标题 + 原因框）。从未决表移出 |
| 3、「3 是什么意思？要改 design 吗？design 归设计方案阶段的不应该留到开发阶段」 | 把 `design/` 契约与 refs 当成开发模块 M15，写「并入实现卡同 PR」 | 把设计交付从开发阶段摘出，改为设计阶段执行；dev 卡只消费契约、不碰 `design/` |
| 3 追加（v0.3/v0.4）：设计阶段由 designer 执行完毕并合并（分支 `design/echuan-14-current-version-148`，4 个 commit `9bdbcdb → 19f0879 → 95f1ea9 → fba4f53`，PR #149 → `main@8c525f6`） | v0.2 只写「由设计阶段的卡承接」，仍是纸面安排 | 设计契约与 24 张 refs 已进主干；同时纠正了 refs 重出范围（12 → 24 张：`error`/`leased` 两态也渲染评审区） |
| **4、「ui 设计已经修改了，不需要 D1 那几个卡了」（本版）** | §4.2 把设计交付列成 D1–D6 六行「交付 + 验收判据」表，读起来仍像待办卡 | **删掉整张表与全部 D 编号**：§4.2 改为一节「设计契约现状（已合并，不再建卡）」，只说三件事——改了什么文件、合并在哪个 commit、dev 卡按主干契约实现；§1 范围不再列设计阶段；§3/§5/§9/§10/§11 里所有「D1/D6/结构基线」引用改为直接指 `design/screens/*.md`、`design/components.md` |

## 1 结论

前端加一处「当前版本」纯函数，两处列表与预览左列表都改成它的投影；评审区删掉「通过/驳回」开关与被评审附件下拉，被评审附件由系统写入当前正文输出；后端只补 1 个 `task_id` 字段（项目视图不加这个字段就无法区分「同一任务的两个版本」与「两张任务各出一份同 kind」，§1.1）。

范围只有开发一段；设计契约已改完并合并，本方案不再包含设计交付（§4.2）：

| 阶段 | 范围 | 数量 |
|---|---|---|
| **开发阶段**（消费上面的契约） | 纯函数、两处列表、评审区、后端 1 个字段 | 前端 9 个文件（含 1 新增模块 + 1 新增门禁脚本）、后端 5 个文件（含 1 测试）、文档 1 处 |

**0 张表、0 个迁移、0 个新依赖、0 个新端点。**

最大风险：`reviewTargetId` 与服务端 `LatestOutput` 的口径漂移（§11 R3 / R11）；判据拿不到时界面退回全量展示，不静默藏件（§1.3 决定 3 / R1）。

### 1.1 一个必须先说的缺口：项目视图的规则缺字段

PRD R2 把分组键定成「任务 + kind」，AC7 要求 `echuan-1` 的报告组显示 10 条（10 张 dev 卡各 1 条，另 5 条来自 4 张返工卡）。但 `GET /v1/projects/materials` 的条目只有 `material_id / title / kind / role / source_material_id`（列定义 `internal/store/v3/context.go:30`，DTO `internal/api/v3_handlers.go:519-532`），**没有任务身份**。前端因此无法区分：

- 「同一任务返工两次」→ 同交付物的两个版本，旧版应隐藏；
- 「两张 dev 卡各交一份 report」→ 两个交付物，都必须显示。

2026-11-23 只读核对（`GET /v1/projects/materials?id=`）给的反例：

| 项目 | 返回 | 报告组构成 | 按 kind 去重会变成 |
|---|---|---|---|
| `echuan-1` | 16 条 = 15 报告 + 1 计划 | 10 张 dev 卡；「C1+E4」一卡 3 份、「B1」「A2/A3/A4」「C2」各 2 份 | **1 条报告**（藏掉 9 条真数据，AC7 直接失败） |
| `echuan-10` | 12 条 | 报告 2 条来自 2 张不同 dev 卡 | **1 条报告** |
| `echuan-13` | 16 条 | 「方案」组 2 条来自同一张 design 卡的两次交付 | 1 条（正确） |

所以本方案给 `GET /v1/projects/materials` 的条目**增加 1 个字段 `task_id`**：只加字段，不改现有字段的语义与返回顺序，**也不在服务端过滤**。任务详情不需要它（单任务内 kind 即分组键），保持响应不变。**需求方已按默认放行（见变更记录第 1 条）。**

### 1.2 选项对照（含「维持现状」）

| 选项 | 做法 | 代价 | 结论 |
|---|---|---|---|
| A 维持现状 | 15/16 条并列 | issue #148 未解决；AC1–AC7 全不成立 | 不做 |
| B 前端按 kind 去重 | 不碰接口 | `echuan-1` 报告 15 → 1、`echuan-10` 2 → 1，藏真数据 | 不做（数据丢失） |
| C 前端按标题/相邻顺序启发式 | 不碰接口 | 要解析标题（PRD §1 明令禁止，返工版本标题本就不同）→ 漏隐藏与误隐藏都可能 | 不做（不可判定） |
| D 服务端过滤项目资料 | 改 store 查询跳过旧版 | 会让 AC1/AC2/AC7 的「接口仍返回 4/16/16 条」对照失效，并把展示口径写进 store；任务详情仍要前端过滤 → 同一规则两处实现 | 不选（备用） |
| E 前端 N+1（先取任务列表再逐任务取附件） | 不改契约 | 1 次请求变 1+N（`echuan-1` = 11 次）；跨任务行序只能改用 `material_id`（ULID）推算；`GET /v1/projects/materials` 在 Web 端变死接口 | 不选 |
| **F 加 `task_id` 字段 + 前端过滤（已拍板）** | store 加 1 列、DTO 加 1 字段、TS 加 1 字段；去重仍在同一处纯函数 | 破 PRD 非目标 4 第一句（已由需求方放行）；前后端需同批发布 | **选** |

F 的回退（若事后被推翻）：退 D（服务端过滤），并把 AC2/AC7 的对照口径改成「改前接口 16/11 条 → 改后 14/10 条」，同时在 §7 记一条语义变更；退 D 时只保留任务详情一路的纯函数。

### 1.3 四条硬决定

1. **「当前」= 接口顺序里该组最后一条**；不解析标题、不依赖 `materials.version`（线上恒 1，PRD §1）。顺序由 SQL 保证：任务附件 `ORDER BY a.role ASC, a.sort_no ASC, a.id ASC`（`internal/store/v3/context.go:132`），项目资料 `ORDER BY a.id ASC`（`internal/store/v3/attachment.go:161`）。
2. **过滤只在前端一处**：`web/src/lib/current-versions.ts` 三个纯函数；任务详情与项目详情都调它；服务端不感知「当前」。
3. **拿不到判据就不隐藏**：项目资料条目缺 `task_id`（旧后端窗口 / 混合部署）时每条各成一版，界面退回改前的全量展示——宁可多显示，不静默藏件。
4. **设计契约已在主干，开发只按契约实现**：`design/` 的屏规格、流程、组件契约、mock 与 refs 已改完并随 PR #149 合并（`main@8c525f6`），开发卡**不再碰 `design/`**（N12）。

### 1.4 设计契约的现状（已合并且不再建卡）

- 上一轮问的「3」是 PRD §9 U2：`design/` 契约更新由谁承接。该问题**已闭环**：设计阶段派单给 designer 执行完毕，PR #149 已合并为 `main@8c525f6`（4 个 commit：`9bdbcdb` 契约文本 + mock + `default`/`rejected` 12 张 refs、`19f0879` 补 `error`/`leased` 12 张、`95f1ea9` 组件用途收口、`fba4f53` 术语对齐）。
- 改动落在 `design/screens/task-detail.md`、`design/screens/projects.md`、`design/flows.md`、`design/components.md`、`design/mock/**`，并重出 `task-detail` 的 24 张 refs（`default/rejected/error/leased` × 3 视口 × 2 主题；总量仍 240 张）。
- **不再需要设计卡**：本方案只交付开发范围；dev 卡的契约基线就是主干上的 `design/`（取图按 `fba4f53` 的 24 张、取口径按 `design/screens/*.md` 与 `design/components.md` 现行文本）。

## 2 目标与非目标

目标（PRD §2 原文，指标口径见 PRD §7）：

- A 只看得见当前版本：全量 74 条附件中 11 条旧版不进界面；每个 (任务, kind) 恰 1 行；界面可见 63 行。
- B 评审只做「给结论」：通过 4 步 → 2 步；驳回 5 步 → 3 步。
- C 审计指向正确版本：新 `gate_decisions.material_id` 100% 等于该任务当前正文输出。

非目标（PRD §2 N1–N8 原文）：不删/不归档历史数据、不做历史入口、不补版本链落库、不改 `POST /v1/tasks/decide` 语义、不做批量/撤销/权限、不改驳回原因存储与「上次驳回」条、不做跨任务归并。

本方案附加的技术边界：

- **N9** 不在服务端过滤「当前」（§1.2 D 只作备用）。
- **N10** 不删 `ui/Segmented` / `ui/SelectField`：导出面仍是 22 个组件（`web/scripts/ui-imports-check.mjs:30-53`、`design/components.md:32`）；`Segmented` 从此没有使用方，登记为待决债务（§11 R7）。
- **N11** 不加前端埋点与新指标（`AGENTS.md` §11「禁止加指标埋点」）。
- **N12** 开发卡不碰 `design/`（§1.3 决定 4）。

## 3 AC 覆盖表

来源：AC1–AC13 照抄 PRD §6（原文，编号与判定方式一致，未改判定标准）。「模块」列只列开发阶段的 M 编号；AC8 的结构基线见主干上的 `design/screens/task-detail.md`（评审区只留标题 + 驳回原因）。

| AC | 原文要点 | 模块 | 接口 | 数据 | 验证方式 |
|---|---|---|---|---|---|
| AC1 | 任务 `t_01M83XY4XGEYT1G8F8NMN5GG9N` 附件区 2 行（v0.2 正文 + 其简报），计数 `2 个`，无「历史」字样 | M2 M3 | `GET /v1/tasks/attachments`（仍 4 条） | 无 | 界面点数；DOM `.fx-detail__attachment-list > li` 长度 = 2、hint 文本含「2 个」 |
| AC2 | 项目 `echuan-13` 计数 `14 项`，「方案」组 2 行，无「历史版本」入口 | M2 M7 M8 | `GET /v1/projects/materials`（仍 16 条，含新字段） | 无 | 界面点数；`.fx-materials__hint` 文本、`.fx-materials__group` 行数 |
| AC3 | 项目 `echuan-10`「需求」组只 PRD v0.2 + 其简报；「其他」组无因隐藏而变孤儿的简报 | M2 M7 M8 | 同上 | `source_material_id` 配对 | 界面点数 + 与接口里两条简报的 source 对照 |
| AC4 | 未返工任务（`t_01M83YT9HS7FR9Y239P68DET9Q`）仍是 2 行、计数 `2 个` | M2 M3 | 同上 | 无 | 界面与改前逐项一致 |
| AC5 | 给它挂 1 条 `role=input` 后该行仍显示 | M2 | `echuan_task_attach(role=input)` | `attachments.role` | 界面点数 = 3；关掉去重开关对照（R-a） |
| AC6 | 预览左列表 2 条，无 v0.1 正文或其简报 | M3 M7 | `GET /v1/materials?id=` | 无 | 左列表项数 = 附件区行数（同一数组喂两处） |
| AC7 | 项目 `echuan-1` 报告组：过滤后 10 行仍按 5 条阈值折叠，展开 10 行 | M2 M7 M8 | `GET /v1/projects/materials`（仍 16 条） | 无 | 界面点数 + 折叠按钮文案 `展开全部（10）` |
| AC8 | `in_review` 详情：评审区只有「评审」+ 原因框；无 `Segmented`、无附件选择控件；底部三按钮 | M4 M5（结构见 `design/screens/task-detail.md`） | 无 | 无 | DOM：`[data-ui="review"] select` = 0、`input[name*="material"]` = 0；`[data-ui="review-reject"]` / `[data-ui="review-submit"]` 存在 |
| AC9 | 空原因点「驳回」→ 就地标红，无请求 | M3 M5 | `POST /v1/tasks/decide` 0 次 | 无 | 网络面板 + 红框 |
| AC10 | 填原因后点「驳回」→ 弹确认；取消 → 0 次请求 | M5 | 同上 | 无 | 网络面板（取消 0 次） |
| AC11 | 确认驳回 → 恰 1 次 `decide(reject)`；任务退出 `in_review`，顶部出现驳回条 | M3 M5 | `POST /v1/tasks/decide` | `gate_decisions.material_id` / `decision` | 网络面板 + 界面 |
| AC12 | 「通过并关闭」→ 确认 → 恰 1 次 `decide(approve)`，抽屉关闭 | M3 M5 | 同上 | 同上 | 网络面板 + 界面 |
| AC13 | 新增两条决策的 `material_id` = 各自任务当前正文输出（非旧版、非简报） | M2 M3 | `POST /v1/tasks/decide` 的入参 | `gate_decisions.material_id` | `SELECT task_id, material_id, decision FROM gate_decisions WHERE task_id=? ORDER BY id DESC LIMIT 1`（dev 在自检库执行并贴输出），PM 用 `GET /v1/tasks/attachments` 的当前输出对照 |

## 4 现状与改动点

### 4.1 开发阶段逐文件（M1–M15）

| 文件 / 模块 | 现状 | 改成什么 | 影响面 |
|---|---|---|---|
| `web/src/api/types.ts`（M1） | `AttachmentRef{material_id,title,kind,role}`（`:53-58`）未声明接口已返回的 `source_material_id`；`ProjectMaterial` 只多一个 `source_material_id`（`:60-66`） | 抽出共享形状 `VersionRef`（`material_id/title/kind/role` + 可选 `source_material_id`、`task_id`）；`AttachmentRef extends VersionRef`；`ProjectMaterial extends VersionRef` 且 `task_id` 必填 | 类型层；两个 hook 与两处列表 |
| `web/src/lib/current-versions.ts`（**新增**，M2） | 无「当前版本」概念 | 三个纯函数：`currentTaskVersions` / `currentProjectMaterials` / `reviewTargetId`（契约见 §5.2） | 唯一判定处；决定「藏哪一条」 |
| `web/src/features/task-detail/use-task-detail.ts`（M3） | `materialOptions` 由全部 output 生成（`:180-186`），默认选中第一项（`:188-194`）；`decision` 由开关持有（`:107`）；`submit` 发 `material_id: materialId ?? undefined`（`:219`） | 删 `materialOptions/materialId/setMaterialId`；`decision` → `lastDecision`（只在点按钮时写，供「重试提交」用）；新增 `reviewTargetId`；`attachments` 对外改为过滤后的可见集合，原始数组留在内部；`submit` 用 `reviewTargetId` | 只被 `TaskDetailRoute` / `ReviewPanel` 消费；调用方签名收敛 |
| `web/src/features/task-detail/ReviewPanel.tsx`（M4） | 三个控件：`Segmented`（`:45-55`）、`SelectField`（`:56-71`）、`TextArea`（`:72-81`） | 只留「评审」标题 + `TextArea`（label 改「驳回原因（驳回必填）」，按 PRD §4.2 文案表）+ 最多一行提示（§5.3） | 评审区结构；AC8 |
| `web/src/features/task-detail/TaskDetailRoute.tsx`（M5） | 底部「取消/驳回/通过并关闭」（`:169-191`）不看附件是否就绪；重试提交用 `detail.decision`（`:254`）；分区与预览都用全量附件（`:287`、`:310-315`） | 两个结论按钮在 `attachmentsLoaded === false` 时禁用；重试提交用 `lastDecision`（空则禁用）；传过滤后的附件给分区与预览 | 详情页交互 |
| `web/src/features/task-detail/task-detail.css`（M6） | `.fx-detail__review .ui-segmented`（`:165-168`）只为那一个开关存在 | 删这 4 行（不留死规则） | 样式债门禁口径不变 |
| `web/src/features/projects/ProjectDetailSheet.tsx`（M7） | 资料与预览各自吃 `materials.materials` 全量（`:73-89`） | 用 `useMemo` 过滤一次，**同一数组**喂 `MaterialsSection` 与 `AttachmentPreviewSurface`（AC6 的项目侧对应） | 项目抽屉 |
| `web/src/features/projects/MaterialsSection.tsx`（M8） | 计数 `materials.length`（`:100`）、分组/简报/折叠（`:39-69`、`:122-123`） | 逻辑一行不改：入参已是可见集合，计数与折叠自然跟着变（R9/R10）——只补注释说明「入参为当前版本」 | 计数文案；AC2/AC7 |
| `internal/store/v3/attachment.go`（M9） | `ListProjectMaterialRefs` 返回 `[]MaterialRef`（6 列，无任务身份，`:153-166`） | 新增扁平结构 `ProjectMaterialRef{TaskID, MaterialID, Title, Kind, Role, SourceMaterialID}`（`storedb` 的列映射**跳过匿名字段**，见 `internal/store/query.go:110-116`，不能嵌入 `MaterialRef`），查询补 `t.task_id`（`tasks t` 已在 JOIN 里） | 项目资料读路径；`role` 过滤在 service 层 |
| `internal/app/v3/service.go`（M10） | `ProjectMaterials` 返回 `[]storev3.MaterialRef`（`:414-436`） | 签名换成 `[]storev3.ProjectMaterialRef`；`role` 过滤循环随类型调整（逻辑不变） | handler 调用点 |
| `internal/api/v3_handlers.go`（M11） | `projectMaterials` 复用 `v3AttachmentList{Items []v3ClaimInput}`（`:884-886`、`:927`） | 新增 `v3ProjectMaterialItem{task_id,material_id,title,kind,role,source_material_id?}` 与 `v3ProjectMaterialList`，`projectMaterials` 用它；`attachments` 与 claim 的 `v3ClaimInput` **不动** | 接口响应 +1 字段；claim 契约不受影响 |
| `api/openapi.yaml`（M12） | `listProjectMaterials` 只有 summary + 响应描述，无 item schema（`:129-142`） | summary 补一句「each item carries the task_id that produced it」；`make contract` 只比对 operation 集合，字段增量不触发门禁 | 契约文档 |
| `internal/api/v3_api_test.go`（M13） | `TestProjectMaterials*` 断言条数/配对（`:833-905`） | 补 `task_id` 非空且等于所属任务；新增一例「两张 dev 卡各挂一份 report → 两条都在、`task_id` 不同」（钉住 AC7 的前提） | 后端回归 |
| `web/scripts/current-versions-check.mjs`（**新增**，M14）+ `web/package.json` | 前端没有单测框架，`build` = `tsc --noEmit && vite build && node scripts/check-size.mjs`（`:8`） | 用仓库已有的 esbuild（从 `vite` 的解析上下文取，同 `ui-imports-check.mjs:70-95`）打包纯函数，跑 7 组夹具断言；接进 `build` 链（`tsc --noEmit && node scripts/current-versions-check.mjs && vite build && node scripts/check-size.mjs`） | 门禁；不需要浏览器与后端 |
| `docs/tech/project-materials-ui-v0.1.md`（M15） | §6 接口只列 5 个字段（`:94-101`）；§8 AC2 写「显示 16 条」 | §6 补 `task_id`；§8 AC2 改为「当前版本 14 条、接口仍 16 条」 | 旧方案文档与新口径一致 |
| `docs/tech/current-version-only-148-v0.5.md`（M15） | 本文件 | 随实现 PR 入库 | 方案落库 |

### 4.2 设计契约现状（已合并，不再建卡）

设计侧已改完并合并，本方案不再列设计交付项，也不再开设计卡：

| 项 | 现状 |
|---|---|
| 契约文本 | `design/screens/task-detail.md`（评审区只留「评审」标题 + 驳回原因框；组件表去 `ui/Segmented` / `ui/SelectField`；附件段写明只显示当前版本、计数取可见条数）、`design/screens/projects.md`（资料区版本口径 / 简报跟随 / 计数口径）、`design/flows.md` F3 第 2 步、`design/components.md`（`ui/Segmented` 改「暂保留（当前无使用方）」、`ui/SelectField` / `ui/TextField` 用途去掉「详情评审」、术语对齐到「驳回原因」） |
| mock | `design/mock/**` 删掉评审区的开关与附件下拉（8 份同源副本 md5 全等） |
| refs | `task-detail` 重出 **24 张**（`default/rejected/error/leased` × 3 视口 × 2 主题）；总量仍 240 张、`task-detail` 仍 36 张 |
| 合并位置 | PR #149 → `main@8c525f62c8328809bb9a73d1fee7b9945c57542f`（4 个 commit：`9bdbcdb` / `19f0879` / `95f1ea9` / `fba4f53`） |
| 对开发卡的约束 | 只按主干契约实现，不改 `design/`（N12）；视觉对照取 `fba4f53` 的 `task-detail` 24 张 |

### 4.3 本次不改

- `ui/Segmented.tsx`、`ui/SelectField.tsx`、`ui/index.ts`、`ui-imports-check.mjs`：导出面保持 22 个（N10）。删组件要连改 4 处（`index.ts` / `EXPECTED` / `components.md` / `README.md`），与本需求无关。
- `use-attachment-preview.ts`、`attachment-preview.tsx`、`ui/MarkdownViewer.tsx`、`ui/AttachmentRow.tsx`：预览六态、8s 超时、512 KB 门限、并发去重全部不动。
- `internal/store/v3/context.go`（任务附件查询）、`attachments` 端点、`v3ClaimInput`（claim 载荷）：任务侧响应零改动。
- `POST /v1/tasks/decide` 的服务端实现（`internal/app/v3/service.go:209-254`）：`material_id` 本就可空、`reason` 仅 reject 必填，语义不变；变的只是客户端传什么（PRD 非目标 5）。
- `materials.version` / `previous_material_id`：不写、不显示（PRD 非目标 3）。
- 开发阶段不碰 `design/`（N12）；看板、收件箱、产品页、`ProjectsPage` 不涉及。

## 5 总体设计

### 5.1 组件关系

```text
GET /v1/tasks/attachments ─┐
                          ├─ 原样进 state ─ useTaskDetail
                          │                   ├─ currentTaskVersions(raw) ─→ attachments（可见）
                          │                   │                             ├─→ <AttachmentSection>（计数=长度）
                          │                   │                             └─→ <AttachmentPreviewSurface>（左列表）
                          │                   └─ reviewTargetId(raw) ───────→ submit() 的 material_id
                          │
GET /v1/projects/materials ─ 原样进 state ─ ProjectDetailSheet
  （items + task_id）                          └─ useMemo currentProjectMaterials(items) ─→ 同一数组
                                                  ├─→ <MaterialsSection>（计数/分组/折叠）
                                                  └─→ <AttachmentPreviewSurface>（左列表）
```

一处判定、两处呈现：判定只在 `web/src/lib/current-versions.ts`；页面只做投影。

| 组件 | 职责 | 同步/异步 | 失败后的行为 |
|---|---|---|---|
| `lib/current-versions` | 纯函数判定「当前版本」与「评审目标」 | 同步（纯） | 无失败路径；输入异常（缺 `task_id`）走 §5.2 R-g 兜底 |
| `useTaskDetail` | 取数 + 派生 + 提交 | 异步 | 附件失败 → 既有错误条 + 保留已渲染分区；提交失败 → 错误条 + 重拉 |
| `ProjectDetailSheet` / `MaterialsSection` | 项目资料的取数与投影 | 异步 | 既有区块内错误条 + 重试，不影响字段区 |
| `POST /v1/tasks/decide` | 记录结论与被评审附件 | 同步 | 非 2xx → 客户端错误条 + 立即重拉（既有） |

### 5.2 纯函数契约（`web/src/lib/current-versions.ts`）

```ts
export interface VersionRef {
  material_id: string
  title: string
  kind: string        // material kind：requirement|design|plan|report|context|other
  role: 'input' | 'output'
  source_material_id?: string
  task_id?: string
}

/** 任务详情：同一任务内按 kind 保留最后一条正文输出 */
export function currentTaskVersions<T extends VersionRef>(items: T[]): T[]

/** 项目资料：按 (task_id, kind) 保留最后一条；缺 task_id 的条目各成一版（不隐藏） */
export function currentProjectMaterials<T extends VersionRef>(items: T[]): T[]

/** 评审目标：最后一条 role=output 且 kind ∈ 正文 kinds 的 material_id；没有则 null */
export function reviewTargetId(items: VersionRef[]): string | null
```

| # | 规则 | 依据 |
|---|---|---|
| R-a | `role=input` 永不隐藏，位置不变 | PRD R1 |
| R-b | 去重只作用于 `role=output` 且 `kind ∈ {requirement, design, plan, report}`；`other` 不参与（无版本证据，按条显示） | 与服务端 `LatestOutput` 的 kinds 1–4 同口径（`internal/store/v3/attachment.go:177`、`internal/domain/enum/enum.go:151-162`） |
| R-c | 任务详情分组键 = `kind`；项目资料分组键 = `(task_id, kind)` | PRD R2/R6；§1.1 |
| R-d | 组内保留**数组顺序最后一条**（接口顺序即交付插入顺序）；输出保持原数组顺序，不重排 | PRD R3/R8 |
| R-e | 简报（`kind=context` 且有 `source_material_id`）**当且仅当其 source 被去重丢弃**时一并丢弃；source 从未出现在返回集合（真孤儿）→ 保留 | PRD R4/R5 |
| R-f | 无 `source_material_id` 的 context 全保留 | 判定不了归属，数据无损优先 |
| R-g | 项目资料条目缺 `task_id` → 每条各成一版（等价于不隐藏） | §1.3 决定 3（旧后端窗口，参照 ADR-0002 §4 C3 的部署防线做法） |
| R-h | `reviewTargetId` 取 kinds 1–4 的最后一条；空集合 → `null`（提交时不带 `material_id`） | PRD R13；服务端 `material_id` 可空（`internal/app/v3/service.go:223-230`） |

`reviewTargetId` 与 `LatestOutput` 的一致性：服务端取 `role=output AND kind IN (1,2,3,4) ORDER BY a.id DESC LIMIT 1`（`internal/store/v3/attachment.go:171-186`）；任务附件接口按 `a.role ASC, a.sort_no ASC, a.id ASC` 返回（`internal/store/v3/context.go:132`），所以「数组里最后一条」只有在**该任务输出的 `sort_no` 全相同时**才等价于「`a.id` 最大」。

- 假设：平台自身的交付写入不传 `sort_no`（`internal/api/v3_handlers.go:473,489` 可缺省、`kit` 只在调用方显式给时才传，`kit/src/tools/v3.ts:394-406`），线上 74 条附件均由平台流程产出 ⇒ `sort_no` 恒 0。
- 复核（写进实现卡）：`SELECT COUNT(*) FROM attachments WHERE sort_no <> 0;` 期望 0；非 0 则客户端无法复现服务端口径，按 §11 R11 升级 CTO。

### 5.3 评审区结构与提示

评审区只三样东西（PRD R11；结构见主干 `design/screens/task-detail.md`）：

```text
评审
┌──────────────────────────────────────────────┐
│ 驳回原因（驳回必填）                          │
│ ┌──────────────────────────────────────────┐ │
│ │ 写清缺口与需要改的地方                    │ │
│ └──────────────────────────────────────────┘ │
└──────────────────────────────────────────────┘
```

提示行（最多一行，互斥，由状态选一条）：

| 状态 | 提示文案 | 依据 |
|---|---|---|
| 附件未收口（首拉中） | `正在读取本任务的产出物…`，两个结论按钮禁用 | 防「附件未到就提交」写进空 `material_id`（M3 目标 100%） |
| 收口且无正文输出 | `本任务还没有产出物，本次结论会记录为「未指定附件」。` | PRD §4.2 原文 |
| 收口但附件请求失败 | `产出物读取失败：本次结论会记录为「未指定附件」，可先重试。` | §8 异常路径；与上一行同一出口（`material_id` 可空） |
| 正常 | 无提示 | — |

不显示「本次结论记录在哪份产出物」：已按默认拍板（变更记录第 2 条），R11 只允许标题 + 原因框。

## 6 无数据模型改动

本期无表、无字段、无迁移、无回填，因此没有 up/down SQL，也没有数据回滚。与「数据」相关的只有一处**读取投影的字段增量**：

| 项 | 位置 | 说明 |
|---|---|---|
| `task_id` 出现在项目资料响应里 | `internal/store/v3/attachment.go:153-166` 的 SELECT 列 + `internal/api/v3_handlers.go` 的新 DTO | 来自已有列 `tasks.task_id`，不新增列、不新增索引 |
| 为什么不需要索引 | 查询已 `JOIN tasks t ON t.id = a.task_id` 且按 `a.id ASC` 排（主键序），`task_id` 只随行带出 | 现有执行计划不变 |
| 回滚 | `git revert` 单 commit；旧前端忽略新字段，新前端见 §5.2 R-g 不隐藏 | 无迁移即无数据回滚 |
| 存量数据 | 无需回填：`task_id` 由 JOIN 现算 | — |

`attachments` / `gate_decisions` 保持只追加、原样保留（PRD 非目标 1）：本方案一个写操作都不做。

## 7 接口契约

### 7.1 `GET /v1/projects/materials?id=&role=`

- 鉴权：无（平台无鉴权，`X-Actor` 只是身份声明，`AGENTS.md` §7）。
- 幂等：只读 GET，天然幂等。
- 限流：无（既有端点，本次不加）。
- 语义变化：**只有一处**——响应条目多一个字段；行数、顺序、`role` 过滤、404 行为都不变。

请求：不变（`id` 必填；`role` 可选 `input|output`）。

响应（200）：

| 字段 | 类型 | 变更 | 说明 |
|---|---|---|---|
| `task_id` | string | **新增** | 产出这条材料的任务业务号（`t_…`）；项目详情用它做 (task, kind) 分组 |
| `material_id` | string | 不变 | 材料业务号 |
| `title` | string | 不变 | — |
| `kind` | string | 不变 | `requirement\|design\|plan\|report\|context\|other` |
| `role` | string | 不变 | `input\|output` |
| `source_material_id` | string? | 不变（缺省即省略，`omitempty`） | 简报指向它解释的那一版正文 |

错误码（不变）：`400 invalid_argument`（缺 `id` / `role` 非法）、`404 not_found`（项目不存在）。

### 7.2 `GET /v1/tasks/attachments?task_id=`

契约零改动（单任务内 `kind` 已足够，§1.1）。响应仍返回全部 4 条，过滤在前端——AC1/AC4 的对照口径因此成立。

### 7.3 `POST /v1/tasks/decide`

服务端语义**零改动**（PRD 非目标 5）：`material_id` 可空、`reason` 仅 reject 必填、approve → done / reject → ready（`internal/app/v3/service.go:209-254`、`internal/store/v3/task.go:332-356`）。

客户端行为变化（唯一的入参变化）：

| 字段 | 改前 | 改后 |
|---|---|---|
| `material_id` | 界面上「被评审附件」下拉的第一项（默认最旧版，候选还含简报） | `reviewTargetId(attachments)`：当前正文输出；没有则省略 |
| `decision` | 开关的当前值 | 点的是哪个按钮 |
| `reason` | 同上（驳回必填、通过可填） | 不变 |
| `decided_by` | `VITE_ACTOR` 或 `web` | 不变 |

## 8 关键流程

### 8.1 任务详情（读 + 评审）

1. `GET /v1/tasks?id=`、`/dependencies`、`/attachments` 三路并发（既有，`use-task-detail.ts:118-154`）；附件按接口顺序原样进 state。
2. 同一份 raw 上派生两个值：`attachments = currentTaskVersions(raw)`、`reviewTargetId(raw)`。两者同源同帧，界面显示与审计取值不分叉。
3. 附件区计数 = 可见条数；预览左列表吃同一个数组（AC1/AC6）；空态文案不变。
4. 评审区渲染 §5.3 的结构与提示；底部「取消 / 驳回 / 通过并关闭」；`attachmentsLoaded === false` 时两个结论按钮禁用。
5. 点「驳回」→ `validate('reject')`：原因为空 → 就地标红、不发请求（AC9）；非空 → `setConfirm('reject')` 弹确认（AC10 的取消路径不发请求）。
6. 确认 → `submit('reject')` → `POST /v1/tasks/decide{decision:'reject', material_id, reason}`（AC11 恰好 1 次）→ 2xx：`publishListWrite` + 重拉详情 + 关闭抽屉。
7. 「通过并关闭」走同一链路，`decision:'approve'`，`reason` 空或非空都随请求发出（AC12）。

### 8.2 项目资料（读）

1. 抽屉打开 → `GET /v1/projects/materials?id=`（含 `task_id`）。
2. `useMemo currentProjectMaterials(items)` → 可见集合（同一数组喂资料区与预览层）。
3. 资料区：计数 = 可见条数；分组顺序 需求 → 方案 → 拆分 → 报告 → 其他；组内按接口顺序倒序（`MaterialsSection.tsx:57` 的 `reverse()`）；简报缩进挂在 source 下；报告组 5 条阈值作用于过滤后的行（AC7）。
4. 点行 → 预览面板（左列表 = 可见集合）；返回回到资料列表。

### 8.3 异常路径

| 场景 | 检测方式 | 处理 | 最终状态 |
|---|---|---|---|
| 附件请求失败 | `Promise.allSettled` 里 rejected（既有） | 既有错误条 + 保留已渲染分区；评审区提示改「产出物读取失败…」；仍允许提交（与「没有产出物」同一出口） | 结论照常记录，`material_id` 为空 |
| 附件未收口就点结论 | `attachmentsLoaded === false` | 两个结论按钮 disabled | 不会写入空 `material_id`（除非附件确实加载失败，见上一行） |
| 任务没有正文输出 | `reviewTargetId === null` | 提示「本次结论会记录为『未指定附件』」；提交不带 `material_id` | 服务端已允许（`material_id` 可空） |
| 评审并发冲突（他人已处理） | 非 2xx | 既有：错误条显示服务端 message + 立即重拉 | 用户看到最新状态；「重试提交」按 `lastDecision` 重弹确认，`lastDecision` 为空则按钮禁用 |
| 重复点击 | `submittingRef` 门闩（既有） | 同一 tick 只发一次 | 恰好 1 次请求（AC11/AC12） |
| 旧后端（响应无 `task_id`） | `items.every(i => i.task_id === undefined)` | 项目资料不隐藏（R-g）；任务详情照常过滤（单任务不需要 task_id） | 界面退回改前的全量；不会藏件 |
| 打开详情后有人挂了新附件 | 无（不做二次校验） | 以打开时刻的可见集合为准提交 | 低概率错指；登记 R5，不在本期修 |
| 同一 source 出现两份简报（理论上服务端唯一约束排除） | `buildGroups` 的 Map 只留最后一份 | 保留一份渲染；计数会比行数多 1 | 已知偏差，登记 R4 |
| 项目资料请求失败 | 既有 | 区块内错误条 + 重试；字段区不受影响 | 抽屉不关 |
| 取数超时 | 后端中间件 `Timeout(30s)`（`AGENTS.md` §7）/ 预览正文 8s（`use-attachment-preview.ts:20`） | 走上面的「请求失败」分支，错误条 + 重试；不新增超时值 | 分区降级、详情不白屏 |
| 部分失败（附件成功、依赖失败，或反之） | 既有 `Promise.allSettled` | 只补一条错误条，成功的分区照常渲染；可见集合仍按成功的附件算 | 当前版本口径不受依赖失败影响 |

## 9 部署与配置

新增依赖：**无**（前端不引入测试框架，自检脚本复用 `vite` 传递依赖里的 esbuild；后端只用标准库与既有反射扫描器）。

| 环境变量 | 默认值 | 必填 | 作用 |
|---|---|---|---|
| 无 | — | — | 本期不加任何配置项 |

唯一剩余交付点（开发）：

1. **设计阶段：已完成**——`design/` 契约与 24 张 refs 已随 PR #149 合并进 `main@8c525f6`，没有设计卡，也没有「先等设计」的前置。
2. **开发阶段**：同一 PR 内提交后端字段（M9–M13）+ 前端消费（M1–M8、M14）+ 文档（M15）；然后 `make web` → `make build` → `make docker-build` → `make docker-up`。

若被迫分开部署（例如后端先合）：先部署带 `task_id` 的后端，再上前端——反过来会走 R-g 兜底（不隐藏），不会藏件。

灰度：**不做**（内网单实例、无分组能力、无埋点可观测；单 commit 纯前后端改动，回滚成本低于灰度成本）。

回滚：`git revert <commit>` → `make web` → `make docker-build` → `make docker-up`；无迁移、无数据变更、无残留；旧前端忽略 `task_id`。设计资产回滚 = revert 设计阶段那个 commit（refs 回到上一版）。

两条运维注意（写进 dev 卡验证步骤，不写成本文执行动作）：

1. `make docker-build` 收尾会跑**全局** `docker builder prune -f` 与 `docker image prune -f`（`AGENTS.md` §10），属破坏性操作，需人工确认后再跑。
2. 浏览器验收必须落在**改动后的产物**上：测试平台 `http://echuan:8720` 跑的是已部署镜像，`make web` 后必须重建镜像；或 `cd web && pnpm dev`（其 `/v1` proxy 硬编码 `127.0.0.1:8721`，见 `AGENTS.md` §3）。

前置门禁命令（dev 卡执行顺序固定）：

```bash
node web/scripts/current-versions-check.mjs     # 新增：7 组夹具（含 echuan-1/10/13 的真实形状）
node web/scripts/style-debt.mjs                 # 6 项样式债门禁
make web                                        # tsc --noEmit + 纯函数自检 + vite build + 体积门禁
make build && make test                         # 后端改了 store/app/api
make contract                                   # openapi 与 routeSpecs 双向一致
```

## 10 可观测性

- **不新增指标与埋点**：`AGENTS.md` §11 明确禁止（无 `/metrics`），PRD §7 已认账「本屏无埋点，人工核对」。因此本章不写指标名与告警阈值——**无指标即无告警**，失败可见性靠下面的 DOM 钩子、门禁脚本与既有错误条。
- 日志：沿用既有访问日志一行 JSON（`ts/level/msg/request_id/actor/method/path/status/duration_ms`，业务路由额外带 `subject`，错误额外带 `code`，见 `AGENTS.md` §10）。示例：`{"ts":"2026-11-23T13:20:01Z","level":"info","request_id":"...","method":"GET","path":"/v1/projects/materials","status":200,"duration_ms":3,"subject":"echuan-13"}`；本期不加字段、不加日志行。

可断言钩子（验收与回归都用它）：

| 钩子 | 位置 | 取值 | 用途 |
|---|---|---|---|
| `[data-ui="detail-attachments"] .fx-detail__attachment-list > li` | 任务详情附件区 | 数量 | AC1/AC4/AC5 |
| `[data-ui="detail-attachments"] .fx-detail__section-hint` | 同上 | `2 个 · 可在线读正文` | AC1 的计数口径 |
| `[data-ui="review"] select` / `[data-ui="review"] input` | 评审区 | 0 | AC8（控件已删净） |
| `[data-ui="review-reject"]` / `[data-ui="review-submit"]` | 底部操作条 | 存在 | AC8/AC11/AC12 |
| `[data-ui="project-materials"] .fx-materials__hint` | 项目资料区 | `14 项 · 来自任务` | AC2 |
| `[data-ui="project-materials"] .fx-materials__group` 内 `.fx-materials__row` | 同上 | 行数（含简报行） | AC2/AC3/AC7 |
| `[data-preview="open"]` 左列表项 | 预览层 | 数量 = 附件区行数 | AC6 |
| `gate_decisions.material_id` | 自检库 | 等于 `reviewTargetId` | AC13 |

- 脚本级断言：`web/scripts/current-versions-check.mjs` 用 7 组夹具钉住规则表（§5.2）：`echuan-13` 形状 16→14、`echuan-10` 形状 12→8、`echuan-1` 形状 16→11、input 不隐藏、真孤儿简报保留、缺 `task_id` 不隐藏、`reviewTargetId` 排除 context 与无正文输出。
- 当前门禁基线（2026-11-23 本仓库实测）：`style-debt` 6/6 达标（未引用类名 1/263 = 0.4%，断点 2 个取值 `47.99em/63.99em`）；`internal/web/dist/assets` 全部 JS 的 gzip 合计 **184,107 B**，阈值 344,056 B（占 53.5%，`web/scripts/check-size.mjs:13`）。
- 设计资产侧已在主干：`grep "评审时选\|详情评审" design/components.md` 只剩 `ui/TextArea` 一行、组件仍 22 个、`task-detail` 24 张 refs 已重出；这些是设计阶段的验收证据，不进入 `make web`。

## 11 风险与未决

### 11.1 已拍板（本版按此落地）

| # | 问题 | 结论 | 依据 |
|---|---|---|---|
| 1 | 是否给 `GET /v1/projects/materials` 加 `task_id` | **加**（F 方案）；不加则 `echuan-1` 的 15 条报告塌成 1 条、AC7 失败 | 需求方 2026-11-23 返工意见「1 和 2 按照默认的」 |
| 2 | 评审区是否显示「本次结论记录在哪份产出物」 | **不显示**：R11 只允许标题 + 原因框 | 同上 |
| 3 | `design/` 契约与 refs 的归属 | **已合并、不再建卡**：PR #149 → `main@8c525f6`；开发卡不碰 `design/` | 需求方 2026-11-23 合并后确认「ui 设计已经修改了，不需要那几个卡」 |

### 11.2 风险

| # | 风险 | 触发信号 | 影响 | 缓解 | 回滚 |
|---|---|---|---|---|---|
| R1 | 项目视图缺 `task_id` 时按 kind 去重 | `echuan-1` 报告组只剩 1 行 | 藏 9 条真数据 | §5.2 R-g：缺字段不隐藏；M14 夹具钉住 | 无（默认安全侧） |
| R2 | 新增响应字段破 PRD 非目标 4 第一句 | 后续评审再提越界 | 方案返工 | 需求方已放行（§11.1 第 1 条）；仍保留 D 方案作退路 | 退 D（服务端过滤 + 改 AC 对照口径） |
| R3 | 客户端「当前正文」口径与服务端 `LatestOutput` 漂移 | AC13 指向的版本与 `LatestOutput` 不一致 | 审计口径两套 | kinds 写成常量、与服务端注释对齐；M14 夹具 + AC13 兜底 | 改常量一处 |
| R4 | 同 source 两份简报（服务端唯一约束理论上排除） | 计数比行数多 1 | 数字打架 | 沿用既有 Map 只留一份渲染；登记不改服务端 | 无 |
| R5 | 打开详情后附件变化 → 提交的是打开时刻的当前版 | AC13 抽查不一致 | 低概率错指 | 不二次校验（decide 无归属校验）；登记 | 无 |
| R6 | refs 重出范围算错（**已发生并已处置**：先按 12 张写，实际需 24 张——`error`/`leased` 两态也渲染评审区） | 基准目录里仍有带旧控件的图 | dev 视觉回归撞上自相矛盾的基准 | 按 mock 的渲染条件枚举态集合（`review = !failed`、`loading` 提前 return）；已补出 `error`/`leased` 12 张，现为 24 张 | 回退该 refs commit |
| R7 | `ui/Segmented` 从此没有使用方 | 后续想「顺手删组件」 | 导出面 22 → 21 的连带改动（4 处） | N10 明确保留；`ui-imports-check` 只断言导出集合、`style-debt` 的未引用类名把组件自身引用算进去（当前未引用仅 `ui-segmented--danger` 1 条） | 无 |
| R8 | 与在飞改动冲突：`internal/api/v3_handlers.go`、`api/openapi.yaml`、`web/src/api/client.ts` 在工作区已有 23 项他人已暂存改动 | rebase 冲突 | 合并返工 | 实现卡先 `git fetch` + 基于最新 `main` 动手；冲突面限于 `projectMaterials` 附近与类型文件 | 谁先合并谁先跑全量门禁 |
| R9 | `docs/tech/project-materials-ui-v0.1.md` §8 AC2 仍写「16 条」 | 文档与实现冲突 | 长出第二份过期口径 | M15 同批改 | 无 |
| R10 | 两个结论按钮都过确认框，评审步数比「驳回无确认」多 1 | 评审人抱怨慢 | 体验 | 需求方已裁定（PRD R15）；确认键不自动聚焦，避免 Enter 误确认 | 无 |
| R11 | 任务的 output 附件若用了非零 `sort_no`，客户端「数组最后一条」≠ 服务端 `LatestOutput`（`a.id` 最大） | 实现卡自检 `SELECT COUNT(*) FROM attachments WHERE sort_no <> 0` 非 0 | 审计可能指向另一版 | 假设 `sort_no` 恒 0（`kit/src/tools/v3.ts:394-406`）；复核命令写进实现卡；命中则升级 CTO | 无 |
| R12 | 设计阶段与开发阶段的顺序被打乱（**已关闭**：设计契约 PR #149 已于 2026-11-23 合并进 `main@8c525f6`，顺序不再是风险） | — | — | §11.3 第 0 步已完成 | — |

### 11.3 实施顺序与依赖（不排期、不估人日）

0. **设计阶段：已完成**（契约文本 + mock + 24 张 refs，PR #149 合并为 `main@8c525f6`；无设计卡）。开发卡的前置是「基于 `main@8c525f6` 的 `design/` 契约实现」，取图按 `fba4f53` 的 `task-detail` 24 张。
1. **M9–M13（后端字段与测试）**：`ProjectMaterialRef` + 查询 + service 签名 + 新 DTO + openapi 摘要 + 跨任务用例。前置：0 完成；后置：M7 依赖它的字段。
2. **M1–M2 + M14（类型与纯函数）**：`VersionRef`、三个纯函数、自检脚本接进 `build`。前置：0，可与 1 并行。
3. **M3–M6（任务详情三件套 + 死样式）**：hook / panel / route / CSS。前置：2 与 0（M4 的结构按 `design/screens/task-detail.md`）。
4. **M7–M8（项目详情）**：过滤上提、计数与折叠跟着可见集合走。前置：1、2、0。
5. **M15（文档）**：前置：3、4。
6. **验收**：AC1–AC13 按 §3 逐条；测试平台 `http://echuan:8720` 需先重建镜像（§9）。

外部前置：PRD v0.3 已定稿（本次输入）；工作区里他人的 23 项暂存改动先落地（R8）；R11 的 `sort_no` 复核在开发卡启动时执行。

---

## 附录 A · 交付报告（驿传报告契约四段）

**一、改了什么（逐条回应驳回原因）**

- **驳回原因 1「需要拍板的第 1 条按默认的」**：把「是否给 `GET /v1/projects/materials` 加 `task_id`」从「未决」改为**已拍板：加**（§1.2 F 行、§11.1 第 1 条）；R2 风险从「可能被越界打回」降级为「已放行、保留 D 退路」。方案主体不变：仍只加 1 个字段、不在服务端过滤、任务详情接口零改动。
- **驳回原因 2「需要拍板的第 2 条按默认的」**：把「评审区是否显示『结论记在哪份产出物』」改为**已拍板：不显示**（§5.3 末段、§11.1 第 2 条），依据 PRD R11 只允许标题 + 原因框。
- **本轮驳回「ui 设计已经修改了，不需要 D1 那几个卡了」**：上一版 §4.2 还留了一张 D1–D6 的「交付 + 验收判据」表，读起来像还在排设计卡。本版删掉整张表与全部 D 编号：§4.2 改成一节「设计契约现状（已合并，不再建卡）」，只写改了什么文件、合并在哪个 commit、对开发卡的约束；§1 不再列设计阶段；§3/§5/§9/§10/§11 里所有「D1/D6/结构基线」引用改为直接指主干上的 `design/screens/*.md`、`design/components.md`。
- 其余章节按新范围重算：§1 范围只留开发一段（设计已合并）；§9 只留开发交付点；§3 AC8 的结构基线改指主干屏规格；§11 仍是「已拍板 / 风险 / 实施顺序」三段。
- v0.4/v0.5：设计契约（PR #149，4 commit）已合并为 `main@8c525f6`，契约与 `task-detail` 24 张 refs 在主干；基线由 `388f4a1` 更新为 `8c525f6`（代码引用仍按 `388f4a1`，本方案不碰代码）。
- 未改代码、未改设计契约（本卡只读仓库）、未建下游卡、未改任务状态与优先级。

**二、怎么自检**

- 仓库同步：`git fetch --prune origin` → `origin/main` = `8c525f62c8328809bb9a73d1fee7b9945c57542f`（= `388f4a1` + PR #149 合并）；主工作区 `git status --porcelain | wc -l` = 23（他人已暂存改动，未触碰）；代码引用按 `git show 388f4a1:<path>`、`design/` 契约按 `git show 8c525f6:<path>` 读取。
- 驳回意见核对：`design/screens/task-detail.md:7,9,14,21` 确认开关与「自动带入 output 附件」写进屏规格；`design/components.md:32,397-415` 确认 `ui/Segmented` 用途是「详情评审」且 `:415` 反例正是本需求的做法；`design/mock/shots/task-detail/mock.js:260-269` 确认 mock 画了 segmented + select；`ls design/refs | grep -c task-detail` = 36（6 态 × 3 视口 × 2 主题）；mock 的 `review = !failed` + `loading` 提前 return ⇒ default/rejected/error/leased 四态含评审区 ⇒ 需重出 24 张（4×3×2），v0.3 已纠正原先只写 12 张的错。
- 线上只读核对（2026-11-23，`curl http://echuan:8720`）：`GET /v1/projects/materials?id=echuan-1` → 16 条（1 plan + 15 report）；`echuan-10` → 12 条（2 条 report 来自不同 dev 卡）；`echuan-13` → 16 条；`GET /v1/tasks/attachments?task_id=t_01M83XY4XGEYT1G8F8NMN5GG9N` → 4 条且带 `source_material_id`，`t_01M83YT9HS7FR9Y239P68DET9Q` → 2 条。
- 代码事实核对：`internal/store/v3/context.go:132`、`internal/store/v3/attachment.go:161,171-186`、`internal/store/query.go:110-116`（列映射跳过匿名字段 ⇒ 必须扁平结构）、`internal/api/v3_handlers.go:884-937`、`internal/app/v3/service.go:219-230`、`web/src/features/task-detail/use-task-detail.ts:180-194,219`、`ReviewPanel.tsx:45-81`、`projects/MaterialsSection.tsx:100`。
- 门禁基线实测：`node web/scripts/style-debt.mjs` → 6/6 达标（未引用类名 1/263 = 0.4%，断点 2 个取值）；`internal/web/dist/assets/*.js` 逐个 `gzip -9` 求和 = 184,107 B（阈值 344,056 B，占 53.5%）。
- 设计合并核对：`git log --oneline -1 origin/main` = `8c525f6 Merge pull request #149 …`；`git ls-tree -r origin/main -- design/refs` = 240 张（task-detail 36 张）；`git show origin/main:design/components.md` 中 `ui/` 组件仍 22 个、`grep "评审时选\|详情评审"` 只剩 `ui/TextArea` 一行（原因框确实用 TextArea）——即「契约已在主干且自洽」。
- 未执行（如实记录）：`make web`、`make build`、`make test`、`make contract`、`web/scripts/current-versions-check.mjs`（属开发卡产物）；设计 refs 已由设计阶段重出并合并，本方案卡自身未跑截图与浏览器——只做只读/静态分析。

**三、git 交付**

- 不适用：`design` 阶段卡不产生代码交付物；仓库全程只读（未新增/修改任何路径），无分支、无 commit、无 PR、无 compare 链接。
- 证据基线 commit：`8c525f62c8328809bb9a73d1fee7b9945c57542f`（代码引用按 `388f4a1186333ec79986bdc8b920b523a33526e7`）；方案正文建议入库路径（由开发卡随 PR 提交）：`docs/tech/current-version-only-148-v0.5.md`。设计资产已随 PR #149 合并，无单独设计卡。

**四、遗留风险**

- `design/` 增量已由设计阶段派单给 designer 执行并合并（PR #149 → `main@8c525f6`，commit 链 `9bdbcdb…→19f0879→95f1ea9→fba4f53`）。本方案卡自身只读仓库、不跑截图，故不直接产出设计资产；**当前不再需要设计卡**。
- 设计交付经三轮收紧（refs 由 12 张纠正为 24 张、`ui/SelectField` / `ui/TextField` 同栏用途收口、组件文档术语对齐）。共同教训：**改契约要按「口径」扫全文，不能只改被点名的那一处**；dev 卡若再发现同类残留，按变更单处理。
- `reviewTargetId` 与 `M3`（审计正确率）依赖客户端和服务端 kinds 口径一致，并假设 `sort_no` 恒 0（R11）；本期靠常量 + 夹具 + AC13 兜底，没有服务端强校验。
- 「缺 `task_id` 就不隐藏」是部署防线而非需求口径：混合部署期界面显示全量（与改前一致），不是缺陷。
- 与工作区里他人的 23 项已暂存改动（含 `v3_handlers.go`、`openapi.yaml`、`web/src/api/client.ts`）存在合并面重叠，开发卡需先 rebase 到最新 `main`（R8）。
- PRD 未决 U3（版本链落库）仍挂下期，本方案不依赖 `materials.version`。
