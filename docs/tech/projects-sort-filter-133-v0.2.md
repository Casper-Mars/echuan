# 项目列表倒序 + 状态筛选 技术方案 v0.2（排序与筛选下沉到后端）

- 状态：待评审（v0.1 被驳回后的返工版）
- 作者：CTO
- 日期：2026-11-23
- 返工原因（评审原文）：**「排序和筛选都需要改后端的接口」**
- 输入：PRD `prd-issue-133-projects-sort-filter-v0.1.md`（级别 m）+ 需求卡决策简报 + [issue #133](https://github.com/Casper-Mars/echuan/issues/133) + 本 ADR [ADR-0002](../decisions/ADR-0002-projects-list-order-filter-api.md)
- 代码基线：`Casper-Mars/echuan@6484e3fa85579f1f023dcc9bc7cd46a01a4ad7e3`（`main`，已 `git fetch --prune origin`，工作区与 `origin/main` 一致）
- 运行时证据：只读 `GET /v1/projects?limit=100`（`http://echuan:8720`，2026-11-23，13 条，见 §3.1）

---

## 1 结论

**把顺序与筛选都下沉到 `GET /v1/projects`：排序改 `p.id DESC`（新建在前）、`after_id` 键集方向翻转为 `p.id < ?`、`next_after_id` 改为「取更早一页」的游标，并新增可选 `product_id` 过滤；前端不再排序、不再做状态过滤，只负责把 `product_id`/`status` 传给服务端并渲染结果。**

- 改动范围：后端 2 个文件（`internal/store/v3/project.go`、`internal/api/v3_handlers.go`）+ 契约文件 `api/openapi.yaml` + `kit` 3 处 + 前端 2 个文件（`web/src/api/client.ts`、`web/src/features/projects/ProjectsPage.tsx`）+ 文档 5 份（ADR-0002、本方案、屏规格、2 份 tech 文档）。**0 表、0 迁移、0 新依赖、0 新路由、0 新响应字段。**
- 工期/人日不在本方案（拆卡见 breakdown 卡）。
- 最大风险：R1 —— `after_id`/`next_after_id` 语义翻转会静默改变 `kit`/外部 agent 的翻页方向，需与发布说明、工具描述一起交付（ADR-0002 §5）。
- 契约变更以 ADR-0002 记录（含回滚与退出条件）；PRD 非目标 1（「不改接口排序」）与 §9-1 结论由本方案推翻，需 PM 回写（U0）。

### 1.1 相对 v0.1 的差异

| # | v0.1（已废弃） | v0.2（本方案） | 为什么改 |
|---|---|---|---|
| 1 | 排序在前端（`seq_no DESC`），接口保持 `id ASC` | 排序在 `GET /v1/projects`（`p.id DESC`），前端不排序 | 评审指令：排序改后端接口 |
| 2 | 筛选在前端本地过滤，请求不带 `status` | 筛选走服务端：请求带 `status`；新增 `product_id` 过滤 | 评审指令：筛选改后端接口；`product_id` 是为了让「倒序 + 单页 50」这个组合不产生新的空列表故障（ADR-0002 §3） |
| 3 | 前端一次取数、0 请求切换筛选 | 每次切换筛选换代重拉（`useAutoRefresh` 的 `scope` 带上 status） | 筛选在服务端，必然有往返；用换代丢弃旧代响应保证不串状态 |
| 4 | 页头「筛选出现条件」用未过滤集合的 `items.length` | 用「首次全部状态加载是否见过行」+ 产品 `project_count` 兜底（均来自既有响应，不新增字段） | 服务端筛选后响应只剩命中项，产品总数不再可得 |
| 5 | 契约不动，无需 ADR | 契约变更，必须有 ADR-0002 + `kit`/openapi/文档同步 | 不可逆决策必须有 ADR 与评审记录 |

### 1.2 三种做法对照（含「不做」）

| 选项 | 首行=最新 | 筛选 | 全局超 50 时 | 结论 |
|---|---|---|---|---|
| 维持现状（`ASC`、无产品过滤、前端无控件） | 否 | 无 | 新建项目落在第 51 条后，看不见 | 否决 |
| 只翻 `DESC`，不补 `product_id` | 是 | 服务端 `status` | 老产品可能**整表为空**（看最新 50 条里恰好没有它） | 否决（ADR-0002 §3） |
| **`DESC` + `product_id` + `status`（本方案）** | 是 | 服务端 | 该产品最新 50 条：只缺最旧，主诉求恒成立 | **选它** |

## 2 目标与非目标

目标（PRD §2 继承）：G1「全部状态」下首行 = 该产品 `create_time` 最大者（基线 0/1，目标 3/3）；G2 选「进行中/已关闭」后每行状态等于所选、行数 = 该产品该状态数（目标 0 差异）。

非目标（明确不做的实现冲动）：

1. **不做 UI 分页/「加载更多」**：`next_after_id` 语义翻转后已可支撑，但本期不接（PRD §9-2 延后，U2）。
2. **不加 `total` / `has_more` 字段**：页头数字 = 表格行数（AC7），控件可见性用「见过行 + `project_count`」兜住，不为一个 UI 布尔扩契约。
3. **不加索引**（ADR-0002 C5）：现有 `ix_projects_product` 足够，未验证的优化不做。
4. **不做 `order` 参数/新端点/双序兼容**：本期无升序消费方（ADR-0002 §3、§6 退出条件）。
5. **不做排序切换控件、全局项目页、搜索、多条件筛选**（PRD 非目标 2/3/4）。
6. **不动 `product_id` 的大小写/别名语义**：按业务 id 精确匹配，与 `GET /v1/projects?id=` 同口径；`project_id` 与 `product_id` 不混用。
7. **不在前端二次排序/过滤状态**：顺序与集合只有一个真相（服务端）；唯一的例外是 §4 的防御性 `product_id` 再过滤（部署窗口防线，不是筛选机制）。

## 3 AC 覆盖表

来源：PRD 原文（AC1–AC10，本方案不改 AC）/ 本方案新增的后端验收项（标注「新增」）/ 推断（标注）。

| AC | 要点 | 模块 | 接口 | 数据 | 验证方式 |
|---|---|---|---|---|---|
| AC1 | 13 条下首行 `echuan-13`、末行 `echuan-1`，序号 `#13→#1` | `ProjectsPage`（渲染服务端顺序） | `GET /v1/projects?product_id=echuan` | `projects.id`/`create_time` | 界面 + `curl` 对照顺序 |
| AC2 | 页头「按状态筛选」下拉，三选项，值「全部状态」 | `ProjectsPage`（`ui/SelectField`） | 同上（不带 status） | 无 | 界面 |
| AC3 | 选「进行中」→ 7 条、首行 `echuan-13` | `ProjectsPage`（换代重拉） | `?product_id=echuan&status=open` | `projects.status` | 界面 + API 对照 |
| AC4 | 选「已关闭」→ 6 条（1,2,5,10,11,12） | 同上 | `?product_id=echuan&status=closed` | `projects.status` | 界面 + API 对照 |
| AC5 | 无 closed 的产品选「已关闭」→ 0 行 + 表内提示、控件仍在、数字 0 | `ProjectsPage` + `DataTable` 的 `empty` 槽 | 同上 | 无 | 界面（验收前构造） |
| AC6 | 产品无项目 / 首屏加载 / 首屏失败：三态页头不出现筛选 | `ProjectsPage`（子态规则 §8.3） | 失败来自 GET | 无 | 界面（离线模拟） |
| AC7 | 三态页头数字 = 表格行数（13/7/6） | `ProjectsPage`（`subtitleOf`） | 三次请求各自的行数 | 无 | 界面 |
| AC8 | 筛「进行中」时关闭测试项目 → 行消失、数字 −1、无错误条 | 写后 `refresh('write')` | `POST /v1/projects/close?id=`（不变） | `status` | 界面（只动测试项目） |
| AC9 | 点首行开 `echuan-13` 抽屉；点末行「打开看板」导航且不开抽屉 | 页面 + 抽屉 | 无 | 无 | 界面 |
| AC10 | 三视口行序一致、控件可用 | `DataTable` 响应式 | 无 | 无 | 三视口截图 |
| B1（新增） | 排序由服务端保证：`ORDER BY p.id DESC` | `internal/store/v3/project.go` | `GET /v1/projects` | `projects.id` | `go test ./internal/store/v3` 倒序用例 + `curl` |
| B2（新增） | `product_id` 过滤：只返回该产品；空串视同未传；不存在 → 空 items（非 404） | store + handler | `GET /v1/projects?product_id=` | `projects.product_id` | 单测三种入参 + `curl` |
| B3（新增） | `after_id` 向下翻页：`p.id < ?`，`next_after_id` = 本页最后一条（最小）id | store + handler | `GET /v1/projects?after_id=` | `projects.id` | 单测两页连续取，无重复无遗漏 |
| B4（新增） | `product_id` + `status` 组合过滤 | store + handler | 同上 | 两列 | 单测组合用例 |
| B5（推断） | 契约测试不受影响（operation 集合不变，只加 query 参数） | `internal/contract` | — | — | `make contract` 通过 |
| B6（推断） | `kit` 侧顺序与翻页方向与新语义一致 | `kit/src/client.ts`、`kit/src/tools/v3.ts` | 同上 | — | `pnpm --dir kit build`（或仓库既有 kit 构建/测试入口）+ 工具描述人工核对 |

### 3.1 运行时快照（只读，2026-11-23）

`GET /v1/projects?limit=100` → 13 条，`next_after_id=13`，当前顺序 `id ASC`（**改动后应为 `echuan-13 … echuan-1`**）：

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

open = `3,4,6,7,8,9,13`（7 条），closed = `1,2,5,10,11,12`（6 条）；`id` 升序与 `create_time` 升序一致，因此 `id DESC` 即「创建时间新→旧」（AC1 口径成立）。

## 4 现状与改动点

| 文件 / 模块 | 现状（基线事实） | 改成什么 | 影响面 |
|---|---|---|---|
| `internal/store/v3/project.go` | `ListProjects` 两分支恒 `WHERE p.id > ? … ORDER BY p.id ASC`（:92,:96）；`limit<=0` 取 50（:87-89） | 两分支改 `WHERE p.id < ? … ORDER BY p.id DESC`；新增 `productID string` 入参，非空时追加 `AND pr.product_id = ?`（**用既有 JOIN 的 `products` 表**：`projects.product_id` 是 `INTEGER REFERENCES products(id)` 内部外键，见 `migrations/0001_init.sql:49`；业务 id 在 `pr.product_id`） | 所有 `GET /v1/projects` 消费者；`after_id` 方向翻转 |
| `internal/api/v3_handlers.go` | 解析 `status`/`after_id`/`limit`（:235-253）；`next_after_id` = 本页最后一条（:257-259） | 解析可选 `product_id`（`strings.TrimSpace`，空串→不传）；透传到 `ProjectFilter`；`next_after_id` 逻辑不变（仍是「本页最后一条 id」，但语义变为取更早） | 同上 |
| `internal/app/v3/project.go` | `ProjectFilter{Status, AfterID, Limit}` | 增加 `ProductID string` 并透传 | 同上 |
| 测试 `internal/store/v3/project_test.go`、`internal/app/v3/project_test.go` | 只断言条数/状态，不断言顺序 | 新增：倒序断言、`product_id` 过滤（含空串/不存在）、`after_id` 向下翻页两页无重叠、`status`+`product_id` 组合 | 回归基线 |
| `api/openapi.yaml` | `get /v1/projects` **无 parameters 段**，summary 仅「List projects or fetch one by id」 | 补 `parameters`（`id`/`status`/`after_id`/`limit`/`product_id`）；summary/description 写明「newest first」「`next_after_id` 取更早一页」「`product_id` 为过滤参数」 | `internal/contract` 测试仍通过（operation 集合不变） |
| `kit/src/v3-types.ts`、`kit/src/client.ts`、`kit/src/tools/v3.ts` | `listV3Projects({status, after_id, limit})`；`echuan_project_list` 工具描述「可按 open/closed 过滤」 | 增加 `product_id` 透传；工具描述改为「按新建倒序（最新在前），可按 product_id/status 过滤，after_id 取更早一页」 | agent 侧行为可见变化（R1） |
| `web/src/api/client.ts` | `listProjects(productId, status?)` 本地按 `product_id` 过滤（:57-61） | 改为 `listProjects({ productId, status })` → 请求带 `product_id`/`status`，返回服务端结果；**保留 1 行按 `product_id` 防御性再过滤**（C3） | 该屏；调用方仅此一处 |
| `web/src/features/projects/ProjectsPage.tsx` | `load: () => api.listProjects(productId)`（:68）；`items` 直接进表格；无筛选 state | 加 `status` state；`scope = productId|status`；`load` 返回 `{key, items}`；页头加 `SelectField`；`DataTable` 传 `empty`；数字 = 行数；抽屉从「当前代」行反查 | 该屏全部用户 |
| `design/screens/projects.md` | 页头无筛选（:7）；无表格顺序口径；数据来源写 `GET /v1/projects?product_id=`（:20） | 页头补筛选、状态矩阵补筛后为空、补「新→旧」与「接口已倒序」 | design 口径 |
| `docs/tech/web-ui-redo-v0.1.md` §7、`docs/tech/web-ui-102-interaction-fix-v0.1.md`:187 | 记录「不支持 product_id、前端本地过滤」 | 改为「服务端支持 product_id，顺序为 id DESC」 | 后来者口径 |
| `docs/decisions/ADR-0002-…md`、本方案 | 不存在 | 新增 | 契约变更留痕 |

本次不改：`migrations/`（无 schema 变更）、`web/src/hooks/useAutoRefresh.ts`（`scope` 已支持换代）、`web/src/ui/*`、`products` 相关屏、`GET /v1/products`（`project_count` 已是既有字段，`internal/store/product.go:24` 相关子查询）。

## 5 总体设计

```text
浏览器 ProjectsPage
  state: status('all'|'open'|'closed')
  useAutoRefresh(scope = `${productId}|${status}`)
      │  load: GET /v1/projects?product_id=<pid>[&status=open|closed]
      ▼
  handler(listOrGetProject) ── 解析 status/after_id/limit/product_id
      ▼
  ProjectService.ListProjects(ProjectFilter{Status, ProductID, AfterID, Limit})
      ▼
  store/v3.ListProjects
      SELECT … FROM projects p JOIN products pr ON pr.id = p.product_id
        WHERE p.id < ? [AND p.status = ?] [AND pr.product_id = ?]
      ORDER BY p.id DESC LIMIT ?      ← 顺序唯一真相
      ▼
  响应 { items[]（新→旧）, next_after_id = 本页最后一条 id（更早一页游标） }
      ▼
  前端：只渲染；若响应代的 key ≠ 当前 key → 表体骨架（控件保留）
  写操作（close/reopen，不变）→ refresh('write') → 同代补拉
```

| 组件 | 职责 | 同步/异步 | 失败后的行为 |
|---|---|---|---|
| `GET /v1/projects`（改） | 唯一的顺序与筛选真相 | 同步 | 400（`status` 非法）/ 500；前端落错误条 |
| `ProjectsPage` 派生层（改） | 请求参数、子态判定、数字与空态分支 | 同步 | 重拉失败保留上一次数据 + 错误条 |
| `ui/SelectField` / `ui/DataTable`（不改） | 下拉 / 渲染 + `empty` 槽 | 同步 | — |
| `useAutoRefresh`（不改） | `scope` 换代、单飞、丢弃旧代响应 | 异步 | 保留旧数据、退避重试 |
| 写流程（不改） | 关闭/重开 → `refresh('write')` | 异步 | 既有错误条 + 重试 |

前端不再持有「排序规则」与「状态过滤规则」，只持有「当前筛选值」和「当前这一代响应」。这是本方案能被评审验收的核心：**删掉前端排序与状态过滤后，AC1–AC10 全部由服务端的顺序与集合直接满足。**

## 6 数据模型

**无变更**：不改表、不加列、不写迁移，故无 up/down SQL；存量数据无需回填。

依赖的既有结构（只读引用）：

| 对象 | 定义 | 本方案为什么依赖 |
|---|---|---|
| `projects.id` | 自增主键，插入即分配（`migrations/0001_init.sql` projects 表） | 顺序键：`id DESC`；`after_id` 键集游标 |
| `projects.product_id` | **`INTEGER NOT NULL REFERENCES products(id)`**（`migrations/0001_init.sql:49`）——内部外键，不是业务 id；业务 id 在 `products.product_id`（`TEXT NOT NULL UNIQUE`，:17） | 过滤条件必须写在既有 JOIN 的 `pr.product_id` 上；`projects p JOIN products pr ON pr.id = p.product_id` 已是 `projectFrom` 的现有形态（`internal/store/v3/project.go:16`） |
| `projects.status` | 枚举 `open`/`closed` | 既有筛选条件 |
| `ix_projects_product (product_id, seq_no)` | `migrations/0001_init.sql:61`（`product_id` 是内部 id） | 不用于本次过滤（本次按 `pr.product_id` 走 `products` 的唯一索引再 JOIN）；不加新索引（ADR-0002 C5） |

查询形态与访问路径（供 dev 落地时对齐，不要求照抄写法）：

```sql
-- 无 status / 有 product_id
SELECT … FROM projects p JOIN products pr ON pr.id = p.product_id
 WHERE p.id < ? AND pr.product_id = ? ORDER BY p.id DESC LIMIT ?
-- 有 status / 有 product_id
SELECT … FROM projects p JOIN products pr ON pr.id = p.product_id
 WHERE p.id < ? AND p.status = ? AND pr.product_id = ? ORDER BY p.id DESC LIMIT ?
-- after_id 缺省 = 0：因 p.id >= 1，p.id < 0 恒假 → 必须用「哨兵」
```

**实现细则（防止一个具体的坑）**：`after_id` 缺省时现状是 `p.id > 0`（恒真）。方向翻转后 `p.id < 0` 恒假，会把首页返回空。两条可行做法，dev 任选其一并在代码注释写明理由：① 缺省时用哨兵 `after_id = math.MaxInt64`（`p.id < MaxInt64` 恒真）；② SQL 片段按「是否提供 after_id」条件拼接（`WHERE 1=1` 起手，不给就不加该条件）。**方案要求：首页必须返回第一页，且不得引入用户可控的 `after_id` 语义歧义。**

## 7 接口契约

### GET /v1/projects（本方案唯一变更的接口）

- 鉴权：同其它 `/v1`，由部署侧决定；本屏不新增鉴权分支，401/403 由壳层既有错误处理。
- 幂等：GET，天然幂等。
- 限流：无单独限流；调用频率 = 30s 自动刷新（`REFRESH_INTERVALS.projects = 5s × 6`）+ 写后补拉 + 每次筛选切换 1 次，均由 `useAutoRefresh` 单飞串行。
- 请求参数：

| 字段 | 类型 | 必填 | 约束 / 语义（变更处标注） |
|---|---|---|---|
| `id` | string | 否 | 有 `id` 时走「取单个」分支，与列表参数互斥（现状不变） |
| `status` | string | 否 | `open` \| `closed`；非法值 → 400 `invalid_argument`（现状不变） |
| `product_id` | string | 否 | **新增**。按产品业务 id 过滤；`TrimSpace` 后空串视同未传；不存在的值 → 200 + 空 `items`（不 404） |
| `after_id` | int64 | 否 | **语义变更**：现在表示「取 id 小于该值的下一页」；缺省 = 第一页 |
| `limit` | int | 否 | 缺省 50（≤0 同缺省）；上限沿用现状 |

- 响应（字段集合不变）：

| 状态 | 字段 | 说明 |
|---|---|---|
| 200 | `items[]` | 项目列表，**顺序 `p.id DESC`（新建在前，语义变更）**；字段：`project_id/product_id/seq_no/name/context_md/status/create_time/update_time/closed_at` |
| 200 | `next_after_id` | **语义变更**：本页最后一条（最小）id，作为「取更早一页」的 `after_id`；本页为空时与现状一样不提供有效游标 |

- 错误码：

| HTTP | 业务码 | 触发条件 |
|---|---|---|
| 400 | `invalid_argument` | `status` 不是 `open`/`closed`（现状不变）；`limit` 非整数 |
| 404 | `not_found` | 仅 `?id=` 分支：项目不存在（现状不变） |
| 500 | `internal` | 存储层错误 |

- 不变量（评审须确认，任一改变要重看本方案）：① 顺序恒 `id DESC`；② `after_id` 为「取更小 id」；③ `next_after_id` = 本页最后一条 id；④ 默认 `limit=50`；⑤ `product_id`/`status` 可组合。

### POST /v1/projects/close?id=&force=1 / POST /v1/projects/reopen?id=

不变（成功 204；关闭 409 → 前端同弹窗切「取消 N 个任务」→ `force=1` 重发；一次点击一个请求 + `busy` 禁重复）。

## 8 关键流程

### 8.1 正常路径

1. 进入 `#/products/:productId/projects` → `scope = productId|all` → 首拉 `GET /v1/projects?product_id=<pid>`（不带 status）。
2. 服务端过滤 + `id DESC` → 返回该产品项目（新→旧）。前端按 `product_id` 防御性再过滤（1 行，C3），得到 `items`。
3. 页头：`SelectField`（label「按状态筛选」，值 = `status`）；数字 = `items.length`。
4. 表体：首屏 loading → 3 个 `SkeletonCard`；首屏 error → `Alert` + 重试；`status === 'all' && items.length === 0` → 既有 `EmptyState`（产品下无项目）；`status !== 'all' && items.length === 0` → `DataTable` 的 `empty` 槽「没有符合筛选条件的项目」；否则 `DataTable(items)`。
5. 切换筛选 → `setStatus` → `scope` 变化 → 换代重拉 `GET /v1/projects?product_id=<pid>&status=…`；旧代响应在途则整笔丢弃（`useAutoRefresh` 既有语义）。
6. 点行 → 从**当前代**的行反查 `project_id` 打开只读抽屉（0 请求）。
7. 行内「关闭/重开」→ 既有 `ProjectWriteModal` → 成功 `refresh('write')` → 重拉 → 行按新状态出现/消失，数字随之变化。

### 8.2 异常路径

| 场景 | 检测方式 | 处理 | 最终状态 |
|---|---|---|---|
| 首屏加载失败 | `data === undefined && error !== ''` | `Alert`（`GET /v1/projects`）+ 重试；**页头不出现筛选**（AC6） | 错误态 |
| 切换筛选后重拉失败 | 已有数据 + `error !== ''` | 保留上一次结果 + 错误条（「自动刷新失败，正在重试」）；**筛选控件保留**，用户可改回「全部状态」 | 可用，数据可能旧 |
| 首屏加载中 | `data === undefined && error === ''` | 骨架 + 无筛选控件（AC6） | 加载态 |
| 切换筛选重拉中 | 响应代 key ≠ 当前 key | 表体骨架、**控件保留**（否则用户回不到「全部状态」） | 过渡态（§8.3 子态规则） |
| 旧后端窗口（新前端 + 未升级后端） | 后端忽略 `product_id` → 返回他产品项目 | 前端防御性 `product_id` 再过滤兜住，不会串产品（C3） | 功能可用、顺序退化为旧→新 |
| 旧前端窗口（未升级前端 + 新后端） | — | 旧前端本地过滤 + 新后端已倒序 → 顺序已正确、筛选仍可用 | 兼容，无需处理 |
| 跨产品切换 | `scope` 换代 | 换代重拉 + `status` 重置为 `'all'`（路由元素复用，`web/src/App.tsx:74`） | 新产品从「全部状态」开始 |
| 单产品项目 > 50 条 | 前端不可判 | 不检测、不提示，渲染该产品最新 50 条（缺最旧，R3/U2） | 已知局限 |
| 抽屉打开中行被筛掉 / 被关闭 | 当前代行反查不到 id | 抽屉自动关闭（与 PRD §4 边界 2 一致） | 无幽灵数据 |
| 重复提交关闭/重开 | 既有 `busy` | 按钮 busy + 二次确认 | 与现状一致 |

### 8.3 前端子态规则（实现须逐条对上）

```text
status: 'all' | 'open' | 'closed' = 'all'          // 组件内；不落 URL、不落存储
scope  = `${productId}|${status}`                  // 换代键，交给 useAutoRefresh
load   = () => api.listProjects({ productId, status })
         .then(list => ({ key: `${productId}|${status}`, items: list.items }))

current = data?.key === `${productId}|${status}` ? data.items : undefined
firstLoad = data === undefined                      // 从未成功过一次（或已换代但只有旧代数据）
```

- **表体分支**：`error !== '' && data === undefined` → 错误条（无控件）；`data === undefined` → 骨架（无控件）；`current === undefined`（重拉中）→ 骨架行 + 控件保留；否则按 `items` 渲染。
- **控件可见**：`!firstLoadError && (hasEverSeenRows || productCountVisible)`，其中 `hasEverSeenRows` = 本次进入该产品后「全部状态」成功响应是否出现过 ≥1 行（首次加载恒为 `'all'`，所以它等价于「该产品至少有一个项目」）；`productCountVisible` = `GET /v1/products?id=` 返回的 `project_count > 0`（既有字段 `internal/api/products.go:65`，随页头取名请求一起拿到，失败则为 `undefined`）。
- **数字**：`subtitleOf(firstLoad, firstLoadError, owner, current?.length ?? 0)`；首屏两态不报数（现状）。
- **空态分层**：`status === 'all' && current.length === 0` → 既有 `EmptyState`；`status !== 'all' && current.length === 0` → `DataTable` 的 `empty` 槽（文案「没有符合筛选条件的项目」，不加「清除筛选」按钮，见 U1）。
- **抽屉反查**：`detailProject = current?.find(p => p.project_id === detailId)`（用当前代，不用旧代）。
- **重置**：`useEffect(() => setStatus('all'), [productId])`。

## 9 部署与配置

- 新增依赖：**无**（后端零依赖；前端零新增包；`kit` 无新增依赖）。环境变量：**无新增**。
- 发布顺序（**必须**按此顺序，理由见 §8.2 兼容矩阵）：

  1. 后端：`make test`（含 `internal/store/v3`、`internal/app/v3`、`internal/contract`）→ `make build` → 发布服务。
  2. 前端：`node web/scripts/style-debt.mjs` → `pnpm --dir web build`（`tsc --noEmit` + `vite build` + `check-size.mjs`，JS gzip ≤ 344,056 B）→ `make web` 同步 `internal/web/dist`（须与源码同一次提交，`Makefile:5-8`）→ 重新构建/重启。
  3. `kit`：随仓库发版（工具描述与 `product_id` 透传）。

  为什么后端先行：新前端 + 旧后端只会退化为「顺序旧→新」，且被 C3 的防御性过滤兜住不串产品；反过来（旧前端 + 新后端）也安全。**唯一危险组合是「新前端 + 中间态后端（只翻了 DESC 没加 product_id）」**，因此 `store` 与 `handler` 的 `product_id` 必须同一次提交发布，不得分两次上线。

- 灰度：单实例部署，无按比例灰度通道 → **不做灰度**；以「发布说明（顺序与 `after_id` 语义变化）+ 首日人工核对 AC1/AC3」替代。
- 回滚：`git revert <实现提交>`（store/handler/openapi/kit/web/文档同批）→ 重跑 §9 步骤 1–3。**数据回滚：无**（无 schema、无数据写入变化）。回滚后顺序恢复「老项目在前」、`after_id` 恢复升序语义，需在同一发布说明里说明二次变化。

## 10 可观测性

- 后端：**不新增日志字段、不新增指标、不新增告警**。理由：本次只改查询的 `WHERE`/`ORDER BY` 与一个可选参数解析，没有新的失败模式（`product_id` 不存在的语义是「空结果」，不产生错误日志）；现有 handler 的请求日志与错误日志沿用，字段集不变。
- 前端：**不新增埋点**（PRD §5/§7 明确延后，本屏无采集通道）；既有可观测面不变（`GET` 失败 → 错误条；写失败 → `ErrorBar`）。
- 发布前自动化信号（每次构建都会跑）：`make test`（后端单测 + `internal/contract` 契约测试）、`style-debt.mjs` 7 项判据、`check-size.mjs`（JS gzip 阈值 344,056 B）。**本方案未执行这些命令**（阶段卡不改代码，见交付报告「怎么自检」）。
- 上线后人工只读核对（替代指标）：① `curl /v1/projects?limit=3` 首条应为最新建的；② `?product_id=echuan&status=open` 与界面行集合比对；③ `?after_id=<上页末条>` 返回更早一页且与本页无重叠；④ 出现「顺序异常 / 筛选不生效 / 项目消失」类反馈 → 回滚信号。
- 观测缺口：无列表延迟/命中率指标；「筛选使用率」无法量化。前者若成为性能问题按 U1 加索引并补一个查询耗时观测，后者见 U4。

## 11 风险与未决

| 编号 | 风险 | 触发信号 | 影响 | 缓解 | 回滚 |
|---|---|---|---|---|---|
| R1 | `after_id`/`next_after_id` 语义翻转静默改变 `kit`/外部 agent 的翻页方向 | agent 侧翻页出现重复/遗漏 | 外部消费方行为变化 | ADR-0002 + `kit` 工具描述改写 + 发布说明；仓库内消费方仅 `kit` 一处 | revert（语义再翻转一次） |
| R2 | 中间态部署（新前端 + 只翻 DESC 未加 `product_id` 的后端） | 产品列表出现他产品项目/空列表 | 串产品或空表 | `store`+`handler` 同一次提交；前端 C3 防御性再过滤 | 后端先行回滚 |
| R3 | 单产品 > 50 条时缺最旧 | 该产品项目数逼近 50 | 表格与数字偏小（自洽） | 屏规格登记已知局限；PRD §9-2（U2） | 无（既有局限） |
| R4 | 默认顺序变化对所有用户可见 | 上线首日 | 「项目少了/排乱了」 | 发布说明；序号与抽屉同源兜住「序号倒序」误读 | revert |
| R5 | 切筛选一次往返，出现短暂骨架 | 网络慢 | 观感闪烁 | 控件保留（§8.3）；`useAutoRefresh` 单飞不叠发 | 无 |
| R6 | 首屏失败/加载中不出现控件（AC6）与「切筛选失败后要能改回」冲突 | 网络抖动 | 用户可能被困在某个筛选 | 子态拆分：只有首屏两态隐藏控件（§8.3），重拉失败保留控件 | 无 |
| R7 | `project_count` 与列表不同步（产品详情只在进屏拉一次） | 他处新建项目 | 控件可见性判断短暂滞后 | 控件可见性同时看 `hasEverSeenRows`；数字始终取行数 | 无 |
| R8 | PRD 非目标 1/§9-1 与本方案冲突（原文要求不改接口排序） | 评审对照 PRD | 文档口径打架 | U0：PM 回写 PRD；本方案与 ADR-0002 为准 | 不适用 |

未决项：

| 编号 | 未决 | 谁定 | 何时 | 未定前默认动作 |
|---|---|---|---|---|
| U0 | PRD 非目标 1 与 §9-1 结论回写（本方案推翻） | PM | 方案确认后 | 以本方案 + ADR-0002 为准，PRD 待回写 |
| U1 | 是否加 `projects(product_id, id)` 索引 | CTO | 列表查询耗时 > 50ms 时 | 不加（ADR-0002 C5） |
| U2 | 单产品超 50 条的完整性/分页 | CTO + 产品（PRD §9-2） | 验收后一周 | 不做，登记已知局限 |
| U3 | 仓库外是否有消费方依赖升序 `after_id` | 需求方 / 运维 | 发布前 | 无清单，按「有」对待：发布说明写明 |
| U4 | 是否为本屏补埋点量化收益 | 产品 + CTO | 下期候选 | 不补（PRD §7） |
| U5 | 筛后为空是否加「清除筛选」按钮 | design（PRD §9-3） | 实现前 | 纯提示文案，不加按钮 |
| U6 | `STATUS_FILTERS` 是否抽公共常量（与产品页重复） | dev | 随实现 | 各自持有（不为此改产品页） |
| U7 | `design/screens/projects.md` refs 截图是否重出 | design | 与实现同批 | 由 design 决定；本方案只要求文字口径更新 |

## 12 自检（7 项判据）

1. AC 覆盖：§3 AC1–AC10（PRD 原文，AC 不变）+ B1–B6（新增/推断）四列非空；假设与未决进 §11 —— 通过。
2. 数据模型：§6 明确「无变更、无迁移、无回填」，索引保持不变并给出依据；新增参数依赖的列与索引列到具体位置 —— 通过（无迁移，故无 up/down）。
3. 接口契约：§7 给出参数表（含变更标注）、响应语义、400/404/500、幂等、鉴权、限流、5 条不变量 —— 通过。
4. 关键流程：§8.1 编号主路径 1–7；§8.2 覆盖首屏两态、切筛选失败、两个部署窗口、跨产品、超 50、抽屉消失、重复提交；§8.3 给前端子态规则 —— 通过。
5. 部署：§9 依赖/env/顺序（含「必须后端先行」的理由）/灰度/回滚命令 —— 通过。
6. 观测：§10 给出「不新增」的逐条理由 + 发布前自动化信号 + 上线后只读核对 + 缺口 —— 通过。
7. 范围：§2 七条非目标；全文无超范围项（不做分页、不加字段、不加索引、不做全局页）—— 通过。
