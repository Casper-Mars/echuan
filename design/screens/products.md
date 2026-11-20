# 屏：products（产品）

> 主题：**暗色为默认**（验收基准 `refs/<screen>__<viewport>__dark__<state>.png`）；亮色同套保留，见同名 `__light__` 文件。

- 目标：从产品进入项目；看清仓库地址与项目数。
- 断点：桌面/平板表格；<768 卡片式行。
- 结构：页头（产品数与项目总数 + 筛选 + 新建产品）→ 表格（产品 · 仓库 · 项目数 · 操作）。
- 使用 token：`surface.raised` `border.subtle` `text.muted` `font-mono` `space.3` `radius.12`
- 组件：`ui/DataTable`、`ui/EmptyState`、`ui/SkeletonCard`、`ui/Alert`、`ui/Button`、`ui/SelectField`
- 状态矩阵：

| 状态 | 内容 | 截图 |
|---|---|---|
| default | 4 行产品，仓库地址 mono | refs/products__{desktop,tablet,mobile}__dark__default.png |
| empty | 没有产品：空态 + 「新建产品」 | refs/products__{desktop,tablet,mobile}__dark__empty.png |
| loading | 4 行 Skeleton | refs/products__{desktop,tablet,mobile}__dark__loading.png |
| error | 错误条（`GET /v1/products`）+ 重试 | refs/products__{desktop,tablet,mobile}__dark__error.png |

- 交互：行内「查看项目」→ `#/products/:productId/projects`。
- 数据来源：`GET /v1/products`（`product_id` / `name` / `git_url` / `project_count`）。
- 不做：产品编辑、删除（后端无对应动作）。
