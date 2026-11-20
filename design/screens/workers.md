# 屏：workers（执行节点）

> 主题：**暗色为默认**（验收基准 `refs/<screen>__<viewport>__dark__<state>.png`）；亮色同套保留，见同名 `__light__` 文件。

- 目标：判断 worker 是否在线、在忙什么、心跳是否过期。
- 断点：桌面/平板表格；<768 转为卡片式行（字段名作为 `data-label`）。
- 结构：页头（忙碌/空闲/过期计数 + 刷新）→ 表格（节点 · 状态 · 当前任务 · 心跳 · 操作）。
- 使用 token：`surface.raised` `surface.overlay` `border.subtle` `feedback.danger` `feedback.success` `font-mono`
- 组件：`ui/DataTable`、`ui/StatusPill`、`ui/EmptyState`、`ui/SkeletonCard`、`ui/Alert`、`ui/Button`
- 状态矩阵：

| 状态 | 内容 | 截图 |
|---|---|---|
| default | 4 行：2 忙 1 闲、心跳 3s/8s/21s | refs/workers__{desktop,tablet,mobile}__dark__default.png |
| empty | 无节点：空态 + 「查看部署说明」 | refs/workers__{desktop,tablet,mobile}__dark__empty.png |
| loading | 3 个 SkeletonCard | refs/workers__{desktop,tablet,mobile}__dark__loading.png |
| error | 错误条 + 重试 | refs/workers__{desktop,tablet,mobile}__dark__error.png |
| stale | 心跳过期行整行淡底 + 红色心跳徽标（4m12s 前） | refs/workers__{desktop,tablet,mobile}__dark__stale.png |

- 交互：行内「查看任务」跳到该 worker 当前任务的详情路由。
- 数据来源：`GET /v1/workers`（`worker_id` / `state` / `current_task_id`）。
- 不做：远程终止 worker、改 lease（属后端能力，不在本期纯前端范围）。
