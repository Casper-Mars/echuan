# Web UI V3 四项交互回归修复（issue #102）· 任务拆分总表（8 卡）

- 来源：breakdown 卡 `t_01M7Z7TZKBEP2FR1XWQH9VE5BE` 的计划 JSON（平台据此生成 dev 卡，本文件与其逐项同源）
- 上游依据：技术方案 [`docs/tech/web-ui-102-interaction-fix-v0.1.md`](../tech/web-ui-102-interaction-fix-v0.1.md)、界面契约 `design/`（v0.3）、`docs/decisions/ADR-0001-web-ui-stack.md`
- 分支模型：项目分支 `feature/echuan-2`；每卡分支 `task/<平台任务号>`，PR base 显式 `feature/echuan-2`
- 产物归属：各实现卡不提交 `internal/web/dist`，由收口卡（T8）统一重生成一次
- 本计划不含排期、人日与提交顺序；拆卡不替开发决定文件名/函数名，实现边界以方案 §3.2 为准

## 依赖总表

| seq | 标题 | 依赖 | 优先级 |
|---|---|---|---|
| 1 | 设计契约同步：issue #102 四条的 screens / flows / components 条目 | — | 0 |
| 2 | 产品列表：行点击只读详情抽屉 + 两个抽屉共用的详情字段件 | T1 | 0 |
| 3 | 项目列表：行点击只读详情抽屉（复用壳层字段件） | T1、T2 | 0 |
| 4 | 项目列表：关闭 / 重开入口（先 close 收 409 再 force 二次确认） | T1、T3 | 0 |
| 5 | 看板：已关闭项目的只读拦截与新建任务失败文案 | T1 | 0 |
| 6 | 导航：侧栏与移动 TabBar 下线「看板」入口（含高亮与首页口径） | T1 | 0 |
| 7 | 面包屑：看板 → 项目列表（壳层反查 + 失败降级） | T1、T6 | 0 |
| 8 | 收口：四组回归端到端验收 + 前端产物重生成 | T2、T3、T4、T5、T6、T7 | 0 |

## 依赖图（箭头 = 前置 → 依赖）

```
T1 设计契约
 ├─ T2 产品抽屉(+共用字段件) ── T3 项目抽屉 ── T4 关闭/重开
 ├─ T5 看板：closed 只读
 └─ T6 导航下线看板 ── T7 面包屑
T2,T3,T4,T5,T6,T7 ── T8 收口
```

**并行安全性**：同一文件不在并行分支上被两张卡同时改——`ProjectsPage` 由 T3→T4 串行、`AppFrame` 由 T6→T7 串行、抽屉共用字段件由 T2 先落地。T5 只碰看板屏与 `board.css`，与其余互斥。

## 集成点

1. **T2 → T3**：抽屉共用字段件（壳层）由 T2 落地，T3 只消费，不另起一套排版。
2. **T3 → T4**：同一屏（项目列表）的交互与写操作按序落，避免同行操作列两次改。
3. **T5 与 T4**：T5 的验收需要「已关闭项目」，允许用 `curl POST /v1/projects/close?id=<pid>&force=1` 造态，不阻塞在 T4 之后。
4. **T6 → T7**：`AppFrame` 依次改「导航项/跳转分支」与「面包屑反查」，避免同文件并发。
5. **T8**：端到端 + 产物重生成，是四条 issue 的唯一整体验收点。

## 各卡正文

### T1 设计契约同步：issue #102 四条的 screens / flows / components 条目

- 依赖：—
- 优先级：0

#### 目标
把方案 §7.9① 的契约变更落到 `design/`，使后续实现卡有契约可依——**不允许实现完了让 design 追认**（方案 §7.9）。

#### 范围
- `design/screens/products.md`：组件补 `ui/DetailSheet`；交互写明「行点击 → 只读详情抽屉（6 字段）、行内『查看项目』不开关抽屉」
- `design/screens/projects.md`：交互改「行点击 → 只读抽屉（9 字段）」+ 操作列「打开看板（default）+ 关闭/重开（ghost）」+ 关闭的 409 二次确认；删除末行「不做：关闭/重开项目」
- `design/screens/board.md`：页头补「已关闭标识（closed 项目）」；closed 项目上「新建任务」disabled、空态动作为「返回项目列表」
- `design/flows.md`：F1 第 1 步补「一级导航不含看板；`#/` 仍直达上次项目看板；入口 = 产品 → 项目列表 → 打开看板」；新增 F5「关闭/重开项目」；F1 分支补面包屑按名显示与退化规则
- `design/components.md`：§ui/DetailSheet 用途扩为「任务详情 / 附件预览 / 产品与项目只读详情」；§ui/DataTable 补行点击与阻断冒泡；§ui/SidebarNav 与 §ui/TabBar 项清单改 3 项；新增「屏内私有详情抽屉」小节（`features/shell` 的字段件不属于 ui 导出面）
- `design/tokens.json`：**不动**（方案结论：无新几何/色阶/断点）

#### 任务级验收（可观察）
1. 上述 5 个 md 的关键条目可用 grep 命中：抽屉字段数 6/9、关闭/重开交互、closed 看板标识与 disabled、一级导航 3 项、F5 流程、面包屑降级规则
2. `design/components.md` 总览表仍是 **21 个组件**，未新增/删减
3. `design/tokens.json` 与 implementation 无关的字段零变动（`git diff` 为空）
4. `design/README.md` 的 refs 口径处补一行「issue #102 偏差登记 42 张，待补（认账：设计侧）」
5. `design/MANIFEST.sha256` 与 design 改动同 commit 处理：能复算则更新；口径不可复现（方案 U-06）则保留旧值并在 commit message 与 `design/README.md` 登记，写明认账人与补的时点
6. `node <design-kit>/scripts/check.mjs design` 退出码 0（kit 不可用则报告标「未执行」+ 原因）

#### 不做
- 不重生成 `design/refs/`（42 张登记为偏差，方案 §7.9②）
- 不改 `design/mock/`、不改 `web/src`

**上游依据**：`docs/tech/web-ui-102-interaction-fix-v0.1.md`（技术方案，本仓库）；`design/`（界面契约 v0.3）。
**通用口径**：分支 `task/<平台任务号>`，PR base 显式 `feature/echuan-2`；不提交 `internal/web/dist`（收口卡统一重生成）；推送前 `git fetch origin && git merge origin/feature/echuan-2`，冲突保留双方改动。
**证据纪律**：改前/改后截图各 1 张（暗色基准）+ 3–5 个关键数值；若 8720 上仍是旧产物（`/version` 返 `dev`/`none`，方案 U-01），改用本地 `pnpm --dir web dev` 或新构建镜像验收，并在报告里写明用的方式。

### T2 产品列表：行点击只读详情抽屉 + 两个抽屉共用的详情字段件

- 依赖：T1
- 优先级：0

#### 目标
闭环 issue #102 第 1 条的产品侧（方案 §7.2 D1 / §7.3 D2 / §7.8）：`#/products` 点行开只读详情抽屉；同时把两个抽屉共用的「键值字段网格」落在壳层，供项目抽屉复用。

#### 范围
- 产品列表页：行点击开抽屉（`ui/DataTable` 的 `onRowClick` 已存在、零调用方），行内「查看项目」按钮阻断冒泡；抽屉内容按当前行派生（自动刷新后跟随，不复制对象）
- 抽屉字段 6 项：产品编号 / 名称 / 仓库 / 描述 / 证书 / 项目数（空值口径见方案 §7.2 字段表）
- 新增壳层共用字段件（与 `features/shell/ErrorBar` 同层，**不进 ui 导出面**）
- 只改样式：产品页 CSS + 抽屉字段网格样式，只用令牌

#### 任务级验收（可观察，对应方案 P1 产品侧）
1. 点第 1 行非按钮单元格 → `[role="dialog"]` 出现且含产品名，`location.hash` 仍为 `#/products`
2. 抽屉含 6 个字段标签；`license`/`description` 为空时分别显示「未设置」/「未填写」
3. 点行内「查看项目」→ hash 变 `#/products/<pid>/projects` 且**不出**抽屉（一次点击一个结果）
4. 1440×900 / 1024×768 抽屉 content 宽 720；390×844 全屏且关闭键命中区 ≥44×44；Esc 可关
5. `node web/scripts/ui-imports-check.mjs` 仍 21 个组件；`node web/scripts/style-debt.mjs` 0 超标；`pnpm --dir web build` 通过

#### 不做
- 不加 `ui/` 组件、不改 `ui/index.ts` 与 `ui-imports-check.mjs` 的期望集合
- 不做抽屉 URL 深链（方案 N4）、不做产品编辑/删除（N2）、抽屉内不放任何动作（AC5）

**上游依据**：`docs/tech/web-ui-102-interaction-fix-v0.1.md`（技术方案，本仓库）；`design/`（界面契约 v0.3）。
**通用口径**：分支 `task/<平台任务号>`，PR base 显式 `feature/echuan-2`；不提交 `internal/web/dist`（收口卡统一重生成）；推送前 `git fetch origin && git merge origin/feature/echuan-2`，冲突保留双方改动。
**证据纪律**：改前/改后截图各 1 张（暗色基准）+ 3–5 个关键数值；若 8720 上仍是旧产物（`/version` 返 `dev`/`none`，方案 U-01），改用本地 `pnpm --dir web dev` 或新构建镜像验收，并在报告里写明用的方式。

### T3 项目列表：行点击只读详情抽屉（复用壳层字段件）

- 依赖：T1、T2
- 优先级：0

#### 目标
闭环 issue #102 第 1 条的项目侧（方案 §7.2 D1）：`#/products/:productId/projects` 点行开只读详情抽屉，字段 9 项；复用 T2 落地的壳层字段件，不重复造排版。

#### 范围
- 项目列表页：行点击开抽屉，行内「打开看板」按钮阻断冒泡；抽屉按当前行派生
- 字段 9 项：项目编号 / 产品编号 / 序号 / 名称 / 上下文（`context_md` 用 `ui/MarkdownViewer` 只读渲染，空则给空态文案）/ 状态（沿用本页既有的 open→「进行中」、closed→「已关闭」映射，不新增 StatusPill 变体）/ 创建时间 / 更新时间 / 关闭时间（open 显示 `—`）
- 时间格式沿用本页既有 `MM-DD HH:mm`
- 只改样式：项目页 CSS（抽屉字段网格样式已由 T2 提供，本卡只补差异）

#### 任务级验收（可观察，对应方案 P1 项目侧）
1. 点行 → 抽屉出现，含 9 个字段标签，`location.hash` 不变
2. closed 项目的抽屉：状态显示「已关闭」且「关闭时间」有值；open 项目「关闭时间」为 `—`
3. 点行内「打开看板」→ `#/projects/<pid>/tasks` 且不出抽屉
4. `context_md` 为空的行：上下文区显示空态文案而非空白
5. 三视口几何同上（720 / 全屏 / 关闭键 ≥44）；三道前端门禁与构建通过

#### 不做
- 不放关闭/重开入口（那属于 T4 的行操作列）
- 不改 `ui/` 导出面；不给 `StatusPill` 加项目状态变体

**上游依据**：`docs/tech/web-ui-102-interaction-fix-v0.1.md`（技术方案，本仓库）；`design/`（界面契约 v0.3）。
**通用口径**：分支 `task/<平台任务号>`，PR base 显式 `feature/echuan-2`；不提交 `internal/web/dist`（收口卡统一重生成）；推送前 `git fetch origin && git merge origin/feature/echuan-2`，冲突保留双方改动。
**证据纪律**：改前/改后截图各 1 张（暗色基准）+ 3–5 个关键数值；若 8720 上仍是旧产物（`/version` 返 `dev`/`none`，方案 U-01），改用本地 `pnpm --dir web dev` 或新构建镜像验收，并在报告里写明用的方式。

### T4 项目列表：关闭 / 重开入口（先 close 收 409 再 force 二次确认）

- 依赖：T1、T3
- 优先级：0

#### 目标
闭环 issue #102 第 2 条的写入口（方案 §7.4 D3 / §7.5 D4 / §5.1）：项目列表行操作列提供「关闭 / 重开」，按后端既有 409 + `force=1` 语义落地，不新增错误码、不做预检请求。

#### 范围
- 行操作列：open 行「关闭」、closed 行「重开」（ghost 级；确认弹窗确认键 danger），与既有「打开看板」同列且不抢主操作
- 关闭编排：先 `POST /v1/projects/close?id=`（不带 force）→ 204 收口；409 且 message 命中 `/unfinished:\s*(\d+)\s+unfinished/` → **同一个弹窗切第二态**（「关闭并取消 N 个任务」）→ `force=1`；解析不到 N 时文案降级为「仍有未完成任务」
- 重开：一次确认（明示「已取消的任务不会恢复」）→ `POST /v1/projects/reopen?id=`
- 成功后 `refresh('write')`（复用 `useAutoRefresh` 的写后补拉），行状态与按钮随之切换
- 错误条口径按方案 §9.1：主文案中文，服务端英文内部文案只进 `detail`

#### 任务级验收（可观察，对应方案 P2-1～P2-4）
1. open 行出现「关闭」，closed 行出现「重开」；点「关闭」出现确认弹窗
2. 对**有未完成任务**的项目：第一次确认不写数据、弹窗切第二态且文案含服务端给出的 N；再次确认后项目状态变「已关闭」，其 7 列看板里 `pending/ready/in_progress/in_review` 全部落到「已取消」列
3. 对已 closed 项目再调 close（curl）→ 204（幂等，方案 §5.1）
4. 重开成功后行状态回「进行中」，此前被取消的任务仍在「已取消」列
5. 失败路径：构造 404（不存在的 id）与 5xx（或断网）→ 错误条标题/说明为中文、`detail` 含 `HTTP <status>`，可重试
6. 三道前端门禁与构建通过

#### 不做
- 不做 `GET /v1/tasks?status=` 预检（方案 D4 选项 C：4 次请求 + 竞态）
- 不做 toast（平台无该组件，新增会牵动 ui 导出面）
- 不改后端与 `/v1` 契约；不在看板页头放关闭入口（AC5）

**上游依据**：`docs/tech/web-ui-102-interaction-fix-v0.1.md`（技术方案，本仓库）；`design/`（界面契约 v0.3）。
**通用口径**：分支 `task/<平台任务号>`，PR base 显式 `feature/echuan-2`；不提交 `internal/web/dist`（收口卡统一重生成）；推送前 `git fetch origin && git merge origin/feature/echuan-2`，冲突保留双方改动。
**证据纪律**：改前/改后截图各 1 张（暗色基准）+ 3–5 个关键数值；若 8720 上仍是旧产物（`/version` 返 `dev`/`none`，方案 U-01），改用本地 `pnpm --dir web dev` 或新构建镜像验收，并在报告里写明用的方式。

### T5 看板：已关闭项目的只读拦截与新建任务失败文案

- 依赖：T1
- 优先级：0

#### 目标
按后端**真实拦截面**做最小只读（方案 §7.4）：closed 项目的看板显示「已关闭」标识、禁用「新建任务」、空态动作改为「返回项目列表」；`project` 取数失败时不拦（不做假拦截）。

#### 范围
- 看板页头：closed 时加「已关闭」标识（沿用本仓既有 StatusPill 映射，不新增变体）；「新建任务」disabled
- 空态：closed 项目不再给「新建任务」，改「返回项目列表」
- 新建任务失败路径：请求真的返回 409 `v3: project is closed` 时，错误条主文案改中文（「项目已关闭，请先在项目列表重开」），服务端原文只进 `detail`
- `project` 尚未取到或取数失败时**不禁用**（方案 E9）

#### 任务级验收（可观察，对应方案 P2-5 与 E8/E9）
1. 用 curl 造一个 closed 项目（`POST /v1/projects/close?id=<pid>&force=1`）→ 打开其看板：页头有「已关闭」标识、「新建任务」disabled 且不可聚焦点击
2. 该看板空态动作为「返回项目列表」且点击回 `#/products/<productId>/projects`
3. open 项目的看板：页头与「新建任务」行为与改前一致（对照截图）
4. 让 `GET /v1/projects?id=` 失败（拦截或断网）→「新建任务」仍可用（可观察：按钮非 disabled），点击后若后端 409 则显示中文错误条、`detail` 含原文
5. 三道前端门禁与构建通过

#### 不做
- 不拦评审/删除/重置（后端未拦，前端拦＝假只读，方案 U-02）
- 不做全局只读体系、不改后端

**上游依据**：`docs/tech/web-ui-102-interaction-fix-v0.1.md`（技术方案，本仓库）；`design/`（界面契约 v0.3）。
**通用口径**：分支 `task/<平台任务号>`，PR base 显式 `feature/echuan-2`；不提交 `internal/web/dist`（收口卡统一重生成）；推送前 `git fetch origin && git merge origin/feature/echuan-2`，冲突保留双方改动。
**证据纪律**：改前/改后截图各 1 张（暗色基准）+ 3–5 个关键数值；若 8720 上仍是旧产物（`/version` 返 `dev`/`none`，方案 U-01），改用本地 `pnpm --dir web dev` 或新构建镜像验收，并在报告里写明用的方式。

### T6 导航：侧栏与移动 TabBar 下线「看板」入口（含高亮与首页口径）

- 依赖：T1
- 优先级：0

#### 目标
闭环 issue #102 第 3 条（方案 §7.6 D5）：一级导航不再有「看板」；同时定死四个连带口径——`#/` 仍跳上次项目看板、`/projects/*` 高亮落「产品」、`localStorage['echuan:last-project']` 保留、「更多」二级层不补看板。

#### 范围
- 侧栏（≥768）：3 项 收件箱 / 产品 / 执行节点，顺序不变；平板 64 图标栏与 Tooltip 行为不变
- 移动 TabBar（<768）：3 项 收件箱 / 产品 / 更多；总高与触控口径不变
- 高亮：`/projects/*` 归到「产品」（看板是产品域下钻层）；`/inbox`、`/workers` 不变
- 清理与「看板」导航项绑定的死代码（该项的图标/键位/跳转分支），不留悬空分支
- `#/` 与「返回看板」类动作（收件箱/队列空态、错误边界退出）**不动**

#### 任务级验收（可观察，对应方案 P3-1～P3-4）
1. 1440 与 1024：侧栏 3 项，无「看板」；平板仍是 64 图标栏（量宽 + title 可见）
2. 在 `#/projects/x/tasks` 上侧栏「产品」为当前项（`aria-current`），其余不高亮
3. 390×844：TabBar 3 项，无「看板」；「更多」层仍含「执行节点」与「派单队列」；底部总高与改前一致（57）
4. `#/`：有记忆 → 该项目看板；清掉 `localStorage` → `#/products`
5. 旧书签 `#/projects/x/tasks` 仍可直接打开（方案 E12）
6. 三道前端门禁与构建通过

#### 不做
- 不在「更多」里补「上次看板」（方案 D5 明确不做）
- 不废弃 `last-project` 记忆、不改 `boardPath()` 的 5 个消费者

**上游依据**：`docs/tech/web-ui-102-interaction-fix-v0.1.md`（技术方案，本仓库）；`design/`（界面契约 v0.3）。
**通用口径**：分支 `task/<平台任务号>`，PR base 显式 `feature/echuan-2`；不提交 `internal/web/dist`（收口卡统一重生成）；推送前 `git fetch origin && git merge origin/feature/echuan-2`，冲突保留双方改动。
**证据纪律**：改前/改后截图各 1 张（暗色基准）+ 3–5 个关键数值；若 8720 上仍是旧产物（`/version` 返 `dev`/`none`，方案 U-01），改用本地 `pnpm --dir web dev` 或新构建镜像验收，并在报告里写明用的方式。

### T7 面包屑：看板 → 项目列表（壳层反查 + 失败降级）

- 依赖：T1、T6
- 优先级：0

#### 目标
闭环 issue #102 第 4 条（方案 §7.7 D6）：`#/projects/:pid/tasks` 的面包屑变成「产品 / <产品名> / <项目名>」，中间段挂 `#/products/<productId>/projects`；显式推翻 issue #89 B2 的「面包屑不为名字新增请求」旧口径，代价限定为首次 ≤2 次请求 + 会话内缓存 + 失败静默降级。

#### 范围
- 壳层面包屑：`/projects/*` 产出 产品（→ `#/products`）/ 产品名（→ `#/products/<productId>/projects`）/ 项目名（当前段不可点）；带 `:taskId` 时 4 段
- 反查：`GET /v1/projects?id=` → `GET /v1/products?id=`；按 id 缓存 + 单飞 + 卸载/切路由丢弃迟到结果
- 降级：项目取不到 → 退回今天的「产品 / `project_id`」（无链接、无错误条）；项目取到、产品失败 → 中间段显示 `productId` 且链接仍可用
- 名字缺失时标签用 mono 字体 + `title` 提示原始 ID
- 不改路由形态、不改 `BoardRedirect` / `ErrorBoundary.onExit` / `boardPath()`

#### 任务级验收（可观察，对应方案 P4-1～P4-5）
1. `#/projects/x/tasks`：3 段，中间段是产品名且点进 `#/products/<productId>/projects`，末段项目名不可点
2. `#/projects/x/tasks/<tid>`：4 段
3. 让 `GET /v1/projects?id=` 失败（拦截/断网）→ 降级为「产品 / `project_id`」，无错误条、壳层不塌
4. 让 `GET /v1/products?id=` 失败 → 中间段显示 `productId` 且仍可点进项目列表
5. 请求预算：首次进某项目网络面板新增 ≤2 个 GET；同会话再进同一项目 0 个；同产品下另一个项目 1 个
6. 三道前端门禁与构建通过

#### 不做
- 不改路由形态（方案 D6 选项 C 已否：旧链接失效且仍要反查）
- 不改 `#/products/:productId/projects` 段的显示文案（方案 U-05）
- 不把名字写进 `localStorage` 记忆（D6 选项 B 已否：陈旧风险）

**上游依据**：`docs/tech/web-ui-102-interaction-fix-v0.1.md`（技术方案，本仓库）；`design/`（界面契约 v0.3）。
**通用口径**：分支 `task/<平台任务号>`，PR base 显式 `feature/echuan-2`；不提交 `internal/web/dist`（收口卡统一重生成）；推送前 `git fetch origin && git merge origin/feature/echuan-2`，冲突保留双方改动。
**证据纪律**：改前/改后截图各 1 张（暗色基准）+ 3–5 个关键数值；若 8720 上仍是旧产物（`/version` 返 `dev`/`none`，方案 U-01），改用本地 `pnpm --dir web dev` 或新构建镜像验收，并在报告里写明用的方式。

### T8 收口：四组回归端到端验收 + 前端产物重生成

- 依赖：T2、T3、T4、T5、T6、T7
- 优先级：0

#### 目标
把四条回归作为一个整体验一遍（方案 §2.2 P1–P4 全量），对齐 `web/src` 与 `internal/web/dist`，回收方案里登记但未落地的偏差（方案 §7.9②、U 系列）。

#### 范围
- 端到端回归：P1（抽屉 5 条）、P2（关闭/重开 5 条）、P3（侧栏 4 条）、P4（面包屑 5 条）逐条给证据
- 三道前端门禁 + `make web` 一次性重生成 `internal/web/dist` 并**本卡提交**（各实现卡不提交）
- 偏差回收：`design/refs` 42 张偏差、`design/MANIFEST.sha256` 口径、U-01 部署 commit 不可核实——逐条写清是否仍开放、认账人与补的时点

#### 任务级验收（可观察）
1. P1–P4 全部通过：暗色基准的改前/改后截图 + 每条的数值（抽屉宽 720、移动全屏、关闭键 ≥44、请求数 ≤2/0/1、TabBar 高 57 等）
2. 关闭/重开回归矩阵跑通：有未完成任务 → 409 → force；reopen 后任务仍 cancelled；对 closed 再 close 返 204
3. `node web/scripts/ui-imports-check.mjs` = 21 个组件；`node web/scripts/style-debt.mjs` 0 超标；`pnpm --dir web build` 体积门禁通过
4. `make web` 后 `internal/web/dist` 与 `web/src` 对齐；`git status` 干净、dist 差异只来自本次重生成
5. `make test` 未执行时在报告里如实标注「环境无 Go」+ 原因，不伪造退出码
6. 四条 issue 逐一给「现象 → 证据 → 结论」，偏差与未决项（U-01/U-02/U-04/U-06 等）状态更新

#### 不做
- 不改后端、不重生成 `design/refs`（偏差仍按方案 §7.9② 跟踪）
- 不写验收结论（验收由平台侧人工给）

**上游依据**：`docs/tech/web-ui-102-interaction-fix-v0.1.md`（技术方案，本仓库）；`design/`（界面契约 v0.3）。
**通用口径**：分支 `task/<平台任务号>`，PR base 显式 `feature/echuan-2`；不提交 `internal/web/dist`（收口卡统一重生成）；推送前 `git fetch origin && git merge origin/feature/echuan-2`，冲突保留双方改动。
**证据纪律**：改前/改后截图各 1 张（暗色基准）+ 3–5 个关键数值；若 8720 上仍是旧产物（`/version` 返 `dev`/`none`，方案 U-01），改用本地 `pnpm --dir web dev` 或新构建镜像验收，并在报告里写明用的方式。

