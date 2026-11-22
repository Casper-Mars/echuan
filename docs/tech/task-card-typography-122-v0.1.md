# 任务卡片排版与文字裁切问题 技术方案 v0.1

- 状态：待评审
- 作者：CTO
- 日期：2026-11-22
- 输入：Issue [#122](https://github.com/Casper-Mars/echuan/issues/122)（标签 bug，open，无评论，原文：「现在看来，任务卡片中的字体都贴着边了，部分情况字还缺了一些。」）；代码基线 `Casper-Mars/echuan@540c2a94ca878799235efe969f4fe7178f3c613c`（`main`，2026-11-22 20:08 +0800，merge PR #121）；设计契约 `design/components.md`（`ui/TaskCard` §71–90）与 `design/mock/styles.css:470–500`；运行时观测对象 = 部署在 `http://echuan:8720` 的构建，其产物与基线一致的证据见 §3.2

---

## 1 结论

**做最小改动：把 TaskCard 命中区的内边距重新变成生效的那一版，并给标题补一条断词规则。不重排字阶（字号/行高），不动设计契约，不动组件结构。**

一句话根因（已实测，不是推测）：任务卡片的 `padding: var(--space-3)` 声明**被 Mantine `UnstyledButton` 的 `padding: 0` 在打包顺序上压掉了**——两者特异性相同（都是 0,1,0），Mantine 规则在样式表里更靠后，于是卡片内距实际为 `0px`，文字直接压在描边与 3px 状态色条上。这就是「字体都贴着边了」；状态色条压住每个卡片首字符的前 3px（`task_id` 的首字符 `t` 同样被压），以及 `overflow: hidden` 静默吃掉超长不可断串，合起来就是「部分情况字还缺了一些」。

- 改动范围：前端样式层 1 个文件（`web/src/ui/ui.css` 的 TaskCard 段，2 处声明级改动），0 接口、0 数据表、0 组件结构改动；`internal/web/dist` 需按既有流程重生成。
- 不做的：不改字号/行高、不改设计契约、不新增断点、不新增依赖、不扩大卡片里塞的内容。
- 最大风险：卡片高度 +16px，列内可视卡片数略减（§11 R1）；以及「8px 内距是否仍偏紧」需要 design 用 ref 实测值拍板（§11 U1）。

## 2 目标与非目标

目标（来自 Issue 原文，两条都可判定）：

| 编号 | 目标 | 判定口径 |
|---|---|---|
| G1 | 卡片内文字与卡片边缘之间恢复可见内距，状态色条不再压字 | 三视口下文本左边缘距卡片内框 ≥ 8px；状态条覆盖区不出现任何字形 |
| G2 | 「部分情况字缺了一些」不再发生：不再有被静默裁掉的字符 | 三视口 × 长文本/长不可断串下，标题与元信息无横向溢出、无半个字被描边切断 |

非目标（本期明确不做，理由在后）：

1. **不重排卡片字阶**（标题 15px/1.57、ID 13px 保持现状）：改字阶会牵动 8 屏 refs 对比与 `design/components.md` 契约，属视觉设计改动，不是 bug 修复；契约漂移单列 §11 U2。
2. **不引入新的卡片变体、不加减卡片里的字段**：卡片的信息取舍是产品决策（§11 U3）。
3. **不做全局「Mantine 覆盖」审计**：同类隐患只在本卡登记为未决项（§11 U4），不在本方案上默默扩模块。
4. **不改任务详情抽屉的排版**：详情抽屉正文不是 TaskCard，实测内距 16px 正常（§3.3），不属本缺陷面。
5. **不改设计契约与 refs**：契约修改需要 design 负责人拍板（§11 U1/U2），本方案不代拍。

## 3 影响面

### 3.1 缺陷面（组件/状态/界面）

TaskCard 在仓库里只有一个实现、三处复用（`web/src/ui/TaskCard.tsx:50–82`；引用点：`features/board/BoardPage.tsx:178`、`features/inbox/InboxPage.tsx:180`、`features/queue/QueuePage.tsx:115`）。因此缺陷面 = 三处复用点 × 五种状态。

| 界面 | 路由 | 容器几何（基线 commit 实测/读码） | 状态 | 实测 |
|---|---|---|---|---|
| 看板列（desktop） | `#/projects/:id/tasks` | 列宽 `--bp-column-w` = `space-10×4 + space-6` = 280（`features/board/board.css:29`），列内栈 `padding: 0 8px 8px`（`ui/ui.css:314–319`） | rest / hover / focus（in_review 首卡） / 折叠列展开后 | 卡片 251×123，命中区 `padding: 0px`，文本左偏移 1px（= 描边） |
| 看板列（mobile 390×844） | 同上 | 列宽 `min(86vw, space-10×5)`（`board.css:46`），吸附横滑 | 同上 | 卡片 291，`padding: 0px`，文本左偏移 1px |
| 收件箱 | `#/inbox` | `.ix-grid` `minmax(260,1fr)` + `.ix-card{min-inline-size:0}`（`features/inbox/inbox.css:145–155`） | 待审核 / 待领取 / 派单队列 三个 section；待审核首卡 focus | 卡片 279，`padding: 0px` |
| 派单队列 | `#/queue` | `.qx-grid` 与 `.ix-grid` 同口径（`features/queue/queue.css:136–145`） | 按项目分组，空态以外 | 复用同一组件与同一几何；**现场无 ready 任务，未做浏览器复核（读码结论）** |
| 加载态 | 三处 | `SkeletonCard`（`.ui-skeleton-card.ui-skeleton-card` 带内距） | loading | 内距 8px 生效 → 与真实卡片**不同几何**（见 §3.4） |

命中区的声明本身没错，错在它没赢：

```
ui/ui.css:167–175  .ui-task-card__hit { padding: var(--space-3); padding-inline-start: calc(var(--space-3) + var(--ui-status-bar)); … }
Mantine UnstyledButton  .m_87cf2631 { padding: 0; border: 0; background-color: transparent; … }
```

打包后两条规则同在 `index-C8Or43x7.css`：`.ui-task-card__hit` 是第 24 条规则，`.m_87cf2631` 是第 339 条；同特异性、后者更靠后 → `padding: 0` 胜出，`padding-inline-start` 同样被清成 0。**本方案定案前必须承认的一点：这不是「忘了写内距」，是「写了但输了」。**

三个需要单独说明的覆盖点（按卡的要求逐条对齐）：

- **状态表达**：卡片内没有 `StatusPill`，状态由 3px 左侧色条 + 整卡 `aria-label`（`TaskCard.tsx:61`）表达；色条正是压住首字符的那个元素。**「状态徽标」在卡片上的唯一形态就是这条色条**，因此它同时是缺陷面，不是可忽略的装饰。
- **标签（多标签）**：元信息行里 `KindBadge`（Mantine Badge，`KindBadge.tsx:26–43`）+ P + 尝试 N/M + 时间，`flex-wrap: wrap` 允许多行；实测三视口均为单行（22px），未触发换行路径，换行后的行为属 §4 H4 的「待验」分支。
- **中英文与数字混排**：标题同一字号行高（15px/1.57），元信息与数字走 `text-caption` + `tabular-nums`（`ui.css:202–205`），`task_id` 走 mono 13px 单行省略；混排不改变换行规则，唯一风险是「不含空格的长串」（URL/token）——即根因 B。

### 3.2 证据基线与复现环境

- 代码基线：`git fetch --prune origin` → `main` = `540c2a94ca878799235efe969f4fe7178f3c613c`（工作树干净）。
- 运行时证据与基线同源：`internal/web/dist/assets/index-C8Or43x7.css` 与 `index-Dr3IahRB.js` 是**被 tracked 的构建产物**，`http://echuan:8720` 返回的正是这两个文件名；dist 里的 `.ui-task-card__hit` 规则文本与 `web/src/ui/ui.css:167–175` 逐字一致 → 浏览器观测可归到基线 commit。
- 观测手段：浏览器（headless）读取 `getComputedStyle` / `Range.getBoundingClientRect` / `scrollWidth` 与三视口截图，均为只读，未改仓库代码；截图见 §8.3。

### 3.3 已排除的相邻面

| 面 | 检查 | 结论 |
|---|---|---|
| 任务详情抽屉 | 打开 `#/projects/echuan-4/tasks/t_01M8…` 实测 `.ui-detail-sheet__body` `padding: 16px`，首个子元素左偏移 16px | 不贴边，不属本缺陷；`ui/DetailSheet.tsx` 的正文是普通 `div`，不与 Mantine 根类冲突 |
| 详情底部操作条「按钮回行」 | `features/task-detail/task-detail.css` 的动作行均 `flex-wrap: wrap`（:183/:223/:236） | 已有换行策略；TaskCard 内部依设计反例不嵌二级按钮（`design/components.md:90`），本卡不涉及 |
| 卡片宽度约束 | 列宽固定 / grid `minmax(260,1fr)` + 子项 `min-inline-size: 0` | 三视口实测卡片宽 251/291/279，均未撑破容器；非根因（§4 H7） |

### 3.4 缺陷面带来的连带不一致

`SkeletonCard` 用双类名选择器（`.ui-skeleton-card.ui-skeleton-card`，`ui/ui.css:545–553`）→ 内距 8px 生効；真实卡片内距 0。于是**加载态与完成态的卡片几何本来就不同**（骨架 `min-block-size` 88，`features/inbox/inbox.css:157–160`；真实卡片实测 76–123）。本方案修好内距后卡片会再高 16px，这个差值会被放大——需要按 §11 U5 定口径，本方案不擅自改骨架几何。

## 4 可证伪的根因假设

一条一条给验证方法与判定证据；只有被证据支持的我写成结论，其余写「不成立」或「待验」。

| # | 假设 | 验证方法（可复跑） | 实测定量 | 判定 |
|---|---|---|---|---|
| H1 | 容器内边距缺失**或被覆盖** | 遍历 `document.styleSheets` 找命中该元素的规则；比对 `getComputedStyle(...).padding` 与声明的令牌值 | 命中区声明 `padding: var(--space-3)`（8px）；实际计算值 `padding: 0px`；同特异性规则 `.m_87cf2631{padding:0}` 位于第 339 条、本组件规则第 24 条 | **成立 = 根因 A（贴边的主因）** |
| H2 | 左侧状态色条压住首字符 | 读 `::before` 的 `inset-inline-start/width`；用 `Range` 取文本首字符矩形与「描边 1px + 色条 3px」比较 | 色条 `width: 3px`、`inset-inline-start: 0`（`ui/ui.css:123–131`）；卡片左 264，文本首字符「t」起点 265，色条覆盖到 268 → 每个卡片**首个字形被压 3px**；`task_id` 的 `t_` 在 1440/390 截图上均被叠压在色条下 | **成立 = 根因 A 的可见放大效应**（A 修好后自动消失） |
| H3 | 固定行高导致裁切 | 比较标题 `clientHeight` 与 `scrollHeight`、行盒与 `line-height` | 普通 2 行标题 47/47；注入长标题后 71/259（3 行可视、11 行内容）；`line-height: 23.55px = 15 × 1.57`，无固定高度、无 `max-height` | **不成立**：没有固定行高裁切；3 行 `-webkit-line-clamp` 是有意截断 |
| H4 | flex 子项被压缩 | 三视口量各子项 `clientWidth` 与容器宽、`meta` 行高 | 标题/元信息/ID 宽度均 = 内容盒宽（249/289），`gap: 4px` 生效；元信息行高 22（单行），`flex-wrap: wrap` | **不成立**（现实宽度下无压缩）。**待验**：元信息里的 Mantine `Badge` 默认 `flex-shrink: 1`，若某行的单项本身宽于容器（极端窄列 + 超长 kind 文案）会被压到省略；仓内已有同类前车（`ui/ui.css:73–89` 记录 issue #89 A1：Mantine Pill 的 `flex: 0` 把 mono 文本压没） |
| H5 | overflow 策略缺失 → 静默裁字 | 向标题注入长不可断串（100+ 字符 URL），量 `scrollWidth` 与 `clientWidth`；查 `overflow-wrap`/`word-break` | 注入后 `scrollWidth 287 > clientWidth 249`（横向溢出 38px），标题 `overflow: hidden`（`ui/ui.css:183–191`）且未设 `overflow-wrap`/`word-break` → 溢出部分被卡片 `overflow: hidden`（`ui/ui.css:110–120`）整段吃掉，不留省略标记 | **成立 = 根因 B（「字缺了一些」的第二个来源）** |
| H6 | 字号与行高不成比例 | 比对实现与契约、量行高比 | 实现：标题 15px / 1.57 / 字重 500；契约 `design/components.md:75` 写 `text.small`，mock `.task-title` 用 `--text-small-size`(13px)/1.45；ID 实现 13px、mock `.task-id` 用 caption 12px | **不成立（不是裁切原因）**，但确认为**契约漂移**：同样的 8px 内距，15px 字的视觉密度明显高于 13px，这让「贴边」更刺眼 —— 单列 §11 U2 |
| H7 | 宽度约束缺失 | 读列宽/grid 定义并量卡片宽 | 列固定 280/`min(86vw,320)`；grid `minmax(260,1fr)` + `.ix-card/.qx-card{min-inline-size:0}`；卡片 `width: 100%` | **不成立** |
| H8 | 圆角 + `overflow: hidden` 裁掉文字（外部容器问题） | 逐层量 `clientWidth` 与子元素 `scrollWidth` | 卡片、命中区 `scrollWidth == clientWidth`（除 H5 的注入样例） | **不成立** |

结论归并：

- **根因 A（贴边）**：`web/src/ui/ui.css:167–175` 的单类名规则被 Mantine 同特异性规则按源序覆盖 → 内距 0。
- **根因 B（缺字）**：`overflow: hidden` + 无断词策略，长不可断串横向溢出后被静默裁切。
- **放大因素 C（同样导致缺字）**：3px 状态条按设计压在内容盒左缘（因为它假设了 11px 起始内距），内距为 0 时直接盖住首字符。
- 关联缺陷 D（不同几何）：骨架卡有内距、真实卡片没有，修好 A 后差值放大。

## 5 方案选项与对照

| 选项 | 做什么（模块级） | 代价 | 风险 | 影响范围 |
|---|---|---|---|---|
| **A 维持现状** | 不改 | 0 | Issue #122 不关；三处复用点全部维持「文字贴边 + 首字被色条压 + 长串缺字」；后续新卡片会照抄 `.ui-task-card__hit` 这个输掉的选择器写法，缺陷复制扩散 | 0 文件 |
| **B 最小改动（推荐）** | ① `web/src/ui/ui.css` TaskCard 命中区规则改用仓内既有双类名写法（`.ui-task-card__hit.ui-task-card__hit`，特异性 0,2,0，与 `.ui-chip.ui-chip` / `.ui-skeleton-card.ui-skeleton-card` / `.ui-task-card.ui-task-card` 同一约定），内距按契约取值：block `space-3`、inline-start `space-3 + ui-status-bar`（8/11）、inline-end `space-3`；② 给 `.ui-task-card__title` 补断词规则（`overflow-wrap: anywhere`），让不可断串在 3 行内断行而不是横向溢出；③ 不改字阶、不改结构、不动 `design/**` | 新增 0 依赖；改动 1 个文件的 2 处声明；卡片高度 +16px | 低：纯样式层，可单 commit 回退；列内可视卡片数 -1 张（§11 R1）；可能与将来 Mantine 样式顺序再碰撞（缓解：双类名 + 在规则旁写清原因） | `web/src/ui/ui.css`（TaskCard 段）→ 看板 / 收件箱 / 队列三个复用点 + 加载态对比；`internal/web/dist` 重生成一次 |
| **C 按 design token 统一修正卡片间距与文字排版** | B + 把实现拉齐契约：标题回到 `text.small`(13px/1.54)、ID 回到 `text.caption`(12px)、卡片内距若 design 判定偏紧则升到 `space-4`(12px) 并在契约里写明「内距 + 状态条」的合成口径；同步 `design/components.md`、`design/mock/styles.css`，重出受影响 refs | 视觉整体变化（字变小、密度变高）；需重跑 8 屏 refs 对比；契约同步成本；跨屏一致性复核 | 中高：改动面从「1 处样式」扩到「契约 + mock + refs」，任何一处不同步都会造成新的契约漂移；若只改实现不改 refs，等于把缺陷换个方向 | `web/src/ui/ui.css`、`design/components.md`、`design/mock/**`、`design/refs/**`（重出）、三处页面视觉快照 |

选择理由（先说选什么）：**本期选 B**。

- A 不能选：缺陷可复现、可归因、可单点修复，没有理由带着走。
- C 不能在本期合并做：它把「修 bug」和「重定视觉契约」捆在一起，后者需要 design 负责人拍板（§11 U1/U2），且会把回归面从 3 个页面扩到 8 屏 refs。**C 不是被否定，是被排在 B 之后作为契约收口**：B 落地后若 design 仍判内距偏紧，只需把 §6 参数表里的 `card-pad` 从 8 换 12（单点替换），不需要重做方案。
- B 的边界明确：只改「赢不赢」和「断不断」，不改「多大、多高、多密」。

**退出条件（什么证据会推翻 B）**：

1. 若 B 落地后三视口截图仍显示文字贴边 → 说明还有更高优先级的覆盖源（如内联样式或另一条 `!important`），本方案作废，回到 §4 重新定位。
2. 若 design 用 ref 实测判定卡片内距必须是 `space-4` 及以上 → B 的内距取值由 §11 U1 改判，方案其余部分不变。
3. 若同一张卡同时被要求改字阶（C 范围）→ 按项目边界回到决策与方案，不在本方案上加模块。

## 6 布局与排版规则

本节是给实现的规则表，不含补丁。数值全部来自令牌（`web/src/theme/tokens.css:81–92`、`theme/base.css:44–59`），不写 px 字面量。

### 6.1 卡片几何（默认口径）

| 项 | 规则 | 令牌/推导 | 现值 |
|---|---|---|---|
| 卡片最小宽度 | 卡片自身**不设** `min-inline-size`；由父容器保证：看板列固定宽、grid 子项 `min-inline-size: 0` | — | 251 / 279 / 291（1440/收件箱/390 实测） |
| 内距 block | `space-3` | 8px | 生效值 0（缺陷）→ 目标 8 |
| 内距 inline-start | `space-3 + ui-status-bar`（内距 + 状态条宽），保证色条落在内距里、不压字 | 8 + 3 = 11px | 生效值 0 → 目标 11 |
| 内距 inline-end | `space-3` | 8px | 目标 8 |
| 子项间距 | `space-2` | 4px | 4（已生效） |
| 状态条 | 宽 `ui-status-bar`（3px），贴 `inset-inline-start: 0`，**必须完全落在内距内** | `base.css:52` | 3px（已生效，但因内距为 0 压字） |

### 6.2 文本规则（字号/行高/换行/省略）

| 元素 | 字号/行高（本期不改） | 换行策略 | 省略策略 | title 属性 |
|---|---|---|---|---|
| 标题 `.ui-task-card__title` | `text-body` 15px / 1.57 / 字重 500 | 整行换行（中文按字、西文按词）；**不可断串按字符断行**（新增 `overflow-wrap: anywhere`） | 最多 3 行，第 3 行行尾省略（`-webkit-line-clamp: 3` 自带省略号）；**不允许横向裁切** | 不需要（3 行已含足够信息）。移动端无 hover「查看全文」口径见 §11 U6 |
| 元信息 `.ui-task-card__meta` | `text-caption` 12px | `flex-wrap: wrap`：整项换行 | 不省略、不缩字；单项不压缩（`flex: 0 0 auto`，防 issue #89 A1 同类压缩） | 不需要 |
| 任务 ID `.ui-task-card__id` | `text-mono` 13px | 单行，不换行 | `white-space: nowrap` + `text-overflow: ellipsis`，**保留 `title` 完整值**（现状已正确，不改） | 必须保留 |

判定顺序（一句话版）：**标题超过 3 行 → 行尾省略；标题里有不可断的长串 → 先按字符断行，仍然超 3 行才省略；元信息 → 只换行不省略；ID → 只单行省略且必须留 title。**

### 6.3 溢出策略

- 卡片保留 `overflow: hidden`（保证圆角、状态条、玻璃层不出界），但**它只允许裁掉装饰，不允许裁字**：因此子元素必须自己把内容收敛（换行或省略），不能依赖父级裁切。
- 任何文本元素**不得**以「溢出后被裁」代替「换行或省略」。

### 6.4 响应式

- 不新增断点（`web/scripts/style-debt.mjs` ⑤ 只允许 `47.99em` / `63.99em` 一套）；卡片在 320–1440 用同一套几何，靠父容器宽度自适应。
- 窄视口极限：320px 视口下按 `--bp-column-w = min(86vw, 320)` 估算卡片内宽 ≈ 257px；元信息最长组合（kind + P + 尝试 1/3 + 时间）按令牌宽度加 3 个 `space-2` 间隔估算 ≈ 166px，可整行容纳（两项都是估算，实际值以 §8.2 的三视口截图实测为准）；不足时按 §6.2 换行。

## 7 改动点与不改清单（模块级）

| 模块 | 现状 | 改成什么（规则，不是补丁） | 类 |
|---|---|---|---|
| `web/src/ui/ui.css` TaskCard 命中区 | 单类名选择器，内距声明被 Mantine 覆盖（生效值 0） | 用仓内既有双类名约定提升特异性，使 §6.1 内距生效 | 改（B 的核心） |
| `web/src/ui/ui.css` 标题 | `-webkit-line-clamp: 3`，无断词策略 | 补断词规则（§6.2） | 改（B 的次项） |
| `web/src/ui/ui.css` 元信息 | `flex-wrap: wrap` | 追加单项不压缩的显式约束（§6.2） | 改（防同类压缩，一行） |
| `web/src/ui/TaskCard.tsx` | 结构：标题 → 元信息 → ID，整卡一个按钮 | 不变 | 不改 |
| `design/components.md` / `design/mock/**` / `design/refs/**` | 契约写 `space.3` + `text.small`，实现用 15px | 本期不动；契约收口见 §11 U1/U2 | 不改 |
| `web/src/ui/SkeletonCard*`、`features/*/**.css` | 骨架有内距、页面栅格各自定义 | 本期不动；几何对齐口径见 §11 U5 | 不改 |
| `internal/web/dist/**` | tracked 构建产物 | 按既有流程 `make web` 重生成后提交一次（不在源码改动同一步里手工编辑） | 改（流程产物） |

前置条件（顺序，不写排期）：

1. 先定 §11 U1（内距取 8 还是 12）——这是唯一会影响数值的决策；未定则按 8 实施。
2. 源码改动 → `make web`（内含 `style-debt.mjs` 与 `check-size.mjs` 两道门禁）→ dist 与源码同 commit。
3. 三视口截图回归（§8.2）通过后再交平台验收。

## 8 验收口径

### 8.1 可判定项

| 编号 | 判定 | 方法（可复跑） | 通过线 |
|---|---|---|---|
| AC1 | 内距生效 | 三视口读 `getComputedStyle(命中区).paddingBlockStart / paddingInlineStart / paddingInlineEnd` | 8 / 11 / 8（若 U1 改判为 12 则 12 / 15 / 12） |
| AC2 | 状态条不压字 | 量卡片左缘 → 文本首字符矩形的水平距离，与「描边 + 色条」比较 | 首字符矩形左边缘 ≥ 卡片左缘 + 1px(描边) + 3px(色条)，即无像素重叠 |
| AC3 | 文字不贴边 | 量标题/元信息/ID 的左边缘相对卡片内框 | ≥ 8px（三视口一致） |
| AC4 | 长文本（极端 1） | 标题为 ≥120 全角字符（或用 §4 H5 的注入法造样例） | 卡片高度 ≤ 3 行标题 + 元信息 + ID；第 3 行末可见省略标记；`scrollWidth ≤ clientWidth` |
| AC5 | 长不可断串（极端 1 之二） | 标题含 100+ 字符无空格串（URL / token / 长 ID） | 3 行内按字符断行；无横向溢出；无被描边切掉的半个字 |
| AC6 | 窄视口（极端 2） | 390×844 与 320×800 两档 | 卡片不撑破列/网格（`卡片宽 ≤ 容器内宽`）；元信息可换行且无字被裁；ID 单行省略且 `title` 保留完整值 |
| AC7 | 三处复用一致 | 看板 / 收件箱 / 队列（队列若无可派任务，按读码 + 组件复用说明豁免并注明） | 同一卡片在三处内距与断行行为一致 |
| AC8 | 焦点环不被裁 | Tab 到卡片 | 焦点环完整包裹整卡，`overflow` 不裁环 |
| AC9 | 门禁不回归 | `make web` | `style-debt.mjs` 与 `check-size.mjs` 均 exit 0；无新增断点、无重复选择器 |
| AC10 | 加载态差值已记录 | 量骨架卡与真实卡高度 | 高度差写入交付报告（本项只要求「记录」，是否对齐见 §11 U5） |

### 8.2 人工核对方式（三视口截图对比）

- 视口：**1440×900（desktop）/ 768×1024（tablet）/ 390×844（mobile）**，另加 320×800 作为窄视口加强档（只用于 AC6，不作为设计基准）。
- 状态矩阵（每个视口都要过）：长标题（≥120 字）/ 超长不可断串 / 多标签（kind+P+尝试 N/M+时间）/ 长 `task_id` / focus 变体 / 折叠列展开 / 加载态。
- 对比三方：改前截图（本方案已采：看板 1440、看板 390、收件箱 1440、长串注入样例）、改后截图、`design/refs/*__board__*` 与 `*__inbox__*` 基准图。
- 命名与落盘：`shots/issue122/<viewport>-<state>.png`，与改前图同名成对，评审逐项勾 AC1–AC8。
- 判读口径：**贴边看文本左缘与描边之间的可见空隙；缺字看字形边缘是否被色条/描边截断、行尾是否有省略标记**，不靠肉眼「感觉紧凑」。

### 8.3 本方案已采集的改前证据（只读）

| 图 | 内容 |
|---|---|
| `issue122-board-1440-scrolled.png` | 看板 desktop：7 张卡片文字贴边、色条压首字、第 4 张卡末行「（复」被右缘截断 |
| `issue122-board-390-card.png` | 看板 mobile（2x DPR）：同上，首字被色条压得更明显 |
| `issue122-inbox-1440.png` | 收件箱 desktop：两张待审核卡均贴边；`task_id` 首字符被压 |
| `issue122-probe-longtoken.png` | 注入 100+ 字符 URL 后的标题：三处裁切现象（贴边 + 首字被压 + 行尾省略被挤出可视区） |

（这些图存于本卡交付物与 `.verify/issue122/`，不进仓库；仓库只收方案正文。）

## 9 观测与回滚

### 9.1 样式回归如何被发现（检查方式 + 检查点）

按「自动门禁 → 断言脚本 → 人工截图」三层，前两层可复跑，第三层是验收依据：

| 层 | 检查 | 检查点 | 失败信号 |
|---|---|---|---|
| 构建门禁 | `make web`（`style-debt.mjs` + `pnpm build` + `check-size.mjs`） | 无重复选择器、断点仍一套、未引用类名占比 < 20%、JS gzip ≤ 344,056 B | 任一项非 0 退出 |
| 断言脚本（一次性，随卡交付） | headless 打开三处页面，按 §8.1 AC1–AC6 逐项断言 | 内距 8/11/8；每个文本节点 `scrollWidth ≤ clientWidth`；卡片内后代 rect 不越出 padding box；卡片宽 ≤ 容器内宽 | 任一断言失败即返回失败明细（视口 + 页面 + 卡片文本前 20 字） |
| 人工截图 | §8.2 三视口 × 状态矩阵 | 与改前图成对比较，与 ref 基准图对齐 | 任一项判「仍贴边 / 仍有字被截」 |

需要盯的**信号面**（本缺陷最容易复发的地方）：

1. `web/src/ui/ui.css` 里任何**单类名**选择器挂在 Mantine 根类元素上——Mantine 一升级/打包顺序一变就可能重演；规则旁必须写明原因。
2. 卡片几何变化（内距/字号/行数上限）一旦改动，`SkeletonCard` 与 `--ix-skeleton` 的高度口径要同步核对（§11 U5）。
3. dist 与源码不同步：改了 `web/src` 却没重生成 `internal/web/dist`，线上不会变——`index-*.css` 文件名变化是「真的生效」的最简判据。

### 9.2 回滚

- 触发判据（任一即回滚）：三视口截图出现「文字仍贴边」、出现新的溢出容器现象、卡片高度变化导致列内滚动行为异常、`make web` 门禁失败无法在当轮修复。
- 回滚动作：`git revert <本卡 commit>` → `make web` 重生成 `internal/web/dist` → 重新构建并替换二进制；无数据迁移、无接口变更、无配置项，因此**回滚是纯样式回退，不涉及数据**。
- 回滚后状态：视觉回到 `540c2a9` 现状（含缺陷），缺陷由后续卡重新处理；不允许「只回滚源码不回滚 dist」——那会让源码与线上不一致。
- 灰度：不适用（内网单二进制、无灰度通道）；改用「三视口截图 + 断言脚本」作为替换前的门禁。

## 10 未决项

| 编号 | 未决 | 谁定 | 缺什么信息 | 未定前的默认动作 |
|---|---|---|---|---|
| U1 | 卡片内距取 `space-3`(8px) 还是 `space-4`(12px)？8px 是契约值，但实现标题是 15px（契约写 13px），密度更高 | design 负责人（`design/` 契约维护者） | ref 上「卡片描边到文字」的实测像素值，以及 design 对 15px 字阶下 8px 内距的观感判定 | 按契约 `space-3` 实施；升级只需改 §6.1 一个参数 |
| U2 | 标题字阶是否回到契约 `text.small`(13px/1.54)、ID 是否回到 `caption`(12px)？ | design 负责人 | 契约收紧 vs 实现现状的取舍；重出 refs 的成本由谁承担 | 保持实现现状（15px/13px），本卡不动 |
| U3 | 标题「最多 3 行」是否保留？长标题被截断是信息量取舍 | PM | 任务标题在列表里需要露出多少字符才算可判断（是否有长标题导致误点的实例） | 保留 3 行 |
| U4 | 同类「单类名选择器被 Mantine 覆盖」隐患是否系统排查？ | CTO（转维护卡） | 排查清单（哪些 `ui-*` 规则挂在 Mantine 根类上）与优先级 | 不排查；本卡只记录约定 |
| U5 | 骨架卡与真实卡片的高度对齐口径（骨架 `min-block-size` 88、真实卡片 76–123） | design 负责人 | 加载态→完成态允许的高度跳变上限 | 只记录差值（AC10），不调骨架 |
| U6 | 移动端无 hover，长标题是否提供「长按/展开查看全文」 | PM + design | 移动端长标题的可读性诉求强度；是否与「整卡可点」冲突 | 不提供；保留 3 行 + 省略 |

## 11 风险与假设

| 风险 | 触发信号 | 影响 | 缓解 | 回滚 |
|---|---|---|---|---|
| R1 卡片变高（+16px），列内可视卡片数减少约 1 张 | 1440×900 下同一列卡片数从 N 变 N-1 | 扫读效率略降 | 接受（可读性优先）；不动列宽、不动卡片字阶；在验收截图里记录列内卡片数变化 | §9.2 |
| R2 与 Mantine 样式顺序再碰撞 | 升级 Mantine 或调整打包顺序后内距再次变 0 | 缺陷复发 | 采用双类名（0,2,0）比 Mantine 根类高一级；规则旁注释写明原因；断言脚本可随时复跑 | §9.2 |
| R3 断词规则改变既有标题折行位置 | 现有卡片标题行数变化（例如 3 行变 2 行） | 视觉位移，属预期内 | 只有「不可断串」的标题会变；中文标题不受影响 | §9.2 |
| R4 只改源码未重生成 dist | `index-*.css` 文件名未变 | 线上不生效，验收与实现不一致 | `make web` 作为交付前置条件，交付报告里贴新旧 `index-*.css` 文件名 | §9.2 |
| R5 契约漂移（15px vs 13px）继续存在 | review 时被指出「与 ref 不符」 | 观感争议反复 | 列 U1/U2 由 design 拍板，不在本卡内隐式决定 | — |
| R6 队列页未能在现场复现（无 ready 任务） | AC7 缺一处的浏览器证据 | 复用一致性缺一环 | 队列与收件箱共用同一组件与同一栅格口径（读码证据）；验收时若有 ready 任务则补测，否则在交付报告里写明「未复核」 | — |

假设（已在方案内按最低风险取默认值，均为可推翻）：

1. 「贴边」指卡片内距丢失，而不是 design 要求更大的内距——由 H1 的实测支撑；若 U1 改判为 12px，只影响 §6.1 一个参数。
2. 三行截断是设计意图（`design/components.md:74` 明写「最多 3 行省略」），因此不按缺陷处理。
3. 部署环境仍是「单二进制 + tracked dist」，因此 dist 重生成是必要条件（读 `Makefile:52–62` 与项目既有口径 E3）。

---

附：本方案不涉及数据模型与接口契约（无字段、无 API 变更），因此不设「数据模型」「接口契约」「关键流程」章节；部署仅涉及前端产物重生成，已在 §7 前置条件与 §9.2 回滚中写明。
