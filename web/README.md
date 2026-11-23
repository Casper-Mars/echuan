# echuan web

驿传平台的 React + Vite 前端。8 屏 + 壳层由 `src/features/*` 承载，组件层 `src/ui`、令牌层 `src/theme`。

## 目录

- `src/api` — 同源请求层（X-Actor / If-Match / 统一错误体）与契约类型。
- `src/hooks` — 5s 轮询封装 `usePolling`（写操作后立即补拉）、产品名称。
- `src/ui` — 22 个自有组件（唯一允许 import `@mantine/core` 的层）；`src/theme` — 令牌层 + 元素级底座。
- `src/features/*` — 8 屏（board / task-detail / attachment-preview / inbox / queue / products / projects / workers）+ shell。
- `src/components` — 上一代页面组件（已无引用，但不在 T11 的删除白名单里，留给后续卡）；旧 `src/pages/*` 已于 T11 删除。

Markdown（需求/方案/报告正文）由 `marked` 解析、`DOMPurify` 净化后渲染。

项目详情支持多份独立技术方案：在规格评审或规划中，新建方案；打开“阅读全文”可查看完整正文、切换历史版本、下载对应版本或编辑当前版。每份方案独立修订；项目统一评审全部当前方案。需求仍是一份文档及其版本链。本轮只接受 Markdown 正文，不提供 Word/PDF 附件上传。

“项目资料包”按需下载：服务端把需求当前版和每份方案当前版现拼成 zip，不落库，也不移动任何指针。项目详情的 `spec` 只表示最后提交的那份方案，不再是汇总包；旧接口与执行任务的工具包读取的是该单份文档。完整约定见 [多方案能力与兼容约定](../docs/project-specs.md)。

## 路由

采用 `HashRouter`，可直接刷新/分享。全部路由挂在同一个应用外壳（`src/features/shell/AppFrame`）下，
壳层负责三形态导航（桌面 240 侧栏 / 平板 64 图标栏 + Tooltip / 移动底部 TabBar）与顶栏：

- `#/` — 上次访问的项目看板；无记录时重定向到 `#/products`
  （记录键：`localStorage['echuan:last-project']`，值是 `project_id`）
- `#/projects/:projectId/tasks` — 项目看板（缺 `taskId` 一律回这里）
- `#/projects/:projectId/tasks/:taskId` — 任务详情，独立路由、可直接打开/分享，关闭回看板
- `#/projects/:projectId` — 归一重定向到该项目看板
- `#/inbox`、`#/queue`、`#/products`、`#/products/:productId/projects`、`#/workers` — 旧书签保留

未知 hash 重定向到 `#/products`。

## 看板（`src/features/board`，T5 主路径）

`#/projects/:projectId/tasks` 由 `src/features/board/BoardPage` 承载，是应用的主路径：

- **7 列固定枚举**：`pending ready in_progress in_review done failed cancelled`；列内按 `priority` 降序、
  再按 `update_time` 降序；`in_review` 列品牌描边，其首卡（本列最高优先级）借 `ui/TaskCard` 的 focus
  描边强调（`design/screens/board.md` default 态「P60 待审卡片高亮描边」）。
- **列折叠**：`done` / `cancelled` 默认折成 56 竖条（计数仍可读），列头按钮切换。
  偏好键 **`localStorage['echuan:board:collapsed']`**，值是折叠状态枚举的 JSON 数组
  （例如 `["done","cancelled"]`）；脏值 / 存储不可用一律按默认值降级，不让看板崩掉。
- **筛选进 URL**：`#/projects/:projectId/tasks?kind=dev&status=in_review`
  （`kind` = `requirement|design|breakdown|dev`，`status` = 上面 7 个枚举）。每次切换是 push，
  所以可分享、后退可用；手改 URL 写错的枚举按「不筛选」处理。
- **15s 自动刷新**：复用 `src/hooks/useAutoRefresh.ts`（周期取 `lib/refresh` 的 `REFRESH_INTERVALS.tasks`），
  刷新只替换数据、不动滚动位置与输入焦点；全局开关（`localStorage['echuan:auto-refresh']`）关掉时
  页头如实显示「自动刷新已关闭」。
- **移动端 M1**：列宽 `min(86vw, 320)`、`scroll-snap-type: x mandatory`、列头 sticky，
  看板条上有「第 N / 7 列 · <status>」指示器；列表失败保留上次数据 + `features/shell/ErrorBar` 错误条。
- **六态**：`default / empty / loading / error / collapsed / long-list`；
  可复跑的自动化门禁是 `scripts/ui-verify.mjs --viewports`（见文末「三视口移动端回归」），
  逐屏截图对照由当次任务按需跑一次性脚本，不入库（T11 已退役 `board-shots / task-detail-shots`）。

分层红线（不变）：`src/features/**` 只能 import `src/ui`、`src/theme`、`src/api`、
`src/hooks`，不得直接 `import '@mantine/core'`。唯一例外是应用入口 `src/main.tsx`：它用
`<AppShell>` 提供 Mantine 的 AppShell 安全上下文（`ui/SidebarNav` 内部是 `AppShell.Navbar`，
Mantine 要求必须在 `<AppShell>` 里），三形态与几何仍全部由 `src/features/shell` 决定。

## 列表页（`src/features/products`、`projects`、`workers`，T9）

产品 / 项目 / 执行节点三张列表页在 `src/features/*`（旧 `src/pages/*` 已于 T11 整体删除），形态统一：

- **结构**：页头（标题 + 一句计数说明 + 该屏动作）→ `ui/DataTable` 卡片。列定义照 `design/screens/<screen>.md`：
  产品（产品名 + `product_id` 副标题 / 仓库 mono / 项目数 / 查看项目）、
  项目（序号 / 名称 + `project_id` / 状态 `ui/StatusPill` / 更新时间 `MM-DD HH:mm` / 打开看板）、
  执行节点（节点 mono / 状态 / 当前任务 mono / 心跳 / 查看任务）。
- **状态矩阵**：产品与项目 `default / empty / loading / error`；执行节点多一个 `stale`。
  `empty` 用 `ui/EmptyState`，`loading` 用 `ui/SkeletonCard`，`error` 用 `ui/Alert`（danger + 重试）。
  非 default 态下页头不出现「新建」动作（与 `design/refs` 一致）。
- **心跳过期**：`src/features/workers/WorkersPage` 的 `STALE_AFTER_MS = 90000`（3 个 30s 心跳周期）。
  过期的行整行淡底（`ui/DataTable` 的 warn-row 变体，行上有 `data-warn`），心跳格换成 `ui/StatusPill`
  的 danger 徽标并明写「心跳过期」——不只靠颜色表达。
- **移动端（<768）**：`ui/DataTable` 自带的卡片式媒体块生效，每格 `data-label` 就是字段名（产品 / 仓库 /
  项目 / 操作 …），页面不靠横向滚动；外层卡片在该断点让位给逐行卡片。
- **数据**：只用既有接口——`GET /v1/products`（页头产品数与 `project_count`）、
  `GET /v1/projects`（项目页主数据；产品页另取一次用于项目数精确计数与「按项目状态」筛选）、
  `GET /v1/workers`。写操作一律不接：新建产品 / 新建项目只展示，关闭重开项目与终止 worker 不出现入口。
- **分层**：三张页面只 import `src/ui`、`src/api`、`src/hooks`、`src/lib`；样式在各自的 `*.css` 里只消费令牌，
  没有 hex / px 字面量（`grep -rnE '#[0-9a-fA-F]{3,8}\b|[0-9]+px\b' web/src/features/{products,projects,workers}` 为空）。

## 附件预览（`src/features/task-detail`，T7 / issue #128）

详情「附件」区点任一附件 → **应用内**开预览（不新开标签、不跳路由），两处入口（任务详情的附件区、
项目详情的资料区）共用同一个 surface：

- **≥1024**：整页居中的 80vw×80vh 浮层（`ui/ContentModal`，底图是看板 / 项目列表整页）。预览期间
  详情抽屉整棵隐去（`ui/DetailSheet` 的 `concealed` + `visibility: hidden`）——画面里没有右侧 720 抽屉；
  左列表 280 独立滚动 + 右正文吃满剩余宽度（1440×900 下正文 ≥800）。
- **<1024**：仍是抽屉形态（768–1023 抽屉 720 内两栏；<768 全屏上下堆叠，列表 ≤32vh、正文吃剩余）。
- **头部**：角色徽标 + 附件标题 + 关闭键（`aria-label="关闭附件预览"`）；Esc / 关闭键只退浮层，
  回到详情并保留详情滚动位置，焦点回触发的附件行。点浮层外遮罩**不**关闭。

- **数据源**：`GET /v1/materials?id=`（`text/markdown`，浏览器 HTTP 缓存 immutable + ETag 直接复用）；
  前端**只做同 `material_id` 的并发去重**（在飞请求共用一条 Promise），不自建缓存。
- **六态**（`[data-testid="attachment-preview"]` 的 `data-state`）：`default` · `empty`（「附件为空」+「返回详情」）·
  `loading`（5 行骨架，列表可点但置灰）· `error`（404「附件不存在或已删除」+「在新标签打开原文」）·
  `oversized`（`Content-Length` > 512 KB：提示大小 +「仍然加载」+ 兜底链接，**不自动渲染**）·
  `image-placeholder`（正文内嵌图片 → 显式占位，不静默空白）。
- **降级口径**：8s 超时 → 「加载超时」+「重试」；413 → 「附件过大」；其他非 2xx → 原文提示；
  渲染异常 → 降级 `<pre>` 原文（错误边界，不白屏）。
- **验收锚点**：正文区根节点恒带 `data-testid="markdown-viewer"`（`src/ui/MarkdownViewer`），
  断言「点附件后同页出现该节点」即可证明应用内渲染。
- **分层**：预览层只用 `src/ui` 组件（`ui/AttachmentRow`、`ui/MarkdownViewer`、`ui/Alert`、`ui/EmptyState`、
  `ui/SkeletonCard`、`ui/Button`），样式在 `task-detail.css` 里只消费令牌（无 hex / px 字面量）。

## 开发

```bash
pnpm install
# 先启动后端（127.0.0.1:8721），Vite 会把 /v1 代理过去
pnpm dev
```

## 构建

```bash
pnpm build        # 产物在 web/dist
cd .. && make web # 同步到 internal/web/dist，随 go:embed 打进二进制
```

`VITE_ACTOR` 可覆盖默认的 `X-Actor: human:web`。

## 产物体积门禁

`pnpm build` 末尾串了 `scripts/check-size.mjs`，构建后打印 JS / CSS 的 raw 与 gzip 体积：

```text
[size] JS  raw=280209 gzip=90302 files=1
[size] CSS raw=44354 gzip=8836 files=1
[size] 阈值 JS gzip <= 344056 B（基线 88056 B + 预算 256000 B = 344056 B）
[size] 通过：JS gzip 90302 B <= 阈值 344056 B（剩余 253754 B）
```

判据只看 JS gzip 总量：默认阈值 = 基线 88,056 B（ADR-0001 §2 C7 实测，引入 Mantine 前）
+ 预算 250 KB(256,000 B) = **344,056 B**。超阈值即打印超标项并让 `pnpm build` / `make web`
非 0 退出。阈值可用环境变量覆盖，用于演示失败路径或临时放宽：

```bash
SIZE_JS_GZIP_MAX=1024 node scripts/check-size.mjs   # 必然超标，退出码 1
node scripts/check-size.mjs                         # 用默认阈值复跑
```

gzip 口径为 Node zlib level 9，与 vite build 打印的 gzip 数值一致。

## 设计令牌层与 ui 封装层（T3 底座）

`design/tokens.json` 是唯一令牌源，代码里不出现第二份视觉常量：

```bash
node scripts/gen-tokens.mjs            # design/tokens.json → src/theme/tokens.css + src/theme/tokens.ts
node scripts/gen-tokens.mjs --check    # 只校验产物是否与令牌源一致（漂移即退出码 1）
node scripts/ui-imports-check.mjs      # 真实打包 src/ui/index.ts，断言 22 个组件全部可 import
```

- `src/theme/tokens.css`：生成物。暗色即默认（令牌层只有一个根块 = 暗色解析），亮色由
  `html[data-theme='light']` 覆写同一批变量；`index.html` 静态写 `data-theme="dark"`，首屏不闪白。
- `src/theme/base.css`：元素级底座 + Mantine 变量桥接 + 由令牌推导的度量（`--ui-*`）。
- `src/theme/mantine-theme.ts`：令牌 → Mantine theme（字体/字号/间距/圆角/阴影/断点/色阶/变体解析）。
- `src/theme/color-scheme.ts`：`setThemeMode('light'|'dark')`，落 localStorage 并通知订阅者。
- `src/ui/**`：22 个封装组件（与 `design/components.md` 总览表一一对应），只从 `src/ui/index.ts` 导出；
  页面层禁止直接 `import '@mantine/core'`。

令牌产物漂移检测（CI/本地均可）：

```bash
node scripts/gen-tokens.mjs && git diff --exit-code src/theme/
```

## 可选：端到端冒烟（CDP）

`scripts/ui-verify.mjs` 用 headless Chrome 验证路由、面包屑、5s 轮询、抽屉与错误提示。
需要先启动后端（默认 18720）、用 `APP / APP-1` 示例数据填充，并用 `--remote-debugging-port`
启动 Chrome；然后：

```bash
ECHUAN_UI_BASE=http://127.0.0.1:18720 CHROME_CDP=http://127.0.0.1:19222 node scripts/ui-verify.mjs
```

## 样式债门禁（`scripts/style-debt.mjs`，T11）

旧视觉层（`src/styles.css` 2,613 行）与旧页面（`src/pages/*.tsx`）已在 T11 删除，
「样式债出清」不再靠 review 时的肉眼判断，而是落成可复跑的门禁，并**串进 `make web`**（在
`pnpm --dir web build` 之前跑，任一指标不达标即非 0 退出，构建不会产出新产物）：

```bash
node web/scripts/style-debt.mjs                          # 6 项指标 + 阈值判定
STYLE_DEBT_UNUSED_MAX=0 node web/scripts/style-debt.mjs  # 覆盖「未引用类名占比」阈值，演示失败路径
```

| # | 指标 | 口径 | 阈值 |
|---|---|---|---|
| ① | 未引用类名占比 | 扫描面 `src/**/*.css` 里定义的类名，在 `src/**/*.{ts,tsx}` + `index.html` 中「出现过」即算被引用（动态拼接可覆盖，偏宽松）；`mantine-*` 是组件库类名，不计入分母 | **< 20%** |
| ② | 重复定义选择器 | 同一层叠上下文（顶层，或同一个 `@media` 块）内同一条选择器出现 > 1 次 | **= 0** |
| ③ | 令牌层口径 | 顶层 `:root` 规则数 / 令牌层（`theme/*.css`）在 `@media` 内声明自定义属性的条数；组件/页面自己按断点调局部变量（`--button-height` / `--bp-column-w`）不算令牌覆写，单列展示 | **:root = 1**、**媒体查询内令牌覆写 = 0** |
| ④ | 全局样式层行数 | `theme/*.css` 物理行数之和（页面级底座；`ui/ui.css` 与 `features/*/*.css` 作组件层/页面层单列展示，不计入本门禁） | **≤ 300 行** |
| ⑤ | 断点套数 | 全部 `@media` 断点值去重后必须只剩一套：`47.99em`（tokens `breakpoint.sm` 768）与 `63.99em`（`md` 1024） | **1 套** |
| ⑥ | 死类子系统 | `.board-column* / .row-* / .notice-* / .state-panel / .spinner` 在扫描面里的命中数 | **= 0** |

画布（暗底 + 氛围光 + 细网格）只有一处来源：`src/theme/base.css` 的 `body` / `body::before`。
各 `features/*` 页面根节点不再自绘底色（那层是当年为了盖住旧 `styles.css` 的浅色 body 才加的），
细网格按 `design/components.md`「顶部居中径向遮罩衰减」实现，页面下半部分回到纯画布色。

一次性截图脚本（`board-shots.mjs` / `task-detail-shots.mjs` / `ui-shell-check.mjs` /
`ui-selfcheck-product-drawer.mjs`）已在 T11 退役：它们各自带一份 CDP 实现与一份 mock 数据，
真正的门禁只剩 `ui-verify.mjs --viewports`（断言）与当次任务按需写的临时截图脚本（不入库）。

## 移动端三条专项回归：三视口（M1 / M2 / M3 + 通用项）

`scripts/ui-verify.mjs --viewports` 是**同一个脚本**的三视口模式：一条命令把「移动端好用」跑成断言，
一次覆盖 1440×900 / 834×1112 / 390×844，逐条打印 `PASS/FAIL`，**任一失败退出码 1**。数据全部由
请求拦截伪造（`/v1/*` 本地应答，静态托管没有后端），不打真实服务、不改后端、不落任何文件。

```bash
# 1) 产物 + 静态托管（静态托管没有后端，`/v1/*` 由脚本拦截伪造；重启无需重跑 build，bind mount 生效）
pnpm build
docker run -d --name echuan-preview-t10 -p 127.0.0.1:4198:80 \
  -v "$PWD/web/dist:/www:ro" busybox:latest httpd -f -p 80 -h /www

# 2) 一次跑完三视口（默认视口就是 1440x900,834x1112,390x844）
node scripts/ui-verify.mjs --viewports 1440x900,834x1112,390x844
# 等价的默认写法：node scripts/ui-verify.mjs --viewports
# 只跑一条：node scripts/ui-verify.mjs --viewports 390x844
```

断言口径（每条都带原始数值，失败行会枚举不达标项）：

| 组 | 断言 |
|---|---|
| M1 横向切列 | 看板容器 `scrollWidth > clientWidth`；列宽（<768 `min(86vw,320)`，≥768 桌面 280）；列内卡片栈滚到底后列头 `top` 不变（sticky）；滚到最右最后一列 `right ≤ 视口宽 + 1` |
| M2 读附件 | 详情深链 → 点附件行 → `[data-testid=attachment-preview]` 内出现 `[data-testid=markdown-viewer]`；`browser.pages()` 不增（不新开标签）；正文 `clientHeight > 0` 且文本命中 |
| M3 推进状态 | `in_review` 下评审区可见（驳回/通过按钮都在）；驳回不带意见被就地拦下且 `POST /v1/tasks/decide` 计数 0；填意见 → 二次确认 → 恰好 1 次 `POST /v1/tasks/decide` |
| 通用项 | `document.scrollWidth === clientWidth`（无横向溢出）；正文容器 `fontSize ≥ 15px`；可点元素尺寸**按断点判**：`<768` **所有**可点元素 `boundingBox ≥ 44×44`（触控口径，逐项枚举不达标项）；`≥768` 按 `design/components.md` 的控件基线（`ui/Button` md 36 / sm 32、`ui/IconButton` 40、`ui/AttachmentRow` 44、`ui/TabBar` 44），**不判 44** |

### 可点元素的判据为什么分断点

计划里 AC 的原文是「所有可点元素 boundingBox ≥44×44」，这句出自**触控目标**语境（`design/flows.md` F4-3
「触控 ≥44×44」+ `design/components.md §ui/Button`「md = 高 36（桌面）/ 44（移动）」）。桌面/平板是鼠标口径，
36 / 32 是设计明确规定的尺寸，硬套 44 会把**设计正确**的界面判成红，门禁随即失去意义。所以脚本按断点判：
`<768` 一律 44（真实触控尺寸），`≥768` 只对设计规定了尺寸的组件组判基线；设计没规定尺寸的元素
（面包屑链接、Mantine 关闭键等）在 `≥768` 只统计、不计入门禁，逐项列在 `PASS` 行的 detail 里。

移动断点的 44 兜底落在两处纯尺寸声明里（`@media (max-width: 47.99em)` 内，不动颜色 / 间距 / 结构 / 令牌）：
`src/ui/ui.css`（`ui/Button` md+sm 的 `min-height`、`ui/SelectField` 输入框、`ui/BoardColumn` 列头按钮、
`ui/Segmented` 的 control+label）与 `src/features/shell/shell.css`（面包屑链接 `min-width/min-height`）。
`ui/Button` 的高度来自行内 `--button-height`（行内变量压过类选择器），所以兜的是命中区 `min-height`，不改变量本身。

环境变量（都不指向仓库内路径；token 是本机一次性凭证，不入库）：

- `ECHUAN_UI_BASE` — 被测静态站点，默认 `http://host.docker.internal:4198`（浏览器在容器里，
  `127.0.0.1` 指浏览器自己，所以用 `host.docker.internal`）
- `DSH_BROWSER_CDP_URL` — 浏览器级 ws 端点（含 token），给了就不再读 token 文件
- `ECHUAN_BROWSER_TOKEN_FILE` — browserless token 文件，默认 `/workspace/.agent-env/echuan/browser-token`，
  端点拼成 `ws://host.docker.internal:3010?token=<token>`
- `ECHUAN_BROWSER_TOOLS` — `puppeteer-core` 所在目录，默认 `/workspace/.agent-env/browser-tools`
  （**刻意不写进 `package.json`**：浏览器驱动属于 agent 侧工具，不进前端依赖与 lock）

失败传播：把任意断言用的选择器改坏（例：`.ui-task-card__title` → `.ui-task-card__title-BROKEN`），
脚本会打印 `FAIL` 行并以退出码 1 结束，不会静默通过；还原后回到 `48/48`、退出码 0。

> 首次落脚本时的扫描结论（已修）：`ui/Button` 行内 `--button-height` 压过 `ui.css` 的移动媒体规则，
> 移动端 `md` 仍是 36（与设计「44（移动）」冲突）；同类还有 `ui/SelectField` 36、`ui/BoardColumn`
> 列头 38.4、`ui/Segmented` 选项 label 32、shell 面包屑链接 26×20.4。已在本卡用上面的移动断点
> `min-height` 兜底修掉（`ui/**` 与 `features/shell` 各一处纯尺寸声明），三视口现为 48/48。

