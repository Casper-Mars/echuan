# 技术方案：Web UI V3 四项交互回归修复（issue #102）v0.1

| 项 | 值 |
|---|---|
| 关联 issue | [#102 前端有几个bug](https://github.com/Casper-Mars/echuan/issues/102)（open，label `bug`） |
| 项目 | `echuan-2`（Web UI V3 交互回归修复）／分支 `feature/echuan-2`；本卡分支 `task/t_01M7Z6XP5YABQK9QWXW984875N` |
| 代码基线 | `main@274213004777e0982b516788e055c7415966c592`（`git fetch --prune origin` 后核对；本文所有「文件:行」以该 commit 为准） |
| 上游方案 | `docs/tech/web-ui-redo-v0.1.md`（上一轮 Web UI 重做技术方案）、`docs/decisions/ADR-0001-web-ui-stack.md` |
| 界面契约 | `design/`（v0.3：`tokens.json` / `components.md` / `flows.md` / 8 屏规格 / `refs/` 240 张 / `MANIFEST.sha256`） |
| 本卡级别 | `m`（11 章技术方案） |
| 交付物 | 本文档（唯一交付物；不改代码、不拆卡、不开 PR） |

**一句话结论**：四条回归全部是**前端结构缺失**，后端与 `/v1` 契约零改动即可闭环——抽屉用既有 `ui/DetailSheet`（`ui/DataTable` 已有 `onRowClick`，只是没人用）、关闭/重开接既有 `POST /v1/projects/close|reopen`（`api/client.ts` 已封装、零调用方）、侧栏与 TabBar 同步去掉「看板」、面包屑由壳层按项目反查补产品段（承认并推翻 issue #89 B2 的「面包屑不为名字新增请求」旧口径，代价限定为每次进入项目 1–2 次请求且有会话内缓存）。

---

## 1 目标与非目标

### 1.1 目标

在 `feature/echuan-2` 上以**纯前端改动**闭环 issue #102 的四条现象，并同时把 `design/` 契约、门禁与观测口径交代清楚，使后续拆卡者不需要回头猜口径。

1. `#/products` 与 `#/products/:productId/projects` 的行点击可打开**只读详情抽屉**（产品字段 6 项 / 项目字段 9 项）。
2. 项目列表提供**关闭 / 重开**入口，按后端既有 409 + `force=1` 语义落地，不新增错误码。
3. 侧栏（桌面/平板）与移动 TabBar 去掉「看板」一级入口，连带口径（`#/`、高亮、`localStorage` 记忆、入口可达性）一并定死。
4. 看板面包屑补上「项目列表」段并按名显示，使「看板 → 项目列表 → 产品」可回退。

### 1.2 非目标（明确不做，避免范围蔓延）

| # | 不做 | 理由 |
|---|---|---|
| N1 | 后端任何改动（`internal/**`、`migrations/**`、`/v1` 契约） | issue 所需能力已全部存在（§5） |
| N2 | 产品/项目的**编辑、删除**、以及「新建产品 / 新建项目」的真实创建流程 | 现状是占位按钮（`ProductsPage.tsx:43`、`ProjectsPage.tsx:38`），创建流程不在 issue #102 范围；占位按钮的误导性登记为未决项 U-03 |
| N3 | 任务详情、附件预览、收件箱、队列、执行节点各屏的结构与交互 | 与本 issue 无关 |
| N4 | 抽屉的可分享 URL（深链） | 任务详情需要 URL 是因为它是主路径；产品/项目抽屉是「看清字段」，用局部 state 足够（§7.2 选项对比，退出条件已写） |
| N5 | 「全局只读」体系（关闭项目后禁用评审/删除/重置等所有写操作） | 后端只在 `createTask` 拦 `ErrProjectClosed`（`internal/store/v3/task.go:56-58`），前端单方面拦截＝假拦截（§7.4） |
| N6 | 新增 UI 组件、新增依赖、新增路由 | §7.3、§8；ADR-0001 锁版本 |
| N7 | `design/refs/` 重生成 | 登记为偏差，责任人与补的时点见 §7.9 |
| N8 | 远程错误上报、性能埋点 | 平台无上报端点（`docs/tech/web-ui-redo-v0.1.md:163`） |
| N9 | 任务拆解、排期、人日、提交顺序 | 拆卡是下一张卡的事 |

---

## 2 AC 覆盖

### 2.1 四条 issue 逐条闭环

> 「现象」来自项目上下文「实测复现」表（2026-11-21 实测）；「源码证据」逐条在本卡重新核对到行；「通过判据」全部是**浏览器可观察**的，dev 卡与评审照此验。

**AC-1 详情抽屉（issue 第 1 条）**

| 项 | 内容 |
|---|---|
| 现象 | `#/products`、`#/products/echuan/projects` 点击表格行：`location.hash` 不变、无任何 sheet 出现 |
| 源码证据 | ① 两页的 `ui/DataTable` 调用都没传 `onRowClick`：`web/src/features/products/ProductsPage.tsx:180-188`、`web/src/features/projects/ProjectsPage.tsx:152-157`；② `ui/DataTable` **已支持**行点击（`web/src/ui/DataTable.tsx:36`、`:97-109`：`onClick` + `tabIndex=0` + Enter/Space），只是零调用方；③ 两页操作列只有下钻按钮：`ProductsPage.tsx:131-136`（查看项目）、`ProjectsPage.tsx:110-115`（打开看板）；④ `grep -rn DetailSheet web/src/features/{products,projects}` 为空 |
| 方案 | §7.2（D1）：行点击开只读抽屉，行内下钻按钮保留并阻断冒泡；抽屉复用 `ui/DetailSheet`，不加 ui 导出面（D2） |
| 通过判据 | 见 §2.2 P1 |
| 不做 | 无（全做） |

**AC-2 项目关闭/重开（issue 第 2 条）**

| 项 | 内容 |
|---|---|
| 现象 | 项目列表行操作只有「打开看板」，看板页头只有「新建任务」，全仓无关闭入口 |
| 源码证据 | ① `web/src/api/client.ts:64-67` 定义 `closeProject(id, force)` / `reopenProject(id)`，`grep -rn "closeProject\|reopenProject" web/src` 只有这两行定义 = **零调用方**；② `ProjectsPage.tsx:110-115` 操作列只有「打开看板」；③ `design/screens/projects.md:21` 当前登记「不做：关闭/重开项目」——本方案改契约（§7.9） |
| 方案 | §7.4（D3 入口与只读口径）+ §7.5（D4 409 交互）+ §6 流程 |
| 通过判据 | 见 §2.2 P2 |
| 不做 | 无（全做） |

**AC-3 侧栏「看板」入口下线（issue 第 3 条）**

| 项 | 内容 |
|---|---|
| 现象 | 侧栏四项：看板 / 收件箱 / 产品 / 执行节点 |
| 源码证据 | ① `web/src/features/shell/nav.tsx:95-100`（`SIDEBAR_NAV` 含 `board`）、`:103-108`（`TAB_NAV` 第 1 项也是 `board`）、`:116-121`（`activeKeyOf('/projects/*')` 返回 `board`）；② `AppFrame.tsx:85-91`（`go('board')` → `boardPath`）、`:109-132`（侧栏渲染）、`:180-182`（TabBar 渲染） |
| 方案 | §7.6（D5）：侧栏与移动 TabBar **同步去掉**看板项；`#/` 仍跳上次看板；`/projects/*` 高亮落到「产品」；`localStorage['echuan:last-project']` 保留 |
| 通过判据 | 见 §2.2 P3 |
| 不做 | 「更多」里不补「上次看板」入口（D5 明确列为不做，理由见该节） |

**AC-4 面包屑回项目列表（issue 第 4 条）**

| 项 | 内容 |
|---|---|
| 现象 | 看板面包屑 `产品 / echuan-1`：仅「产品」可点（→ `#/products`），无项目列表段，且显示 `project_id` 而非项目名 |
| 源码证据 | `web/src/features/shell/AppFrame.tsx:43-61`（`crumbsOf` 只从 URL 段推导）：`:53-58` 的 `/projects` 分支产出 `产品 / <pid> / [<tid>]`，**不产出项目列表链接、不取名字**；`:38-42` 的注释是本轮要推翻的旧口径（「只从 URL 段推导，不额外请求接口」＝ issue #89 B2） |
| 方案 | §7.7（D6）：壳层反查候选 A（`GET /v1/projects?id=` + `GET /v1/products?id=`）+ 会话内缓存 + 失败降级；显式推翻 B2 并写理由 |
| 通过判据 | 见 §2.2 P4 |
| 不做 | `/products/:productId/projects` 段改显示产品名（D6 明确不做：会与页面重复请求，且不是 #102-4 的目标；登记未决 U-05） |

### 2.2 可观察通过判据（dev 卡 / 评审照此验）

**P1 抽屉**

1. `#/products`：点第 1 行**非按钮**单元格 → `[role="dialog"]` 出现，含产品名，`location.hash` 仍为 `#/products`；抽屉内含 6 个字段标签（产品编号/名称/仓库/描述/证书/项目数）。
2. `#/products/echuan/projects`：点行 → 抽屉出现，含 9 个字段标签（项目编号/产品编号/序号/名称/上下文/状态/创建时间/更新时间/关闭时间）。
3. 点行内「查看项目」→ hash 变 `#/products/<pid>/projects` 且**不出**抽屉；点行内「打开看板」→ hash 变 `#/projects/<pid>/tasks` 且**不出**抽屉（一次点击只有一个结果）。
4. 三视口：1440×900 / 1024×768 抽屉 content 宽 720（`--ui-layout-sheet`）；390×844 全屏（宽 = 视口宽，无左右留白），关闭键命中区 ≥44×44。
5. 键盘：Tab 到行 → Enter 打开；Esc 关闭；关闭后列表不被重挂（行数与滚动位置不变）。

**P2 关闭/重开**

1. `open` 项目行出现「关闭」；点击 → `[role="dialog"]` 确认弹窗；确认后该行状态变「已关闭」，按钮变「重开」。
2. 存在未完成任务时：第一次 `close` 返回 409 → 弹窗第二次确认，文案含服务端给出的 N；确认后（`force=1`）该项目 7 列看板里 `pending/ready/in_progress/in_review` 全部落到 `已取消` 列，且行状态为「已关闭」。
3. `closed` 项目点「重开」→ 行状态回「进行中」；此前被取消的任务**仍在**「已取消」列（不复活）。
4. 幂等：对已 `closed` 项目再调 `close` → 204（`internal/store/v3/project.go:115-117` 提前返回）；对已 `open` 项目调 `reopen` → 204（`project.go:152-176`：`RowsAffected=0` 时先确认项目存在再返回 nil）。
5. 看板页（`closed` 项目）：页头出现「已关闭」标识，「新建任务」为 disabled 且不可聚焦点击；空态不再提供「新建任务」动作。

**P3 侧栏**

1. ≥768：侧栏 3 项 `收件箱 / 产品 / 执行节点`，无「看板」；平板 collapsed 仍是 64 图标栏（令牌不变）。
2. 在 `#/projects/x/tasks` 上侧栏「产品」为当前项（`aria-current`），「收件箱」「执行节点」不高亮。
3. <768：TabBar 3 项 `收件箱 / 产品 / 更多`，无「看板」；「更多」二级层仍含「执行节点」与「派单队列」。
4. `#/`：有 `localStorage['echuan:last-project']` → 该项目看板；无记录 → `#/products`（行为不变）。

**P4 面包屑**

1. `#/projects/x/tasks`：`产品`（→ `#/products`） / `<产品名>`（→ `#/products/<productId>/projects`） / `<项目名>`（当前，不可点）；3 段。
2. `#/projects/x/tasks/<tid>`：4 段，末段仍是 task id。
3. 断网或 `GET /v1/projects?id=` 失败：降级为 `产品 / <project_id>`（与今天一致），无链接、无错误条、壳层不塌。
4. 项目取到但产品取失败：`产品 / <productId>`（该段仍链到 `#/products/<productId>/projects`） / `<项目名>`。
5. 请求预算：首次进入某项目的路由 → 网络面板新增 ≤2 个 `GET`（`/v1/projects?id=`、`/v1/products?id=`）；同一会话再进同一项目 → 0 个；再进同产品下另一个项目 → 1 个。

---

## 3 现状与改动点

### 3.1 现状（只引证据，不写「疑似」）

| # | 现状 | 源码位置 | 关键结论 |
|---|---|---|---|
| S1 | 产品/项目列表的行点击无行为；`ui/DataTable` 的 `onRowClick` 零调用方 | `ProductsPage.tsx:180-188`、`ProjectsPage.tsx:152-157`、`ui/DataTable.tsx:36,93-110` | 能力已有，缺接线；行点击是「死点击」 |
| S2 | 页面无 `ui/DetailSheet` 引用 | `grep -rn DetailSheet web/src/features/{products,projects}` 为空 | 抽屉从零接，但容器不需要重写（`ui/DetailSheet.tsx:27-83` 已提供 720 / 移动全屏 / 遮罩 0.35） |
| S3 | 关闭/重开 API 已封装、零调用方 | `api/client.ts:64-67` | 前端只缺入口、确认与错误转化 |
| S4 | 项目状态渲染是「借用」任务状态色 | `ProjectsPage.tsx:94-99`（`open`→`in_progress`/「进行中」，否则 `cancelled`/「已关闭」） | 本方案沿用这套映射，不新增 `StatusPill` 变体（否则要动 ui 导出面与设计契约） |
| S5 | 侧栏与 TabBar 各 4 项，第 1 项都是「看板」 | `nav.tsx:95-108` | 两形态同源（`nav.tsx:1-10` 注释），必须同步取舍（D5） |
| S6 | `/projects/*` 高亮返回 `board` | `nav.tsx:116-121` | 去掉 board 项后高亮会「无所指」，D5 一并定 |
| S7 | 面包屑只从 URL 推导，`/projects/*` 无项目列表段、显示 ID | `AppFrame.tsx:43-61`（`:53-58`） | 需要 `product_id` + 两个名字，路由里都没有（D6） |
| S8 | `#/` 与侧栏「看板」共用 `boardPath()`（读 `localStorage['echuan:last-project']`） | `home/last-project.ts:35-43`、`HomeRoute.tsx:9`、`AppFrame.tsx:85-91`、`InboxPage.tsx:151`、`QueuePage.tsx:96`、`AppFrame.tsx:169-177`（`ErrorBoundary.onExit`） | `boardPath` 有 5 个消费者，去掉侧栏项**不能**顺手动这条链路 |
| S9 | closed 项目上看板不标识、`新建任务` 可用 | `BoardPage.tsx:231`（页头按钮）、`:268-275`（空态动作）；后端 `task.go:56-58` 返回 409 | 用户会走完整个创建表单才撞错误条，且 `CreateTaskSheet.tsx:61` 把英文内部文案原样显示 |
| S10 | 面包屑末段渲染规则：`to` 且非末段才是 `Link` | `AppFrame.tsx:146-153` | 新增段必须遵守「末段不可点」的既有约束 |
| S11 | 页面头的「新建产品 / 新建项目」是占位（不发请求） | `ProductsPage.tsx:43,156`、`ProjectsPage.tsx:38,128` | 本卡不动，登记 U-03 |

### 3.2 改动点（文件级影响面；不含实现代码）

| 文件 | 改动性质 | 关联 |
|---|---|---|
| `web/src/features/products/ProductsPage.tsx` | 接 `onRowClick`；操作列按钮阻断冒泡；抽屉挂载与状态 | AC-1 |
| `web/src/features/products/ProductDetailSheet.tsx` | **新增**（产品只读抽屉，页面私有） | AC-1 / D2 |
| `web/src/features/projects/ProjectsPage.tsx` | 接 `onRowClick`；操作列加「关闭 / 重开」；确认弹窗与 409 二次确认；写后 `refresh('write')` | AC-1 / AC-2 |
| `web/src/features/projects/ProjectDetailSheet.tsx` | **新增**（项目只读抽屉，页面私有） | AC-1 / D2 |
| `web/src/features/projects/close-project.ts` | **新增**（关闭/重开的调用编排：409 判定、N 解析、文案收敛；不掺 UI） | AC-2 / D4 |
| `web/src/features/shell/DetailFields.tsx` | **新增**（键值网格，两个抽屉共用；与 `ErrorBar` 同层，不走 ui 导出面） | D2 |
| `web/src/features/shell/nav.tsx` | 删除 `SIDEBAR_NAV`/`TAB_NAV` 的 `board` 项与 `BoardIcon`；`NavKey` 去掉 `board`；`activeKeyOf('/projects/*')` → `products` | AC-3 / D5 |
| `web/src/features/shell/AppFrame.tsx` | `crumbsOf` 接名字与项目列表段；`go()` 删 `board` 分支；侧栏/TabBar 项数变化（渲染逻辑不改） | AC-3 / AC-4 / D6 |
| `web/src/features/shell/use-crumb-names.ts` | **新增**（项目/产品名反查 + 会话内缓存 + 静默降级） | AC-4 / D6 |
| `web/src/features/board/BoardPage.tsx` | closed 时页头加「已关闭」标识、`新建任务` 禁用；空态动作为「返回项目列表」 | AC-2 / D3 |
| `web/src/features/board/CreateTaskSheet.tsx` | 把 `ErrProjectClosed` 的英文内部文案收敛成中文错误条（`detail` 留原文） | AC-2 / §9 |
| `web/src/features/{products,projects,board}/*.css`、`web/src/features/shell/shell.css` | 抽屉字段网格、行内动作组、页头标识的几何与配色（只消费令牌） | 全部 |
| **不改** | `web/src/api/client.ts`（`closeProject`/`reopenProject` 已就绪）、`web/src/api/types.ts`（字段齐全）、`web/src/ui/**`（无新导出，21 个组件不变）、`internal/**`、`migrations/**`、`design/**`（本卡不改，清单见 §7.9）、`internal/web/dist`（收口卡统一重生成） | — |

---

## 4 数据模型

**本期无数据模型变更**：不新增表、不改字段、不写迁移，`migrations/` 零改动，回滚不涉及数据。

前端只**消费**既有结构（`web/src/api/types.ts` 已定义，无需改动）：

| 类型 | 用到字段 | 来源 |
|---|---|---|
| `Product` | `product_id` `name` `git_url` `description` `license` `project_count?` | `GET /v1/products`（`internal/api/products.go:58-68`：响应另含 `create_time`/`update_time`，本方案**不展示**，见 §7.2 字段表注） |
| `Project` | `project_id` `product_id` `seq_no` `name` `context_md` `status`(`open`/`closed`) `create_time` `update_time` `closed_at?` | `GET /v1/projects`（`internal/api/v3_handlers.go:42-52`） |
| `Task` | `status`（判 7 列与「已取消」列） | `GET /v1/tasks?project_id=` |

写操作只改 `projects.status` / `projects.closed_at` 与 `tasks.status`，**全部由后端在既有事务内完成**（`internal/store/v3/project.go:102-146`、`:192-231`）；前端不直接拼任何 DDL/DML。

---

## 5 接口契约

**不新增接口、不改契约**。前端消费面（签名与错误语义均以 `2742130` 源码为准）：

| 端点 | 用途 | 现状语义（硬依赖） | 本期变化 |
|---|---|---|---|
| `GET /v1/products` | 产品列表 | `productsHandler.listProducts`；`project_count` 可能缺省 | 无 |
| `GET /v1/products?id=` | 壳层面包屑取产品名（抽屉不取数） | `products.go:104-129`；不存在 → 404 `not_found` | 新消费（面包屑） |
| `GET /v1/projects` | 项目列表 | 支持 `status`/`after_id`/`limit`；**issue #133 起服务端支持 `product_id` 过滤**（业务 id，空串视同未传），顺序 `id DESC`（新建在前），`after_id` 取更早一页（`docs/tech/projects-sort-filter-133-v0.2.md`、ADR-0002）；前端改为传参，本地只保留 1 行防御性 `product_id` 再过滤（ADR-0002 §4 C3） | 无（本期）；上述 `product_id`/顺序/游标语义为后续变更 |
| `GET /v1/projects?id=` | 壳层面包屑取项目（含 `product_id`；抽屉不取数） | `v3_handlers.go:223-233`；不存在 → 404 `not_found` | 新消费（面包屑） |
| `POST /v1/projects/close?id=&force=1` | 关闭项目 | `v3_handlers.go:291-304`；成功 **204 无 body** | 新消费 |
| `POST /v1/projects/reopen?id=` | 重开项目 | `v3_handlers.go:306-317`；成功 **204** | 新消费 |
| `POST /v1/tasks` | 新建任务 | closed 项目 → **409** `conflict`，message `v3: project is closed: <pid>`（`store/v3/task.go:56-58`） | 前端补拦截 + 文案收敛 |
| `GET /v1/tasks?project_id=&limit=300` | 看板 | 不变 | 无 |

### 5.1 关闭项目的错误与幂等（照后端现状写，不假设新错误码）

- `409 conflict` 的两种来源，前端必须区分（靠 message 子串，不靠 code——两者都是 `conflict`）：
  - **未完成任务**：message `v3: project still has unfinished tasks: N unfinished`（`store/v3/models.go:23` + `project.go:128`），`force=1` 后在同一事务内取消 `pending/ready/in_progress/in_review` 任务并把 `lease_id/lease_until/worker_id` 清空（`project.go:192-231`）。
  - **其它 409**（message 不含 `unfinished`，例如后端文案变更、中间层返回的冲突）：本方案**不转 force**，直接错误条（不猜语义）。
- `404 not_found`：项目不存在（`project.go:108-113`）。
- 幂等：`closed` 项目再 `close` → 提前返回 nil → **204**；`open` 项目再 `reopen` → `RowsAffected=0` 且项目存在 → nil → **204**。前端因此可以「重试不心虚」，但**仍应在成功后就地刷新**，不靠幂等掩盖竞态。
- 响应体：统一 `{code, message, details, request_id}`（`internal/api/errors.go:97-120`），前端 `ApiError(status, message)`（`api/client.ts:14-38`，`message` 取 `body.message`）。

### 5.2 请求预算（新增的取数只有一处）

| 场景 | 新增请求 | 说明 |
|---|---|---|
| 打开产品/项目抽屉 | **0** | 抽屉字段全部来自当前列表行（行数据在 15s 自动刷新内保持新鲜） |
| 关闭 / 重开 | 1 次写 + 1 次列表补拉（`refresh('write')`） | 复用 `useAutoRefresh` 的写后补拉语义 |
| 面包屑（D6 选定 A） | 首次进某项目 ≤2（project + product），会话内缓存命中 → 0 | 见 §7.7 |

---

## 6 关键流程

### 6.1 主路径

**F-1 打开只读抽屉**

1. 用户在产品/项目列表点行（或键盘 Enter/Space，`ui/DataTable.tsx:99-108` 已支持）。
2. 页面把该行 `id` 写进本地 state（`openProductId` / `openProjectId`），并 `DetailSheet opened={id !== null}`。
3. 抽屉内容**按 id 从当前 `rows` 派生**（不复制对象）→ 自动刷新一轮后抽屉内容跟着更新；若该行被筛掉（如关闭后被状态筛选过滤），抽屉自动关闭。
4. 关闭：× / Esc / 遮罩（`ui/DetailSheet` 内建），不发请求。

**F-2 关闭项目**

1. open 行的「关闭」→ `ui/ConfirmModal`（`tone="danger"`）：标题「关闭项目 <name>？」，影响面「关闭后不能新建任务；未完成任务会被取消且**不会**随重开恢复；可随时重开」。
2. 确认 → `POST /v1/projects/close?id=`（不带 `force`）。
   - `204` → 关闭弹窗 + `refresh('write')`；行状态变「已关闭」、按钮变「重开」。
   - `409` 且 message 命中 `/unfinished:\s*(\d+)\s+unfinished/` → **同一个弹窗**切第二态（不叠弹窗）：标题「还有 N 个未完成任务」，按钮文案「关闭并取消 N 个任务」（danger），再次确认 → `POST /v1/projects/close?id=&force=1`。
   - 其它错误 → 关弹窗 + `ErrorBar`（§9 文案表）。

**F-3 重开项目**

1. closed 行「重开」→ 一次确认（标题「重开项目 <name>？」、影响面「已取消的任务不会恢复」）→ `POST /v1/projects/reopen?id=`。
2. `204` → `refresh('write')`；行状态回「进行中」；此前被取消的任务仍在 `已取消` 列（`project.go:147-148` 注释：reopen 不是时间机器）。

### 6.2 失败路径（逐条给处理与最终状态）

| # | 场景 | 检测方式 | 处理 | 最终状态 |
|---|---|---|---|---|
| E1 | 抽屉取数失败 | 不适用（0 请求）；列表失败由既有 `Alert` 兜底（`ProductsPage.tsx:161-164`、`ProjectsPage.tsx:133-136`） | 列表错误条 + 重试；不提供抽屉入口的行（数据都没到） | 页面停在错误态，抽屉不可开 |
| E2 | 关闭返回 409（有未完成任务） | `ApiError.status===409` + message 含 `still has unfinished tasks` | 弹窗切第二态（D4），N 解析失败则文案退化为「仍有未完成任务」 | 未写任何数据；用户可选取消或 force |
| E3 | force 后部分任务被取消 | `204` 后 `refresh('write')` 的列表结果 | 行状态变「已关闭」；看板 `已取消` 列增加；每个被取消任务有 `task_events(event=cancelled, detail={"reason":"project closed"})`（§9） | 项目 closed，相关任务 cancelled，租约被清空 |
| E4 | force 后 worker 仍在跑该任务 | 不检测（前端不可见） | 前端不处理；worker 侧下一次 `report`/`lease` 会失败（租约已被清空），需人工重排队 | 任务 cancelled，workspace 保留；登记风险 R-04 |
| E5 | reopen 后任务仍是 cancelled | `204` 后看板/列表刷新结果 | 不改任务；空态/列里如实呈现「已取消」；不提供「复活」动作 | 项目 open，历史任务保持 cancelled |
| E6 | 面包屑反查失败（项目 404 / 请求失败 / 断网） | 壳层 hook 的 promise reject | 静默降级：`产品 / <project_id>`（与今天一致），不弹错误条、不阻塞页面 | 壳层与页面照常可用 |
| E7 | 项目取到、产品取失败 | 同上（第二段 reject） | 中间段标 `<productId>`（mono），**链接仍指向** `#/products/<productId>/projects` | 结构与链接完整，只是名字退化为 ID |
| E8 | closed 项目上点「新建任务」 | `project.status === 'closed'` | 按钮 disabled（`BoardPage.tsx` 页头与空态两处）；空态动作为「返回项目列表」 | 零请求、零错误条 |
| E9 | `project` 尚未取到就点了「新建任务」（含取数失败） | `project === null` | **不禁用**（保守：不知道状态就不做假拦截）；若后端 409 | 请求到达后端 → 409 → `CreateTaskSheet` 错误条显示中文「项目已关闭」，`detail` 保留原文 |
| E10 | 两个标签页并发：A 关闭、B 也在关闭 | 第二次返回 409 或 204 | 409（未完成任务已被 A 的 force 取消）/ 204（幂等）都按成功路径收敛；列表补拉后状态以服务端为准 | 不出现「错误条 + 实际已关闭」的矛盾态 |
| E11 | 关闭成功但列表补拉失败 | `refresh('write')` 失败 → `useAutoRefresh` 保留上次数据 + `error` | 页面显示既有错误条「看板/列表刷新失败，保留上次数据」；用户可手动重试 | 视觉上可能仍显示 open，但服务端已 closed；重试即自愈 |
| E12 | 侧栏去掉看板后，用户仍从旧书签进 `#/projects/:pid/tasks` | 路由不变（`App.tsx` 不改） | 正常打开看板；侧栏「产品」高亮（D5） | 深链/旧收藏全部有效 |

---

## 7 前端形态与决策点

### 7.1 三形态几何（本方案不新增几何）

| 形态 | 抽屉 | 行内动作 | 侧栏 / TabBar |
|---|---|---|---|
| ≥1280 桌面 1440×900 | 右侧 720（`--ui-layout-sheet`，`base.css:57`），遮罩 0.35 无模糊（`ui/DetailSheet.tsx:44-48`） | 行内按钮 `sm`（32 高，鼠标口径） | 侧栏 240 有文字，3 项 |
| 768–1279 平板 1024×768 | 同 720 抽屉（`ui/DetailSheet` 无平板差异） | 同上 | 侧栏收 64 图标栏（文字进 Tooltip），3 项 |
| <768 移动 390×844 | **全屏 sheet**（`ui/ui.css:622-651`：`--drawer-size:100%`、关闭区 ≥44、safe-area） | 卡片式行（`data-label`），按钮命中区 ≥44（`ui/ui.css:977-985` 已兜底，无需新 CSS） | TabBar 3 项，总高 57（`ui/ui.css:677-683`），**新增/删除项不改变总高** |

### 7.2 D1 详情抽屉的入口与内容

| 选项 | 代价 | 退出条件 |
|---|---|---|
| **A（选定）行点击 = 开只读抽屉；行内下钻按钮 = 进下一层（按钮阻断冒泡）** | 需要给两个列表接 `onRowClick` + 行内按钮 `stopPropagation`；移动端「点卡片」= 看字段而不是进下一层，与 V2 的「卡片点击进项目列表」相反（V2 证据：`archive/v1-main-20261104:web/src/pages/ProductsPage.tsx` 卡片 `onClick` 是 `navigate(.../projects)`，抽屉由行内「详情」按钮触发） | 若验收要求「与 V2 完全一致（行点击下钻 + 详情按钮开抽屉）」，改回 B，一处接线即可 |
| B 行点击仍进下一层，新增「详情」按钮开抽屉（= V2 行为） | 每行再加一枚按钮：产品页 2 枚、项目页 3 枚，移动端卡片动作区拥挤；且 issue 第 1 条的诉求（点卡片出详情）无法满足 | 若 issue 方明确说「点卡片应进下一层」→ 采用 B |
| C 维持现状（行不可点，无抽屉） | 零成本 | 但 issue #102-1 未闭环，不能选 |
| 最小改动变体 A′ 与 A 相同但抽屉内容改为「取数详情接口」 | 每开一次抽屉 +1 请求，且要处理 404/loading；行数据已足够 | 若出现「列表行字段不全」的字段（如产品 `create_time`），按需切 A′ |

**字段口径（A 选定的内容）**

产品抽屉（数据来自列表行，0 请求）：

| 字段 | 值 | 空值处理 |
|---|---|---|
| 产品编号 | `product_id`（mono） | — |
| 名称 | `name` | 空 → `—` |
| 仓库 | `git_url`（mono，单行省略 + `title`） | 空 → `未设置` |
| 描述 | `description`（多行，正文可滚动） | 空 → `未填写` |
| 证书 | `license` | 空 → `未设置` |
| 项目数 | 列表页本地计数（`ProductsPage.tsx:78-86`），回退 `project_count` | 无项目 → `0` |

> 注：`GET /v1/products` 响应里还有 `create_time` / `update_time`（`internal/api/products.go:66-67`），但前端 `Product` 类型未声明、issue 也未要求 → **本期不展示**，登记未决 U-04（改动只需扩类型 + 两行渲染）。

项目抽屉（数据来自列表行，0 请求）：

| 字段 | 值 |
|---|---|
| 项目编号 / 产品编号 / 序号 | `project_id` / `product_id` / `seq_no`（编号 mono） |
| 名称 | `name` |
| 上下文 | `context_md`，用 `ui/MarkdownViewer` 只读渲染；空 → 「未填写上下文」 |
| 状态 | 沿用 `ProjectsPage.tsx:94-99` 映射（open→「进行中」/closed→「已关闭」） |
| 创建 / 更新时间 | `create_time` / `update_time`，格式沿用列表的 `MM-DD HH:mm`（`ProjectsPage.tsx:173-177`） |
| 关闭时间 | `closed_at`；`open` → `—` |

**与既有按钮的关系（避免一次点击两个结果）**：抽屉内**不放**任何写动作、也不放下钻按钮（连「打开看板」也不放，footer 不传）。动作只留在列表行，理由：AC5「同一屏主操作只出现一次」，且抽屉叠在列表上时背后行的动作仍然可见可点。底部操作条因此为空 → `ui/DetailSheet` 的 `footer` 保持 undefined（组件已支持可选 footer）。

### 7.3 D2 是否新增 ui 组件

| 选项 | 代价 | 退出条件 |
|---|---|---|
| **A（选定）复用 `ui/DetailSheet` + 页面私有组件 `features/products/ProductDetailSheet.tsx` / `features/projects/ProjectDetailSheet.tsx` + 壳层共用 `features/shell/DetailFields.tsx`** | 两个 feature 里各有一段「字段清单」；键值网格靠 `features/shell/DetailFields.tsx` 收敛（与 `ErrorBar` 同层，`features/*` 已允许 import `features/shell`） | 若同构抽屉出现第三处（如执行节点详情）→ 升级为 ui 组件 |
| B 新增 `ui/ProductDetailSheet` / `ui/ProjectDetailSheet`（导出面 21→23） | 必须同步 4 处：`web/src/ui/index.ts`、`web/scripts/ui-imports-check.mjs`（`EXPECTED` 数组断言集合，漏改直接红）、`design/components.md` 总览表、`web/README.md:9,147,155` 的「21 个组件」表述；且 ui 层要开始承载 `Product`/`Project` 的业务字段形状（分层倒退） | 若 ui 层决定接纳「业务详情抽屉」这一类别 → 采用 B，并按上述 4 处同步 |
| C 最小改动（只在列表行加「详情」按钮 + `ui/ConfirmModal` 里塞字段） | 0 新文件，但字段排版不可复用、移动端 Modal 不是全屏 sheet，违反 `design/components.md` §ui/DetailSheet 移动端口径 | 不可选（与设计契约冲突） |

**结论**：选 A。`ui/` 导出面保持 **21 个不变**，`node web/scripts/ui-imports-check.mjs` 不改也应通过（这是本方案的一条硬自检）。

### 7.4 D3 关闭/重开的入口位置与只读口径

**入口位置**

| 选项 | 代价 | 退出条件 |
|---|---|---|
| **A（选定）只放在项目列表行操作列**（open 行「关闭」/closed 行「重开」，`Button variant="ghost" size="sm"`，破坏性确认在弹窗里用 `tone="danger"`） | 两枚 `ghost` 按钮让行高不变、不抢「打开看板」的视觉权重；每行 2 枚动作 | 若验收认为行内动作太挤（移动端卡片），把「关闭/重开」收进行内「更多」二级层；入口位置不变 |
| B 只放看板页头 | 与 issue 场景（在项目列表找不到关闭）不符；页头会同时出现「新建任务」（primary）与「关闭」（danger），主操作竞争 | 若验收要求「在看板里关闭」→ 采用 B 并把「新建任务」降为 default |
| C 两处都放 | 违反 AC5「同一屏主操作只出现一次」；还要处理「从列表进去关、从看板进去关」两条 409 分支 | 不选 |
| D 抽屉里放 | 抽屉叠在列表上，行内已有入口 → 同一动作两处可见 | 不选 |
| 最小改动（不做关闭入口） | 0 成本 | issue #102-2 未闭环，不能选 |

**只读口径（closed 项目）——按后端真实拦截面做，不做假拦截**

| 写操作 | 后端现状 | 前端本期 |
|---|---|---|
| 新建任务 | 拦：`ErrProjectClosed` → 409（`store/v3/task.go:56-58`） | **disabled** + 页头「已关闭」标识 + 空态动作改「返回项目列表」（E8/E9） |
| 评审通过/驳回、重置、删除任务、claim/lease | **不拦**（`grep -rn ErrProjectClosed internal` 仅 `task.go:57`） | 不拦（前端拦了后端放行 = 假只读，反而制造不一致）；登记 U-02 |
| 关闭/重开 | 幂等（§5.1） | 入口按状态二选一 |

> 「要不要把 closed 项目做成全面只读」需要**后端先加拦截**，超出本 issue 范围 → U-02（谁定：CTO；缺什么：后端只读语义设计；不定的后果：closed 项目上的评审/删除仍可写，与本方案页头「已关闭」的视觉承诺存在口径差）。

### 7.5 D4 409 未完成任务的交互

| 选项 | 代价 | 退出条件 |
|---|---|---|
| **A（选定）先 `close`（不带 force）→ 409 → 同一个弹窗切「取消 N 个任务」二次确认 → `close&force=1`** | 最坏 2 次请求 + 2 次点击；用户看到 N 是**服务端真值**而非前端推测 | 若实测发现 409 占比极高（多数项目带未完成任务），把首击改为「预检后单次确认」——但那要加预检请求（见 C） |
| B 先确认再直接 `force=1` | 1 次请求，但用户永远看不到「会取消几个任务」；盲 force 有误关风险 | 不可选（破坏性动作必须先知道范围） |
| C 加 `GET /v1/tasks?project_id=&status=` 预检 | **每个未完成状态一次请求**（4 次：pending/ready/in_progress/in_review；接口只支持单 `status`，`v3_handlers.go` 的 `listOrGetTask` 无多值），或 1 次 `limit=300` 全量后本地过滤（300 条上限下不可靠）；且预检与执行之间有竞态（预检 0 条 → close 时已有新任务 → 仍 409） | 若产品要求「先告知后一次点击完成」→ 采用 C 的全量预检变体，并接受竞态误差 |
| 最小改动（不做 409 分支，直接展示错误条） | 0 成本 | issue #102-2 的「无法关闭」会变成「关了报错」，未闭环 |

**文案与解析口径**

- 服务端 message 是英文内部文案（`v3: project still has unfinished tasks: N unfinished`）→ **原样展示给用户不合格**。前端解析 `/unfinished:\s*(\d+)\s+unfinished/`：
  - 命中 → 「该项目还有 **N** 个未完成任务。确认关闭会取消它们，且**重开不会恢复**。」
  - 未命中（后端改文案）→ 退化为「该项目还有未完成任务。确认关闭会取消它们，且重开不会恢复。」→ 仍可 force。
- 技术细节原文只进 `ErrorBar.detail`（排障用），主文案必须是中文。
- 关闭成功后**不做 toast**：行状态变「已关闭」+ 按钮变「重开」即反馈；平台无 toast 组件，新增会牵动 ui 导出面（与 D2 冲突）。

### 7.6 D5 侧栏去掉「看板」后的连带口径

| 子问题 | 选项 | 代价 | 结论与退出条件 |
|---|---|---|---|
| 侧栏（≥768） | A 三项：收件箱/产品/执行节点；B 保留看板 | A：移动端侧栏少一个入口；B：issue 未闭环 | **A**。退出：无（issue 明确要求） |
| 移动 TabBar（<768）第 1 项 | **A（选定）同步去掉**：收件箱/产品/更多；B 保留「看板」 | A：移动端失去「一键回上次看板」，需 产品→项目→打开看板；B：同一导航模型两形态不一致（`nav.tsx:1-10` 明确两形态同源），且「看板」项在无记忆时跳 `#/products`，标签承诺落空 | **A**。退出条件：若移动端实测「回上次看板」是高频路径（可从 `#/` 与项目列表入口的点击量判断——当前无采集，见 §9），再加回 TabBar 项并同步契约 |
| `#/` 首页 | **A（选定）仍跳上次项目看板（`HomeRoute` 不变）；B 改为产品列表** | A：一级入口消失后 `#/` 成隐式入口；B：改 `HomeRoute.tsx` 一行 + `design/flows.md` F1 改写，且失去「继续上次工作」的默认行为 | **A**。退出条件：若验收认为「看板不应有隐式入口」→ 采用 B（一行改动 + 契约同步） |
| `activeKeyOf('/projects/*')` | **A（选定）→ `'products'`**（看板是产品域的下钻层，父域高亮）；B 返回 `''`（全栏不高亮） | A：在产品域内任意下钻都高亮「产品」，位置感稳定；B：导航全灭，用户失去位置感 | **A**。退出条件：若验收要求「下钻层不高亮父级」→ 改回 `''`（一行） |
| `localStorage['echuan:last-project']` | **A（选定）保留**；B 废弃 | A：`boardPath()` 有 5 个消费者（`HomeRoute`、`InboxPage`、`QueuePage`、`AppFrame.go`、`ErrorBoundary.onExit`），废弃=改 5 处且丢「继续上次工作」；B：无收益 | **A**。退出条件：若 `#/` 也改为产品列表（上一行的 B），记忆只剩错误边界兜底作用 → 届时随该决策一起废弃 |
| 「更多」二级层是否补「上次看板」 | **A（选定）不补**；B 补一项 | A：二级层定位是「侧栏放得下的入口」，与「看板不是一级入口」的取向一致；B：等于把看板换个地方放回一级导航 | **A**。退出条件：若移动端可达性被验收判为不达标 → 采用 B |
| **维持现状 / 最小改动对照** | 维持现状 = 侧栏四项、TabBar 四项、「看板」高亮、记忆与 `#/` 行为全部不动 | 0 成本；但 issue #102-3 未闭环 | 不可选；若必须最小化改动，最小合法变体是「只删侧栏 `board` 项、TabBar 与高亮不动」——代价是两形态不一致、`/projects/*` 高亮无处落（`nav.tsx:120` 返回 `board` 但该项已不在列表）→ 仍需改 `activeKeyOf`，省不下改动 |

**去掉后的可达性（写清用户从哪里进）**：`产品列表 → 查看项目 → 项目列表 → 打开看板`；另 `#/` 与「返回看板」类动作（收件箱/队列空态、错误边界 `onExit`）仍按 `boardPath()` 直达上次项目看板——这条路径不变。

### 7.7 D6 面包屑回项目列表的数据来源

现状：`/projects/:pid/tasks` 只有 `project_id`（`AppFrame.tsx:63-67`），要渲染「产品 / <产品名> / <项目名>」并给中间段挂 `#/products/:productId/projects`，需要 `product_id` + 两个名字。

| 候选 | 额外请求 | 深链/旧收藏 | 失败降级 | 对 `BoardRedirect` / `ErrorBoundary.onExit` / 分享链接 | 判断 |
|---|---|---|---|---|---|
| **A（选定）壳层反查 `GET /v1/projects?id=` + `GET /v1/products?id=`（`api.getProject`/`getProduct` 已存在）** | 首次进某项目 ≤2；产品名按 `productId` 缓存（同产品下第二个项目只 +1）；项目+产品双缓存命中 → 0 | 完全兼容：路由不动，任何深链/旧书签都能反查 | 项目失败 → 退回今天的 `产品 / <project_id>`（无链接）；产品失败 → `产品 / <productId>`（链接仍可用） | 三者**零改动**（路由形态未变）；`#/projects/:pid/tasks` 分享链接继续有效 | **选**：唯一能同时满足「旧链接不变」与「深链也有名字」 |
| B 扩 `last-project` 记忆带 `product_id`/名字 | 记忆命中 0；深链无记忆 → 仍要反查（否则只剩 ID） | 深链进入无记忆 → 必须回落到 A | 同 A，但两套来源 | 记忆写入点变多（`AppFrame` + 项目列表 → 陈旧风险） | 不单独选；作为 A 的**可选优化**（缓存层已覆盖同等收益，且没有陈旧问题） |
| C 改路由形态：`#/products/:productId/projects/:projectId/tasks` | 0（productId 在路径里） | **旧链接全部失效**：`#/projects/:pid/tasks` 必须保留重定向；而重定向要构造新 URL 仍需要 `product_id` → 还是得反查 | 同 A | `BoardRedirect`、`boardPath()`（5 个消费者）、`ErrorBoundary.onExit`、`App.tsx` 路由表、`design/flows.md` F1 全要改；分享链接双形态长期共存 | 不选：**没有免掉请求，反而多一套路由与兼容分支** |
| 最小改动（维持现状 `产品 / <pid>`） | 0 | 兼容 | — | — | issue #102-4 未闭环 |

**实现约束（A）**

- 新 hook `features/shell/use-crumb-names.ts`：输入 `pathname`；`projectId` 用既有 `projectIdOf` 提取；命中模块级缓存直接返回，否则 `api.getProject` → `api.getProduct`；**单飞 + 按 id 去重**（并发进入同一项目只发一次）；卸载/切路由后丢弃旧结果（`alive` 守卫，与 `ProjectsPage.tsx:52-67` 同口径）。
- 只在 `/projects/*` 分支启用；`/products/:productId/projects` 段**不改名**（见 U-05），`/inbox`、`/queue`、`/workers` 不受影响。
- 失败**静默**：不设全局错误条、不阻塞渲染（`AppFrame.tsx:38-42` 的「壳层不能因接口失败塌掉」口径不变）。名字缺失时标签用 mono 字体 + `title` 提示原始 ID。
- 末段规则沿用 `AppFrame.tsx:146-153`：当前项目段为末段时是文本（不可点）；带 `:taskId` 时项目段变成 `Link`（`to=/products/<productId>/projects`），末段是 task id。

**是否推翻 issue #89 B2（「面包屑不为名字新增请求」）**：**推翻**，理由三条：

1. B2 的前提是「面包屑只需展示 ID」——当时 `/projects/*` 不要求回项目列表，`product_id` 根本不需要；
2. #102-4 要求从看板回到**项目列表**，而项目列表的 URL 需要 `product_id`，路由段里没有 → 不改路由的前提（D6 的 C 已被否）下，**只能反查**；
3. 代价被限定：仅 `/projects/*` 一路径、首次 ≤2 次请求、会话内按 id 缓存、失败静默降级（P4-5 可测）。这与 B2 要防的「壳层因取名字而变脆」不冲突——降级路径就是 B2 想要的稳态。

### 7.8 交互一致性约束（实现时必须遵守）

1. **一次点击一个结果**：行内所有可点元素（按钮、链接）必须 `event.stopPropagation()`，否则 `tr` 的 `onClick` 与按钮动作同时触发（抽屉 + 导航）。V2 用 `isInteractiveTarget`（`archive/v1-main-20261104` 的 `lib/interaction.ts`）解决同一问题，本方案用显式 `stopPropagation`（更局部）。
2. **同一屏主操作只出现一次**（AC5）：产品页页头「新建产品」= 唯一 primary；项目页页头「新建项目」= 唯一 primary，行内「打开看板」default、「关闭/重开」ghost；看板页头「新建任务」primary（closed → disabled）。
3. **触控 ≥44×44**：移动端由 `ui/ui.css:977-985` 对 `ui/Button` 的 `md`/`sm` 统一兜 `min-height`，本方案**不新增**触控 CSS；只要求行内动作组在 390 宽下不换行溢出（可用 `flex-wrap` 兜）。
4. **不新增 `ui/` 组件、不新增依赖、不改路由表**（`App.tsx` 零改动）；页面私有组件按 D2 落在 `features/**`。
5. **颜色/间距只用令牌**：新 CSS 不出现 hex / px 字面量（`design/README.md` 验收第 2 条）；不新增 `@media` 断点值（只用既有 `47.99em` / `63.99em` 两档，`web/scripts/style-debt.mjs` ⑤ 会判）。

### 7.9 design 基线同步（必答项）

`design/` 是界面契约（`design/README.md`）。四项都改结构或交互，因此契约先改、实现跟上，**不允许实现完了让 design 追认**。本卡只列清单，不改 `design/**`（本卡唯一交付物是本文档）。

**① 要改的条目清单**

| 文件 | 改什么 | 依据 |
|---|---|---|
| `design/screens/products.md` | 「组件」补 `ui/DetailSheet`；「交互」写明「行点击 → 只读详情抽屉（6 字段），行内『查看项目』→ `#/products/:productId/projects`（点按钮不开抽屉）」；「结构」操作列保持 1 枚按钮；「不做」补「产品编辑/删除（不变）」 | D1/D2 |
| `design/screens/projects.md` | 「交互」改：行点击 → 只读详情抽屉（9 字段）；操作列 = 打开看板（default）+ 关闭/重开（ghost）；「数据来源」补 `POST /v1/projects/close` / `POST /v1/projects/reopen` 与 409 二次确认；删除末行「不做：关闭/重开项目」 | D1/D3/D4 |
| `design/screens/board.md` | 「结构」页头补「已关闭标识（closed 项目）」；「交互」补「closed 项目上『新建任务』disabled，空态动作为『返回项目列表』」；「不做」不变 | D3 |
| `design/flows.md` | F1 第 1 步补「一级导航不含看板；`#/` 仍直达上次项目看板；看板入口 = 产品 → 项目列表 → 打开看板」；新增 **F5 关闭/重开项目**（含 409 → force 二次确认、reopen 不复活任务）；F1 分支补「面包屑按名显示，取不到时退回 ID」 | D5/D6 |
| `design/components.md` | §ui/DetailSheet「用途」扩为「任务详情 / 附件预览 / 产品与项目只读详情」；§ui/DataTable 补「行点击开只读抽屉（`onRowClick`），行内动作需阻断冒泡」；§ui/SidebarNav 与 §ui/TabBar 的项清单改为 3 项（去掉「看板」）；总览表**不改**（仍 21 个组件）；新增一节「屏内私有详情抽屉」说明 `features/shell/DetailFields` 不属于 ui 导出面 | D2/D5 |
| `design/tokens.json` | **不动**（预期确认）：本方案不新增几何、不新增颜色档位、不新增断点 | 全部 |
| `design/README.md` | 「主路径」「文件」两节不涉及本改动 → 不动；若 §7.9② 采纳「补 refs」则需在 README 的 refs 数量口径（240）后追加新增张数 | 视 ② 结论 |

**② `refs/` 是否重生成：本期不重生成，登记为偏差**

若补齐，需要的张数（按 `design/refs/<screen>__<viewport>__<theme>__<state>.png` 口径，三视口 × 双主题 = 6 张/态）：

| 屏 | 新增态 | 需重生成的既有态 | 张数 |
|---|---|---|---|
| `products` | `drawer`（行点击开抽屉） | `default`（操作列语义变化，视觉可能零差异） | 新 1×6 = 6；重生 1×6 = 6 → **12** |
| `projects` | `drawer`、`closing-confirm`（含 409 二态）、`closed-row` | `default`（操作列 +1 枚按钮，必有视觉差异） | 新 3×6 = 18；重生 1×6 = 6 → **24** |
| `board` | `closed`（页头标识 + 新建任务 disabled） | 无（open 项目页头不变） | 新 1×6 = 6 → **6** |
| 合计 | 5 个新态 | 2 个既有态 | **42 张**（暗 21 / 亮 21），生成后 refs 由 240 → 282 |

生成方式（`design/README.md`「复核方式」既有流水线，不改）：

```bash
# 令牌 → CSS（tokens.json 若未改可跳过）
node <kit>/design-kit/scripts/tokens-to-css.mjs design/tokens.json design/mock/tokens.css

# 逐屏截图（需要 mock 先支持新状态：document.documentElement.dataset.state = drawer|closing-confirm|closed-row|closed）
node <kit>/design-kit/scripts/screenshot.mjs --mock design/mock/shots/<screen> \
  --out design/refs --screen <screen> --states drawer,default \
  --url "$DSH_BROWSER_CDP_URL" --browser-host <本容器 IP>

# 契约校验（必须 0 错误）
node <kit>/design-kit/scripts/check.mjs design
```

**本期决定：不重生成 `refs/`，登记为偏差。** 理由与认账：

- 理由：`refs/` 是 `design/mock/` 的确定性截图（`design/README.md`「复核方式」）。新增的 5 个态在 mock 里**尚不存在状态机**，重生成必须先改 mock（属于设计侧物料工作量），而本卡与后续 dev 卡的验收判据是「文字契约 + 浏览器实测」（§2.2），不依赖新 refs。
- 偏差清单（谁认账 / 何时补 / 不补的后果）：
  - 谁认账：**设计侧（`design/` 物料维护人）**，决策人确认；
  - 何时补：issue #102 的四条合并进 `main` 之后、下一轮全量视觉回归之前统一补 42 张（含 `products`/`projects` 的 `default` 重生成）；
  - 不补的后果：`refs/` 与 `design/screens/*.md` 文字契约出现口径差（抽屉/关闭流程/closed 看板无截图基准），后续视觉回归只能靠文字契约 → 需在 `design/README.md` refs 口径处留一行「偏差登记 42 张，待补」。
- 明确不做的事：不「实现完后用实现截图回填 refs」（那会让 refs 从契约退化为记录）。

**③ `design/MANIFEST.sha256`（design 文件变动后怎么办）**

- 现状：单行内容哈希，`design/README.md:66` 明确它是「物料完整性凭据、不是 design_ref」；仓库内**没有生成该哈希的脚本**（`git log -1 -- design/MANIFEST.sha256` 只有入库 commit `032c2ca`；本卡用 4 种常见递归哈希口径复算均未命中 → 口径不可复现，见 U-06）。
- 本期要求：§7.9① 的 md 一旦改动，**同一 commit 内必须更新 `design/MANIFEST.sha256`**，否则该文件与物料不一致、凭据失效。
- 由于生成口径不可复现，本方案把它作为**未决项 U-06**（谁定：设计侧/决策人；缺什么：哈希口径或一个可复跑脚本；不定的后果：MANIFEST 从「完整性凭据」退化为「没人验的随机串」，只能靠 `git` 版本本身保证完整性）。
- 不做的替代方案（明确写清，避免「悄悄不更新」）：保留旧值并在 commit message 里注明「MANIFEST 待 U-06 决议后统一重算」→ 但**不允许无人认账**：认账人为设计侧，与 ② 的偏差清单同一条跟踪项。

---

## 8 部署与回滚

### 8.1 依赖与构建（无新增依赖）

- 运行期依赖不变：`react` 18.3.1 / `react-dom` / `react-router-dom` 6.30 / `@mantine/core` + `@mantine/hooks` 8.3 / `marked` / `dompurify`（`web/package.json`）。ADR-0001 锁版本，本方案不新增包、不新增环境变量。
- 构建链（**无 Go 也能跑**）：

```bash
export npm_config_cache=/workspace/.npm-cache
pnpm --dir web install --store-dir /workspace/.pnpm-store   # 依赖已在则跳过
node web/scripts/style-debt.mjs          # 样式债 6 项门禁（make web 也会跑）
pnpm --dir web build                     # tsc --noEmit && vite build && node scripts/check-size.mjs
node web/scripts/ui-imports-check.mjs    # 断言 ui/ 导出面仍是 21 个组件（D2 的硬自检）
```

- `internal/web/dist` 是 tracked 的构建产物：**本卡与各 dev 卡都不提交**，由收口卡统一执行 `make web` 重生成（`Makefile:52-62`：先 style-debt、再 `pnpm build`、再 `rm -rf internal/web/dist && cp -r web/dist internal/web/dist`）。
- 发布：`make build`（需 Go，本环境无）→ `make docker-build` → `docker compose up -d`；单二进制、内网部署，**不做流量灰度**。

### 8.2 回滚

| 方式 | 命令 | 验证点 |
|---|---|---|
| 镜像回滚 | 升级前 `docker tag echuan-v2:local echuan-v2:rollback`；回滚时把 compose 的镜像指回该 tag → `docker compose up -d` | 容器 `/version` 的 commit 与预期一致（**当前部署 `/version` 返回 `dev`/`none`，无法核实**，见 U-01） |
| commit 回滚 | `git revert <merge sha>` → 重跑 §8.1 构建链 → `make docker-build` → `docker compose up -d` | 构建链三道门禁全绿；P1–P4 在旧版上回到「改前」行为 |
| 数据回滚 | **不需要**：本方案无迁移、无 DDL/DML 由前端直接执行 | — |

**结论：纯前端改动，无数据迁移。** 但必须写清一个非对称后果：**回滚代码不会自动 reopen 已关闭的项目**（关闭是后端写操作，§5.1）。回滚前请先按 `closed_at` 排查误关项目：

```sql
SELECT project_id, name, closed_at FROM projects WHERE status = 'closed' ORDER BY closed_at DESC LIMIT 20;
```

如属误关：逐个 `POST /v1/projects/reopen?id=<pid>`（**被取消的任务不会复活**，`internal/store/v3/project.go:147-148`）；若需要任务也回来，只能走 `scripts/restore.sh` 回数据（会丢备份之后的数据，`docs/deploy.md:445-456`）。

---

## 9 观测

本平台**无远程上报端点**（`docs/tech/web-ui-redo-v0.1.md:163`），因此观测只由「前端可见口径 + 后端既有日志/事件表 + 构建期门禁」三者构成；其余如实标「未覆盖」。

### 9.1 前端可见错误条口径（逐路径写死，dev 卡照此实现）

| 路径 | 标题 | 说明（用户可读） | detail（技术原文） | 动作 |
|---|---|---|---|---|
| 关闭 409（有未完成任务） | 关闭项目需要确认 | 「该项目还有 N 个未完成任务，确认关闭会取消它们，且重开不会恢复」 | 服务端 `message` 原文 | 「取消」/「关闭并取消 N 个任务」 |
| 关闭/重开其它错误 | 关闭项目失败 / 重开项目失败 | 「操作没有生效，可以重试；如果反复失败，可能是项目已被他人修改」 | `HTTP <status> · <message>` | 「重试」 |
| 列表/看板刷新失败 | 沿用既有 `ErrorBar` 文案（`BoardPage.tsx:236-246`） | 保留上次数据 | 既有 | 「重试」 |
| 新建任务撞 closed（E9） | 项目已关闭 | 「该项目已关闭，不能新建任务；如需继续，请先在项目列表重开」 | `HTTP 409 · v3: project is closed: <pid>` | 「关闭」 |
| 面包屑反查失败 | **不显示错误条**（静默降级为 ID） | — | — | — |
| 抽屉 | 不产生错误条（0 请求） | — | — | — |

### 9.2 后端既有覆盖面（不改后端，只用现有能力）

| 观测点 | 现状覆盖 | 怎么用 |
|---|---|---|
| 请求访问日志 | `internal/api/middleware.go:151-176`：每请求一行 `request_id/actor/method/path/status/duration_ms`；**不含 query string** | `docker compose logs echuan \| grep '/v1/projects/close'` 可看 status 分布（204 / 409 / 500），但**定位不到具体项目**（无 `id`） |
| 5xx 错误日志 | `internal/api/v3_handlers.go:181-185`：只有 `StatusOf==500` 才打 `v3 request failed`（含 error） | 500 可追；**409 不产生专门日志** |
| 任务取消事件 | `internal/store/v3/project.go:228` 每个被取消任务写 `task_events(event='cancelled', detail_json='{"reason":"project closed"}')` | `GET /v1/tasks/events?task_id=` 或 SQL：`SELECT COUNT(*) FROM task_events WHERE event='cancelled' AND detail_json LIKE '%project closed%';` → 「force 关闭连带取消了多少任务」是可查的 |
| 关闭/重开本身的审计 | `projects.last_actor` / `closed_at` / `update_time` 由后端写（`project.go:135-139`） | `SELECT project_id, closed_at, last_actor FROM projects WHERE closed_at IS NOT NULL;` |

### 9.3 构建期门禁（本方案最有效的自动化观测）

| 门禁 | 命令 | 判据 |
|---|---|---|
| 样式债 6 项 | `node web/scripts/style-debt.mjs` | 未引用类名 <20%、同层重复选择器 0、顶层 `:root` =1、媒体查询内令牌覆写 0、全局层 ≤300 行、断点唯一 1 套 |
| ui 导出面 | `node web/scripts/ui-imports-check.mjs` | 恰好 21 个组件（D2 选 A 就是为了这条不改） |
| 产物体积 | `pnpm --dir web build`（内含 `check-size.mjs`） | js gzip ≤ 344,056 B |

### 9.4 未覆盖（如实登记，不写「加日志」了事）

1. 前端 409 → force 转化率、用户中途放弃率：无采集端；
2. 抽屉打开次数、面包屑降级次数与降级率：无采集端；
3. 关闭/重开按钮的 `path` 日志缺少项目 id → 无法按项目聚合（要改日志字段＝改后端，超出本 issue 范围 → U-07）；
4. 浏览器侧回归：`web/scripts/ui-verify.mjs` 目前覆盖路由/面包屑/轮询/抽屉/错误提示与三视口（`--viewports 1440x900,834x1112,390x844`），**本方案不扩展该脚本**；四条判据由 dev 卡按 §2.2 用浏览器实测 + 改前/改后截图（暗色基准）各 1 张。

---

## 10 风险与未决项

### 10.1 风险

| # | 风险 | 触发信号 | 影响 | 缓解 | 能否回滚 |
|---|---|---|---|---|---|
| R-01 | 行点击与行内按钮「一次点击两个结果」 | 点「打开看板」同时开出抽屉 | 用户困惑、导航错位 | §7.8 强制 `stopPropagation`；dev 卡把「点按钮不出抽屉」写成硬判据（P1-3） | 是（去掉 `onRowClick` 一行） |
| R-02 | 壳层反查让顶栏在慢网下显示 ID → 名字跳变 | 首帧 `产品 / echuan-1` 之后变 `产品 / 驿传 / 项目名` | 轻微视觉跳动 | 缓存 + 单飞；不阻塞渲染；名字段宽度走省略号不换行（既有 `fx-crumbs` 已支持） | 是（D6 退回「最小改动」） |
| R-03 | force 关闭连带取消在飞任务（worker 正跑） | worker 报 `lease_lost` / `invalid transition` | 在飞工作白做、workspace 残留 | 二次确认文案明示「取消且不恢复」；事件表可追溯；reopen 后需人工重排 | 否（任务状态不可逆，只有 `restore.sh` 能回数据） |
| R-04 | 关掉「看板」一级入口后，用户找不到自己刚在做的看板 | 收件箱/项目列表成为唯一路径；移动端少一跳捷径 | 可达性下降 | `#/` 仍直达上次看板（D5 保留）；项目列表「打开看板」为显式入口 | 是（把板项加回导航） |
| R-05 | closed 项目只按后端真实拦截面拦截（只拦新建任务） | 用户以为「已关闭」= 全只读，结果仍能评审/删除 | 语义落差 | 页头标识文案只写「已关闭」，不写「只读」；U-02 跟踪 | 无需回滚（文案/范围可调） |
| R-06 | 面包屑反查与「壳层零请求」旧口径冲突引发评审争议 | 评审引用 issue #89 B2 | 方案被驳回 | §7.7 已显式推翻并给三条理由 + 请求预算 + 降级稳态 | 是（退回最小改动） |
| R-07 | 42 张 refs 偏差被遗忘 | 下一轮视觉回归发现 refs 无抽屉态 | 契约与截图长期不一致 | §7.9② 指定认账人（设计侧）与补的时点；`design/README.md` 需留偏差行 | 是（补生成即消除） |
| R-08 | 已取消任务在 reopen 后仍占「已取消」列，用户误以为没救 | 关开一次后看板 `cancelled` 列变长 | 误解为 bug | 重开确认文案明示「已取消任务不会恢复」；F-3 流程写明 | 否（领域语义，后端既定） |

### 10.2 未决项

| # | 未决问题 | 谁定 | 缺什么信息 | 不定的后果 |
|---|---|---|---|---|
| U-01 | 当前部署（`http://echuan:8720`）到底是哪个 commit？`/version` 返回 `dev`/`none` | CTO / 部署责任人 | 构建时的 `-ldflags` commit 注入（`Makefile:11,14-16,24`）是否在镜像构建里生效 | 改前/改后截图与「证据基于哪个 commit」无法对齐；dev 卡验收必须基于新构建镜像，不能拿现有 8720 上的产物当「改后」 |
| U-02 | closed 项目是否要做「全面只读」（评审/删除/重置也拦） | CTO（需后端改动） | 后端只读语义设计（现在只拦 `createTask`，`store/v3/task.go:56-58`） | 页头「已关闭」的视觉承诺与实际可写范围不一致；用户可能在已关闭项目上继续评审 |
| U-03 | 页头「新建产品 / 新建项目」占位按钮（点了没反应） | PM + 设计侧 | 创建流程是否立项、字段与校验口径 | 用户误判为前端故障；#102 修复后仍会被当作新 bug 报上来 |
| U-04 | 产品抽屉是否展示 `create_time` / `update_time`（接口已返回） | 设计侧 | 抽屉字段是否需要与 V2 对齐（V2 显示了创建/更新时间） | 字段口径与 V2 有差；改动量很小（扩类型 + 两行），但会改 P1-1 字段数（6→8） |
| U-05 | `#/products/:productId/projects` 面包屑第二段是否也改成产品名 | 设计侧 | 是否接受与 `ProjectsPage` 重复 1 次 `getProduct`（或让页面消费壳层缓存） | 面包屑在产品列表页仍显示 `product_id`，与看板页「产品名」风格不一致 |
| U-06 | `design/MANIFEST.sha256` 的生成口径（无脚本，4 种常见递归哈希复算未命中） | 设计侧 / 决策人 | 哈希覆盖面与算法，或一个可复跑脚本 | 契约物料改动后该凭据无法重算 → 要么失效，要么每次手写入一个没人能验的值 |
| U-07 | 关闭/重开的访问日志是否补 `subject`/项目 id（现只有 `path`+`status`） | CTO | 是否愿意为运维可诊断性改后端日志字段 | 409 发生率高时无法定位是哪个项目，只能靠事件表与人工追 |

---

## 11 附录

### 11.1 源码证据索引（`main@2742130`，与 §3 一一对应）

| 证据 | 位置 | 读到的事实 |
|---|---|---|
| 列表未接行点击 | `web/src/features/products/ProductsPage.tsx:180-188`、`web/src/features/projects/ProjectsPage.tsx:152-157` | 两处 `<DataTable>` 均未传 `onRowClick` |
| DataTable 已支持行点击 | `web/src/ui/DataTable.tsx:36,61,97-109` | `onRowClick` + `tabIndex=0` + Enter/Space + `cursor:pointer` |
| 无抽屉引用 | `grep -rn DetailSheet web/src/features/{products,projects}` → 空 | 抽屉未接 |
| 关闭/重开零调用方 | `web/src/api/client.ts:64-67`；`grep -rn "closeProject\|reopenProject" web/src` → 仅定义两行 | 能力已就绪 |
| 关闭语义 | `internal/store/v3/project.go:102-146`（未完成任务 409 → `force` 取消）、`:192-231`（取消 4 类状态 + 清租约 + 写事件）、`:115-117`（已关闭 → nil）、`:149-176`（reopen 幂等、不复活任务） | §5.1 依据 |
| 错误映射 | `internal/app/v3/errors.go:64-86`（`ErrProjectNotEmpty` → 409 `conflict`）、`internal/store/v3/models.go:22-23`（英文 message 常量） | 409 文案来源 |
| closed 项目写任务被拦 | `internal/store/v3/task.go:56-58`（`ErrProjectClosed`） | 只读口径依据（仅 createTask） |
| 响应 DTO | `internal/api/products.go:58-68`（product 含 `create_time`/`update_time`）、`internal/api/v3_handlers.go:42-52`（project 含 `closed_at`） | 字段口径 |
| 路由与处理器 | `internal/api/router_v3.go:37-38,111-112`、`internal/api/v3_handlers.go:291-317` | 端点存在、204 语义 |
| 侧栏 / TabBar | `web/src/features/shell/nav.tsx:95-108,116-121`、`AppFrame.tsx:85-91,109-132,180-182` | 四项、`activeKeyOf`→board |
| 面包屑 | `web/src/features/shell/AppFrame.tsx:38-42,43-61,63-67,146-153` | 只从 URL 推导、末段不可点 |
| 首页与记忆 | `web/src/features/home/{HomeRoute,BoardRedirect}.tsx`、`home/last-project.ts:6,35-43`、`AppFrame.tsx:75-79,169-177` | `boardPath` 的 5 个消费者 |
| 看板页头 | `web/src/features/board/BoardPage.tsx:75-88（project 取数）,197（项目名）,231（新建任务）,268-275（空态动作）` | closed 无标识/不拦截 |
| 抽屉容器 | `web/src/ui/DetailSheet.tsx:27-83`（720/移动全屏/遮罩 0.35/footer 可选）、`web/src/ui/ui.css:587-651,977-985` | 复用即可，不需新几何 |
| 门禁 | `web/scripts/ui-imports-check.mjs`（EXPECTED 21）、`web/scripts/style-debt.mjs`（6 项阈值）、`web/scripts/check-size.mjs`（344,056 B）、`Makefile:52-62` | §8.1 / §9.3 |
| V2 行为参照 | `archive/v1-main-20261104:web/src/pages/ProductsPage.tsx`（卡片 onClick=下钻、`DetailButton`=开抽屉）、`web/src/components/ProjectDetailDrawer.tsx`、`web/src/components/ProjectActions.tsx`、`web/src/components/Sidebar.tsx:40-41`（侧栏仅 2 项） | 只对齐行为，不复用代码 |

### 11.2 任务级验收自查（本卡 10 条 → 落在哪一章）

| # | 要求 | 落点 | 状态 |
|---|---|---|---|
| 1 | 文档在 `feature/echuan-2` 提交、11 章齐全 | 本文；commit 见交付报告 | 完成 |
| 2 | 四条 issue 逐条闭环（现象 → 源码证据 → 方案 → 可观察判据 → 不做的理由+未决） | §2.1 / §2.2 | 完成 |
| 3 | 六个决策点各有选项/代价/退出条件，且都有「维持现状/最小改动」对照 | §7.2–§7.7（每节表格末行即最小改动或现状） | 完成 |
| 4 | 数据模型与接口如实写「无变更」，列消费的接口、错误码、幂等 | §4 / §5 / §5.1 | 完成 |
| 5 | 关键流程覆盖失败路径（抽屉失败、409、force 取消、reopen 不复活、面包屑降级、closed 新建任务） | §6.2 E1–E12 | 完成 |
| 6 | 部署与回滚：单二进制、dist 由收口卡重生成、回滚方式、纯前端无迁移 | §8 | 完成 |
| 7 | 观测：前端错误条口径 + 后端既有日志/事件 + 未覆盖如实标 | §9 | 完成 |
| 8 | 未决项每条写「谁定 / 缺什么 / 不定的后果」 | §10.2 U-01–U-07 | 完成 |
| 9 | 不出现排期、人日、提交顺序，不写任务拆解 | 全文（无） | 完成 |
| 10 | 影响面到文件级，不写实现代码/不给补丁 | §3.2 | 完成 |

### 11.3 未执行项（不伪造）

| 命令 / 动作 | 状态 | 原因 |
|---|---|---|
| `make test`（Go 全量单测） | **未执行** | 本环境无 Go（`go.mod` 要求 1.26.1） |
| `make build` / `make docker-build` | **未执行** | 同上；本卡不产出二进制/镜像 |
| `make web`（同步 `internal/web/dist`） | **未执行** | 本卡不改 `web/src`，dist 由收口卡统一重生成 |
| `pnpm --dir web build`、`ui-imports-check`、`style-debt`、`check-size` | **未执行** | 本卡只新增一篇文档，前端源码零改动；三条门禁属 dev 卡自检项 |
| 浏览器改前/改后截图 | **未执行** | 属 dev 卡验收证据；且当前部署 `/version` 返回 `dev`/`none`，无法与 `2742130` 对齐（U-01），不能作为基线 |
| `node <kit>/design-kit/scripts/check.mjs design` | **未执行** | 本卡未改 `design/**` |
| `design/MANIFEST.sha256` 复算 | **未执行（口径不可复现）** | 仓库无生成脚本；本卡尝试 4 种常见递归哈希口径均未命中原值 → U-06 |

### 11.4 术语与文案口径（避免同一件事三种叫法）

| 概念 | 统一叫法 | 出处 |
|---|---|---|
| 项目状态 | `open` = 「进行中」，`closed` = 「已关闭」 | 沿用 `ProjectsPage.tsx:94-99` |
| 关闭动作 | 「关闭项目」（动词：关闭），force 分支叫「关闭并取消 N 个任务」 | §7.5 |
| 重开动作 | 「重开项目」（不叫「恢复」「启用」） | §6.1 F-3 |
| 抽屉 | 「详情」（UI 标题用产品名/项目名，不用「详情」当标题） | §7.2 |
| 面包屑第二段 | 产品名（退化时 `productId`） | §7.7 |
| 被取消任务 | 「已取消」（任务状态 `cancelled`），不与项目「已关闭」混用 | §7.4 |
