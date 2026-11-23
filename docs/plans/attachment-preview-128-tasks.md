# 附件预览改整页居中浮层（issue #128）· 任务拆分总表（2 卡）

- 来源：breakdown 卡的计划 JSON（平台据此生成 dev 卡，本文件与其逐项同源）；本卡上游物料 `AGRAOIMZXEOBFPIICYUEUWC3C4`
- 上游依据：issue #128「文件在线查看改成弹窗（页面正中心，80% 大小）」、技术方案 `docs/tech/attachment-preview-overlay-128-v0.2.md`（由 T2 随卡入库）、界面契约 `design/`（v0.3 + 本计划 T1 的契约增量）
- 分支模型：项目分支 `feature/echuan-10`；每卡分支 `task/<平台任务号>`，PR base `feature/echuan-10`
- 产物归属：T1 只产设计侧对照源（`design/` + 36 张 refs），不碰产品代码；T2 产 `web/src` 实现并**在同一个 PR 内**重建 `internal/web/dist`（本仓无收口卡）
- 本计划不含排期与提交顺序；拆卡不替开发决定文件名/函数名，实现边界以方案 §4/§5 为准

## 拆卡理由（为什么是 2 张）

- **验收层冲突**：T1 在「契约与像素」层判（refs 逐张产出、`design/check.mjs` 0 错误、mock 能出六态），T2 在「DOM 与行为」层判（AC1–AC15 断言）。T1 产出**对照源**，T2 的目标是**与对照源一致**——不是同一次验收能一起判的。
- **依赖方向**：T2 的 AC3 / AC12 / AC15 直接引用 T1 重出的 refs 文件名，必须先有 refs。
- 计划结构：**T1 → T2 单链**（关键路径即此链）。
- 缓冲：若 design-kit 截图工具不可用，T1 在报告里标「未执行 + 原因」，T2 的验收退回 DOM 断言（AC1–AC14）先行、AC3/AC12/AC15 的像素对照挂起，不得整链卡死。
- 不设「收口 / 回归」卡：`internal/web/dist` 重建并入 T2（本仓近期提交 `52f1d90` / `e5abfd7` 都是「源码 + dist 同 PR」）。

## 依赖总表

| seq | 标题 | 依赖 | 优先级 |
|---|---|---|---|
| 1 | 设计契约：整页居中浮层形态落到 `design/` 并重出 `attachment-preview` 的 36 张 refs | — | 70 |
| 2 | 实现：附件预览改为整页居中 80% 浮层（≥1024）+ <1024 头部与关闭语义，两处入口 | T1 | 60 |

## 依赖图（箭头 = 前置 → 依赖）

```
T1 设计契约 + 36 张 refs ──→ T2 实现（ui/ContentModal + 两处入口 + dist 重建）
```

**关键路径**：T1 → T2。T2 可在 T1 未合并时先写代码，但 AC3 / AC12 / AC15 的像素对照必须用 T1 重出的 refs（`design_ref` = T1 的契约增量 commit sha）。

## 集成点

1. **T1 → T2 的契约面**：T1 定 `ui/ContentModal`（几何 80vw×80vh 居中、`Modal` 底座）与 `ui/DetailSheet` 的 `closeLabel` / `concealed` 两个 prop 语义；T2 按此实现，不自创第二套浮层。
2. **导出面 21 → 22**：T1 只改 `design/components.md` 的三处表述与总览表；`web/src/ui/index.ts`、`web/scripts/ui-imports-check.mjs` 的 `EXPECTED`、`web/README.md` 由 T2 同步（漏改即门禁红）。
3. **两处入口同构**：任务详情 input/output 与项目详情资料区共用同一个 surface（`AttachmentPreviewSurface`），T2 内一次做到，不拆两张卡。
4. **断点唯一**：≥1024 浮层、<1024 抽屉；768–1023 仍是抽屉 720（不做 80% 形态）。

## 各卡正文

### T1 设计契约：整页居中浮层形态落到 `design/` 并重出 36 张 refs

- 依赖：—；优先级：70
- 负责人（角色）：设计侧（`design/` 物料维护 + ui-prototype 截图；本卡不碰产品代码）
- 里程碑（可检查结果）：36 张 refs 重出且桌面 / 平板帧里没有右侧抽屉 + `design/check.mjs` 退出码 0 + 契约四件改完可 grep 命中

#### 目标（可观察结果）

把「整页居中浮层」形态写进 `design/` 契约，并重出 `attachment-preview` 的 36 张 refs 作为实现卡唯一的视觉对照源：**桌面 1440 与平板 1024 的画面里不能再出现右侧 720 抽屉**（这正是上一版技术方案被驳回的点）。

#### 范围

- `design/components.md`：总览表加 `ui/ContentModal`（底座 `Modal`，用途「附件预览（≥1024 整页内容浮层）」）；新增 §ui/ContentModal（用途 / 几何 80vw×80vh 居中 / 结构 / 状态矩阵 / 可访问性 / 反例「用浮层承载表单与确认」）；§ui/DetailSheet 补 `closeLabel` / `concealed` 两句并写明「预览态抽屉隐去、不参与构图」；「21 个组件」表述改 22
- `design/screens/attachment-preview.md`：断点改为「≥1024 整页居中浮层（80vw×80vh）/ 768–1023 详情内两栏面板（抽屉 720）/ <768 全屏堆叠（列表 ≤32vh）」；结构补「浮层头部 = 角色徽标 + 附件标题 + 关闭」，并写明「≥1024 画面里没有侧边抽屉」
- `design/flows.md` F2：第 2 步改「≥1024 整页居中的 80% 浮层（详情抽屉在预览期间隐去）、<1024 详情内二级面板」；第 4 步补「Esc / 关闭键只退浮层，回详情并保留滚动位置；焦点回触发的附件行」
- `design/mock/shots/attachment-preview/{mock.js,styles.css}`：`attachmentPage()` 改为看板整页底图（复用 `boardBody`）+ 居中 80% 浮层 + 头部（徽标 / 标题 / 关闭），`@media (max-width:63.99em)` 回落抽屉形态与 32vh 堆叠；六态文案不变
- `design/refs/attachment-preview__*`：6 态 × 3 视口 × 2 主题 = **36 张**重出
- `design/README.md`：契约同步记录追加一条（增量 commit、范围、refs 口径、`design_ref` 处理）+ 完整截图命令
- 本文件：把计划总表落到仓库

#### 任务级验收（可观察）

1. `ls design/refs | grep -c attachment-preview` = **36**，且 36 张 mtime 全部晚于本卡开工时间（证明是重出，不是沿用旧图）
2. 抽查 `refs/attachment-preview__desktop__dark__default.png` 与 `__tablet__dark__default.png`：画面中央是 80% 浮层，右侧**没有** 720 抽屉面板（改前旧图用 `git show <base>:design/refs/...` 取出并排对照）
3. 抽查 `refs/attachment-preview__mobile__dark__default.png`：仍是全屏抽屉 + 头部（徽标 / 标题 / 关闭）
4. `design/components.md` 总览表 22 行且含 `ui/ContentModal`；`grep -n "closeLabel\|concealed" design/components.md` 命中 §ui/DetailSheet 段
5. `grep` 命中三类新口径，`screens` / `flows` / `components` 各至少 1 处：「≥1024 整页居中浮层」「画面里没有（侧边）抽屉」「焦点回附件行」
6. 重跑命令可复现，完整命令写入 `design/README.md`
7. `node <design-kit>/scripts/check.mjs design` 退出码 0（kit 不可用则报告写「未执行 + 原因」，不得写「通过」）
8. `git diff --stat design/MANIFEST.sha256` 为空（未重算）

#### 不做

- 不改 `web/src`（组件、样式、门禁脚本都在 T2）
- 不动 `design/tokens.json`（无新令牌）
- 不重算 `design/MANIFEST.sha256`（沿用「保留旧值，不重算」的既有挂账）
- 不改其它 7 屏的 refs
- 不做 768–1023 的 80% 形态

### T2 实现：附件预览改为整页居中 80% 浮层（≥1024）+ <1024 头部与关闭语义，两处入口

- 依赖：T1；优先级：60
- 负责人（角色）：开发侧（`web/src` 实现 + 构建产物重建；不碰 `design/` 契约文本）
- 里程碑（可检查结果）：AC1–AC15 全绿 + 五条门禁全绿 + `internal/web/dist` 随 PR 提交

#### 目标（可观察结果）

附件预览在 ≥1024 变成**浮在整页之上、视口居中的 80vw×80vh 浮层**，预览期间整个详情抽屉隐去（画面里没有侧边抽屉，底图是看板 / 项目列表整页）；<1024 保持抽屉形态但补齐「关闭附件预览」语义与可达的关闭键；任务详情与项目详情资料区两处入口一致；六态文案零回退。

#### 范围

- `web/src/ui/ContentModal.tsx`（新增）+ `web/src/ui/ui.css`（`.ui-content-modal*` 几何与结构、`.ui-detail-sheet[data-concealed='true']{visibility:hidden}`）
- `web/src/ui/DetailSheet.tsx`（新增 `closeLabel` / `concealed`）+ `web/src/ui/index.ts` + `web/scripts/ui-imports-check.mjs`（`EXPECTED` 加 `ContentModal`）+ `web/README.md`（21→22 与附件预览段落）
- `web/src/features/task-detail/`：`attachment-preview.tsx`（拆 `AttachmentPreviewSurface` / `PreviewPane` / `PreviewBody` / `usePreviewSheet`）、`task-detail.css`（`.fx-preview` 几何改容器无关，删除「预览时隐藏详情正文」规则）、`TaskDetailRoute.tsx`
- `web/src/features/projects/ProjectDetailSheet.tsx`（第二处入口同构接入）
- `docs/tech/attachment-preview-overlay-128-v0.2.md`（方案入库）
- `internal/web/dist`（`make web` 重建后随 PR 提交）

#### 依赖

- **前置（方向：T1 → 本卡）**：T1（设计契约 + 36 张 refs 重出）——AC3 / AC12 / AC15 的视觉对照必须用重出后的 refs；refs 未就绪时可先写代码，但验收不能判「通过」。

#### 任务级验收（可观察）

1. 视口 1440×900 打开任一附件预览：`[data-preview-surface="overlay"]` 的内容盒 rect = 1152±2 × 720±2，中心与视口中心 (720, 450) 偏差 ≤2px（AC1 / AC2）
2. 同一状态下正文容器 `clientWidth` ≥800（记录实际数值，改前基线 439），左列表宽 280±1，滚动正文时左列表 `rect.y` 不变（AC3 / AC7）
3. 同一状态下抽屉不可见：抽屉根节点 `visibility` 计算值为 `hidden`；`document.elementFromPoint(1360, 450)`（浮层右边界 1296 之外的抽屉区域）返回的元素 `closest(".ui-detail-sheet") === null`（AC15，本轮返工的核心验收点）
4. 头部三项齐全（角色徽标 + 附件标题 + 关闭键），关闭键 `aria-label` =「关闭附件预览」；1440 与 390 各验一次（AC4）
5. 详情滚动到中部后开预览 → 按 Esc：浮层关闭、回详情，`.ui-detail-sheet__body` 的 scrollTop 与进入前差 0，`data-preview` 变 `closed`（AC5）
6. 浮层内点左列表另一附件：浮层不关，头部标题与正文切换，`data-material-id` 变化（AC6）
7. 视口 900×800 打开预览：容器仍是 720 宽抽屉（<1024 几何不变）；视口 390×844：抽屉 390×844、关闭键右边缘 ≤390、命中区 ≥44×44、点击回详情（AC8 / AC9 / AC10）
8. 六态逐态与 `refs/attachment-preview__*` 对应帧一致，文案与动作按钮无回退（AC12）
9. 打开后连按 Tab ≥40 次，`document.activeElement` 始终在浮层子树内；关闭后 50ms 断言焦点回到触发的附件行（AC13）
10. 点浮层外遮罩：浮层不关闭，正文滚动位置不变（AC14）
11. 项目详情资料区点任一资料：与任务详情同几何的居中浮层，关闭回项目详情（AC11）
12. 门禁全绿：`node web/scripts/style-debt.mjs` / `pnpm --dir web build`（含 `tsc --noEmit` 与体积门禁）/ `node web/scripts/ui-imports-check.mjs`（导出面 = 22）/ `node web/scripts/gen-tokens.mjs --check` / `make build && make test` 全部退出码 0
13. `internal/web/dist` 已随 PR 重建提交（`git show --stat HEAD` 中能看到 dist 产物变化），二进制里不是旧前端

#### 未决项的默认动作（卡不被阻塞）

- 平板 1024×768：按方案 §4 走**整页浮层**（阈值 = `min-width: 1024px`）。若要改成「平板仍面板」，走变更单，不在本卡改口径。
- 跨断点缩放后正文滚动位置回 0：默认接受（内容与六态不丢，不重新取数）。
- ≥1920 不加正文宽度上限：按 PRD 严格 80%。

#### 不做

- 不改详情抽屉本身几何（右侧 720 / <768 全屏）
- 不做 768–1023 的 80% 浮层
- 不改 `use-attachment-preview`（8s 超时 / 并发去重 / 512 KB 门禁）、`MarkdownViewer`、六态文案
- 不加新预览入口、不做拖拽 / 缩放 / 最大化、不加 ≥1920 宽度上限
- 不改后端与 `/v1` 契约、不动 `design/tokens.json`、不重算 `design/MANIFEST.sha256`
- 不把 #126（echuan-9）的 JSON 渲染改动夹带进来

## UI 设计引用

- T1 的 `design_ref`（**对照基线，非本卡产物**）：`032c2ca243dd1229a569ccc9f0bc1a03fed56231`——动工前的契约 commit，用于证明 refs 前后有差异；T1 产出后把**契约增量 commit 的 sha** 登记到 `design/README.md` §契约同步记录。
- T2 的 `design_ref`：**T1 交付的契约增量 commit sha**（在 PR 描述里写死；不得引用未合并的本地 sha）；改前基线 `032c2ca…` 仅用于未变形状态的对照。
- 必须覆盖的状态：`attachment-preview` 的 default / empty / loading / error / oversized / image-placeholder × desktop 1440 / tablet 1024 / mobile 390 × 暗色基准 + 亮色对照（共 36 张）。

## 共同口径

- 分支 `task/<平台任务号>`，PR base `feature/echuan-10`；本计划不含 merge main 的步骤，冲突一律保留双方改动并回报
- T1 不提交 `internal/web/dist`；T2 必须在 PR 内重建提交 `internal/web/dist`（`make web`），不提交 `web/node_modules`
- 截图一律用 design-kit 的 `screenshot.mjs`（CDP 指向环境自带 browserless），不自行安装 Chrome；浏览器不可用时如实登记「未执行 + 原因」
- 没跑过的命令写「未执行 + 原因」，不伪造退出码；不以「我跑过了」代替命令与退出码
