# 屏：board（看板 · 主路径）

> 主题：**暗色为默认**（验收基准 `refs/<screen>__<viewport>__dark__<state>.png`）；亮色同套保留，见同名 `__light__` 文件。

- 目标：一眼看清 7 个状态列，找到要推进的任务并进入详情；移动端靠横向切列看完整个看板。
- 断点：≥1280 桌面（左栏 240px 有文字 + 列 280px 横向排布）/ 768–1279 平板（左栏收 64px 图标栏）/ <768 移动（无侧栏，底部 TabBar，列宽 min(86vw,320px)）。
- 结构：页头（项目名 · 已关闭标识（closed 项目）· 任务数 · 自动刷新提示 · 类型/状态筛选 · 新建任务）→ 看板条（列数 · 列指示器 · 滑动提示）→ 7 列（`pending ready in_progress in_review done failed cancelled`），列头 sticky + 计数，列内卡片栈。
- 使用 token：`surface.raised` `surface.overlay` `border.subtle` `action.primary` `space.3` `space.4` `radius.12` `text.caption`
- 组件：`ui/BoardColumn`、`ui/TaskCard`、`ui/StatusPill`、`ui/KindBadge`、`ui/EmptyState`、`ui/SkeletonCard`、`ui/Alert`、`ui/SelectField`、`ui/Button`
- 状态矩阵：

| 状态 | 内容 | 截图 |
|---|---|---|
| default | 7 列有内容；`in_review` 列品牌描边强调；`P60` 待审卡片高亮描边 | refs/board__{desktop,tablet,mobile}__dark__default.png |
| empty | 项目无任务：整块空态（标题 + 为什么 + 「新建任务」），不显示 7 个空列 | refs/board__{desktop,tablet,mobile}__dark__empty.png |
| loading | 每列 2 个 SkeletonCard，列头与筛选保持稳定 | refs/board__{desktop,tablet,mobile}__dark__loading.png |
| error | 顶部错误条「保留上次数据」+ 重试；列内容仍在 | refs/board__{desktop,tablet,mobile}__dark__error.png |
| collapsed | `done`/`cancelled` 折叠成 56px 竖条，计数仍可读 | refs/board__{desktop,tablet,mobile}__dark__collapsed.png |
| long-list | 每列 ≥8 张卡，验证列内滚动与标题省略 | refs/board__{desktop,tablet,mobile}__dark__long-list.png |

- 交互：点卡片 → `#/projects/:pid/tasks/:tid`（桌面右侧 720px 抽屉，URL 可分享）；列头折叠按钮切换折叠态并入 localStorage；筛选（kind/status）写进 URL query；15s 自动刷新（不打断滚动与输入）。**closed 项目**：页头「新建任务」`disabled`（hover 提示「项目已关闭，请先在项目列表重开」），空态动作为「返回项目列表」（→ `#/products/:productId/projects`），不提供「新建任务」；若仍触发新建并撞后端 409 → 错误条「项目已关闭」+「关闭」。
- 数据来源：`GET /v1/tasks?project_id=&kind=&status=&limit=300`；列顺序固定 7 个 `TaskStatus`；列内按 `priority` 降序、`update_time` 降序。
- 不做：拖拽改状态（本期用详情里的动作推进）；列自定义排序；看板内联编辑。
