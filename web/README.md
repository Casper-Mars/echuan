# echuan web

驿传平台的 React + Vite 前端（任务-14：骨架与三级路由）。

## 目录

- `src/api` — 同源请求层（X-Actor / If-Match / 统一错误体）。
- `src/hooks` — 5s 轮询封装 `usePolling`、产品名称。
- `src/components` — 布局、面包屑、详情抽屉容器、状态组件。
- `src/pages` — 产品列表 / 项目列表 / 任务看板三个最小页面。

## 路由

采用 `HashRouter`，可直接刷新/分享：

- `#/products`
- `#/products/:productId/projects`
- `#/products/:productId/projects/:projectId/tasks`

根路径与未知 hash 重定向到 `#/products`。

## 开发

```bash
pnpm install
# 先启动后端（127.0.0.1:8720），Vite 会把 /v1 代理过去
pnpm dev
```

## 构建

```bash
pnpm build        # 产物在 web/dist
cd .. && make web # 同步到 internal/web/dist，随 go:embed 打进二进制
```

`VITE_ACTOR` 可覆盖默认的 `X-Actor: human:web`。

## 可选：端到端冒烟（CDP）

`scripts/ui-verify.mjs` 用 headless Chrome 验证路由、面包屑、5s 轮询、抽屉与错误提示。
需要先启动后端（默认 18720）、用 `APP / APP-1` 示例数据填充，并用 `--remote-debugging-port`
启动 Chrome；然后：

```bash
ECHUAN_UI_BASE=http://127.0.0.1:18720 CHROME_CDP=http://127.0.0.1:19222 node scripts/ui-verify.mjs
```
