# 开发任务计划：项目列表倒序 + 状态筛选（issue #133）

## 1 范围与约束

- 目标：PRD `prd-issue-133-projects-sort-filter-v0.1.md` 的 G1（新建项目排最前）+ G2（按状态筛选），实现方式按返工结论 **排序与筛选都下沉到 `GET /v1/projects`**。
- 输入：PRD `prd-issue-133-projects-sort-filter-v0.1.md`（级别 m）；技术方案 `docs/tech/projects-sort-filter-133-v0.2.md`；契约决策 `docs/decisions/ADR-0002-projects-list-order-filter-api.md`；AC = PRD §6 AC1–AC10（AC 不变）。
- 团队与可用时间：单人 + Agent 维护（ADR-0001 C6）；本计划只给顺序、依赖与前置条件，不给日期与人日。
- 明确不做：UI 分页/「加载更多」；`total`/`has_more` 字段；新索引；`order` 参数或新端点；全局项目页/搜索/多条件筛选；本屏埋点；PRD 非目标 1/§9-1 的正文回写（PM 侧，见 §7）。

## 2 工作流划分

| 工作流 | 交付物 | 负责人 | 依赖 |
|---|---|---|---|
| 接口契约 | `GET /v1/projects` 倒序 + `product_id` 过滤 + `after_id` 方向翻转 + openapi + 单测/契约测试 | dev-backend | 无 |
| 消费面同步 | `kit` 的 `product_id` 透传与工具语义（agent 侧可见） | dev-backend | T1 |
| 规格口径 | 屏规格与 2 份 tech 文档的数据源/顺序口径 | dev-frontend（文档） | T1 |
| 前端呈现 | 项目列表屏接服务端筛选与倒序（子态、空态、控件可见性）+ 构建门禁 + 三视口核对 | dev-frontend | T1、T4 |

> 单一负责人原则：同一 owner 承担多张卡是允许的，一张卡不允许写两个 owner。

## 3 任务清单

### 任务 1：落地 `GET /v1/projects` 倒序与产品过滤

- 负责人：dev-backend
- 目标（可观察结果）：接口返回按新建倒序（最新在前），支持 `product_id` 过滤，`after_id` 变为「取更早一页」，首页不因缺省 `after_id` 而空。
- 范围：v3 项目列表的存储查询、应用层过滤器、HTTP handler 参数与 `next_after_id` 语义；`api/openapi.yaml`；store/app 单测。依据方案 v0.2 §4/§6/§7/§8.3 与 ADR-0002 §2/§4。
- 依赖：无（关键路径起点）。
- 任务级验收：
  - `go test ./... -race` 通过；新增用例覆盖：倒序、`product_id`（正常 / 空串视同未传 / 不存在→空集）、`after_id` 连续两页无重复无遗漏、`status`+`product_id` 组合。
  - `make contract` 通过（operation 集合不变）。
  - 起服务后：`?limit=3` 首条 = id 最大；`?product_id=echuan&status=open` 只含该产品 open 且新→旧；`?after_id=<上页末条>` 返回更早一页且与本页无重叠；`?product_id=<不存在>` → 200 + `items: []`（非 404）；不带参数（首页）返回非空。
  - openapi 的 `get /v1/projects` 出现 `product_id` 参数，且说明「newest first」与「`next_after_id` 取更早一页」。
- UI 设计引用：无
- 必须覆盖的状态：无
- 里程碑：M1 —— 契约变更落在主分支且上述命令与 curl 证据齐（store 与 handler 的 `product_id` 必须同一次提交，ADR-0002 R2）。

### 任务 2：接通项目列表屏的服务端筛选与倒序

- 负责人：dev-frontend
- 目标（可观察结果）：项目列表首行为最新项目；页头「按状态筛选」三选项默认「全部状态」，切换即生效；筛后为空保留控件与所选值并给表内提示；首屏加载/失败/产品无项目三态不出现控件。
- 范围：项目列表屏的取数参数与派生（改用服务端顺序与集合）、页头筛选控件、表体四态与空态分层、页头数字、抽屉按当前代反查、跨产品重置。依据方案 v0.2 §4/§8.2/§8.3。
- 依赖：T1（契约可用）、T4（`design_ref`）。
- 任务级验收：
  - `pnpm --dir web build` 通过（`tsc --noEmit` + vite + 体积门禁 ≤ 344,056 B）。
  - 在 `http://echuan:8720` 三视口（桌面 / 平板 / <768）核对 AC1、AC2、AC5、AC6、AC7、AC10：首行 `echuan-13`、末行 `echuan-1`、序号 `#13→#1`；三选项值「全部状态/进行中/已关闭」；「已关闭」6 条、「进行中」7 条与只读接口一致；筛后为空时控件仍在、数字 0；三态页头无控件。
  - 网络面板或服务端日志可见：默认态请求带 `product_id`、不带 `status`；筛选态带 `product_id` + `status`（切一次筛选 = 一次请求，同一时刻不叠发）。
  - 快速连续切换两次筛选：表格终态与所选值一致（旧代响应不串）。
- UI 设计引用：design_ref = T4 完成后的提交 sha（`design/screens/projects.md` 含页头筛选与表格顺序口径）
- 必须覆盖的状态：projects: default / empty（产品下无项目）/ 筛后为空 / loading / error；三视口 desktop / tablet / <768
- 里程碑：M3 —— 屏上 AC1–AC10 的抽样证据齐，构建门禁输出贴在交付报告里。

### 任务 3：同步 kit 消费面（`product_id` 透传 + 工具语义）

- 负责人：dev-backend
- 目标（可观察结果）：agent 侧 `echuan_project_list` 支持 `product_id` 过滤，返回顺序与新语义一致，工具描述写明「按新建倒序、`after_id` 取更早一页」。
- 范围：`kit` 的 v3 客户端方法入参与透传、工具 schema 与描述、类型声明、相关工具测试。依据 ADR-0002 §5 与方案 v0.2 §4。
- 依赖：T1。
- 任务级验收：
  - `pnpm --dir kit verify` 通过（typecheck + vitest）。
  - 工具 schema 断言含 `product_id`；描述文本含「倒序/更早一页」语义。
  - 对测试平台实际调用一次：`echuan_project_list`（带 `product_id=echuan`）返回新→旧，与 `curl` 同参数结果顺序一致。
- UI 设计引用：无
- 必须覆盖的状态：无
- 里程碑：M3 —— 与 T2 同批可验收（agent 侧与屏侧口径一致）。

### 任务 4：更新屏规格与数据源口径文档

- 负责人：dev-frontend
- 目标（可观察结果）：后来者按文档就能知道「接口已倒序、筛选在服务端」；屏规格的页头结构与状态矩阵含筛选与筛后为空。
- 范围：`design/screens/projects.md`（页头结构、状态矩阵、数据来源、表格顺序）；`docs/tech/web-ui-redo-v0.1.md` §7 与 `docs/tech/web-ui-102-interaction-fix-v0.1.md` 数据源表的相关行。依据方案 v0.2 §4。
- 依赖：T1（口径以已落地行为为准）。
- 任务级验收：
  - 屏规格文案含：页头「按状态筛选」、状态矩阵新增「筛后为空」行、「表格按接口返回顺序（`id DESC`）呈现、序号随之递减」。
  - 两份 tech 文档的数据源行改为「服务端支持 `product_id`；顺序 `id DESC`」；全仓 `grep -rn "不支持 .*product_id" docs/` 无残留；`grep -rn "ORDER BY p.id ASC" docs/` 无残留表述。
  - 不重出 refs 截图（由 design 决定，见 §7）。
- UI 设计引用：无（本卡只改文字口径）
- 必须覆盖的状态：无
- 里程碑：M2 —— 文档口径与已落地接口一致，T2 可用其作 `design_ref`。

## 4 依赖 DAG

```text
T1 ──→ T4 ──→ T2
 └──→ T3
```

（T3 与 T4 可并行；T2 待 T4 给出 `design_ref` 后开工。无环。）

## 5 关键路径与缓冲

- 关键路径：T1 → T4 → T2。
- 缓冲：T2 是唯一有 UI 三视口核对与构建门禁的卡，任何口径返工都发生在 T4 之前 —— 若 T4 发现文档与实现不一致，先改文档再开 T2，不把口径问题带进实现卡。

## 6 集成点与交接

| 集成点 | 上游 | 下游 | 负责人 | 时间 |
|---|---|---|---|---|
| 契约握手（参数、顺序、`next_after_id` 语义） | T1 | T2、T3、T4 | dev-backend → dev-frontend | T1 完成即交接，M1 为准 |
| 部署窗口（后端先行；前端保留防御性 `product_id` 过滤） | T1 | T2 | dev-backend → dev-frontend | T2 开工前后端已在测试平台可用 |
| 规格口径（`design_ref`） | T4 | T2 | dev-frontend（同 owner，需在卡上写 sha） | M2 之后 |

## 7 风险与未决

| 风险 / 未决 | 影响 | 负责人 | 截止 | 默认动作 |
|---|---|---|---|---|
| `after_id`/`next_after_id` 语义翻转被外部 agent 依赖 | 翻页方向静默变化 | 需求方 / 运维（U3） | 发布前 | 无消费方清单，按「有」对待：发布说明写明 |
| 中间态部署（新前端 + 只翻 DESC 未加 `product_id` 的后端） | 串产品或空列表 | dev-backend（T1 同批提交） | T1 内 | store 与 handler 同一提交；前端保留防御性过滤 |
| 单产品项目 > 50 条缺最旧 | 列表与数字偏小（自洽） | CTO + 产品（U2） | 验收后一周 | 登记已知局限，不做分页 |
| PRD 非目标 1 / §9-1 与新结论冲突 | 文档口径打架 | PM（U0） | 方案确认后 | 以方案 v0.2 + ADR-0002 为准，PRD 待回写 |
| 筛后空态是否加「清除筛选」按钮 | 影响 T2 空态形态 | design（U5） | T2 开工前 | 纯提示文案，不加按钮 |
| refs 截图是否重出（顺序变化后旧图与屏不一致） | 视觉回归基线过期 | design（U7） | 与实现同批 | 由 design 决定，T4 只改文字 |
| 是否补索引 / 埋点 | 性能与收益量化 | CTO（U1/U4） | 观测触发 | 不加不加 |

## 8 自检（10 项判据）

1 粒度：按部署面（后端 / 屏 / kit / 文档）切，非按文件或行数 —— 通过（split_reason 见 §9）。
2 责任：每卡恰好一个 owner —— 通过。
3 依赖：DAG 无环且每个依赖写方向 —— 通过（§4）。
4 并行：T3 与 T4 不写同一批文件、不争同一接口归属 —— 通过。
5 关键路径：T1→T4→T2，缓冲说明见 §5 —— 通过。
6 里程碑：M1/M2/M3 均为可检查结果（命令 + curl + 文档 grep）—— 通过。
7 任务级验收：每条是命令或可观察结果，不看代码可验 —— 通过。
8 范围：覆盖 PRD G1/G2 与方案 v0.2 全部改动点；未覆盖项（PRD 回写、refs、分页、埋点）已列 §7 —— 通过。
9 UI 交付引用：T2 有 `design_ref`（T4 的 sha）与状态清单（default/empty/筛后为空/loading/error × 三视口）—— 通过。
10 Right-size：无「收口/回归」独立卡；>1 张 dev 卡均有 split_reason —— 通过（§9）。

## 9 split_reason（>1 张 dev 卡的合并测试结果）

| 卡 | 能否与其它卡同一次验收 | 结论 | split_reason |
|---|---|---|---|
| T1 接口契约 | 与 T2 不能：验收是 `go test`/`make contract`/`curl`，T2 是三视口 UI | 保留 | 独立部署面 + 冲突的验收标准 |
| T2 前端呈现 | 与 T1 见上；与 T4 不能：T4 的验收是文档口径 grep | 保留 | 独立部署面（web 产物）+ 不同验收标准 |
| T3 kit 消费面 | 与 T1 可同一命令集验，但交付物落在另一个包（agent 侧工具面），失败面不同 | 保留 | 独立发布面（kit 包）+ 独立验收（`pnpm --dir kit verify`） |
| T4 规格口径 | 与 T1/T2 的文件面与验收都不同；且它是 T2 的 `design_ref` 前置 | 保留 | 独立交付物（规格层）+ 验收标准冲突（文档可读性 vs 代码行为） |
