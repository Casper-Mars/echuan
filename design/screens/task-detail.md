# 屏：task-detail（任务详情 · 可分享路由）

> 主题：**暗色为默认**（验收基准 `refs/<screen>__<viewport>__dark__<state>.png`）；亮色同套保留，见同名 `__light__` 文件。

- 目标：在一个界面里读任务、读附件、给评审结论；URL 可分享，从收藏/历史能直接打开同一任务。
- 断点：≥1280 桌面右侧 720px 抽屉（背后看板保留上下文）/ 768–1279 平板同抽屉 / <768 移动全屏 sheet + 底部固定操作条。
- 结构：头部（标题 + 关闭）→ 元信息（状态 · 类型 · 尝试 · 优先级）→ 租约条（有租约时）→ 评审区（仅 `in_review`）→ 附件（输入/输出，可点开预览）→ 任务描述 → 任务上下文 → 前置依赖 → 驳回/失败原因 → 底部操作条。
- 使用 token：`surface.canvas` `surface.raised` `action.primary` `feedback.danger` `space.4` `space.5` `radius.12` `shadow.e3`
- 组件：`ui/DetailSheet`、`ui/StatusPill`、`ui/KindBadge`、`ui/Segmented`、`ui/SelectField`、`ui/TextArea`、`ui/AttachmentRow`、`ui/MarkdownViewer`、`ui/Chip`、`ui/Alert`、`ui/Button`、`ui/IconButton`
- 状态矩阵：

| 状态 | 内容 | 截图 |
|---|---|---|
| default | `in_review` 任务：元信息 + 评审区（通过/驳回 + 自动带入的 output 附件 + 意见）+ 3 个附件 + 描述 | refs/task-detail__{desktop,tablet,mobile}__dark__default.png |
| loading | 元信息与正文 Skeleton，头部与底部操作条保持稳定 | refs/task-detail__{desktop,tablet,mobile}__dark__loading.png |
| error | 错误条（含 HTTP status 与「重试」）；已有分区不整块消失 | refs/task-detail__{desktop,tablet,mobile}__dark__error.png |
| leased | 租约条：`lease_id` + 到期时间 + `worker_id`，mono 展示 | refs/task-detail__{desktop,tablet,mobile}__dark__leased.png |
| failed | `3/3` 用尽：失败原因区 + 底部「重置为待处理」主操作 + 次要「删除」 | refs/task-detail__{desktop,tablet,mobile}__dark__failed.png |
| rejected | 顶部驳回原因条（原文 + 时间），评审区仍可再次提交 | refs/task-detail__{desktop,tablet,mobile}__dark__rejected.png |

- 交互：关闭 → 回看板路由；附件点击 → 附件预览（不动详情状态）；通过/驳回 → 二次确认 → `POST /v1/tasks/decide`；移动端操作条固定底部，与滚动区分离。
- 数据来源：`GET /v1/tasks?id=`、`GET /v1/tasks/dependencies?task_id=`、`GET /v1/tasks/attachments?task_id=`；评审提交 `POST /v1/tasks/decide`（`decided_by` 沿用 `VITE_ACTOR`）。
- 不做：详情内直接新建任务；字段内联编辑（本期只支持删除/重置/评审）。
