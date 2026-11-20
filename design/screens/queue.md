# 屏：queue（派单队列 · 按项目分组）

> 主题：**暗色为默认**（验收基准 `refs/<screen>__<viewport>__dark__<state>.png`）；亮色同套保留，见同名 `__light__` 文件。

- 目标：看清「依赖已满足、等 worker 领取」的任务分布（`#/queue` 路由保留，主入口已并入收件箱）。
- 断点：卡片网格自适应；<768 单列。
- 结构：页头（待派发计数 + 刷新）→ 每个项目一组（项目 id mono + 任务数 + 卡片网格）。
- 使用 token：`surface.raised` `space.4` `radius.12` `text.caption` `font-mono`
- 组件：`ui/TaskCard`、`ui/StatusPill`、`ui/EmptyState`、`ui/SkeletonCard`、`ui/Alert`
- 状态矩阵：

| 状态 | 内容 | 截图 |
|---|---|---|
| default | 1 个项目组、2 张 ready 卡 | refs/queue__{desktop,tablet,mobile}__dark__default.png |
| empty | 队列空：空态 + 「打开看板」 | refs/queue__{desktop,tablet,mobile}__dark__empty.png |
| loading | 4 个 SkeletonCard | refs/queue__{desktop,tablet,mobile}__dark__loading.png |
| error | 错误条 + 重试 | refs/queue__{desktop,tablet,mobile}__dark__error.png |
| long-list | 多项目长列表（≥10 张卡），验证分组滚动 | refs/queue__{desktop,tablet,mobile}__dark__long-list.png |

- 交互：点卡片进详情；分组标题不折叠（本期）。
- 数据来源：`GET /v1/tasks?status=ready&limit=300`，按 `project_id` 分组。
- 不做：手工指派 worker（领取由 worker 心跳决定）。
