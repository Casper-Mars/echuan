# 项目列表倒序 + 状态筛选 技术方案 v0.1

- 状态：**已废弃**（2026-11-23 评审驳回：排序与筛选都需要改后端接口；由 [v0.2](./projects-sort-filter-133-v0.2.md) 取代，契约变更记于 [ADR-0002](../decisions/ADR-0002-projects-list-order-filter-api.md)）
- 历史状态：待评审
- 作者：CTO
- 日期：2026-11-23
- 输入：PRD `prd-issue-133-projects-sort-filter-v0.1.md`（级别 m）+ 需求卡决策简报（material `AGRAPUN5BRALWCBX7UWCFASTFE`）+ [issue #133](https://github.com/Casper-Mars/echuan/issues/133)
- 代码基线：`Casper-Mars/echuan@6484e3fa85579f1f023dcc9bc7cd46a01a4ad7e3`（`main`，已 `git fetch --prune origin`，工作区与 `origin/main` 一致，2026-11-23）
- 运行时证据：只读 `GET /v1/projects?limit=100`（测试平台 `http://echuan:8720`，2026-11-23），13 条，见 §3.1

---

## 1 结论

**做最小改动，全部落在项目列表这一屏的前端呈现层：`web/src/features/projects/ProjectsPage.tsx` 一处模块内加「本地筛选 + 本地排序 + 页头下拉」，请求字节不变，接口契约与 `kit` 工具一行不改。**

### 1.1 两条拍板（回答 PRD §9-1 与「筛选落哪层」）

| 编号 | 决策 | 不选的那条 | 代价 | 什么证据会推翻它 |
|---|---|---|---|---|
| D1 | 「倒序」落**前端呈现层**：`GET /v1/projects` 保持 `id ASC`，列表在页面内按 `seq_no DESC` 重排 | 把接口改成 `id DESC` | API 顺序与 UI 顺序口径不一致，须在代码注释 + 屏规格 + 两份 tech 文档里写死「接口仍升序、呈现倒序」 | ① 需要「新→旧」的跨页游标（项目数常态超单页且要求倒序翻页）；② `kit`/agent 侧明确要求 `listProjects` 倒序；③ 出现第三个需要倒序的屏。任一发生 → 走 ADR 重开 |
| D2 | 「状态筛选」落**本地过滤**：仍只发一次不带 `status` 的请求，筛选是页面内的纯派生 | 切筛选时带 `status` 重拉（看板页先例） | 全局项目超单页时，可见集合仍受同一页截断（PRD 已声明不在本期） | 产品下项目数常态超单页 → PRD §9-2 立项（服务端 `product_id` 过滤 + 分页），届时筛选与排序一并下沉，本方案作废 |

D1 的理由不是「改动小」，是**方向翻转是契约级不可逆**：`after_id` 现在是 `p.id > ? … ASC`（`internal/store/v3/project.go:92,96`），`next_after_id` 取本页最后一条（`internal/api/v3_handlers.go:257-259`）。改 DESC 必须同时把键集方向翻成 `p.id < ?`、把 `next_after_id` 语义改成「本页最小 id」，而这两个字段已被 `kit` 与外部 agent 消费（`kit/src/client.ts:1339`、`kit/src/tools/v3.ts:469-491`）。本期的收益只有「人看列表顺序对」，没有需求方要求 agent 侧也倒序 —— 用不可逆契约换零收益，不做。

D2 的理由不是「看板也这么做」，是**页头「筛选是否出现」必须以未过滤集合为准**（PRD §4 硬要求：判断依据是该产品项目总数，不是命中数）。走服务端过滤后，响应里只剩该状态的条目，产品总数要么另存一份「上次未过滤的总数」（自刷新下会与当前数据代不同步），要么再发一次请求；而本地过滤**一次请求都不多发、零竞态**，筛选切换也不会出现「旧行还在、新行未到」的中间态。

- 改动范围：前端 1 个模块（`ProjectsPage.tsx`）+ 4 份文档（屏规格 1、tech 文档 2、本方案 1）；**0 接口、0 表、0 迁移、0 新依赖、0 新组件**。
- 工期与任务：本方案只给顺序与依赖，拆卡见 breakdown 卡。
- 最大风险：§11 R1（单页 50 条截断，既有局限，本期不承诺完整性）。

### 1.2 三种做法对照（含「不做」）

| 选项 | 用户能否看到「新建项目排最前」 | 筛选能力 | 改动面 | 结论 |
|---|---|---|---|---|
| 维持现状 | 否（`echuan-1…13` 升序，§3.1） | 无控件 | 0 | 不做 = issue #133 两条诉求都不解决，否决 |
| **本方案（前端排序 + 本地过滤）** | 是（按 `seq_no` 降序） | 有（3 选项，0 请求切换） | 1 模块 + 4 文档 | **选它** |
| API `id DESC` + 前端带上 `status` 重拉 | 是 | 有 | 后端 + `kit` + 2 份 tech 文档 + 键集方向翻转 | 收益为零、不可逆，另立 ADR 才能做 |

## 2 目标与非目标

目标（PRD §2 原样继承，均为可判定口径）：

| 编号 | 目标 | 判定口径 | 基线 |
|---|---|---|---|
| G1 | 「全部状态」下表格首行 = 该产品 `create_time` 最大的项目 | 验收抽样 3/3 首行为最新 | 首行 = 最老（`echuan-1`），命中 0/1 |
| G2 | 选定「进行中」/「已关闭」后，表格每行状态均等于所选，行数 = 该产品该状态项目数 | 3 个选项各抽样 1 次，与只读 API 集合 0 差异 | 无筛选控件 |

非目标（每条对应一次本方案明确拒绝的实现冲动）：

1. **不改 `GET /v1/projects` 的排序、默认 `limit=50` 与 `next_after_id` 语义**（PRD 非目标 1）。本方案**一个字节的请求都不改**：`api.listProjects(productId)` 的调用形态保持现状，`status` 参数本期不使用。
2. **不新增 `product_id` 服务端过滤、不做翻页**（PRD §9-2）。本方案的筛选只在已返回的单页内成立，超页缺条登记为 R1。
3. **不做排序切换控件、不做全局项目页、不做搜索、不做看板式多条件**（PRD 非目标 2/3/4）。
4. **不动 `api.listProjects` 的签名与本地 `product_id` 过滤**（`web/src/api/client.ts:57-61`）：它对本项目仍是唯一调用方（全仓 grep 仅 `ProjectsPage.tsx:68` 一处），没有第二个消费者需要新语义。
5. **不抽公共筛选常量**：`STATUS_FILTERS`（`web/src/features/products/ProductsPage.tsx:36`）与项目页需要的三选项相同，但本期不把产品页一起改动 —— 为一个 3 行常量给无关屏引入回归面不划算；重复登记为 U5。
6. **不动 `useAutoRefresh`、`ui/DataTable`、`ui/SelectField`、`products.css`/`projects.css` 令牌**：下拉与空行文案都用既有 props。

## 3 AC 覆盖表

来源列：PRD 原文（PRD §6）/ 推断（本方案补充的判定口径，假设进 §11）。

| AC | 原文要点 | 模块 | 接口 | 数据 | 验证方式 |
|---|---|---|---|---|---|
| AC1 | 13 个项目下首行 `echuan-13`、末行 `echuan-1`，整表 `create_time` 新→旧，序号 `#13→#1` | `ProjectsPage`（排序） | `GET /v1/projects`（无参数变化，只作对照） | `projects.seq_no` / `projects.create_time`（只读） | 看界面 + 只读 `GET /v1/projects?limit=100` 对照 `create_time` |
| AC2 | 页头出现 label「按状态筛选」下拉，选项「全部状态/进行中/已关闭」，值「全部状态」 | `ProjectsPage`（页头 `ui/SelectField`） | 无 | 无 | 看界面 |
| AC3 | 选「进行中」→ 只剩 open，行数 = 该产品 open 数（快照 7），首行 `echuan-13` | `ProjectsPage`（本地过滤） | 无（不发请求；对照用 `?status=open`） | `projects.status` | 看界面 + 只读 API 对照集合 |
| AC4 | 选「已关闭」→ 只剩 closed，行数 = 6（`echuan-1,2,5,10,11,12`），顺序仍新→旧 | 同上 | 同上 | `projects.status` | 同上 |
| AC5 | 无 closed 项目的产品选「已关闭」→ 无数据行 + 表内提示，控件仍在、值为「已关闭」，页头数字 0 | `ProjectsPage`（`ui/DataTable` 的 `empty` 槽） | 无 | 无 | 看界面（验收前构造数据） |
| AC6 | 产品下无项目 / 加载中 / 失败：三态页头都不出现筛选 | `ProjectsPage`（控件出现条件） | 失败态来自 `GET /v1/projects` 错误 | 无 | 看界面（可用浏览器离线模拟失败） |
| AC7 | 依次切三态，页头数字 = 表格行数（快照 13/7/6） | `ProjectsPage`（`subtitleOf` 入参改为行数） | 无 | 无 | 看界面 |
| AC8 | 筛选「进行中」时关闭一个测试项目 → 行消失、数字 −1、无错误条、无额外提示 | `ProjectsPage` 写后 `refresh('write')` 派生 | `POST /v1/projects/close?id=`（不变） | `projects.status` | 看界面（只对测试项目执行） |
| AC9 | 点首行开 `echuan-13` 抽屉；点末行「打开看板」导航且不开抽屉 | `ProjectsPage` + `ProjectDetailSheet` | 无 | 无 | 看界面 |
| AC10 | 桌面/平板/<768 三视口行序一致，控件可用 | `ProjectsPage`（`ui/DataTable` 响应式）+ `projects.css`（不改） | 无 | 无 | 三视口截图 |
| AC1′ | 推断：`seq_no` 降序等价于 `create_time` 新→旧（产品内 `seq_no` 唯一且按写锁递增） | 排序键选择 | 无 | `UNIQUE (product_id, seq_no)`（`migrations/0001_init.sql:50,58`） | §3.2 静态证据 + §3.1 线上 13 条单调一致 |

### 3.1 运行时快照（只读，2026-11-23）

`GET /v1/projects?limit=100` → 13 条，`next_after_id=13`，顺序为 `id ASC`：

```text
echuan-1  seq=1  closed  2026-11-21T04:35:49.507Z
echuan-2  seq=2  closed  2026-11-21T14:07:05.386Z
echuan-3  seq=3  open    2026-11-22T06:35:35.515Z
echuan-4  seq=4  open    2026-11-22T07:30:35.271Z
echuan-5  seq=5  closed  2026-11-22T10:54:12.750Z
echuan-6  seq=6  open    2026-11-22T14:34:01.772Z
echuan-7  seq=7  open    2026-11-22T16:43:43.805Z
echuan-8  seq=8  open    2026-11-22T16:47:36.308Z
echuan-9  seq=9  open    2026-11-22T17:49:56.922Z
echuan-10 seq=10 closed  2026-11-23T06:00:29.960Z
echuan-11 seq=11 closed  2026-11-23T08:28:48.321Z
echuan-12 seq=12 closed  2026-11-23T09:05:59.475Z
echuan-13 seq=13 open    2026-11-23T09:48:01.868Z
```

open = `3,4,6,7,8,9,13`（7 条），closed = `1,2,5,10,11,12`（6 条），与 PRD AC3/AC4 快照一致。

### 3.2 排序键为什么用 `seq_no` 而不是 `create_time`

- `projects.seq_no` 有 `CHECK (seq_no >= 1)` 与 `UNIQUE (product_id, seq_no)`（`migrations/0001_init.sql:50,58`），分配发生在写锁内（`internal/store/v3/project.go:19` 注释：`<product_id>-<seq_no>`，seq_no under the write lock）；因此**同一产品内 `seq_no` 严格单调 = 创建顺序**，且天然唯一。
- `create_time` 是字符串（`web/src/api/types.ts:28`）。按字符串比较会在「有小数秒 vs 无小数秒」时出错（`…49Z` 与 `…49.507Z` 的字典序与真实时间序相反），要正确比较得 `Date.parse`，多一层解析与 `NaN` 兜底。
- 结论：主排序键 = `seq_no DESC`；因为产品内唯一，**比较器不需要二级键**（并列不可达）。约束登记在 §8.3 的代码注释要求里：该比较器只在「已按产品过滤」的集合上使用。

## 4 现状与改动点

| 文件 / 模块 | 现状（基线 commit 事实） | 改成什么 | 影响面 |
|---|---|---|---|
| `web/src/features/projects/ProjectsPage.tsx` | `useAutoRefresh({ scope: productId, load: () => api.listProjects(productId) })`（:65-69）；`items = data?.items ?? EMPTY_PROJECTS`（:90）直接交给 `DataTable`（:305），接口顺序即屏上顺序；页头只有「新建项目」（:267-271）；无筛选 state | 增加 `status` state（`'all' \| 'open' \| 'closed'`，默认 `'all'`，`productId` 变化时重置）；`items` → `rows`（先过滤、后排序，均 `useMemo`）；页头 `pj-actions` 里加 `ui/SelectField`；`DataTable` 传 `empty` 文案；`subtitleOf` 的计数入参改成 `rows.length`；`detailProject` 从 `rows` 反查 | 该屏全部用户；请求不变（回归面 = 纯渲染） |
| `web/src/api/client.ts` | `listProjects(productId, status?)` 已本地按 `product_id` 过滤（:57-61） | **不改**（本期不传 `status`，也不加 `limit`） | 无 |
| `internal/store/v3/project.go` / `internal/api/v3_handlers.go` | `ORDER BY p.id ASC`（:92,96）；`after_id`/`next_after_id` 升序键集 | **不改**（D1） | 无 |
| `kit/src/client.ts` / `kit/src/tools/v3.ts` | 透传 `after_id`，消费同一 GET | **不改** | 无；agent 侧顺序保持升序（U6 跟踪是否需要另立 ADR） |
| `web/src/ui/DataTable.tsx` | 已有 `empty?: ReactNode`（:31）并在空表时渲染（:134-135） | **不改** | 无 |
| `design/screens/projects.md` | 页头无筛选（:7），表格无顺序口径（:26 只定义了资料区倒序） | 页头结构补「按状态筛选」；状态矩阵补「筛后为空」行；交互/数据来源补「表格按创建时间新→旧，序号随之递减；接口仍为升序」 | design 侧口径；后来者不再改回升序 |
| `docs/tech/web-ui-redo-v0.1.md` | §7 数据源表：`GET /v1/products`、`GET /v1/projects` 标「无变化」 | 在 `GET /v1/projects` 那行补一句「接口仍升序；项目列表按 `seq_no` 降序呈现，筛选在本地」 | 后来者不会把接口当倒序 |
| `docs/tech/web-ui-102-interaction-fix-v0.1.md` | :187 数据源表记录 `GET /v1/projects` 不支持 `product_id`、前端本地过滤 | 同句补「呈现顺序与筛选也在前端；接口排序未改」 | 同上 |
| `docs/tech/projects-sort-filter-133-v0.1.md` | 不存在 | 本文件 | — |

本次不改：`web/src/hooks/useAutoRefresh.ts`（无需新触发）、`web/src/features/products/ProductsPage.tsx`（见非目标 5）、`web/src/ui/SelectField.tsx`、`projects.css`（若要微调页头间距，属 design 侧 ref 对照，不由本方案开白）。

## 5 总体设计

```text
                     ┌──────────────── ProjectsPage（唯一改动模块）────────────────┐
GET /v1/projects ───▶│ useAutoRefresh(scope=productId)                            │
（不带 status/limit） │   ├─ data.items  = 该产品的项目（api/client 已本地按产品过滤）│
                     │   ├─ items ──filter(status)──▶ filtered ──sort(seq_no DESC)─▶ rows │
                     │   ├─ 页头：SelectField(status) + 数字 = rows.length        │
                     │   └─ 表体：DataTable(rows, empty=「没有符合筛选条件的项目」)│
POST /projects/close │                                                            │
POST /projects/reopen│── 成功 → refresh('write') → 重新取数 → rows 重算 ──────────┘
```

| 组件 | 职责 | 同步/异步 | 失败后的行为 |
|---|---|---|---|
| `useAutoRefresh`（不改） | 30s 自动刷新 + 写后补拉 + 单飞 + 换代丢弃；`scope=productId` | 异步 | 保留上一次 `data`，页面进错误态（`Alert` + 重试）；筛选 state 不被清空 |
| 派生层（新增，页面内纯函数） | `filter(status)` → `sort(seq_no DESC)` → `rows` | 同步、0 请求 | 无失败路径（纯计算）；`status` 为非枚举值不可能出现（选项由常量给出） |
| `ui/SelectField`（不改） | 3 选项下拉，label「按状态筛选」，不 `clearable` | 同步 | 无 |
| `ui/DataTable`（不改） | 渲染 `rows`；空表渲染 `empty` 槽 | 同步 | 无 |
| 写操作（既有 `ProjectWriteModal` 流程，不改） | 关闭/重开 → `refresh('write')` | 异步 | 既有错误条 + 重试；列表保留旧数据 |

不新增数据流、不新增缓存层、不新增请求。**筛选与排序都在一次成功响应之后完成**，因此不存在「筛选结果比列表新/旧」的第二份真相。

## 6 数据模型

### 6.1 变更：无

本期不改表、不加列、不加索引、不写迁移，因此无 up/down SQL。依赖的既有约束（只读引用，用于支撑 §3.2 与 §8.3）：

| 对象 | 定义（基线事实） | 本方案为什么依赖它 |
|---|---|---|
| `projects.seq_no` | `INTEGER NOT NULL CHECK (seq_no >= 1)`（`migrations/0001_init.sql:50`） | 排序键，产品内唯一且随创建递增 |
| `UNIQUE (product_id, seq_no)` | `migrations/0001_init.sql:58` | 保证「产品内 `seq_no` 降序」是比较全序，无需二级键 |
| `INDEX ix_projects_product (product_id, seq_no)` | `migrations/0001_init.sql:61` | 仅说明「按产品+序号」查询有索引支撑；本期不在服务端按它排序 |
| `projects.status` | 枚举 `open` / `closed`（`internal/domain/enum`，`ParseProjectStatus`） | 筛选值的唯一来源；前端选项 `all/open/closed` 与之一一对应，`all` 是前端概念，不下发 |

### 6.2 被否决选项需要的东西（写在这里，便于将来 ADR 复用）

若将来选 API 倒序，最小改动是两条 SQL（`internal/store/v3/project.go:92,96` 改 `WHERE p.id < ? ORDER BY p.id DESC`）加 `next_after_id` 取本页最小 id（`internal/api/v3_handlers.go:257-259`），**迁移：无**（无 schema 变更），**回滚：revert 这两个文件**；但对外语义（`after_id` 方向、`next_after_id` 含义）随之翻转，必须同步 `kit` 与两份 tech 文档，且外部消费方会被静默改变 —— 这是 D1 不选它的原因，不是不能做。

## 7 接口契约

**本期不改任何接口。** 以下是本页依赖的契约，写清楚是为了区分「不变的」与「可能会被误改的」。

### GET /v1/projects

- 用途：项目列表唯一数据源（本页只发一次，不带 `status`、不带 `limit`、不带 `after_id`）。
- 鉴权：与其它 `/v1` 一致，由部署侧鉴权/网关决定，本页不新增鉴权分支；401/403 由壳层既有错误处理，列表落错误态。
- 幂等：GET，天然幂等。
- 限流：本页消耗与现状相同（30s 一次 + 写后补拉，`useAutoRefresh` 单飞），不新增请求类型。
- 请求参数（不变的现状）：`status`（`open|closed`，非法值 → 400 `invalid_argument`，`v3_handlers.go:235-243`）、`after_id`（`p.id > ?` 键集）、`limit`（≤0 时取 50，`internal/store/v3/project.go:87-89`）。前端**不传**前三个。
- 响应：`items[]`（`project_id/product_id/seq_no/name/context_md/status/create_time/update_time/closed_at`）+ `next_after_id`（本页最后一条 id，`v3_handlers.go:257-259`），**顺序 `p.id ASC`**。
- 本页依赖的不变量（须在方案评审时确认，任一改变都要重看本方案）：
  1. 顺序恒为 `id ASC`，不做前端以外的排序；
  2. 单次返回上限 50，无 `product_id` 过滤，前端只本地按 `product_id` 过滤（`web/src/api/client.ts:57-61`）；
  3. `seq_no` 在产品内唯一且随创建递增（§6.1）。

### POST /v1/projects/close?id=&force=1 / POST /v1/projects/reopen?id=

- 不变。成功 204 无 body；关闭遇 409（有未完成任务）→ 前端同一弹窗切「取消 N 个任务」第二态，`force=1` 重发。前端幂等策略：一次点击一个请求，靠 `busy` 禁重复（既有）。
- 对本期的影响：写成功后 `refresh('write')`，`rows` 重算；若该行不再命中当前筛选，行消失、数字减一（AC8），无额外提示。

## 8 关键流程

### 8.1 正常路径

1. 进入 `#/products/:productId/projects` → `useAutoRefresh` 首拉（`scope=productId`，`manual`）→ `GET /v1/projects`（不带 status）。
2. `api.listProjects` 按 `product_id` 过滤 → `items`（该产品项目，接口 id 升序）。
3. 派生：`status='all'` → 不过滤；否则 `items.filter(p => p.status === status)`。随后复制数组按 `seq_no` 降序排序 → `rows`。
4. 页头：`items.length > 0 && !loading && error === ''` 时渲染 `ui/SelectField`（label「按状态筛选」，值 = `status`）；副标题数字 = `rows.length`。
5. 表体：`error` → `Alert` + 重试；`loading` → 3 个 `SkeletonCard`；`items.length === 0` → 既有 `EmptyState`（文案不变）；否则 `DataTable(rows, empty="没有符合筛选条件的项目")`。
6. 人切换筛选 → `setStatus(...)` → **0 请求**，`rows` 立即重算；不进入 loading、不改 URL、不写 localStorage。
7. 人点行 → `setDetailId(project_id)` → 抽屉从 `rows` 反查当前行（`0` 请求）。
8. 行内「关闭/重开」→ 既有 `ProjectWriteModal` 流程 → 成功 `refresh('write')` → `items` 更新 → `rows` 重算。

### 8.2 异常路径

| 场景 | 检测方式 | 处理 | 最终状态 |
|---|---|---|---|
| 首拉失败 | `useAutoRefresh` 置 `error` | `Alert`（`GET /v1/projects`）+ 重试；页头不出现筛选（PRD AC6） | 错误态；`status` state 保留 |
| 自动刷新失败（已有数据） | 同上，`dataRef` 非空 | 保留上次 `items` → `rows` 不变；错误条文案为「自动刷新失败，正在重试」；退避重试 | 列表可用、数字不跳 |
| 写操作失败（404/5xx/断网/另一种 409） | 既有 `isUnfinishedConflict` 等判定 | 既有 `ErrorBar` + 重试；行状态不变，`rows` 不重算 | 列表与筛选保持 |
| 筛选切换瞬间 | 无网络参与 | 同步重算；无「旧行残留」窗口 | — |
| 跨产品切换（`productId` 变化） | `scope` 换代 | `useAutoRefresh` 换代重拉；**同时把 `status` 重置为 `'all'`**（`useEffect([productId])`） | 新产品的列表从「全部状态」开始 |
| 抽屉打开中行被筛掉 / 被关闭 | `rows` 反查不到该 id | 抽屉自动关闭（沿用既有按 id 反查机制；反查源从 `items` 改为 `rows`，与 PRD §4 边界 2 一致） | 无幽灵数据 |
| 产品下项目数 ≥ 单页上限 | 无（前端不可判） | 不做检测、不提示；只渲染返回页内命中项（R1） | 已知局限 |
| 重复提交关闭/重开 | 既有 `busy` | 按钮 busy、二次确认 | 与现状一致 |

### 8.3 派生规则（实现须逐条对上，不留解释空间）

```text
state: status: 'all' | 'open' | 'closed' = 'all'      // 仅组件内；不落 URL、不落存储
effect: productId 变化 → status = 'all'                // 路由元素复用（web/src/App.tsx:74），不重置会跨产品残留
items  = data?.items ?? EMPTY_PROJECTS                 // 已按 product_id 过滤（api/client.ts:57-61）
rows   = useMemo(
           () => sortBySeqDesc(status === 'all' ? items : items.filter(p => p.status === status)),
           [items, status])
```

- `sortBySeqDesc`：**先复制再排序**（`[...list].sort(...)`），不改 `data.items` 的引用顺序；比较器 `(a, b) => b.seq_no - a.seq_no`，并列返回 `0`。
- 比较器的使用约束（写成代码注释）：只在「已按产品过滤」的集合上使用；`seq_no` 仅在产品内唯一，跨产品复用必须补二级键。
- 控件出现条件三合一：`!loading && error === '' && items.length > 0`。**不能用 `rows.length`** —— 否则筛后为空时控件消失，用户回不到「全部状态」（PRD §4 硬要求）。
- 页头数字：`subtitleOf(loading, error !== '', owner, rows.length)`；`loading`/`error` 分支不报数（现状不变）。
- 空态分层：`items.length === 0` → 既有 `EmptyState`；`items.length > 0 && rows.length === 0` → `DataTable` 的 `empty` 槽（文案「没有符合筛选条件的项目」，无「清除筛选」按钮 —— 见 U1）。

## 9 部署与配置

- 新增依赖：**无**（不新增 npm 包、不新增服务、不改 `pnpm-lock.yaml`）。
- 环境变量：**无新增**。
- 发布顺序：1) `node web/scripts/style-debt.mjs`（样式债门禁）→ 2) `pnpm --dir web build`（`tsc --noEmit` + `vite build` + `check-size.mjs` 体积门禁，JS gzip ≤ 344,056 B）→ 3) `make web` 同步产物到 `internal/web/dist`（`go:embed` 源，**必须与源码同一次提交**，否则二进制里是旧前端，见 `Makefile:5-8`）→ 4) `make build` 或 `docker build` → 5) 重启服务。
- 灰度：当前是单实例部署，无按比例灰度通道，**不做灰度**；以「发布说明写明默认顺序变化（PRD §8）+ 首日人工核对 AC1/AC3」替代。
- 回滚：`git revert <实现提交>` → 重跑上面 1)–5)（前端产物与源码一起回退）；或部署侧回滚到上一镜像 tag。**数据回滚：无**（无 schema、无数据写入变化）。
- 不可回滚部分：无。回滚后唯一残留是用户已经形成的「新→旧」预期（信息性，不需处理）。

## 10 可观测性

本改动**不新增日志、指标、告警、埋点**，理由与替代手段如下（不是「以后补」）：

- 本屏无埋点通道（PRD §7 已声明；加埋点需先立项，非目标）。
- 无服务端行为变化（请求字节不变），因此没有任何新的日志字段或指标可加；加一条前端 console 日志既无采集端也无消费者 —— 属于「加了也没人看」，不做。
- 既有可观测面（不改）：`GET /v1/projects` 失败 → 页面 `Alert`（含重试）；写操作失败 → `ErrorBar`；两者都是用户可见信号。
- 自动化的发布前信号（每次构建都会跑）：`web/scripts/style-debt.mjs` 的 7 项判据、`web/scripts/check-size.mjs` 打出的 JS/CSS raw + gzip（阈值 344,056 B）。（本方案**未执行**这两个脚本，见交付报告「怎么自检」。）
- 告警：不新增。理由同上（无新指标）；既有服务级告警不受影响。
- 上线后核对（人工、只读，替代指标）：
  1. `GET /v1/projects?limit=100` 取 `create_time` 最大者，与屏上首行比对（AC1）；
  2. `GET /v1/projects?status=open|closed` 的 `product_id` 子集，与屏上行集合比对（AC3/AC4）；
  3. 出现「顺序异常 / 筛选不生效」类反馈 → 视为回滚信号，按 §9 回滚。
- 观测缺口（登记）：无「筛选使用率」「首行可见率」类行为指标，无法量化收益；要量化须先补埋点（U4）。

## 11 风险与未决

| 编号 | 风险 | 触发信号 | 影响 | 缓解 | 回滚 |
|---|---|---|---|---|---|
| R1 | 本地过滤 + 单页 50 条：全局项目数 > 50 时，某产品的项目或筛选结果缺条 | 全局项目数逼近 50；或用户反馈「某项目不见了」 | 表格与页头数字同时偏小（自洽，但不完整） | 屏规格登记「已知局限」；不承诺完整性（PRD §4-3） | 无（既有局限，非本改动引入）；完整性走 U2 立项 |
| R2 | 默认顺序变化对所有用户可见 | 上线后首日 | 用户以为「项目少了/排乱了」 | 发布说明写入（PRD §8）；序号列与抽屉同源兜住「序号倒序」误读 | revert |
| R3 | 序号列倒序被读成 bug | 反馈「序号错乱」 | 一次解释成本 | 保留 `#N` 与抽屉「序号」同源（`ProjectDetailSheet.tsx:109`），不新增解释文案 | 无 |
| R4 | API 升序 / UI 倒序 口径漂移，后来者把接口改成 DESC 或把前端排序删掉 | code review 出现排序改动 | 双重排序或顺序回归 | `ProjectsPage` 头注释 + 屏规格 + 两份 tech 文档各写一句（§4）；AC1 的对照口径固定 | revert |
| R5 | 错误态页头不出现筛选（PRD AC6），错误期间用户无法改选筛选值 | 网络抖动 | 错误恢复后回到原所选值 | 保留 `status` state 不被错误清空（§8.2）；错误条提供重试 | 无 |
| R6 | 跨产品导航沿用筛选值（路由元素复用，`App.tsx:74`） | 切产品后列表被筛 | 新产品「看起来没有项目」 | `useEffect([productId])` 重置为 `'all'`（§8.3） | 无 |
| R7 | 切产品瞬间仍显示上一个产品的行（`useAutoRefresh` 换代不清 `data`，既有行为） | 快速切换 | 短暂错配 | 本方案不引入、不修（既有），不在本期扩围 | 无 |

未决项：

| 编号 | 未决 | 谁定 | 何时 | 未定前默认动作 |
|---|---|---|---|---|
| U1 | 筛后为空是否加「清除筛选」按钮（看板页有先例，产品列表页无） | design（PRD §9-3） | 实现前 | 按纯提示文案「没有符合筛选条件的项目」，不加按钮（PRD §4） |
| U2 | 产品下项目超单页上限（50）的完整性是否单独立项 | CTO + 产品（PRD §9-2） | 本期验收后一周内 | 登记已知局限、不扩围 |
| U3 | issue #133 是否回帖 / 改标签 | 需求方 / 人工授权 | 确认后 | 不回帖、不改状态 |
| U4 | 是否为本屏补埋点以量化收益 | 产品 + CTO | 下期候选 | 不补；用只读 API + 人工核对（§10） |
| U5 | `STATUS_FILTERS` 在 `ProductsPage` 与本页重复（3 选项），是否抽公共常量 | dev（实现时提） | 随实现 | 本期各自持有；出现第三个消费者再抽 |
| U6 | agent/`kit` 侧是否需要倒序（决定 D1 是否该升级为 ADR） | CTO + 需求方 | 出现需求时 | 保持接口升序（D1） |
| U7 | `design/screens/projects.md` 的 refs 截图是否重出（顺序变化后旧 refs 与屏不一致） | design | 与实现同批 | 由 design 决定；本方案只要求文字口径更新 |
