# 附件在线预览改为整页居中浮层 技术方案 v0.2

- 状态：待评审（v0.2 为 v0.1 驳回后的返工版）
- 作者：CTO
- 日期：2026-11-23
- 输入：PRD `AGRAOBKMESEZYE42YCJ6YIL43E`（v0.1，级别 m）；代码 `Casper-Mars/echuan@bf9f45ed2a068709216280f10e9c43e55f3f3346`（`git fetch --prune origin` 后 `origin/main` = 该 commit，工作树干净）
- 关联：issue #128；#126（项目 echuan-9）同改 `attachment-preview.tsx`，先后见 §11 R7

## 1 结论

新增 `ui/ContentModal`（Mantine `Modal` 底座的「内容型整页浮层」），把附件预览改成**浮在整页之上的居中浮层**：宽高各 80vw / 80vh，水平垂直居中于视口，底图是看板 / 项目列表整页；**预览态整个详情抽屉隐去（面板与遮罩都不出现在画面上）**，不是叠在侧边抽屉上。≥1024 生效，<1024 保持抽屉形态并补齐头部与关闭语义。两处入口（任务详情 input/output、项目详情资料区）共用同一个 surface。

改动范围：前端 10 个文件（含 1 个新增）+ 设计契约 6 处；**0 个接口、0 张表、0 个新依赖**。

最大风险：隐去的抽屉仍挂载 → 需要证明它「真的不在画面上、也不抢滚动与焦点」→ §11 R1 / R5。

### 1.1 对上轮驳回的回应（本轮唯一改动方向）

驳回原文：*「文件浏览的弹窗不是叠在详情抽屉之上，抽屉本身就只是侧边的一小块，还叠在上面居中，那肯定是不行啊。而是整个页面，并且居中。」*

| 上一版（v0.1，被驳回） | 这一版（v0.2） |
|---|---|
| 抽屉保持可见，浮层叠在它上面居中 → 画面里同时有「右侧 720 抽屉」和「居中 80% 浮层」，报「叠在侧边小块上」 | **预览态抽屉整体隐去**（`visibility: hidden`，面板与遮罩都不可见），画面里只有「整页底图 + 居中 80% 浮层」 |
| 双层遮罩（0.35 + 0.35 ≈ 0.58） | 单层遮罩 0.35：底图就是整页（看板 / 项目列表），符合「整个页面」 |
| 抽屉层级 200 + 浮层 300 叠着 | 层级仍 300 > 200，但抽屉已不可见，层级不再承担视觉叙事 |

为什么不是「真把抽屉卸载/关闭」：见 §1.2 的 C2。机制上保留挂载是为了 AC5（关闭后回详情、scrollTop 差 0、详情不重载），视觉上它与卸载无差别——验收用 AC15 把「画面里没有抽屉」钉死。

### 1.2 选项对照（含「维持现状」与「被驳回的上一版」）

| 选项 | 做法 | 代价 | 结论 |
|---|---|---|---|
| A 维持现状 | 右侧 720 抽屉内二级面板 | 正文 439px（1440 视口 30%）；issue #128 诉求不成立 | 不做（CTO 已判 enhancement） |
| B 最小改动 | 只把 `--ui-layout-sheet` 从 720 抬到 80vw | 1 行；「居中」不成立；长正文撑出视口 | 不做；保留为紧急止血（§9） |
| **C1 叠在抽屉上** | 抽屉可见 + 浮层 80% 叠在其上居中 | 画面里侧边抽屉与居中浮层并存 → **2026-11-23 被驳回** | 不选 |
| C2 真卸载抽屉 | 预览态不渲染 `<DetailSheet>`，浮层独占画面 | 视觉正确，但 `useTaskDetail` 之外的抽屉内部状态与滚动槽一起销毁；重开要恢复滚动位置并承担 200ms 滑入动画；`keepMounted + opened=false` 也救不回滚动（Mantine 的 `Transition` 在 exited 且 keepMounted 时渲染 `display:none`，滚动槽被夹到 0） | 不选（AC5 成本最高） |
| **D 整页浮层 + 抽屉隐去（选）** | 抽屉保持挂载但 `visibility: hidden`；浮层全视口居中 80% | 画面外的 DOM 里仍有一个不可见 dialog，要证明它不抢滚动/焦点、不进无障碍树 | **选** |

D 的退出条件：AC15（画面无抽屉）或 AC5 / AC13 在 1440×900 / 1024×768 / 390×844 任一视口连做 3 次仍不过 → 退 C2，并把「详情数据与滚动位置上提到路由层」单开一张技术债卡。

### 1.3 三条硬决定

1. **视觉上「整页」，机制上「保留挂载」**：抽屉 `opened` 不变、`visibility: hidden`，滚动槽不销毁 → AC5 天然成立；画面里只剩整页底图 + 居中浮层 → 驳回点解决。
2. **<1024 不套 80%**（与 PRD §4 一致）：899 是「80% 减掉 280 列表后正文不窄于现状 439」的数学下限（439+280=719，719÷0.8≈899），768 宽下 80% 会把正文压到约 334，与 G1 反向。
3. **两种容器共用一套内容**：同一个两栏网格与六态渲染；取数 hook 挂在 surface 上，容器切换不重新取数（§5.3）。

## 2 目标与非目标

目标（PRD §2 原文）：

- G1 1440×900 浮层内正文可读区 ≥800px（基线 439px，PRD E1 实测 2026-11-23）。
- G2 浮层宽/高 ÷ 视口宽/高 = 0.8（±0.002）。
- G3 关闭预览后详情滚动槽 scrollTop 与进入前差 0。
- G4 六态（loading / empty / error / oversized / image-placeholder / 渲染降级）与改前不一致项 = 0。

非目标（逐条理由见 PRD §2 N1–N6，本文不重复）：

- N1 不改详情抽屉本身几何（720 右侧 / <768 全屏）；N2 不做 768–1023 的 80%；N3 不改取数与渲染链路（`use-attachment-preview` / `MarkdownViewer` / 六态文案）；N4 不加新预览入口；N5 不做拖拽 / 缩放 / 尺寸记忆 / 最大化；N6 不加 ≥1920 正文宽度上限。

## 3 AC 覆盖表

来源：AC1–AC14 照抄 PRD §6（原文，编号与判定方式一致）；**AC15 是本轮返工新增的验收点**（PRD 写于驳回之前，没有对应条目），派生几何值标注「推导」。

「接口」列无后端调用（预览仍走既有 `GET /v1/materials?id=`）；「数据」列无表字段——本卡是纯前端容器几何，§6 用几何与令牌口径替代数据模型（判据 2 的「回滚 SQL」以「无迁移、revert 即回滚」落空）。验证命令见 §10。

| AC | 原文要点 | 模块 | 接口 | 数据 | 验证方式 |
|---|---|---|---|---|---|
| AC1 | 1440×900 浮层 1152±2 × 720±2 | M1 M2 M5 | 无 | 无 | `[data-preview-surface="overlay"] .ui-content-modal__content`.getBoundingClientRect() |
| AC2 | 浮层中心与视口中心偏差 ≤2px | M1 M2 | 无 | 无 | 同上 rect 的 `(left+w/2, top+h/2)` vs `(720, 450)` |
| AC3 | 浮层内正文可读区 ≥800px（基线 439） | M2 M6 | 无 | 无 | `[data-ui="attachment-preview"] .fx-preview__body`.clientWidth；对照重出的 `refs/attachment-preview__desktop__dark__default.png` |
| AC4 | 头部同时有角色徽标 + 附件标题 + 关闭键，关闭键 `aria-label` =「关闭附件预览」 | M1 M3 M5 | 无 | 无 | 1440 与 390 各开一次：`.fx-preview__role` / `.fx-preview__title` 存在 + `[aria-label="关闭附件预览"]` |
| AC5 | Esc 关闭后回详情，scrollTop 差 0 | M3 M5 M7 M8 | 无 | 无 | 记录 `.ui-detail-sheet__body` scrollTop → 开预览 → Esc → 再记录，差 0 |
| AC6 | 浮层内切换附件不关闭，标题与正文切换 | M5 | 无 | 无 | 点第二个附件行后 `[data-ui="attachment-preview"]` 的 `data-material-id` 变化且浮层仍在 |
| AC7 | 左列表 280±1，正文滚动时列表 y 不变 | M6 | 无 | 无 | `.fx-preview__list` rect.width 与滚动前后 rect.y |
| AC8 | 390×844 抽屉 390±2 × 844±2，四周无留白 | M6 | 无 | 无 | 抽屉 content rect（<1024 形态不变） |
| AC9 | 900×800 抽屉宽 720±2（<1024 几何不变） | M3 M5 M6 | 无 | 无 | 抽屉 content rect 宽 720 |
| AC10 | 390×844 关闭键右边缘 ≤390、命中区 ≥44×44，点它回详情 | M3 M6 M7 | 无 | 无 | 关闭键 rect + 点击后 `data-preview="closed"` |
| AC11 | 项目详情资料区同几何浮层，关闭回项目详情 | M8 | 无 | 无 | `#/products/:pid/projects` → 资料行，重复 AC1/AC5 断言 |
| AC12 | 六态文案与动作与改前一致 | M5 | 无 | 无 | 逐态截图与 `/workspace/.verify/issue128/` 改前图对照 |
| AC13 | Tab ≥40 次焦点不逃逸；关闭后焦点回附件行 | M1 M3 M5 | 无 | 无 | `document.activeElement` 在浮层子树内；关闭后 50ms 断言回到触发行 |
| AC14 | 点浮层外遮罩不关闭，正文与滚动位置不变 | M1 M5 | 无 | 无 | 点遮罩区，断言 `data-preview="open"` 且正文 scrollTop 不变 |
| **AC15**（返工新增） | **预览态画面里没有抽屉**：抽屉面板与遮罩都不可见，底图是整页 | M3 M6 M9 | 无 | 无 | `getComputedStyle(drawerRoot).visibility === 'hidden'`；`document.elementFromPoint(1200, 450)` 命中的不是抽屉子树；截图 `refs/attachment-preview__desktop__dark__default.png` 里没有右侧抽屉 |

## 4 现状与改动点

### 4.1 逐文件

| 文件 / 模块 | 现状 | 改成什么 | 影响面 |
|---|---|---|---|
| `web/src/ui/ContentModal.tsx`（**新增**，M1） | 无「内容型浮层」先例；`ui/ConfirmModal.tsx` 只做小确认框 | Modal 底座：`centered` + 80vw×80vh + 头部（标题 + 关闭）+ 滚动正文（+ 可选 footer）；`zIndex = z.dialog`；遮罩 0.35 无模糊；`closeOnClickOutside=false`；Esc 关闭；焦点陷阱由 Mantine 提供 | 唯一新增进 `ui/` 的组件；导出面 21 → 22 |
| `web/src/ui/ui.css`（M2） | 只有 `.ui-confirm-modal*` / `.ui-detail-sheet*` 段 | 新增 `.ui-content-modal*` 段：几何（80vw/80vh + `max-width/max-height` 兜住）、头部、正文 `padding:0; overflow:hidden`、flex 列布局。类名用 Mantine `classNames` 挂，不依赖 `.mantine-*` 内部类名 | 组件层样式；`theme/*.css` 零改动（不触 300 行门禁） |
| `web/src/ui/DetailSheet.tsx`（M3） | `closeButtonProps` 写死 `aria-label="关闭详情"`；`Drawer` 默认 `closeOnEscape` / `trapFocus` / `lockScroll` 全开 | 加两个可选 props：`closeLabel?: string`（默认「关闭详情」）、`concealed?: boolean`（true → 根节点 `data-concealed` + `closeOnEscape={false}` + `trapFocus={false}` + `lockScroll={false}`，由 `ui.css` 把 `visibility` 置 hidden）。`opened` 与几何不变 | 三个变体共用；只影响预览态 |
| `web/src/ui/ui.css`（M2 同文件） | 无 | `.ui-detail-sheet[data-concealed='true'] { visibility: hidden }`（保留布局盒 → 滚动槽不被夹到 0；`visibility` 同时把它移出无障碍树与 tab 序） | 一条 3 行规则；不新增媒体查询 |
| `web/src/ui/index.ts` + `web/scripts/ui-imports-check.mjs` + `web/README.md`（M4） | 导出面断言 21 个组件（`ui-imports-check.mjs:30-52`） | 导出 `ContentModal`；`EXPECTED` 加一项；「21 个组件」表述改 22（`README.md:9,147,155`、`design/components.md:403,406,409`） | 门禁：漏改即 `ui-imports-check` 非 0 |
| `web/src/features/task-detail/attachment-preview.tsx`（M5） | 单一组件 `AttachmentPreview`，两栏网格铺在抽屉正文槽；`previewSheetTitle` 只返回徽标 + 标题 | 拆四块：`PreviewPane`（两栏网格，容器无关）、`PreviewBody`（六态，逻辑不动）、`AttachmentPreviewSurface`（选容器 + 持有取数 hook）、`usePreviewSheet`（给抽屉算出 title / concealed / closeLabel）。`previewSheetTitle` 保留 | 两个复用方都改走 surface；六态渲染零行为变更 |
| `web/src/features/task-detail/task-detail.css`（M6） | `.fx-preview` 用 `height: calc(100% + space-5*2)` + 负 margin 撑满抽屉正文槽（`:273-283`）；`.fx-detail__body[data-preview='open']{display:none}`（`:283`） | 基线几何改为**浮层口径**（`height:100%`、无负 margin）；`@media (max-width:63.99em)` 内保留抽屉口径；删除「预览时隐藏详情正文」规则（详情正文不再需要让位，抽屉整体隐去） | 断点只能用 `47.99em` / `63.99em`（style-debt ⑤） |
| AC10 关闭键（M3 M6） | PRD E1 实测 390×844 下关闭键 `rect.x=400`（视口宽 390）。代码层候选机制：Mantine 头部标题元素 `.mantine-Drawer-title`（`ModalBase.css` 的 `.m_615af6c9`）没有 `min-width:0`，min-content 被我们 `white-space:nowrap` 的标题撑开，关闭键靠自动外边距定位 → 推出视口 | 先按 E1 复现 rect，再给 Mantine 头部标题容器加可收缩约束（`ui/DetailSheet` 的 `styles.title` 或 `.ui-detail-sheet .mantine-Drawer-title{min-width:0;overflow:hidden}`）；实测机制不是它则按实测改，判据不变 | 判据：rect 右边缘 ≤视口宽、命中区 ≥44×44（AC10） |
| `web/src/features/task-detail/TaskDetailRoute.tsx`（M7） | 预览面板挂在 `<DetailSheet>` 正文槽；抽屉标题在预览态换成附件标题；`scrollTopRef` + `useLayoutEffect` 记账恢复滚动 | 抽屉 title / `concealed` / `closeLabel` 交给 `usePreviewSheet`；surface 作为抽屉的**兄弟节点**渲染（portal 到 body）；删除 `scrollTopRef` / `scrollSlot` / 恢复 effect（抽屉不再被隐藏或卸载，滚动位置天然不变） | 详情分区、评审区、底部操作条零改动 |
| `web/src/features/projects/ProjectDetailSheet.tsx`（M8） | 同一套二级面板塞进 `readonly` 抽屉（`:61`） | 与 M7 同构：`usePreviewSheet` + surface；`preview` 状态仍归本组件 | 项目资料区视觉与任务详情一致 |
| 设计契约（M9） | 见 4.2 | 见 4.2 | refs 36 张重出 |

### 4.2 设计契约增量（`design/`）

| 文件 | 改什么 |
|---|---|
| `design/components.md` | 总览表加 `ui/ContentModal` / `Modal` / 用途「附件预览（≥1024 整页内容浮层）」；新增 §ui/ContentModal（用途、几何 80vw×80vh 居中、结构、状态矩阵、可访问性、反例「用浮层承载表单/确认」）；§ui/DetailSheet（`:268-274`）补 `closeLabel` / `concealed` 两句 + 「预览态抽屉隐去、不参与构图」；`:403,406,409` 的「21 个组件」改 22 |
| `design/screens/attachment-preview.md` | `:6` 断点表：≥1024 整页居中浮层（80vw×80vh，底图为整页）；768–1023 详情内两栏面板（抽屉 720）；<768 全屏堆叠（≤32vh 列表）。`:7` 结构补「浮层头部 = 角色徽标 + 附件标题 + 关闭」，并写明「≥1024 无侧边抽屉出现在画面里」 |
| `design/flows.md` | F2 第 2 步（`:14`）：≥1024 改为「整页居中的 80% 浮层（详情抽屉在预览期间隐去）」；第 4 步（`:16`）：补「Esc / 关闭键只退浮层、回详情并保留滚动；焦点回触发的附件行」 |
| `design/mock/shots/attachment-preview/{mock.js,styles.css}` | `attachmentPage()` 现在只画 scrim + 侧边 `.drawer`；改为**看板整页底图**（复用 `boardBody`）+ 居中 80% 浮层（头部徽标/标题/关闭），`@media (max-width:63.99em)` 回落抽屉形态与 32vh 堆叠；六态文案不变 |
| `design/refs/attachment-preview__*` | 6 态 × 3 视口 × 2 主题 = **36 张**重出：desktop 1440 与 tablet 1024 是「整页底图 + 居中浮层」（**画面里不得出现右侧抽屉**），mobile 390 只改头部与关闭键 |
| `design/README.md` | 「契约同步记录」追加一条：增量 commit、范围、refs 口径、`design_ref` 处理（§11 未决 3）；`MANIFEST.sha256` 沿用「保留旧值、不重算」挂账（PRD §8） |

### 4.3 本次不改

`web/src/features/task-detail/use-attachment-preview.ts`（超时 / 并发去重 / 512 KB 门禁）、`ui/MarkdownViewer.tsx`、`ui/AttachmentRow.tsx`、`ui/Alert.tsx`、`ui/EmptyState.tsx`、`ui/SkeletonCard.tsx`、`features/shell/ErrorBar.tsx`、六态文案与兜底链接、`internal/**`、`api/openapi.yaml`、`design/tokens.json`（理由见 §6）。

## 5 总体设计

### 5.1 组件关系

```text
TaskDetailRoute ─┐
                 ├─ usePreviewSheet(preview, detailTitle) ─→ { title, concealed, closeLabel } ─→ ui/DetailSheet
ProjectDetailSheet ┘                                                  （预览态：concealed=true → 抽屉隐去）

TaskDetailRoute ─┐
                 ├─ <AttachmentPreviewSurface preview attachments />   （抽屉的兄弟节点，portal 到 body）
ProjectDetailSheet ┘        │
                            ├─ useAttachmentPreview(material_id)   ← 数据只在这里取，切容器不重挂
                            ├─ wide ? ui/ContentModal(整页 80vw×80vh) ─┬─ PreviewPane ─ PreviewBody（六态，逻辑不变）
                            └─ !wide ? PreviewPane（进抽屉正文槽）     ┘
```

分层红线不变：`features/**` 不 import `@mantine/core`，浮层壳必须在 `ui/`。因此**不采用**「屏内私有 Modal 壳」路线（`ProjectWriteModal.tsx` 那类私有先例只在不碰 Mantine 时成立；`grep -r "@mantine/core" web/src/features` 必须为空）。

### 5.2 新增/变更契约

`ui/ContentModal`（M1）：

| prop | 类型 | 默认 | 说明 |
|---|---|---|---|
| `opened` | boolean | 必填 | — |
| `title` | ReactNode | 必填 | 头部标题（预览态传 `previewSheetTitle(current)`） |
| `onClose` | () => void | 必填 | Esc 与关闭键都走它 |
| `closeLabel` | string | `'关闭'` | 关闭键 `aria-label`；预览传「关闭附件预览」 |
| `closeOnClickOutside` | boolean | `false` | 内容型浮层默认**不**点外关闭（AC14）；遮罩同时充当点击吸收层 |
| `footer` | ReactNode | — | 预留；本期预览不传 |
| `className` / `children` | — | — | 正文（`padding:0`，浮层内自管滚动） |

固定行为（不进 props，因为它是本需求的几何口径）：`centered`、`width/height = 80vw/80vh`（写在 `ui.css` 的 `.ui-content-modal__content`，由 `classNames` 挂载）、`zIndex = z.dialog(300)`（Mantine 的 Drawer/Modal 默认取 `elevations.modal = 200`，300 稳定在上层）、遮罩 `backgroundOpacity 0.35 / blur 0`、`trapFocus` 与 `returnFocus` 走 Mantine 默认、初始焦点落关闭键（`data-autofocus`，与 `ui/ConfirmModal` 的「打开时焦点落在取消键」同源）。

`ui/DetailSheet`（M3）：

| prop | 类型 | 默认 | 说明 |
|---|---|---|---|
| `closeLabel` | string | `'关闭详情'` | 关闭键 `aria-label`；<1024 预览态传「关闭附件预览」（AC4/AC10） |
| `concealed` | boolean | `false` | 被上层整页浮层接管：根节点 `data-concealed`，`closeOnEscape=false`、`trapFocus=false`、`lockScroll=false`，`ui.css` 置 `visibility:hidden`（保留布局与滚动位置，同时移出 tab 序与无障碍树） |

其余 props 与三个变体（`detail` / `preview` / `readonly`）不变。**不引入**「浮层 vs 抽屉」的双底座：`concealed` 只是临时让位，不改容器形态（N1）。

`features/task-detail/attachment-preview.tsx`（M5）：

| 导出 | 签名 | 职责 |
|---|---|---|
| `AttachmentPreviewSurface` | `{ attachments, current, onSelect, onBack }` | 选容器 + 持有 `useAttachmentPreview` + 渲染头部（浮层）/ 复用抽屉头部（<1024） |
| `usePreviewSheet` | `(preview: AttachmentRef \| null, detailTitle: ReactNode) => { title, concealed, closeLabel }` | 两个调用方共用，避免 title/concealed 在两处各写一遍后漂移 |
| `previewSheetTitle` | 不变 | 徽标 + 标题节点，浮层头部与 <1024 抽屉头部共用 |
| `PreviewPane` / `PreviewBody` | 内部 | 两栏网格 / 六态正文（`PreviewBody` 逻辑零改动） |

### 5.3 容器选择与断点

- 判定：`matchMedia('(min-width: 1024px)')`（值取 `theme/tokens.ts` 的 `breakpoint.md`，不写字面量 1024），`useSyncExternalStore` 订阅，与 `features/shell/use-layout.ts` 同写法。
- CSS 侧只用既有允许断点：`@media (max-width: 63.99em)` 承载抽屉内几何（style-debt ⑤ 只允许 `47.99em` / `63.99em`，写 `min-width:64em` 会直接红）。
- 跨断点切换：surface 组件本身不卸载，`useAttachmentPreview` 在它内部 → 只换壳、不重新取数、不出现骨架；`concealed` 随 `wide` 翻转（切到 <1024 时抽屉立刻恢复可见）。**已知行为**：正文滚动位置回 0（§11 未决 2）。

## 6 无数据模型改动（几何与令牌落点）

本期无表、无字段、无迁移，也就无回滚 SQL。UI 方案的「数据」是几何与令牌，逐项给出来源与落点：

| 值 | 来源 | 落点 | 为什么不是令牌 |
|---|---|---|---|
| `80vw` / `80vh` | PRD §4 / AC1 / G2 | `web/src/ui/ui.css` 的 `.ui-content-modal__content`（由 `ContentModal` 的 `classNames` 挂在 Modal content 上） | `design/tokens.json` 没有尺寸分组，`gen-tokens.mjs` 只映射固定分组（`color/semantic/type/space/radius/shadow/motion/breakpoint/z` + 结构常量 `border-width`）；新增分组要同时改生成器、两份生成产物与 design-kit 侧脚本，为一个视口比例不值。退出条件：出现第二个「视口比例」用途（如最大化浮层）时提升为 `tokens.json` 的 `layout` 组，并同步 `gen-tokens.mjs` |
| 左列表 280 | 既有 | `task-detail.css`：`calc(var(--space-10)*4 + var(--space-6))` | 已是令牌推导，保持 |
| 遮罩 `0.35` / `blur 0` | 与 `DetailSheet` 同口径（`DetailSheet.tsx:53`） | `ContentModal.tsx` | 遮罩不透明度是组件行为；本轮只有**一层**（抽屉已隐去），不再有合成问题 |
| `radius-16` / `shadow-e3` / `space-4` / `space-5` / `text-h3-*` / `z.dialog` | 令牌 | `ContentModal.tsx` + `ui.css` | 正常用法 |
| `visibility: hidden`（隐去抽屉） | 本轮返工口径（AC15） | `ui.css` 的 `.ui-detail-sheet[data-concealed='true']` | 是组件状态不是视觉值，不新增令牌 |

派生几何（算术值，供 AC 对照；浏览器实测由 dev 卡执行，本文不声称已实测）：

| 视口 | 浮层 | 居中偏移 | 正文可读区 | AC |
|---|---|---|---|---|
| 1440×900 | 1152 × 720 | 左 144 / 上 90 | 1152 − 280 − 12×2 = **848** | AC1/AC2/AC3 |
| 1024×768 | 819.2 × 614.4 | 左 102.4 / 上 76.8 | 819.2 − 280 − 24 ≈ **515** | 派生自 §4 规则（无独立 AC） |
| 900×800 | 720 宽抽屉（<1024） | — | 439（不变） | AC9 |
| 390×844 | 390 × 844 全屏抽屉 | — | 堆叠：列表 ≤32vh | AC8/AC10 |

## 7 组件契约（无 HTTP 契约改动）

后端零改动：预览仍走 `GET /v1/materials?id=`，超时 / 并发去重 / 512 KB 判定 / ETag 复用全在 `use-attachment-preview`，本期一行不动（PRD N3）。**鉴权 / 幂等 / 限流三栏不适用**：本章没有 HTTP 面可写（不改 `api/openapi.yaml`、不改 `routeSpecs`），联调成本全在组件契约上。

可断言钩子（验收与回归都用它，等同本期的「接口」）：

| 钩子 | 位置 | 取值 | 用途 |
|---|---|---|---|
| `data-preview-surface` | surface 根节点 | `overlay` / `sheet` | 区分两种容器；AC1/AC13/AC15 的定位锚点 |
| `data-concealed` | `ui/DetailSheet` 根节点 | `true`（预览态）/ 缺省 | AC15：抽屉隐去 |
| `data-ui="attachment-preview"` | 两栏网格根 | 不变 | 六态断言沿用既有选择器 |
| `data-material-id` | 两栏网格根 | 当前 material_id | AC6 |
| `data-state` | 两栏网格根 | `loading/default/error/oversized/empty`（渲染降级另有 `data-ui="preview-raw-fallback"`） | AC12 |
| `data-preview` | `TaskDetailRoute` 根 / `ProjectDetailSheet` 根 | `open` / `closed` | AC5/AC10/AC14 |
| `aria-label="关闭附件预览"` | 浮层关闭键 / <1024 抽屉关闭键 | 固定文案 | AC4 |

错误与降级：容器层不引入新的错误分支——浮层打开失败只有一种可能（shell 渲染异常），由 `features/shell` 既有错误边界兜底；正文侧的 404 / 413 / 超时 / 超大 / 空 / 渲染降级仍是 `PreviewBody` 原有六态（AC12）。

## 8 关键流程

### 8.1 正常路径（≥1024）

1. 详情里点附件行 → `openAttachment(ref)` → `preview = ref`；同一帧里 `concealed = true`（抽屉根 `data-concealed`，`visibility: hidden`，`lockScroll` 关掉），`opened` 保持 true。
2. `AttachmentPreviewSurface` 渲染 `ContentModal`（portal 到 body，`z-index 300`）→ 画面 = 整页底图（看板 / 项目列表）+ 居中 1152×720 浮层（AC1/AC2/AC15）；Mantine 焦点陷阱接管，初始焦点落关闭键。
3. 头部显示「角色徽标 + 附件标题」；两栏网格：左列表 280（当前项高亮）+ 右正文（六态）。
4. 点左列表另一附件 → `onSelect` → 只换正文与头部标题，浮层不关（AC6）。
5. Esc 或关闭键 → `onBack` → `preview = null` → 浮层关闭、`concealed` 复位、抽屉立刻可见（无动画、无重挂）；滚动槽全程未卸载 → scrollTop 差 0（AC5）；Mantine `useFocusReturn` 在 10ms 后把焦点还给触发行（AC13）。

### 8.2 正常路径（<1024）

抽屉形态不变：`usePreviewSheet` 返回附件的 `title`、`closeLabel='关闭附件预览'`、`concealed=false`；`PreviewPane` 铺在抽屉正文槽（`max-width:63.99em` 分支的负 margin 几何）；Esc / 关闭键仍由抽屉承接，`onClose = onBack`。

### 8.3 异常路径

| 场景 | 检测方式 | 处理 | 最终状态 |
|---|---|---|---|
| 预览态详情触发轮询刷新 | 详情数据在路由层，刷新只重渲染 | 抽屉隐藏但仍渲染，浮层不受影响 | 浮层保持打开，正文不闪 |
| 打开期间窗口跨 1024 | `matchMedia` change → `wide` 翻转 | 浮层 ↔ 抽屉面板换壳；`concealed` 同步翻转；`useAttachmentPreview` 未卸载 → 不重新请求、不出现骨架 | 正文内容与六态保持；正文滚动位置回 0（未决 2） |
| 两个 Dialog 同时要吃 Esc | 代码事实：Mantine 每个 Modal/Drawer 实例各自在 window capture 注册 keydown（`ModalBase/use-modal.mjs:22-31`），不做跨实例仲裁 | `concealed` 时抽屉 `closeOnEscape=false`，Esc 只属于浮层 | 一次 Esc 只关浮层，不连带关详情 |
| Tab 焦点跑到隐去的抽屉 | 代码事实：每个 active 的 FocusTrap 都在 document 上 `scopeTab` 自己（`@mantine/hooks/use-focus-trap.mjs`） | `concealed` 时抽屉 `trapFocus=false`，且 `visibility:hidden` 的元素本就不在 tab 序 | AC13 |
| 浮层内滚动被抽屉的滚动锁吞掉 | 代码事实：`react-remove-scroll` 每个 active 实例都在 document 上挂 non-passive 的 `wheel` / `touchmove`，事件目标不在自己的 shard 子树时按 `noIsolation` 直接 `preventDefault`（`SideEffect.js` 的 `shouldPrevent` 分支）——抽屉与浮层是两个互不相含的子树，抽屉的锁会掐掉浮层内的滚轮 | `concealed` 时抽屉 `lockScroll=false`（RemoveScroll 不再激活），滚动锁只由浮层持有 | 浮层正文可滚动（仍需实测，见 R6） |
| 点浮层外遮罩 | Modal 遮罩层铺满视口（300）并吸收点击 | `closeOnClickOutside=false` | 浮层不关（AC14） |
| 关闭瞬间焦点归属 | 抽屉 `trapFocus` 复位会 `setTimeout(focusNode)`；浮层 `useFocusReturn` 在 10ms 后归还 | 后者晚到，最终落在触发行 | AC13 后半（断言时点取关闭后 50ms） |
| 预览 404 / 413 / 超时 / 超大 / 空正文 / 渲染异常 | 既有六态 | 不变（`PreviewBody` 零改动） | 与改前一致（AC12） |
| 同一附件连点 | `use-attachment-preview` 并发去重 | 不变 | 无叠加请求 |
| 隐去期间详情取数失败 | 既有错误条（`fx-detail` 内） | 抽屉仍隐藏，用户看不到错误条 | 关闭浮层后可见；不影响预览（见 R9） |

## 9 部署与配置

新增依赖：**无**（`@mantine/core` 的 `Modal` 已在用，`ui/ConfirmModal.tsx` 就是它）。

| 项 | 值 |
|---|---|
| 环境变量 | 无新增 |
| 构建 | `make web`（内含 `node web/scripts/style-debt.mjs` + `pnpm --dir web build`（含 `tsc --noEmit` 与体积门禁）+ 同步 `internal/web/dist`） |
| 发布顺序 | 1) 前端产物 `internal/web/dist`（随 PR 提交）2) `make build` 单二进制 3) `make docker-build` 4) `make docker-up` |
| 灰度 | **不做**：内网单实例、无分组能力、无埋点可观测；灰度成本 > 回滚成本（单 commit 纯前端） |
| 回滚 | `git revert <commit>` → `make web` → `make docker-build` → `make docker-up`；或回退镜像 tag 重建容器。无数据变更、无迁移，回滚无残留 |
| 紧急止血（B 方案） | 把 `.ui-content-modal__content` 的宽高换回抽屉口径（`--ui-layout-sheet` + 100% 高）并把 `concealed` 固定为 false，即退回「抽屉内面板」 |

两条运维注意（写进 dev 卡的验证步骤，不写成本文的执行动作）：

1. `make docker-build` 收尾会执行**全局** `docker builder prune -f` 与 `docker image prune -f`（AGENTS.md §10），属破坏性操作，需人工确认后再跑。
2. 浏览器验收必须跑在**改动后的产物**上：`make web` 后重建镜像，或 `cd web && pnpm dev`（其 `/v1` proxy 硬编码 `127.0.0.1:8721`，见 AGENTS.md §3）。测试平台 `http://echuan:8720` 跑的是已部署镜像，不重建就看不到新几何。

## 10 可观测性

- **不新增指标 / 埋点**：AGENTS.md §11 明确禁止加指标，平台无前端埋点，PRD §7 已认账「发布后无结果指标」。由此**无新增指标名、无告警阈值**（无指标即无告警），失败可见性靠 §3 的断言与发布后的人工反馈（PRD 证伪条件：「还在用新标签打开原文」）。
- 日志：沿用既有 `console.error`（`MarkdownBoundary` 的渲染降级仍走这条），容器层不加日志（浮层没有运行时失败路径可记）。
- 观测 = 可断言的 DOM 钩子（§7 表）+ 截图对照：`[data-preview-surface]` 与 `[data-concealed]` 让「打开的是浮层还是抽屉面板」「抽屉是否隐去」在 DOM 上直接可判，回归脚本不必靠像素猜。
- 验收命令（dev 卡执行，顺序固定）：

```bash
node web/scripts/style-debt.mjs            # 6 项样式债门禁（断点集合 / 未引用类名 / :root / 行数）
pnpm --dir web build                       # tsc --noEmit + vite build + check-size
node web/scripts/ui-imports-check.mjs      # 断言导出面 = 22 个组件
node web/scripts/gen-tokens.mjs --check     # 令牌产物无漂移（本期不该有 diff）
make build && make test                    # 后端未改，仍按 AGENTS.md §3 走一遍
```

- 当前基线（2026-11-23 实测，供体积门禁对照）：`internal/web/dist/assets` 全部 JS 的 gzip 合计 **184,185 B**，阈值 344,056 B（占 53.5%）→ 新增一个 Modal 用法（约 1–3 KB gzip）有余量。

## 11 风险与未决

| # | 风险 | 触发信号 | 影响 | 缓解 | 回退 |
|---|---|---|---|---|---|
| R1 | 「隐去」被当成「还在叠着」 | 评审再问「抽屉不是还在吗」 | 返工两次 | 用 AC15 把可见性钉死：`visibility` 断言 + `elementFromPoint` + 截图里无右侧抽屉；并明确「真卸载」的代价（C2） | 退 C2 |
| R2 | 双层 Dialog 抢 Esc / 焦点（Mantine 每实例自注册，`Modal.Stack` 是「同时只开一个」的互斥用法，不适用） | AC5 一次 Esc 关掉详情；AC13 焦点来回跳 | 验收不过 | `concealed` 让抽屉让出 Esc 与焦点陷阱 | 退 C2 |
| R3 | 浮层关闭瞬间抽屉陷阱复位抢一次焦点（`useFocusTrap` 在 active 变 true 时 `setTimeout(focusNode)`），与 `useFocusReturn` 的 10ms 归还赛跑 | AC13 关闭后 `activeElement` 落在抽屉关闭键 | 焦点语义错 | 断言时点放关闭后 50ms；仍不稳则在 `onBack` 里记录触发行、关闭后双 rAF 显式归还 | 同上 |
| R4 | 跨断点切换时组件重挂 → 正文重新取数 | 缩放时闪骨架 / 多发一次 `GET /v1/materials` | 违反「正文不重载」 | 取数 hook 上提到 `AttachmentPreviewSurface`（不卸载） | 无（结构保证） |
| R5 | 隐去的抽屉仍在 DOM：无障碍树与 tab 序 | 读屏仍能读出「详情」dialog；Tab 能进隐藏面板 | a11y 缺陷 | `visibility:hidden` 本身移出 tab 序与 a11y 树；`concealed` 同时关 `trapFocus`；验收时用 `elementFromPoint` + Tab 走查双确认 | 若实测仍有残留，补 `aria-hidden="true"` + `inert`（ref 赋值） |
| R6 | 两个 `react-remove-scroll` 实例同时在场上，浮层正文滚不动：每个 active 实例都在 document 上挂 non-passive `wheel`/`touchmove`，事件目标不在自己的 shard 子树时直接 `preventDefault`（`react-remove-scroll/dist/es2015/SideEffect.js` 的 `shouldPrevent` / `noIsolation` 分支） | 浮层内滚轮无效 | AC3/AC7 直接不过 | `concealed` 时抽屉 `lockScroll=false`，滚动锁只由浮层持有 | 必要时给浮层 `removeScrollProps` 放行纵向 |
| R7 | 导出面 21→22 的连带改动漏一处 | `ui-imports-check.mjs` 非 0 或文档仍写 21 | 门禁红 / 文档漂移 | 同一 PR 内四处同改（`index.ts` / `EXPECTED` / `components.md` / `web/README.md`） | 无 |
| R8 | 样式债门禁被新样式打红 | `style-debt.mjs` 非 0（断点不在白名单、媒体查询内写令牌、`theme/*.css` >300 行） | 构建失败 | 新样式只落 `ui/ui.css` 与 `task-detail.css`；只用 `max-width: 63.99em`；`theme/*.css` 零改动 | 回退样式片段 |
| R9 | 隐去期间详情侧的错误条看不见 | 详情刷新失败时用户无感 | 信息丢失 | 关闭浮层即可见（既有错误条不变）；本期不加提示，登记为已知行为 | 无 |
| R10 | 设计契约不同步 → refs 里还是侧边抽屉 | refs 与实现互相打脸，且**正好复现驳回点** | 视觉回归失去对照 | M9 与实现同 PR；refs 重出必须含「无抽屉」的桌面/平板帧 | 以代码为准，回补契约 |
| R11 | 与 #126 同改 `attachment-preview.tsx` | rebase 冲突 | 合并顺序 | 建议 #126 先落（它改 `PreviewBody` 内部），本卡后 rebase，冲突面限于文件拆分处，凭 AC12 复跑六态 | 谁先合并谁先跑全量六态 |
| R12 | PRD 内部口径矛盾：§8 写「tablet 只改头部与关闭语义」，但 §4/§9 规则是 ≥1024 走浮层，refs 的平板视口正好是 1024×768 | refs 重出时平板出浮层，与 §8 字面不符 | 设计与 PRD 对不上 | 按 §4（有 AC 与 899 下限推导）执行：平板出浮层；差异登记未决 1 | 若 PM 判平板保持面板，阈值改 1280（一处常量） |

未决项：

| # | 问题 | 谁定 | 何时 | 未定前默认动作 |
|---|---|---|---|---|
| 1 | tablet（1024×768）按 §4 走浮层，还是按 §8 字面保持面板？ | 需求方（PRD 作者，经 CTO 转达） | dev 卡启动前 | 按 §4：≥1024 走浮层（R12） |
| 2 | 跨断点切换后正文滚动位置回 0 是否接受 | 需求方 | dev 卡启动前 | 接受；保留内容与六态，不保留正文滚动位置 |
| 3 | refs 重出后 `design_ref` 是否指向新的契约增量 commit | 设计侧 | 设计契约同 PR 内 | 更新为新 commit，并在 `design/README.md` 契约同步记录写清「dev 取图按新 commit」 |
| 4 | ≥1920 是否加正文宽度上限（PRD 未决 2 仍未拍板） | 需求方 | 同上 | 按 PRD：严格 80%，不加 cap |
| 5 | 预览态底图是「看板 / 项目列表整页」是否就是驳回意见里的「整个页面」 | 驳回人 | dev 卡启动前 | 按本方案：整页底图 + 单层 0.35 遮罩 |

### 11.1 实施顺序与依赖（不排期、不估人日）

1. **M1–M4（ui 层）先行**：`ContentModal` + 样式 + `DetailSheet` 的 `closeLabel` / `concealed` + 导出面 22。前置：无。后置：M5 起都依赖它。
2. **M5–M6（预览组件与样式）**：拆分 surface / pane / body，几何改为容器无关，删除「预览时隐藏详情正文」规则。前置：M1–M4。
3. **M9 与 M5 并行**：设计契约 + mock（改为整页底图 + 居中浮层）+ refs 36 张重出；refs 必须在视觉对齐验收（AC3/AC12/AC15）之前产出。前置：断点口径定稿（未决 1 用默认值即可启动）。
4. **M7–M8（两处入口）**：任务详情与项目详情同构接入。前置：M5。
5. **文档与验收**：`web/README.md`（21→22 与「附件预览」段落，属 M4）、`design/README.md` 契约同步记录（属 M9）；命令见 §10，AC1–AC15 按 §3 逐条验。前置：全部。

外部前置：PRD 已定稿（本次输入）；#126 的合并顺序见 R11；镜像重建的破坏性操作需人工确认（§9）。

---

## 改了什么

1. **按驳回意见改掉了叠层模型**：v0.1 是「浮层叠在可见的侧边抽屉上」，v0.2 改为「预览态整个详情抽屉隐去（`visibility: hidden`，面板与遮罩都不进画面），浮层浮在整页之上、居中 80%」——即驳回原文要的「整个页面，并且居中」。对应改动：`ui/DetailSheet` 新增 `concealed`（同时关掉 Esc / 焦点陷阱 / 滚动锁）、`ui.css` 一条 `data-concealed` 规则、`TaskDetailRoute` 删掉「预览时隐藏详情正文」的旧逻辑。
2. 视觉与机制分离写清楚：画面里只有整页底图 + 单层 0.35 遮罩 + 居中 1152×720 浮层；DOM 里抽屉仍挂载，是为了 AC5（回详情 scrollTop 差 0、详情不重载）。为什么不用「真卸载」或「keepMounted + opened=false」：Mantine 的 `Transition` 在 exited 且 keepMounted 时渲染 `display:none`（`esm/components/Transition/Transition.mjs:46,48`），滚动槽会被夹到 0，仍要额外恢复逻辑并带 200ms 滑入动画（§1.2 选项 C2）。
3. 新增 AC15 把驳回点钉成可判定项：`getComputedStyle(drawerRoot).visibility === 'hidden'` + `elementFromPoint` 不落在抽屉子树 + 截图里没有右侧抽屉（配设计契约 refs 重出）。
4. 其余不变：仍然新增 `ui/ContentModal`（导出面 21→22），仍然两种容器共用一套两栏网格与六态；<1024 保持抽屉形态（PRD N2）；0 接口、0 表、0 新依赖。

## 怎么自检

1. 仓库同步与基线：`git fetch --prune origin` 无新提交、`git checkout main` 已在 main、`git rev-parse HEAD` = `bf9f45ed2a068709216280f10e9c43e55f3f3346`（与 PRD 基线一致）、`git status --porcelain` 空；全程只读，无分支、无提交。
2. 代码事实核对（读源码，不靠记忆）：
   - Esc：`@mantine/core/esm/components/ModalBase/use-modal.mjs` 每个实例在 window capture 注册 keydown，无跨实例仲裁 → 抽屉必须让出 Esc；
   - 焦点：`@mantine/hooks/esm/use-focus-trap/use-focus-trap.mjs` 每个 active 陷阱都在 document 上 `scopeTab` 自己 → 两层同时 active 会互抢；
   - 归还焦点：`use-focus-return.mjs` 关闭后 10ms 归还到打开前的 `activeElement`；
   - 隐去语义：`ModalBase` 用 `react-remove-scroll` 做滚动锁（`ModalBase.mjs:4,71-74`），所以隐去时必须同时关 `lockScroll`；
   - 滚动锁冲突：`react-remove-scroll/dist/es2015/SideEffect.js` 的 `shouldPrevent` 在事件目标不属于自己的 shard 子树时按 `noIsolation` 直接 `preventDefault` → 抽屉与浮层两个锁同开会掐掉浮层滚动（R6）；
   - 关闭态 `keepMounted` 是 `display:none`（`Transition.mjs:46,48`）→ 不能靠它保滚动；
   - 层级：`get-default-z-index` 的 `elevations.modal = 200`，令牌 `z.dialog = 300` 稳定压上；
   - 门禁：`style-debt.mjs` 断点白名单只有 `47.99em` / `63.99em`、`theme/*.css` ≤300 行；`ui-imports-check.mjs:30-52` 断言 21 个组件；`.fx-preview` 的负 margin 几何在 `task-detail.css:273-283`。
3. 设计资产核对：`ls design/refs | grep -c attachment-preview` = 36（6 态 × 3 视口 × 2 主题）；截图视口 1440×900 / 1024×768 / 390×844；`design/mock/shots/attachment-preview/mock.js` 的 `attachmentPage()` 目前只画 `scrim + .drawer`（无整页底图）→ 已登记为 M9 的必改项；`design/README.md:68-75` 的 MANIFEST「保留旧值、不重算」挂账口径。
4. 体积门禁基线实测：对 `internal/web/dist/assets` 下全部 `.js` 逐个 gzip(level 9) 求和 = **184,185 B**，阈值 344,056 B（占 53.5%）。
5. 浏览器未执行（design 卡按 CTO 边界只做只读 / 静态分析）：AC1–AC15 的 DOM 实测留给 dev 卡；§3 给逐条断言方式，§6 给派生几何（1440×900 → 1152×720、正文 848）。

## git 交付

不适用：`design` 阶段卡不产生代码交付物，仓库全程只读，无分支、无 commit、无 PR、无 compare 链接。核对基线 `main@bf9f45ed2a068709216280f10e9c43e55f3f3346`。方案正文的建议入库路径（由实现卡随 PR 提交）：`docs/tech/attachment-preview-overlay-128-v0.2.md`。

## 遗留风险

- 本轮没起浏览器：`1152×720`、正文 `848`、平板 `819.2×614.4` 都是算术值；实际像素、Mantine 过渡期是否有 1–2px 抖动都要 dev 实测。
- 「两个 `react-remove-scroll` 同时在场会挡住浮层滚动」是读源码得出的**判断**（`SideEffect.js` 的 `noIsolation` 分支，R6），必须实测；`concealed` 关 `lockScroll` 是按这条判断下的预防措施，不是已验证结论。
- `visibility:hidden` 是否足以让读屏与 Tab 完全忽略那个 dialog（R5），要键盘走查 + 无障碍树抽查确认；不够就补 `aria-hidden` / `inert`。
- AC10 的根因（390×844 下关闭键 `rect.x=400`）只给到候选机制，需实测确认后再动手。
- 与 #126 合并顺序未定（R11）；未决 1–5 未拍板前按「默认动作」执行，口径要改走变更单。
