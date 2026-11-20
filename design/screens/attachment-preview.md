# 屏：attachment-preview（附件 markdown 在线阅读）

> 主题：**暗色为默认**（验收基准 `refs/<screen>__<viewport>__dark__<state>.png`）；亮色同套保留，见同名 `__light__` 文件。

- 目标：在应用内读附件正文，不再跳新标签；任何失败都有明确文案与兜底链接，绝不静默空白。
- 断点：≥1280 桌面两栏（左 280px 附件列表 + 右正文）/ 768–1279 平板同两栏 / <768 移动上下堆叠（列表 ≤32vh，正文吃剩余高度）。
- 结构：头部（角色徽标 + 标题 + 关闭）→ 左：附件列表（当前项高亮）→ 右：正文（markdown）或状态提示。
- 使用 token：`surface.canvas` `surface.raised` `border.subtle` `font-mono` `space.4` `radius.8` `text.body`
- 组件：`ui/DetailSheet`、`ui/AttachmentRow`、`ui/MarkdownViewer`、`ui/Alert`、`ui/EmptyState`、`ui/SkeletonCard`、`ui/Button`
- 状态矩阵：

| 状态 | 内容 | 截图 |
|---|---|---|
| default | 渲染正文：h1/h2、段落、列表、行内 code、表格、代码块 | refs/attachment-preview__{desktop,tablet,mobile}__dark__default.png |
| empty | 正文为空：明确「附件为空」+「返回详情」，不留白 | refs/attachment-preview__{desktop,tablet,mobile}__dark__empty.png |
| loading | 正文 5 行 Skeleton，列表可点但显示为禁用 | refs/attachment-preview__{desktop,tablet,mobile}__dark__loading.png |
| error | 404：「附件不存在或已删除」+「在新标签打开原文」兜底 | refs/attachment-preview__{desktop,tablet,mobile}__dark__error.png |
| oversized | `Content-Length > 512 KB`：提示大小 + 「仍然加载」+ 兜底链接 | refs/attachment-preview__{desktop,tablet,mobile}__dark__oversized.png |
| image-placeholder | 正文内嵌图片：显式占位 `[图片：<alt>（平台不存二进制附件，无法预览）]` | refs/attachment-preview__{desktop,tablet,mobile}__dark__image-placeholder.png |

- 交互：切换附件只换正文，不关面板；返回保留详情滚动位置；同 `material_id` 并发去重；浏览器 HTTP 缓存（immutable + ETag）直接复用，不自建缓存。
- 数据来源：`GET /v1/materials?id=`（`text/markdown; charset=utf-8`，支持 Range/HEAD）；正文用既有 `marked` + `DOMPurify`。
- 不做：图片预览（平台无二进制存储）、附件下载另存、全文搜索。
