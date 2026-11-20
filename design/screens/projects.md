# 屏：projects（某产品下的项目）

> 主题：**暗色为默认**（验收基准 `refs/<screen>__<viewport>__dark__<state>.png`）；亮色同套保留，见同名 `__light__` 文件。

- 目标：选择项目进入看板；看 open/closed 与最近更新时间。
- 断点：桌面/平板表格；<768 卡片式行。
- 结构：页头（产品名 + 项目数 + 新建项目）→ 表格（序号 · 项目 · 状态 · 更新时间 · 操作）。
- 使用 token：`surface.raised` `border.subtle` `feedback.success` `action.primary-text` `space.3` `radius.12`
- 组件：`ui/DataTable`、`ui/StatusPill`、`ui/EmptyState`、`ui/SkeletonCard`、`ui/Alert`、`ui/Button`
- 状态矩阵：

| 状态 | 内容 | 截图 |
|---|---|---|
| default | 3 个项目：2 open 1 closed | refs/projects__{desktop,tablet,mobile}__dark__default.png |
| empty | 产品下无项目：空态 + 「新建项目」 | refs/projects__{desktop,tablet,mobile}__dark__empty.png |
| loading | 3 个 SkeletonCard | refs/projects__{desktop,tablet,mobile}__dark__loading.png |
| error | 错误条（`GET /v1/projects`）+ 重试 | refs/projects__{desktop,tablet,mobile}__dark__error.png |

- 交互：行内「打开看板」→ `#/projects/:projectId/tasks`；closed 项目操作保留（只读可看）。
- 数据来源：`GET /v1/projects?product_id=`。
- 不做：关闭/重开项目（本期不接 `POST /v1/projects/close|reopen`）。
