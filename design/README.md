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

## 视觉方向（已定：B 暗色仪表盘）

默认主题 = **暗色**（决策人 2026-11-19 选定「B 好看点」）。亮色同套保留为对照，切换成本一行 `data-theme`。

| 方向 | 状态 | 截图 | 用途 |
|---|---|---|---|
| B 暗色仪表盘 | **默认 / 验收基准** | `refs/<screen>__<viewport>__dark__<state>.png`（120 张） | dev 实现对照、视觉回归 |
| A 亮色精修 | 可选主题 | `refs/<screen>__<viewport>__light__<state>.png`（120 张） | 亮色模式对照；`check.mjs` 契约基准命名 |

暗色特征：深空底 + 双径向氛围光 + 56px 细网格 + 玻璃面板 + 状态色条发光 + 品牌蓝→青渐变标题。
v2 视觉规则（氛围光/网格/渐变/玻璃/状态条/数字对齐）写在 `components.md` 末节，全部绑定 token，实现时不许写裸值。

**暗色对比度自检**（`check.mjs` 只校验亮色 token）：`text.primary #e9f0fb` / `text.muted #93a7c4` on `#070b14`、`action.primary-text #9db8ff` on `#0b1322` 均 ≥7:1，正文（15px）无需额外放宽。
