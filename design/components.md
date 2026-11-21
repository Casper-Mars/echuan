# 组件规格

> 每个组件一节；状态矩阵是必填项——缺状态就等于没设计。
> **红线：业务页面（`web/src/features/**`）不得直接 `import '@mantine/core'`，只能用本文件的组件。** 验收命令：`grep -r "@mantine/core" web/src/features` 必须为空。

Mantine 版本：`@mantine/core@^8.3.18` + `@mantine/hooks@^8.3.18`（仅这两个包）。下面的「底层」列即实现时包装的 Mantine 组件。

## 总览

| 自有组件 | 底层（Mantine 8） | 出现在哪些屏 |
|---|---|---|
| `ui/Button` | `Button` | 全部 |
| `ui/IconButton` | `ActionIcon` | 详情、附件预览 |
| `ui/TaskCard` | `UnstyledButton` + `Paper` | 看板、收件箱、队列 |
| `ui/StatusPill` | `Badge` | 看板、详情、收件箱、队列 |
| `ui/KindBadge` | `Badge`（subtle） | 看板、详情 |
| `ui/BoardColumn` | `Paper` + `ScrollArea` | 看板 |
| `ui/DataTable` | `Table` | 产品、项目、执行节点 |
| `ui/TextField` / `ui/TextArea` | `TextInput` / `Textarea` | 详情评审、新建任务 |
| `ui/SelectField` | `Select` | 看板筛选、详情评审 |
| `ui/Alert` | `Alert` | 全部（错误条/提示条） |
| `ui/EmptyState` | `Stack` + `Text` + `Button` | 全部 |
| `ui/SkeletonCard` | `Skeleton` | 全部加载态 |
| `ui/ConfirmModal` | `Modal` | 删除/驳回二次确认 |
| `ui/DetailSheet` | `Drawer` | 详情、附件预览 |
| `ui/TabBar` | `AppShell.Footer` 或自建 `Tabs`（移动） | 移动端全部 |
| `ui/SidebarNav` | `AppShell.Navbar` + `NavLink` | 桌面/平板 |
| `ui/MarkdownViewer` | 无（marked + DOMPurify）+ `TypographyStylesProvider` | 详情、附件预览 |
| `ui/AttachmentRow` | `UnstyledButton` | 详情、附件预览 |
| `ui/Segmented` | `SegmentedControl` | 详情评审 |
| `ui/Chip` | `Pill` 或 `Badge` | 详情依赖、租约 |

## ui/Button

- 用途：唯一按钮入口；页面里不出现裸 `<button>`。
- 结构：容器（可选左侧图标）→ 文案 →（可选右侧计数）。
- 尺寸：md = 高 36（桌面）/ 44（移动）；sm = 32；内边距 `space.4`。
- 使用 token：`action.primary` `text.on-action` `border.default` `space.4` `radius.8` `focus.ring`
- 变体：`primary`（实心）/ `default`（描边）/ `ghost`（无底）/ `danger`（红描边）。
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 主按钮实心品牌蓝，文字白 | `action.primary`, `text.on-action` |
| hover | 主按钮加深一档 | `action.primary-hover` |
| active | 无额外位移，避免移动端误触反馈过大 | — |
| focus-visible | 2px 焦点环，偏移 2px | `focus.ring` |
| disabled | 透明度降低 + 禁用点击；提交中显示 loading | `text.muted` |
| loading | 文案前 loading 指示，按钮禁用，宽度不跳 | `text.muted` |

- 可访问性：原生 `button`；图标按钮必须有 `aria-label`；loading 时 `aria-busy="true"`。
- 反例：把「通过评审」做成 ghost（主操作必须一眼可辨）。

## ui/IconButton

- 用途：关闭抽屉、返回、刷新等无语义文案的操作。
- 结构：40×40 点击区 → 20×20 图标（内联 SVG）。
- 使用 token：`text.muted` `space.3` `radius.8`
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 透明底、灰图标 | `text.muted` |
| hover | 淡底 + 文字色 | `surface.overlay`, `text.primary` |
| focus-visible | 焦点环 | `focus.ring` |
| disabled | 置灰不可点 | `text.muted` |

- 可访问性：必须 `aria-label`；移动端点击区 ≥44×44。
- 反例：用 `✕` 字符冒充图标（字体缺失即豆腐块）。

## ui/TaskCard

- 用途：任务的唯一卡片形态，看板列/收件箱/队列复用。
- 结构：标题（最多 3 行省略）→ 元信息行（kind 徽标 · 优先级 · 尝试次数 · 更新时间）→ `task_id`（mono，截断）。
- 使用 token：`surface.canvas` `border.subtle` `shadow.e1` `space.3` `radius.8` `text.small`
- 变体：`default` / `focus`（当前打开的任务，1px 品牌描边）。
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 白底描边、极轻阴影 | `surface.canvas`, `border.subtle` |
| hover | 描边加深 | `border.default` |
| active | — | — |
| focus-visible | 焦点环包裹整卡 | `focus.ring` |
| disabled | 不适用（卡片不可禁用） | — |
| loading | 不适用（用 `SkeletonCard` 占位） | — |
| invalid | 不适用 | — |

- 可访问性：整卡可聚焦（`UnstyledButton`），Enter/Space 打开详情；`aria-label` 用「标题 + 状态」。
- 反例：卡片里塞入可点击的二级按钮（整卡可点 + 内嵌按钮 = 误触）。

## ui/StatusPill

- 用途：7 种任务状态的唯一视觉表达。
- 结构：圆点 + 文案；圆点颜色承载语义，文字始终存在（不只靠颜色区分）。
- 尺寸：高 22；`caption` 字号。
- 使用 token：`surface.overlay` `feedback.success` `feedback.danger` `feedback.warning` `action.primary-text` `text.muted`
- 变体：`neutral`（pending/cancelled）/ `info`（ready/in_progress）/ `warning`（in_review）/ `success`（done）/ `danger`（failed）。
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 淡底 + 语义色文字 + 圆点 | 见上 |
| hover | 无变化（非交互） | — |
| active | 无变化 | — |
| focus-visible | 无变化（非交互） | — |
| disabled | 不使用 `disabled` 表达状态，状态是数据不是控件 | — |
| loading | 不使用；加载中用 Skeleton 占位 | — |
| invalid | 不使用 | — |

- 可访问性：`<span>` 文案本身即语义，不依赖 `title`；色盲用户靠文案区分。
- 反例：只用颜色点、不写状态文字。

## ui/KindBadge

- 用途：任务类型（需求/方案/拆解/开发）。
- 结构：描边框 + 2 字标签。
- 使用 token：`surface.overlay` `border.subtle` `text.muted` `radius.4`
- 变体：四种 kind 文案不同，配色统一（类型不是状态，不给语义色）。
- 状态矩阵：与 `StatusPill` 相同（非交互，rest 之外无变化）。
- 可访问性：文案即语义。
- 反例：给 kind 上五种颜色，与状态色抢注意力。

## ui/BoardColumn

- 用途：看板单列容器（含列头计数与折叠）。
- 结构：列头（状态名 · 计数 · 折叠按钮）→ 卡片栈（可滚动）。
- 使用 token：`surface.raised` `border.subtle` `radius.12` `space.3` `z.raised`
- 变体：`expanded` / `collapsed`（56px 竖条，仅 `done`/`cancelled` 默认折叠）/ `accent`（`in_review` 列，品牌描边）。
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 淡底容器 + sticky 列头 | `surface.raised` |
| hover | 列头折叠按钮 hover 才显形 | `surface.overlay` |
| active | 折叠/展开切换 | — |
| focus-visible | 折叠按钮焦点环 | `focus.ring` |
| disabled | 列本身不可禁用 | — |
| loading | 列内 2 个 `SkeletonCard` | `surface.overlay` |
| invalid | 不适用 | — |

- 可访问性：列头按钮 `aria-expanded`；折叠后保留计数文案（`done 12`）以便读屏。
- 反例：把 `done` 列直接隐藏（用户会以为任务丢了）。

## ui/DataTable

- 用途：产品/项目/执行节点的列表。
- 结构：thead（全大写小字号）→ 行（首列标题 + 副标题 mono）→ 行内操作。
- 行交互：行可点击时用 `onRowClick`（打开只读详情抽屉 `ui/DetailSheet`，不发请求、字段取当前行）；行内动作（「查看项目」「打开看板」「关闭/重开」）**必须 `stopPropagation` 阻断冒泡**，保证「一次点击一个结果」——点按钮只执行按钮动作、不开抽屉；无 `onRowClick` 的行（如执行节点）保持不可点。
- 使用 token：`border.subtle` `border.default` `text.muted` `space.3` `caption`
- 变体：`default` / `warn-row`（心跳过期行整行淡底）。
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 行分隔线，首列加粗 | `border.subtle` |
| hover | 整行淡底 | `surface.overlay` |
| active | — | — |
| focus-visible | 行内操作按钮焦点环 | `focus.ring` |
| disabled | 行不可用（如已关闭项目）时操作置灰 | `text.muted` |
| loading | 表体换 Skeleton 行 | `surface.overlay` |
| invalid | 不适用 | — |

- 可访问性：`<table>` 语义；移动端转卡片式时保留 `data-label` 作为字段名（`td::before`）。
- 反例：移动端把表格横向滚动当解决方案（390 宽必须能读）。

## ui/TextField / ui/TextArea

- 用途：新建任务、评审意见等输入。
- 结构：标签 → 控件 →（错误文案）。
- 使用 token：`surface.raised` `border.default` `text.primary` `feedback.danger` `radius.8`
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 描边输入框 | `border.default` |
| hover | 描边加深 | `border.strong` |
| active | 光标进入，描边品牌色 | `action.primary` |
| focus-visible | 焦点环 + 描边品牌色 | `focus.ring` |
| disabled | 淡底不可编辑 | `surface.overlay` |
| loading | 不适用 | — |
| invalid | 描边转红 + 错误文案（如「驳回必须填意见」） | `feedback.danger` |

- 可访问性：`<label for>` 绑定；错误文案 `aria-describedby`；移动端 `font-size ≥15` 防 iOS 缩放。
- 反例：只有 placeholder 没有标签（填完就看不见字段含义）。

## ui/SelectField

- 用途：看板筛选（kind/status）、评审时选被评审附件。
- 底层：`Select`。
- 使用 token：`surface.raised` `border.default` `space.3` `radius.8`
- 状态矩阵：与 `TextField` 相同（rest/hover/active/focus-visible/disabled/invalid）；额外支持 `loading`（选项加载中禁用）。
- 可访问性：`aria-expanded`、键盘上下选择。
- 反例：筛选项写英文枚举（`in_review`）而不写中文（「待审核」）。

## ui/Alert

- 用途：错误条、提示条（附件过大、租约、驳回原因）。
- 结构：图标/圆点 → 文案 →（可选动作按钮）。
- 使用 token：`surface.raised` `feedback.danger` `border.subtle` `space.3` `radius.8`
- 变体：`danger`（可重试的错误）/ `info`（说明与降级提示）。
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 描边 + 淡底 + 文案 | `surface.raised` |
| hover | 无变化（容器） | — |
| active | 无变化 | — |
| focus-visible | 内部动作按钮焦点环 | `focus.ring` |
| disabled | 不适用 | — |
| loading | 动作按钮进入 loading（如「重试中」） | `text.muted` |
| invalid | 不适用 | — |

- 可访问性：错误条 `role="alert"`；动作按钮有明确文案（「重试」「在新标签打开原文」）。
- 反例：`alert` 只写「出错了」（用户不知道下一步）。

## ui/EmptyState

- 用途：空列表/空附件/空队列。
- 结构：标题 → 一句说明（为什么空 + 下一步）→ 主操作。
- 使用 token：`surface.raised` `border.default`（虚线）`text.muted` `space.9` `radius.12`
- 状态矩阵：`rest` 一种；无 hover/focus（除内部按钮）；不适用 disabled/loading/invalid。
- 可访问性：标题用 `<p>` + 视觉层级，不用装饰性插图（无二进制资产）。
- 反例：白屏或只有一句话，没有下一步动作。

## ui/SkeletonCard

- 用途：列表/看板加载占位。
- 使用 token：`surface.overlay` `border.subtle` `radius.8`
- 状态矩阵：单一 `loading` 形态；`prefers-reduced-motion: reduce` 下**不动画**（截图与实现一致）。
- 可访问性：外层 `aria-busy="true"`，不朗读占位块。
- 反例：加载态用「转圈 + 白屏」，丢失页面结构感。

## ui/ConfirmModal

- 用途：删除任务、驳回评审等破坏性动作。
- 底层：`Modal`（`@mantine/core` 自带，不引 `@mantine/modals`）。
- 结构：标题（后果一句话）→ 影响面（哪个任务、能不能恢复）→ 取消/确认。
- 使用 token：`surface.canvas` `shadow.e3` `feedback.danger` `z.dialog`
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 居中对话框 + 遮罩 | `shadow.e3` |
| hover | 确认按钮 hover 加深 | `action.primary-hover` |
| active | — | — |
| focus-visible | 焦点环；打开时焦点落在取消键 | `focus.ring` |
| disabled | 提交中确认键禁用 | `text.muted` |
| loading | 确认键 loading，遮罩不可关闭 | — |
| invalid | 不适用 | — |

- 可访问性：`role="dialog"` `aria-modal="true"`；Esc 关闭；焦点陷阱。
- 反例：用 `window.confirm`。

## ui/DetailSheet

- 用途：任务详情 / 附件预览 / 产品与项目只读详情（后三者共用同一容器，变体见下）。
- 底层：`Drawer`（桌面右侧 720px；移动全屏）。
- 结构：头部（标题 + 关闭）→ 可滚动正文 → 底部固定操作条（移动端必固定）。
- 使用 token：`surface.canvas` `border.subtle` `shadow.e3` `z.dialog` `space.5`
- 变体：`detail`（单栏）/ `preview`（附件列表 + 正文两栏，移动折叠为上下）/ `readonly`（产品、项目只读详情：字段键值网格 + 0 请求，字段取自当前行数据）。
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 右侧滑入 + 全屏遮罩 | `shadow.e3` |
| hover | 头部关闭键 hover 淡底 | `surface.overlay` |
| active | — | — |
| focus-visible | 关闭键焦点环；打开时焦点进标题 | `focus.ring` |
| disabled | 不适用 | — |
| loading | 正文区 Skeleton，头部与底部操作条保持稳定 | `surface.overlay` |
| invalid | 不适用 | — |

- 可访问性：`role="dialog"`；Esc 关闭；移动端关闭区 ≥44；底部操作条与滚动区分离。
- 反例：移动端把操作按钮放在长正文末尾（要滚 3 屏才找到「通过」）。

## ui/TabBar（移动专属）

- 用途：移动端一级导航（3 项）。
- 项清单（3 项，**不含「看板」**）：收件箱 / 产品 / 更多（「更多」二级层放执行节点）。看板不是一级入口，入口路径 = 产品 → 项目列表 → 打开看板；`#/` 仍直达上次项目看板。
- 使用 token：`surface.raised` `border.subtle` `action.primary-text` `text.muted` `z.sticky`
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 图标 + 文字（必须都有文字） | `text.muted` |
| hover | 不适用（触屏） | — |
| active | 当前项品牌色 + 加粗 | `action.primary-text` |
| focus-visible | 焦点环 | `focus.ring` |
| disabled | 不适用 | — |
| loading | 不适用 | — |
| invalid | 不适用 | — |

- 可访问性：单项点击区 ≥44×44；`aria-current="page"`；底部留 `env(safe-area-inset-bottom)`。
- 反例：只给图标不给文字（现状侧栏 56px 全隐藏就是移动端不可用的直接原因）。

## ui/SidebarNav（桌面/平板）

- 用途：桌面 240px 有文字标签；≤1024 收 64px 图标栏，**文字进 Tooltip**。
- 项清单（3 项，**不含「看板」**）：收件箱 / 产品 / 执行节点；与移动 `ui/TabBar` 同源同序，`/projects/*` 下钻时高亮父域「产品」。
- 底层：`AppShell.Navbar` + `NavLink`。
- 使用 token：`surface.raised` `surface.overlay` `action.primary` `text.primary` `space.3`
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 图标 + 文字，透明底 | `text.primary` |
| hover | 淡底 | `surface.overlay` |
| active | 左侧 2px 品牌竖条 + 淡底 + 加粗 | `action.primary` |
| focus-visible | 焦点环 | `focus.ring` |
| disabled | 不适用 | — |
| loading | 不适用 | — |
| invalid | 不适用 | — |

- 可访问性：折叠态保留 `aria-label` 与 Tooltip；不用 `display:none` 抹掉文字而不给替代。
- 反例：折叠后彻底无文字提示（现状）。

## ui/MarkdownViewer

- 用途：任务描述、上下文、附件正文。
- 结构：`h1/h2` 标题、正文 `body`、`code`/`pre` 等宽、`table` 细线、列表缩进。
- 使用 token：`text.primary` `surface.overlay` `border.subtle` `font-mono` `space.3`
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 正常渲染 | `text.primary` |
| hover | 链接 hover 下划线 | `action.primary-text` |
| active | — | — |
| focus-visible | 链接焦点环 | `focus.ring` |
| disabled | 不适用 | — |
| loading | Skeleton 行（5 行） | `surface.overlay` |
| invalid | **内嵌图片必显示占位**：`[图片：<alt>（平台不存二进制附件，无法预览）]` | `border.strong` |

- 可访问性：正文 ≥15；表格保留表头；图片占位文本可被读屏读出。
- 反例：内嵌图片静默空白（用户以为正文缺了一段）。

## ui/AttachmentRow

- 用途：详情与预览里的附件行。
- 结构：角色徽标（输入/输出）→ 标题 → 右侧类型。
- 使用 token：`surface.raised` `border.subtle` `space.3` `radius.8`
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 描边行 | `border.subtle` |
| hover | 淡底 + 描边加深 | `surface.overlay`, `border.default` |
| active | 打开预览 | — |
| focus-visible | 焦点环 | `focus.ring` |
| disabled | 附件不存在时置灰（同时给错误条） | `text.muted` |
| loading | 行内右侧小 loading | `text.muted` |
| invalid | 不适用 | — |

- 可访问性：整行一个按钮，`aria-label` 含角色与标题；点击区 ≥44。
- 反例：附件名做成 `<a target="_blank">` 裸链（本期必须应用内预览）。

## ui/Segmented

- 用途：评审「通过 / 驳回」二选一。
- 底层：`SegmentedControl`。
- 使用 token：`surface.overlay` `surface.raised` `feedback.danger` `shadow.e1`
- 状态矩阵：

| 状态 | 表现 | 使用 token |
|---|---|---|
| rest | 未选中项灰字 | `text.muted` |
| hover | 选项淡底 | `surface.overlay` |
| active | 选中项白底 + 轻阴影 | `surface.raised` |
| focus-visible | 焦点环 | `focus.ring` |
| disabled | 提交中禁用 | `text.muted` |
| loading | 提交中禁用 + 按钮 loading | — |
| invalid | 驳回但意见为空 → 意见框标红 | `feedback.danger` |

- 可访问性：`radiogroup` 语义，左右键切换。
- 反例：把「通过/驳回」做成两个并列按钮（无法表达互斥状态）。

## ui/Chip

- 用途：前置依赖 id、租约信息等 mono 碎片。
- 使用 token：`surface.raised` `border.subtle` `text.muted` `radius.pill` `caption`
- 状态矩阵：仅 `rest`；若可点击则继承 `focus-visible` 焦点环。
- 可访问性：`task_id` 用等宽字体，方便肉眼比对。
- 反例：把 40 字符 id 折行成 3 行（应省略中间/尾部并给 title）。

---

## 屏内私有详情抽屉（不属于 `ui/` 导出面）

产品与项目的**只读详情抽屉**复用 `ui/DetailSheet`（`readonly` 变体），不新增 `ui/` 组件：`ui/` 导出面仍是 21 个组件（`web/scripts/ui-imports-check.mjs` 断言导出集合，本契约不扩）。

- 页面私有部分落在 feature 层：`features/products/ProductDetailSheet.tsx`（6 字段：产品编号 / 名称 / 仓库 / 描述 / 证书 / 项目数）、`features/projects/ProjectDetailSheet.tsx`（9 字段：项目编号 / 产品编号 / 序号 / 名称 / 上下文 / 状态 / 创建时间 / 更新时间 / 关闭时间）。
- 两个抽屉共用的**键值网格**收敛在壳层 `features/shell/DetailFields.tsx`（与 `ErrorBar` 同层；`features/*` 允许 import `features/shell`）。它**不是** `ui/` 组件、不从 `web/src/ui/index.ts` 导出，因此不进总览表、不计入 21 个组件。
- 字段全部取自当前列表行，抽屉打开**不发请求**（0 请求）；因此没有 loading/error 态，只有 `rest`。
- 升级条件：若同构只读抽屉出现第三处（如执行节点详情），再按 D2 升级为 `ui/` 组件，并同步 `ui/index.ts`、`ui-imports-check.mjs` 的 `EXPECTED`、本文件总览表与 `web/README.md` 的「21 个组件」表述。
- 反例：为抽屉新增 `ui/ProductDetailSheet` / `ui/ProjectDetailSheet`（会把 `Product`/`Project` 的字段形状塞进 `ui/` 层，导出面 21→23，四处同步成本高）。

---

## 视觉语言 v2（氛围光 / 玻璃 / 渐变）

这一层不是新组件，是**全部组件共用的视觉规则**，实现时按 token 落地，不写裸值。

| 规则 | 做法 | 用到的 token |
|---|---|---|
| 氛围光 | 页面固定一层双径向渐变（左上品牌蓝、右上信息青），不拦截点击 | `action.primary` `feedback.info` |
| 细网格 | 56px 网格叠加，顶部居中径向遮罩衰减 | `border.subtle` |
| 标题渐变 | 页面主标题用「文字色 → 品牌蓝 → 信息青」渐变裁切 | `text.primary` `action.primary` `feedback.info` |
| 玻璃面板 | 卡片/列/元信息容器：半透明画布 + 背景模糊 + 1px 内高光 | `surface.canvas` `border.subtle` |
| 状态条 | 任务卡左侧 3px 状态色条 + 微弱外发光 | 7 种状态的语义色 |
| 列骨架 | 列顶部 2px 渐变线；`in_review` 列 3px 暖色线 + 品牌描边 | `action.primary` `feedback.warning` `feedback.info` |
| 主按钮 | 品牌蓝 → 青的 135° 渐变 + 投影 | `action.primary` `feedback.info` |
| 分段选中 | 选中项渐变底 + 白字（驳回态用红→橙渐变） | `action.primary` `feedback.danger` |
| 数字对齐 | 计数、时间、优先级用 `tabular-nums` | `text-mono` 域 |

暗色（`data-theme="dark"`）不是第二套设计，而是同一批 token 的暗色解析：surface `#070b14 → #101a2b`、text `#e9f0fb`、action 提亮到 `#3d6bff`、feedback 提亮（success `#2ee6a8` / warning `#ffce4d` / danger `#ff5d6c` / info `#6ee6ff`），氛围光与内高光同步加强。**暗色截图见 `refs-dark/`。**

**已定**：默认主题 = 暗色（决策人 2026-11-19）。两套 token 均已生成；亮色保留为可选对照。验收基准 = `refs/<screen>__<viewport>__dark__<state>.png`。
