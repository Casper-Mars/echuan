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
