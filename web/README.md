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

分层红线（不变）：`src/features/**` 与 `src/pages/**` 只能 import `src/ui`、`src/theme`、`src/api`、
`src/hooks`，不得直接 `import '@mantine/core'`。唯一例外是应用入口 `src/main.tsx`：它用
`<AppShell>` 提供 Mantine 的 AppShell 安全上下文（`ui/SidebarNav` 内部是 `AppShell.Navbar`，
Mantine 要求必须在 `<AppShell>` 里），三形态与几何仍全部由 `src/features/shell` 决定。

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
