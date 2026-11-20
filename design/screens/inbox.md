# 屏：inbox（收件箱：待审核 / 待领取 / 派单队列）

> 主题：**暗色为默认**（验收基准 `refs/<screen>__<viewport>__dark__<state>.png`）；亮色同套保留，见同名 `__light__` 文件。

- 目标：一屏交代「需要我拍板的」与「worker 会拉走的」，并把原派单队列入口并进来（`#/queue` 路由保留兼容）。
- 断点：三档同一结构，卡片网格随宽度自适应（≥1280 三列 / 768–1279 两列 / <768 单列）。
- 结构：页头（计数摘要 + 刷新）→ 三区（待审核 · 待领取 · 派单队列），每区标题 + 计数 + 卡片网格或空行文案。
- 使用 token：`surface.raised` `surface.overlay` `space.4` `radius.12` `text.small`
- 组件：`ui/TaskCard`、`ui/StatusPill`、`ui/EmptyState`、`ui/SkeletonCard`、`ui/Alert`、`ui/Button`
- 状态矩阵：

| 状态 | 内容 | 截图 |
|---|---|---|
| default | 待审核 2 个（`in_review`）+ 待领取 2 个（`ready`）+ 队列空行提示 | refs/inbox__{desktop,tablet,mobile}__dark__default.png |
| empty | 收件箱清空：整块空态 + 「打开看板」 | refs/inbox__{desktop,tablet,mobile}__dark__empty.png |
| loading | 三区各 3 个 SkeletonCard | refs/inbox__{desktop,tablet,mobile}__dark__loading.png |
| error | 错误条 + 重试；分区框架保留 | refs/inbox__{desktop,tablet,mobile}__dark__error.png |

- 交互：点卡片进同一详情路由；`in_review` 卡片直接进评审；刷新与自动刷新并存，手工刷新不排队。
- 数据来源：`GET /v1/inbox`（待审核/待领取）；派单队列复用 `GET /v1/tasks?status=ready`。
- 不做：批量审核、批量领取（Agent 侧不做人工裁决动作）。
