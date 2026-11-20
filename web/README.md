# echuan web

驿传平台的 React + Vite 前端（任务-15：三页面与决策交互）。

## 目录

- `src/api` — 同源请求层（X-Actor / If-Match / 统一错误体）与契约类型。
- `src/hooks` — 5s 轮询封装 `usePolling`（写操作后立即补拉）、产品名称。
- `src/components` — 布局、面包屑、抽屉、弹窗、Markdown 安全渲染、详情与动作组件。
- `src/pages` — 产品列表 / 项目列表 / 任务看板；所有跳转只靠点击，不要求手输 ID。

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
- **六态**：`default / empty / loading / error / collapsed / long-list`，
  三视口 × 六态的自检与截图用 `scripts/board-shots.mjs`（见文末「看板自检」）。

分层红线（不变）：`src/features/**` 与 `src/pages/**` 只能 import `src/ui`、`src/theme`、`src/api`、
`src/hooks`，不得直接 `import '@mantine/core'`。唯一例外是应用入口 `src/main.tsx`：它用
`<AppShell>` 提供 Mantine 的 AppShell 安全上下文（`ui/SidebarNav` 内部是 `AppShell.Navbar`，
Mantine 要求必须在 `<AppShell>` 里），三形态与几何仍全部由 `src/features/shell` 决定。

## 列表页（`src/features/products`、`projects`、`workers`，T9）

产品 / 项目 / 执行节点三张列表页已从旧 `src/pages/*` 迁到 `src/features/*`，形态统一：

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
node scripts/ui-imports-check.mjs      # 真实打包 src/ui/index.ts，断言 21 个组件全部可 import
```

- `src/theme/tokens.css`：生成物。暗色即默认（令牌层只有一个根块 = 暗色解析），亮色由
  `html[data-theme='light']` 覆写同一批变量；`index.html` 静态写 `data-theme="dark"`，首屏不闪白。
- `src/theme/base.css`：元素级底座 + Mantine 变量桥接 + 由令牌推导的度量（`--ui-*`）。
- `src/theme/mantine-theme.ts`：令牌 → Mantine theme（字体/字号/间距/圆角/阴影/断点/色阶/变体解析）。
- `src/theme/color-scheme.ts`：`setThemeMode('light'|'dark')`，落 localStorage 并通知订阅者。
- `src/ui/**`：21 个封装组件（与 `design/components.md` 总览表一一对应），只从 `src/ui/index.ts` 导出；
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

## 看板自检：三视口 × 六态（CDP + 请求拦截）

`scripts/board-shots.mjs` 用同一个 CDP 思路（原生 WebSocket，无 npm 依赖）做两件事：
**给 `features/board` 的六态各拍一张 1440/1024/390 截图**，并**量可量化指标**判 PASS/FAIL。

- 被测页面：`web/dist` 的静态托管（先 `pnpm build`；静态托管没有后端，脚本用 CDP `Fetch`
  拦截伪造 `/v1/projects` 与 `/v1/tasks`，不改后端、也不写死真实数据）
- 六态怎么造：`default` 12 个任务（2/2/2/2/2/1/1）· `empty` 返回 `items: []` ·
  `loading` `/v1/tasks` 永不响应（停在 SkeletonCard）· `error` 先成功再让 15s 自动刷新那笔 500
  （保留上次数据 + 错误条）· `collapsed` 真点列头折叠 → **刷新页面** → 验证粘性 ·
  `long-list` 每列 9 张卡（列内滚动 + 列头 sticky）
- 断言口径：7 列 / 展开列 280 / 折叠列 56 / 计数与数据条数一致 / 列头 sticky /
  文档无横纵溢出 / 移动端 `scrollWidth > clientWidth`、最后一列右边界 ≤ 视口 + 1、
  列宽 ≥ `min(86vw, 320)`、`scroll-snap` / 自动刷新一轮后滚动位置与输入焦点不变

```bash
# 1) 构建产物 + 静态托管到 :4191（任选一种静态服务器，必须与浏览器可达）
pnpm build
docker run -d --name echuan-preview-t5 -p 127.0.0.1:4191:80 \
  -v "$PWD/web/dist:/www:ro" busybox:latest httpd -f -p 80 -h /www

# 2) 跑自检（ECHUAN_CDP_WS 是浏览器级 CDP WebSocket，按本地浏览器环境填写）
ECHUAN_CDP_WS='ws://127.0.0.1:3010?token=<token>' \
ECHUAN_BOARD_URL='http://127.0.0.1:4191/' \
node scripts/board-shots.mjs
```

环境变量：`ECHUAN_CDP_WS`（必需）· `ECHUAN_BOARD_URL`（默认 `http://127.0.0.1:4191/`）·
`ECHUAN_SHOT_DIR`（默认仓库根 `.shots/`，截图不入库）· `ECHUAN_ONLY`（只跑指定用例，
如 `mobile:collapsed,desktop:error`）。退出码：0 全过 / 1 有断言失败 / 2 用法或环境错误。
