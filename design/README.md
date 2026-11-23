# 驿传 Echuan · Web UI 设计实例（v0.3）

内部管理台的界面设计契约。**技术选型与技术方案在 `docs/decisions/ADR-0001-web-ui-stack.md` 与 `docs/tech/web-ui-redo-v0.1.md`；本目录只管「长什么样、怎么交互」。**

## 范围

| 项 | 内容 |
|---|---|
| 平台形态 | Web（桌面 ≥1280 / 平板 768–1279 / 移动 <768），内网单二进制 |
| 屏 | 8 屏：`products` `projects` `board` `task-detail` `attachment-preview` `inbox` `workers` `queue` |
| 技术底座 | Mantine 8（`@mantine/core` + `@mantine/hooks`），React 18.3.1，Vite 6，`HashRouter` |
| 品牌 | accent `#2f5bea`（沿用现状），中性色 slate（冷灰蓝，沿用现有 `#f4f7fb` 一脉） |
| 字体 | 系统字体栈，不自托管 webfont |
| 主题 | 暗色为默认与验收基准，亮色为对照；`tokens.json` 双主题同源，两套截图齐备 |
| 允许 | 视觉大改到「认不出来」 |
| 不允许 | 改后端、改 `/v1` 契约、动数据库；图片预览（平台不存二进制附件） |

## 主路径

看板推进度：`看板 → 点卡片 → 详情路由 → 点附件读 markdown → 提交评审结论`。全程不出应用，附件不再跳新标签。

## 文件

| 文件 | 用途 |
|---|---|
| `tokens.json` | DTCG 唯一令牌源：primitive（slate/brand/jade/amber/red/sky）+ 17 个 semantic 角色 + 字阶/间距/圆角/阴影/断点 |
| `components.md` | 组件规格 + **Mantine 8 映射** + 状态矩阵 + 可访问性 + 分层红线 |
| `flows.md` | 5 条流程：推进看板 / 读附件 / 评审 / 移动端三条 / 关闭/重开项目 |
| `screens/*.md` | 8 屏规格：结构、token、组件、状态矩阵、交互、数据来源、不做 |
| `mock/` | 原型（`index.html?screen=&state=&theme=`），状态写入 `document.documentElement.dataset.state` |
| `refs/` | 240 张截图（暗色 120 基准 + 亮色 120 对照）：8 屏 × 状态 × 三视口（desktop 1440×900 / tablet 1024×768 / mobile 390×844）× 双主题 |

refs 数量口径：**240 张**（暗 120 + 亮 120）。issue #102 偏差登记 42 张，待补（认账：设计侧）。

## 复核方式

```bash
# 令牌 → CSS 变量
node <kit>/design-kit/scripts/tokens-to-css.mjs design/tokens.json design/mock/tokens.css

# 截图（需要 DSH_BROWSER_CDP_URL；本环境为 ws://host.docker.internal:3000/?token=…）
node <kit>/design-kit/scripts/screenshot.mjs --mock design/mock/shots/<screen> \
  --out design/refs --screen <screen> --states default,empty,loading,error \
  --url "$DSH_BROWSER_CDP_URL" --browser-host <本容器 IP>

# 契约校验（必须 0 错误）
node <kit>/design-kit/scripts/check.mjs design
```

`mock/shots/<screen>/` 是逐屏截图入口（每屏一个目录，注入 `window.__SCREEN__`），`mock/` 根目录保留 `?screen=` 交互用法。

### 重出某屏 refs 的完整命令（原样可复制）

`<kit>` = design-kit 包路径（本环境 `/home/node/.dsh/profiles/web/node_modules/dsh-agent-cto/design-kit`）；
CDP 指向环境自带的 browserless（本环境 `ws://browserless:3000?token=test-token`），`--browser-host` 填浏览器容器看到的 agent 容器名（本环境 `dsh-agent`）。

`--theme` **没有循环**：一次只出一种主题，双主题 = 同一命令跑两遍。

```bash
# 18 张：desktop 1440×900 / tablet 1024×768 / mobile 390×844 × 6 态 × 暗色
node <kit>/scripts/screenshot.mjs --mock design/mock/shots/attachment-preview \
  --out design/refs --screen attachment-preview \
  --viewports desktop,tablet,mobile \
  --states default,empty,loading,error,oversized,image-placeholder \
  --theme dark --url "ws://browserless:3000?token=test-token" --browser-host dsh-agent

# 18 张：同上 × 亮色
node <kit>/scripts/screenshot.mjs --mock design/mock/shots/attachment-preview \
  --out design/refs --screen attachment-preview \
  --viewports desktop,tablet,mobile \
  --states default,empty,loading,error,oversized,image-placeholder \
  --theme light --url "ws://browserless:3000?token=test-token" --browser-host dsh-agent
```

合计 36 张（`attachment-preview__<viewport>__<theme>__<state>.png`），命令退出码 0 且日志末尾打印「完成：18 张截图」各一次。

## 实现验收（给 dev 卡）

1. 每屏结构、层级、间距、字号、颜色对照 `refs/<screen>__<viewport>__light__<state>.png`。
2. 实现里不出现 hex / px 字面量（`check.mjs` 可扫）；颜色/间距一律走 token。
3. 状态矩阵里的每个状态都要有，含空态、加载、错误。
4. 视觉偏差回设计侧修，不在 dev 卡里「顺手改成我觉得更好看」。
5. 业务页面不得直接 `import '@mantine/core'`（`grep -r "@mantine/core" web/src/features` 必须为空）。

## design_ref

- design_ref：`032c2ca243dd1229a569ccc9f0bc1a03fed56231`

该 sha 是 **design 物料入库 commit**（`git log --oneline 032c2ca243dd1229a569ccc9f0bc1a03fed56231` = `docs(task-01/design): 入库 Web UI 重做 ADR、技术方案、任务拆分与设计契约 v0.3`）；本任务 PR 的合并 sha 由人工点合并产生、无法预写，因此不在本行记录。

design_ref 指 `design/` 物料入库的仓库 commit：`design/screens/*.md`、`design/tokens.json`、`design/components.md`、`design/flows.md` 与 `design/refs/` 的 240 张截图在该 commit 下可逐字节检出，是后续所有 dev 卡实现与视觉回归的唯一对照源（`git cat-file -e <design_ref>^{commit}` 可解析）。

`design/MANIFEST.sha256`（内容哈希）保留为物料完整性凭据，但**不是 design_ref**——它是内容哈希而非仓库 commit，无法用 `git cat-file` / `git ls-tree` 解析。

**MANIFEST 处理结论（issue #102 四条契约同步时登记）：保留旧值，不重算 ——「MANIFEST 待 U-06 决议后统一重算」。**

- 原因：仓库内没有生成该哈希的脚本，复算口径不可复现（issue #102 方案 U-06；实测 8 种常见递归/拼接口径与 git 树对象口径均未命中）。
- 认账人：设计侧（`design/` 物料维护人）。
- 补的时点：issue #102 四条合并进 `main` 之后、下一轮全量视觉回归之前；与上面「42 张 refs 偏差」挂在**同一条跟踪项**上一起补。
- 在重算之前，MANIFEST 的值与当前 `design/` 内容不一致属已知状态，物料完整性以 git 版本（`design_ref` 及其后的 commit）为准。

## 契约同步记录

按时间追加，不覆盖历史条目。

### issue #122 任务卡片排版与文字裁切（2026-11-22）

- 契约增量 commit：`e8d5f349b6b2e3ff4f1b8ba095d23e5f7599f41b`（`docs(design-122/design): 补 TaskCard 内距与状态条合成口径、换行与省略规则`）；源头方案 `docs/tech/task-card-typography-122-v0.1.md`。
- 范围：只改 `components.md` §ui/TaskCard —— 补「几何（内距与状态条合成口径）」「换行与省略」「已知偏差登记」三块文字。`tokens.json`、`mock/**`、`refs/**`、`screens/**`、`flows.md` 零变动。
- 为什么不动数值、不重出截图：内距契约值一直是 `space.3`，`mock/styles.css`（`.task-card { padding: var(--space-3) }`）与 240 张 refs 都按它出图；#122 是实现侧没让这条内距生效（同特异性的 Mantine 规则更靠后，把 `padding` 覆盖成 0）。契约侧缺的只是「内距与 3px 状态条怎么合成」「不可断长串怎么断行」这两句口径，补完不产生新的像素目标。
- refs：不重出（数量口径仍 240 张）；上一节既有的「42 张 refs 偏差」挂账继续挂。
- `MANIFEST.sha256`：沿用「保留旧值，不重算」（见上一节），本次未重算。
- `design_ref` 仍为 `032c2ca…`（物料入库 commit：240 张 refs 与四份契约文档在该 commit 下可逐字节检出）。本次改动属「`design_ref` 及其后的契约增量」——dev 卡**取图按 `design_ref`，取口径按本条记录的 commit**。
- 随本次一并登记、但**不修**的两条偏差（认账：设计侧；回收时点待 design 负责人决议）：卡片字号（契约 `text.small`/`caption` vs 实现 `text.body`/`text.mono`）、状态条竖直范围（mock 内缩胶囊条 vs 实现通高）。

### issue #128 附件预览改整页居中浮层（2026-11-23）

- 契约增量 commit：`7f2e9b955f3f1e29830f507afb6c418ce265ca5b`（`docs(t_01M83J5JS99H91MA4B6RS0CZS8): 附件预览改为整页居中 80% 浮层契约，重出 36 张 refs`）；本条记录与本文件「重出某屏 refs 的完整命令」在紧随其后的 commit 里，不改变上面那条 sha 的判定。上游方案 `docs/tech/attachment-preview-overlay-128-v0.2.md`（由实现卡入库），issue #128。
- 范围：`components.md`（总览表新增 `ui/ContentModal`（底座 `Modal`）与「22 个组件」口径、新增 §ui/ContentModal、§ui/DetailSheet 补 `closeLabel` / `concealed`、「21 个组件」表述改 22）、`screens/attachment-preview.md`（断点与结构改成 ≥1024 整页居中浮层 + 浮层头部）、`flows.md` F2（第 2 / 4 步）、`mock/shots/attachment-preview/{mock.js,styles.css}`（看板整页底图 + 居中 80% 浮层 + `@media (max-width:63.99em)` 回落抽屉）。`tokens.json` 零变动，`MANIFEST.sha256` 未重算。
- refs 口径：总量仍是 8 屏 × 状态 × 三视口 × 双主题 = 240 张；本次**只重出 `attachment-preview` 的 36 张**（6 态 × 3 视口 × 2 主题），其余 7 屏 204 张未动。桌面 1440×900 与平板 1024×768 帧里是**视口居中的 80vw×80vh 浮层，画面里没有右侧 720 抽屉**（底图是看板整页）；移动 390×844 仍是全屏 sheet + 头部（徽标 / 标题 / 关闭）。
- 截图可复现：完整命令见上文「重出某屏 refs 的完整命令」（dark / light 各一遍，每次 18 张，退出码 0）。
- `design_ref`：既有 `032c2ca…` 仍是**物料入库 commit**（未变形状态的对照基线）；本卡是 refs 的生产方，**实现卡（T2）的 `design_ref` 取本条记录的契约增量 commit `7f2e9b9…`**——取图按该 commit、取口径按该 commit 的契约文本。
- 随本次登记的已知状态：`design/mock/`（根目录交互入口 `?screen=`）未同步本形态，仍画旧的侧边抽屉——本卡范围只含 `mock/shots/attachment-preview/`，36 张 refs 的产出不受影响；需要根入口同构时单开一张物料卡。

### issue #148 资料只留当前版本 + 评审区简化（2026-11-23）

- 契约增量 commit：`9bdbcdb4e21344dcd65a3f9da8ae1a1e40e043b3`（`docs(design-148/design): 资料只留当前版本与评审区简化契约，重出 task-detail 12 张 refs`）——D1–D6 的契约文本、mock 与 `task-detail` 的 `default`/`rejected` 12 张 refs 在该 commit 下可逐字节检出。**本条记录与 `error`/`leased` 的另 12 张 refs 在紧随其后的 commit 里**（`docs(design-148/design): 补重出 task-detail error/leased 12 张 refs，回填契约增量 sha`，写法同 issue #128 条目），不改变上面那条 sha 的判定：`error`/`leased` 两态也渲染评审区，方案 §4.2 D6 原定「只出 12 张」漏了这两态，补出属同一条契约的收口、不是新契约。上游方案 `docs/tech/current-version-only-148-v0.2.md`（v0.2，§4.2 D1–D6，由开发卡随 PR 入库），issue #148。
- 范围：`screens/task-detail.md`（结构行、组件表删 `ui/Segmented` / `ui/SelectField`、default 态、评审区口径、附件段口径、交互行）、`screens/projects.md`（资料区补版本口径 / 简报跟随 / 计数口径三条，折叠阈值改「作用于可见行」）、`flows.md`（F3 第 2 步）、`components.md`（总览表 `ui/Segmented` 用途改「暂保留（当前无使用方）」、§ui/Segmented 用途句与反例句改写；**导出面仍 22 个组件**）、`mock/**`（评审区删「通过/驳回」开关与附件下拉，label 改「驳回原因（驳回必填）」）。`tokens.json` 零变动；`MANIFEST.sha256` **沿用「保留旧值，不重算」**。
- mock 副本：`mock.js` 与 `styles.css` 各 8 份同源（根目录 + `shots/{board,inbox,products,projects,queue,task-detail,workers}`），改一份后复制，`md5sum` 全等（`mock.js` = `c24f65360a51168b8122af99027573cf`，`styles.css` = `b7fa6a721f8cb10fe42a9609db1c1551`）；`shots/attachment-preview/{mock.js,styles.css}` 是独立变体（整页居中浮层），同样删掉评审区两控件与 `.segmented` 样式，与其它副本保持「无 `class="segmented"`」一致。`.segmented` 的 6 条样式在改后已无任何 mock 段落引用，按「不留死代码」删除。
- refs 口径：总量仍是 8 屏 × 状态 × 三视口 × 双主题 = **240 张**；本次重出 `task-detail` 的 **24 张**——`{desktop,tablet,mobile}` × `{dark,light}` × `{default,rejected,error,leased}`（其中 `default`/`rejected` 12 张在 `9bdbcdb…`，`error`/`leased` 12 张在紧随其后的 commit），`task-detail` 仍是 36 张，其余 216 张未动。这 24 张里评审区只有「评审」标题 + 驳回原因框，**没有**结论开关、没有附件选择控件；底部仍是「取消 / 驳回 / 通过并关闭」；附件区行数 = 计数文案（3 个）。`loading` 与 `failed` 两态不渲染评审区（前者骨架屏提前返回、后者 `review=false`），维持原图、与契约无冲突。
- 截图可复现：完整命令见上文「重出某屏 refs 的完整命令」，本次 `--states default,rejected` 与 `--states error,leased` 各跑 dark / light 一遍（每遍 6 张、退出码 0）；同一命令重跑字节一致（md5 全等）。
- `design_ref`：既有 `032c2ca…` 仍是**物料入库 commit**（未变形状态的对照基线）；issue #148 的 dev 卡**取图按本条 commit 的 `task-detail` 24 张、取口径按本条的契约文本**。
- 随本次登记的已知状态：①`ui/Segmented` 从此没有使用方，登记为待决债务（导出面 22 个不变，删要同步 `ui/index.ts` / `ui-imports-check.mjs` / `components.md` / `web/README.md` 四处）；②`design/mock/` 根目录交互入口与 `mock/shots/*` 共用同一份 `mock.js`，本次已同步（8 份 md5 全等）；③**`error`/`leased` 两态的过期 refs 已关闭**——方案 §4.2 D6 原定只重出 `default`/`rejected`，但这两态同样渲染评审区，旧图里留着「通过/驳回」开关与「被评审附件」下拉；已在紧随其后的 commit 里连同这两态一并重出（12 张），与现 mock 和本条契约一致，不再挂待补。

## 视觉方向（已定：B 暗色仪表盘）

默认主题 = **暗色**（决策人 2026-11-19 选定「B 好看点」）。亮色同套保留为对照，切换成本一行 `data-theme`。

| 方向 | 状态 | 截图 | 用途 |
|---|---|---|---|
| B 暗色仪表盘 | **默认 / 验收基准** | `refs/<screen>__<viewport>__dark__<state>.png`（120 张） | dev 实现对照、视觉回归 |
| A 亮色精修 | 可选主题 | `refs/<screen>__<viewport>__light__<state>.png`（120 张） | 亮色模式对照；`check.mjs` 契约基准命名 |

暗色特征：深空底 + 双径向氛围光 + 56px 细网格 + 玻璃面板 + 状态色条发光 + 品牌蓝→青渐变标题。
v2 视觉规则（氛围光/网格/渐变/玻璃/状态条/数字对齐）写在 `components.md` 末节，全部绑定 token，实现时不许写裸值。

**暗色对比度自检**（`check.mjs` 只校验亮色 token）：`text.primary #e9f0fb` / `text.muted #93a7c4` on `#070b14`、`action.primary-text #9db8ff` on `#0b1322` 均 ≥7:1，正文（15px）无需额外放宽。
