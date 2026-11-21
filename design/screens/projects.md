# 屏：projects（某产品下的项目）

> 主题：**暗色为默认**（验收基准 `refs/<screen>__<viewport>__dark__<state>.png`）；亮色同套保留，见同名 `__light__` 文件。

- 目标：选择项目进入看板；看 open/closed 与最近更新时间。
- 断点：桌面/平板表格；<768 卡片式行。
- 结构：页头（产品名 + 项目数 + 新建项目）→ 表格（序号 · 项目 · 状态 · 更新时间 · 操作）。
- 使用 token：`surface.raised` `border.subtle` `feedback.success` `action.primary-text` `space.3` `radius.12`
- 组件：`ui/DataTable`、`ui/StatusPill`、`ui/EmptyState`、`ui/SkeletonCard`、`ui/Alert`、`ui/Button`、`ui/DetailSheet`、`ui/ConfirmModal`
- 状态矩阵：

| 状态 | 内容 | 截图 |
|---|---|---|
| default | 3 个项目：2 open 1 closed | refs/projects__{desktop,tablet,mobile}__dark__default.png |
| empty | 产品下无项目：空态 + 「新建项目」 | refs/projects__{desktop,tablet,mobile}__dark__empty.png |
| loading | 3 个 SkeletonCard | refs/projects__{desktop,tablet,mobile}__dark__loading.png |
| error | 错误条（`GET /v1/projects`）+ 重试 | refs/projects__{desktop,tablet,mobile}__dark__error.png |

- 交互：行点击 → 只读详情抽屉（9 字段：项目编号 / 产品编号 / 序号 / 名称 / 上下文 / 状态 / 创建时间 / 更新时间 / 关闭时间；容器 `ui/DetailSheet`，抽屉不发请求，字段取当前行）。操作列 = 「打开看板」（`default`，→ `#/projects/:projectId/tasks`）+「关闭/重开」（`ghost`：open → 「关闭」、closed → 「重开」）；行内动作必须 `stopPropagation`，点按钮不开关抽屉。closed 项目只读可看（看板仍可进，「新建任务」置灰）。
- 数据来源：`GET /v1/projects?product_id=`；关闭 `POST /v1/projects/close?id=`（可带 `force`）、重开 `POST /v1/projects/reopen?id=`。关闭遇到 **409**（项目下仍有未完成任务）→ 二次确认（`ui/ConfirmModal`：说明「还有 N 个未完成任务，确认关闭会取消它们，且重开不会恢复」），取消 = 不发请求，确认 = 以 `force=true` 重发。重开**不复活**已取消的任务。
- 不做：抽屉只读（不做字段编辑）；不做批量关闭/重开；「重开」不恢复已取消任务（要恢复任务只能回数据）。
