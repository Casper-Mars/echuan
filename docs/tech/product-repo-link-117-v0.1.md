# 技术方案：产品详情仓库链接可点击（issue #117）v0.1

| 项 | 值 |
|---|---|
| 关联 issue | [#117 产品详情的仓库链接可点击](https://github.com/Casper-Mars/echuan/issues/117)（open，0 评论，author = OWNER） |
| 项目 | `echuan-5`／分支 `feature/echuan-5`；本卡分支 `task/t_01M81E91VZJV2KRR97339C549Y` |
| 代码基线 | `main@822c43eeae77a3be28938aefea2b4e3f12cde877`（`git fetch --prune origin` 后核对；本文所有「文件:行」以此 commit 为准） |
| 上游方案 | 无（issue 直接进设计，无 ADR：只是实现层选择，不涉及技术路线） |
| 界面契约 | `design/` v0.3（`tokens.json` / `components.md` / `screens/products.md` / `refs/` / `MANIFEST.sha256`） |
| 本卡级别 | `s`（issue 单点、单屏、无数据/接口变更）；章节按本卡任务级验收的 11 项组织 |
| 交付物 | 本文档（唯一交付物；不改代码、不建拆解卡、不开 PR） |

**一句话结论**：改 `web/src/features/shell/DetailFields.tsx` 一处渲染分支（新增 `link?: string` 字段）+ `ProductDetailSheet` 传一个字段 + `shell.css` 一条移动端触控规则即可闭环；判定口径**沿用仓库里已有的 `web/src/lib/url.ts#repoHref`**（http/https 白名单，今天零调用方），只补一条「内部含空白的值不可点」的收紧；`ui/` 导出面仍是 21 个组件、`design/` 不动、后端 `/v1` 与 `migrations/` 零改动。

**代价**：`DetailFields` 从纯排版件长出「链接渲染 + 安全收口」职责（多一条不可退化的契约）；移动端字段行高从 21px 抬到 44px（`<768` 三视口里仓库行变高，其它行不变）。
**退出条件**：① 若安全评审要求「渲染层不得自证合法」而必须由调用方先过滤，则把 `link` 的判定上移到 `ProductDetailSheet`（改动仍在这两个文件内，契约文档同步改一句）；② 若设计侧裁定「仓库字段在抽屉内保持纯文本、只做列表页可点」，则本文 §5 D2/D3 作废，回退到维持现状。

---

## 1 目标与非目标

### 1.1 目标

1. 产品只读详情抽屉里的「仓库」字段：登记值为**合法 http(s) 地址**时渲染为可点击链接，点击在**新 tab** 打开。
2. 非法地址（含非 http(s) 协议、裸域名、SSH 形式、含内部空白的多链接串）**保持纯文本**，不做任何点击行为。
3. 可点/不可点在视觉上可区分：可点走全局锚点色（`--action-primary-text`）+ 下划线，不可点保持字段正文色。
4. 判定、渲染、安全收口三件事各归一层，后续「项目详情展示 `repo_url`」不需要重新讨论口径。

### 1.2 非目标

| # | 不做 | 理由 |
|---|---|---|
| N1 | 后端任何改动（`internal/**`、`migrations/**`、`/v1` 契约） | `git_url` 已在响应里（§3.3），issue 只需前端渲染 |
| N2 | `git_url` 的**入库校验**（后端把 `javascript:` 存进库是现状，见 §3.1 S4） | 属后端校验面，改它超出本 issue；本方案在前端渲染层收口，登记为未决 U-02 |
| N3 | 新增 `ui/` 组件、新增依赖、新增路由 | ADR-0001 锁版本；`DetailFields` 已是抽屉层共用件（§5 D3） |
| N4 | 产品**列表**表格的仓库列可点 | issue 原文只指详情；统一要动行点击语义（§5 D5），登记 U-01 |
| N5 | **项目**详情展示 `repo_url`（后端已返回、前端未消费） | 字段当前不在 9 项契约里，属新范围；登记 U-01 |
| N6 | URL 规范化（`git@…`→`https`、补 `https://`、punycode 转写） | 会与「登记值原样展示」的既有口径冲突（§4 D1）；登记 U-04 |
| N7 | 远程错误上报、埋点 | 平台无上报端点（`docs/tech/web-ui-redo-v0.1.md:163`） |
| N8 | `design/refs/` 重生成、`MANIFEST.sha256` 重算 | 已登记的既有偏差（`design/README.md` §MANIFEST 处理结论），本卡不重算（§9） |
| N9 | 任务拆解、排期、人日、提交顺序 | 下一张卡的事 |

---

## 2 AC 覆盖（issue 三条，逐条给可观察通过判据）

### 2.1 逐条闭环

| AC | issue 原文 | 现状（基线实测） | 方案落点 | 通过判据（浏览器可观察） |
|---|---|---|---|---|
| AC-1 合法 http(s) 可点 | 「只要是合法的http链接，就可以点击」 | 抽屉「仓库」是纯文本 `dd`，`[role=dialog] a` 数量 = **0**（2026-11-22 实测，§3.1 S1/S2） | §5 D1 判定 + D2/D3 渲染 | P1、P2 |
| AC-2 新 tab 打开 | 「并且在新tab中打开页面」 | 无链接，无 `target` 可谈 | §7 交互 + §5 D4 安全收口 | P3 |
| AC-3 非法地址不可点 | 由 AC-1 的「合法」反推 | 现状本就不可点，改动后**不许回退** | §4 判定表 + §5 D1 的否定分支 | P4 |

### 2.2 可观察通过判据（后续 dev 卡与评审照此验；本卡只定义，不执行实现）

**P1 合法 http(s) 可点**

1. `GET /v1/products?id=<pid>` 的 `git_url` 为 `https://github.com/Casper-Mars/echuan` 时，打开该产品抽屉 → 仓库字段内存在 `a`，其 `href` **逐字节等于**登记值（不是 `URL.href`，不补尾斜杠、不改大小写、不做 punycode 转写）。
2. `http://` 同样可点（白名单两条都放行）。
3. 值前后带空格（`"  https://github.com/x/y  "`）→ 可点，`href` 为 trim 后值。

**P2 长地址不撑破抽屉**

1. 三视口（1440×900 / 1024×768 / 390×844）下：抽屉正文区 `scrollWidth === clientWidth`（无横向滚动），仓库字段保持单行省略（`white-space: nowrap` + `text-overflow: ellipsis`），`title` 为完整原值。

**P3 新 tab 打开**

1. 链接满足 `target="_blank"` 且 `rel` 同时含 `noopener` 与 `noreferrer`（DOM 属性断言，不靠肉眼）。
2. 点击后当前 tab 的 `location.href` 不变，新 tab 的 URL 等于该 `href`（验收用 CDP 的 `Target.targetCreated` 观察，不依赖人工）。
3. 键盘路径：Tab 聚焦仓库链接有焦点环（`--focus-ring`），Enter 触发同上，不出现「同一个按键先开抽屉又开链接」。

**P4 非法地址不可点**

1. 下列值一律**不产生 `a`**，仓库字段内 `a` 数量 = 0，且文本原样显示（空值显示 `未设置`）：
   `github.com/x/y`、`git@github.com:x/y.git`、`ssh://git@github.com/x/y.git`、`file:///etc/passwd`、`ftp://example.com/a`、`/relative/path`、`//github.com/x/y`、`javascript:alert(1)`、`JAVASCRIPT:alert(1)`、`data:text/html,<script>alert(1)</script>`、`vbscript:msgbox(1)`、`https://github.com/x/y https://github.com/a/b`、`https://github.com/x/y 附录`、`https://exa mple.com/a`、`https://`。
2. 空值（`""` / 全空白 / `null` 缺省）→ 显示 `未设置`，不产生 `a`。
3. 对 `javascript:` / `data:` 值，抽屉渲染后 `document.querySelectorAll('[role=dialog] a[href^="javascript"]').length === 0`，页面无 alert/dialog 弹出。

---

## 3 现状（只引证据，不写「疑似」）

### 3.1 现状清单

| # | 现状 | 证据（基线 `822c43e`） | 关键结论 |
|---|---|---|---|
| S1 | 产品详情抽屉的「仓库」是纯文本 | `web/src/features/products/ProductDetailSheet.tsx:52`（`{ label: '仓库', value: textOr(product.git_url, '未设置'), mono: true, truncate: true }`）；实测 `[role=dialog] a` = 0，`dd[title]` = 完整 URL | 抽屉层**没有**链接渲染能力 |
| S2 | 键值网格 `DetailFields` 只做排版，`value` 只接受 `ReactNode` | `web/src/features/shell/DetailFields.tsx:14-35`（`DetailField` 五个字段：`label` / `value` / `mono` / `multiline` / `truncate`）、`:39-56`（渲染 `dd` + `title`） | 要「可点」必须扩展它，或让调用方塞 ReactNode |
| S3 | 判定函数 `repoHref` 已存在，但**全仓零调用方** | `web/src/lib/url.ts:16-27`；`grep -rn repoHref web/src --include=*.ts --include=*.tsx` 只命中定义行本身 | 口径现成、接线缺失；这正是本 issue 的最小修复面 |
| S4 | 后端不校验 `git_url` 协议，只校验非空与 ≤500 字符 | `internal/app/product.go:293-302`（`is required` / `must be at most 500 characters`）、`:20`（`maxGitURLLen = 500`）；字段无协议约束 | `javascript:` / `data:` **能入库**（本卡实测：`POST /v1/products` 传 `javascript:alert(1)` → 201）。今天无害（纯文本），一旦无脑渲染成链接就是存储型 XSS 面 → 判定必须在前端渲染层收口 |
| S5 | 上一轮（issue #40 / 项目 `echuan-1`）确实实现过抽屉内可点链接，被后续重构抹掉 | `b2d2c0e`（`feat(products): 详情抽屉仓库地址可点击并在新标签页打开 (#40)`）新增 `repoHref` + `<a target="_blank" rel="noopener noreferrer">`；`8cae4c7`（`chore(web): 删除旧视觉层 7 个无引用组件`）删掉 `web/src/components/ProductDetail.tsx`；`adf01c3` 起抽屉改由 `ProductDetailSheet` + `DetailFields` 渲染，`d893343` 把仓库字段改成「单行省略 + title」纯文本 | 「这是回归，不是新功能」——本卡核实结论，与任务描述「不预设结论」的要求一致 |
| S6 | `git_url` 在 UI 上另有一处消费：产品**列表**表格 | `web/src/features/products/ProductsPage.tsx:127-133`（`<span className="pd-mono" title={product.git_url}>{product.git_url}</span>`） | 该处是 `<span>`，且所在行有 `onRowClick`（`:210`），改可点会撞「一次点击一个结果」→ §5 D5 只登记不改 |
| S7 | 项目接口已在返回 `repo_url`，前端类型里没有、页面也不展示 | `internal/api/projects.go:79`（`RepoURL *string json:"repo_url"`）、`:533-570`（按产品 `git_url` 派生）；`web/src/api/types.ts:18-27`（`Project` 无 `repo_url` 字段）；`grep -rn repo_url web/src` 为空 | 「其它展示 `git_url` 的位置」**实际只有列表一处**；项目侧是「后端有、前端没接」，登记 U-01 |
| S8 | 抽屉几何：桌面右侧 720、`<768` 全屏 | `web/src/theme/base.css:57`（`--ui-layout-sheet: calc(var(--space-12) * 6 + var(--space-11) + var(--space-10))` = 720）、`web/src/ui/DetailSheet.tsx:57`、`web/src/ui/ui.css:623-639`（`max-width: 47.99em` 全屏） | 长 URL 的收敛必须在两种几何下都成立 |
| S9 | 移动端触控 ≥44 已有先例，但只覆盖面包屑 | `web/src/features/shell/shell.css:422-431`（`@media (max-width: 47.99em)` 内 `.fx-crumbs__link { display: inline-flex; align-items: center; min-width/min-height: var(--ui-control-height-touch) }`）、`web/src/theme/base.css:49`（`--ui-control-height-touch` = 44） | 仓库链接可直接复用同一套令牌与写法，不新造尺寸 |
| S10 | 全局锚点样式已就绪：色 + 下划线，焦点环全局 | `web/src/theme/base.css:144-146`（`a { color: var(--action-primary-text) }`）、`:139-142`（`:focus-visible` 用 `--focus-ring`）；实测 `a` 的 computed `text-decoration-line: underline`、`color: rgb(157,184,255)` | 视觉可识别性不需要新增颜色/下划线样式 |
| S11 | `ui/index.ts` 导出面恰好 21 个组件，无链接/外链组件 | `web/src/ui/index.ts:13-75`（21 个 `export`，无 `Link` / `ExternalLink`）；`web/scripts/ui-imports-check.mjs:30-52`（`EXPECTED` 21 项）；`design/components.md:390`、`web/README.md:9,147,155`（三处「21 个组件」表述） | 新增组件要同步 4 处 + 契约 + 回归基线 → §5 D3 判为不划算 |
| S12 | `DetailFields` 的调用方只有 2 个抽屉 | `grep -rn DetailFields web/src`：`features/products/ProductDetailSheet.tsx:38`、`features/projects/ProjectDetailSheet.tsx:33`（另 `ProductsPage.tsx`、`ProjectsPage.tsx` 仅注释提及） | 扩 `DetailField` 的影响面可枚举，不会外溢 |
| S13 | 空值文案由调用方给，`DetailFields` 不猜 | `DetailFields.tsx:3-6` 注释 + `ProductDetailSheet.tsx:58-61`（`textOr(..., '未设置')`） | 「空值」分支沿用现有 `未设置`，不改文案 |

### 3.2 现状截图（改前）

| 场景 | 观测（2026-11-22 实测） | 证据 |
|---|---|---|
| 抽屉 · 短地址（`https://github.com/Casper-Mars/echuan`） | 仓库字段为纯文本，`a` = 0，`dd[title]` = 完整值 | `docs/tech/evidence-117/before-product-detail.png` |
| 抽屉 · 长地址（>100 字符，桌面 720） | `dd` clientWidth 591 / scrollWidth 1064，单行省略生效；抽屉正文区无横向滚动；行高 21px | `docs/tech/evidence-117/before-long-url-desktop.png` |
| 抽屉 · 长地址（<768 全屏 390） | 抽屉宽 = 390（全屏），`dd` clientWidth 261，行高 21px；**链接若直接内联，命中区仅 20px 高** | `docs/tech/evidence-117/before-long-url-mobile.png` |

> 实测手段：测试实例 `http://echuan:8720`（容器网络，`echuan:local` 镜像）+ browserless；为拿到长地址样本，临时 `POST /v1/products` 建了 3 条探针产品（长地址 / `git@` / `javascript:`），量完即 `DELETE`，复核 `GET /v1/products` 只剩 `echuan`（证据见 §附录 A）。

### 3.3 数据来源（一句话：无数据变更）

| 字段 | 来源 | 形态 | 备注 |
|---|---|---|---|
| `Product.git_url` | `GET /v1/products`（`internal/api/products.go:58-68` 响应体 `git_url`；`internal/store/product.go:23,32` 列 `p.git_url`） | `string`，必填，≤500 字符，后端 `TrimSpace` 后入库（`internal/app/product.go:293-302`） | 前端类型已有：`web/src/api/types.ts:14` |
| `Product.project_count` | 同上（列表响应可能缺省） | `number?` | 本方案不消费 |
| `Project.repo_url` | `GET /v1/projects`（`internal/api/projects.go:79`） | `string \| null` | 前端类型未声明、页面未展示；登记 U-01，本方案不消费 |

---

## 4 判定规则：「合法 http 链接」的准确口径

### 4.1 判定式（四层，全部必须满足）

```
linkable(raw) :=
  (1) typeof raw === 'string'                        // 类型收敛，挡 as any 传数字/对象/Symbol
  ∧ (2) raw.trim() !== ''                            // 空白视为无值
  ∧ (3) !/\s/.test(raw.trim())                       // 内部含任何空白 → 不可点（新增收紧，见 4.3）
  ∧ (4) new URL(raw.trim()).protocol ∈ { 'http:', 'https:' }   // 协议白名单（URL.protocol 已小写）
       // new URL 抛错 → false；整个判定 try/catch，异常一律 false
```

- 命中时 `href = raw.trim()`（**不是** `new URL(...).href`：后者会把 `https://github.com` 补成带尾斜杠、把 Unicode 域名转 punycode，与登记值不一致，也会让 `P1-1` 的「逐字节相等」判据失败）。
- 未命中 → 调用方按纯文本渲染，展示原值（不 trim、不改写）；空值仍走既有 `未设置`。
- 白名单只有 `http:` / `https:`：`javascript:` / `data:` / `vbscript:` / `blob:` / `file:` / `ftp:` / `ssh:` / `git:` / 相对路径 一律不在其中。

这条判定式**已经以代码形式存在**：`web/src/lib/url.ts#repoHref`（(1)(2)(4) 完全一致，(3) 是缺失项）。本方案不新增第二个判定函数，只在 `repoHref` 内补 (3)，避免两套口径漂移。

### 4.2 脏数据分类表（每一类给明确结论）

> 「当前实现」= 基线 `repoHref` 的实际返回；「本方案」= 补 (3) 之后的最终结论。第 1-12、15-17 行两者一致，第 13-14 行是收紧点。

| # | 输入 | 当前实现 | 本方案 | 展示 | 依据 |
|---|---|---|---|---|---|
| 1 | `https://github.com/x/y` | 可点 | **可点** | 链接，文本 = 原值 | 协议命中 |
| 2 | `http://example.com/x` | 可点 | **可点** | 同上 | 白名单含 `http:` |
| 3 | `HTTPS://GitHub.com/X/Y`（大写 scheme） | 可点 | **可点** | 链接，文本与 href 均保留原大小写 | `URL.protocol` 已小写，不回写原值 |
| 4 | `  https://github.com/x/y  `（前后空格） | 可点 | **可点**（trim 后） | href = trim 后值 | 入库已 trim，此处仅兜历史数据 |
| 5 | 空值：`null` / `undefined` / `''` / 全空白 | 不可点 | **不可点** | `未设置`（纯文本） | 既有空值文案 |
| 6 | `https://`（缺 host） | 不可点 | **不可点** | 纯文本原值 | `new URL` 抛错 |
| 7 | `github.com/x/y`（裸域名，无 scheme） | 不可点 | **不可点** | 纯文本原值 | 无 scheme → 解析失败；不自动补 `https://`（U-04） |
| 8 | `git@github.com:x/y.git`（SSH scp 形式） | 不可点 | **不可点** | 纯文本原值 | 非 URL；不移写为 https（U-04） |
| 9 | `ssh://git@github.com/x/y.git` / `git://…` / `file:///…` / `ftp://…` | 不可点 | **不可点** | 纯文本原值 | 协议不在白名单 |
| 10 | `javascript:alert(1)` / `JAVASCRIPT:…` / `data:text/html,…` / `vbscript:…` | 不可点 | **不可点** | 纯文本原值（**不渲染 `a`、不写 `href`**） | 协议不在白名单；S4 说明这类值能入库，必须靠这一层挡 |
| 11 | `//github.com/x/y`（协议相对） | 不可点 | **不可点** | 纯文本原值 | 无 scheme，`new URL` 抛错 |
| 12 | `/relative/path` | 不可点 | **不可点** | 纯文本原值 | 解析失败 |
| 13 | `https://github.com/x/y https://github.com/a/b`（多链接串） | **可点**（现行缺陷：整体被当成一个 URL，浏览器把空格编码成 `%20`，落点是 404 页面） | **不可点**（收紧） | 纯文本原值 + `title` 兜完整串 | 新增 (3)：含空白即不可点。用户点进 404 比不可点更糟；多链接不做拆分（拆分会引入「哪个是仓库」的业务判断，U-04） |
| 14 | `https://github.com/x/y 附录`（URL + 说明文字） | 可点（同上，落点 404） | **不可点**（收紧） | 纯文本原值 | 同 13 |
| 15 | `https://exa mple.com/a`（host 含空格） | 不可点 | **不可点** | 纯文本原值 | `new URL` 抛错 + (3) |
| 16 | `https://例子.中国/仓库`（Unicode） | 可点 | **可点**，href 保留原 Unicode（浏览器自行转写） | 链接，文本 = 原值 | 协议命中；不做 punycode 回写（P1-1 要求逐字节相等） |
| 17 | 非字符串（`123` / `{}` / `Symbol()`，模拟 `as any`） | 不可点 | **不可点** | 纯文本原值 | (1) 类型收敛，全函数在 try 内 |

> 表 1-6、15-17 行为与基线一致；13/14 是本方案**唯一**的口径收紧，理由写在表内，不静默改。

### 4.3 为什么收紧 13/14 而不是「维持现状」

| 选项 | 做法 | 代价 | 结论 |
|---|---|---|---|
| A 维持现状 | 只把现有 `repoHref` 接上（含 13/14 可点） | 零额外改动；但「多链接串」会渲染成**一个**可点链接，点击落 404，用户会当成新 bug 再报一次 | 不选 |
| B 拆分多链接 | 按空白切分，渲染多个链接 | 需要决定「哪个是仓库」（首个？含 github 的？），引入业务判断；且会改变「仓库字段 = 一条地址」的语义 | 不选 |
| **C 收紧：含空白即不可点（选）** | `repoHref` 内加 `!/\s/.test(trimmed)` | 极少数「URL 后跟说明」的合法登记值变不可点（可用 `title` 看到完整串，复制即可）；改动 1 行 | **选** |
| D 最小改动对照：只改 `ProductDetailSheet`，在本文件内判定 + 内联 `<a>` | 不动 `repoHref`、不动 `DetailFields` | 安全口径散在页面层，项目抽屉以后要展示 `repo_url` 时得再抄一遍；判定与渲染分居两文件，回归点分散 | 作为「不推荐但可接受」的退路（§5 D3 的 B 方案） |

---

## 5 六个决策点：选项 / 代价 / 退出条件

> 每个决策点都给「维持现状」与「最小改动」两档对照，结论一律给定，不留「视情况」。

### D1 「合法 http 链接」的判定口径

| 档 | 做法 | 代价 | 结论 |
|---|---|---|---|
| 维持现状 | 不判定，只要非空就渲染链接 | 零成本；但 `javascript:` 直接进 `href` = 存储型 XSS（S4 证明这类值能入库） | **否**（安全红线） |
| 最小改动 | 只用 `协议白名单`，不查空白 | 1 行；脏数据 13/14 落 404 | 否（见 §4.3 A） |
| **选定** | 协议白名单 + 内部空白拒绝 + 类型收敛 + 全量 try/catch，**落在 `web/src/lib/url.ts#repoHref` 一处** | 改动 1 个函数（1 行）、复用既有注释与边界；返回值语义不变（trim 原串） | **是** |

- **退出条件**：若产品/设计侧要求「多链接串拆成多个链接」或「自动补 `https://`」，本条作废，转 U-04 决策后再改（会连带 §7 展示形态）。
- **不选的理由不写「更安全」四个字了事**：`javascript:` 目前**能入库**且**今天不渲染成 href**，是「今天恰好安全」；本方案是把它变成「结构上安全」——即使将来有人在列表页、项目详情页再渲染一次 `git_url`，也不会有第二个 XSS 面。

### D2 展示形态：整串可点 vs 截断 + `title`

| 档 | 做法 | 代价 | 结论 |
|---|---|---|---|
| 维持现状 | 单行省略 + `title`（当前 `truncate`） | 零成本；不可点，等于 issue 没做 | 否 |
| 最小改动 | 整串换行展示（去掉 `truncate`） | 长 URL 最多 500 字符 → 抽屉 720 里会叠 4-6 行，桌面还能看，390 全屏下会把描述挤到首屏外 | 否 |
| **选定** | 保留单行省略 + `title`（`title` = 原值），只把文本换成 `<a>` | 省略号区域不可点（可接受：用户要看全就看 `title`，要点就点可见文本）；宽度不变，不新增溢出风险 | **是** |

- 桌面（抽屉 720，字段宽 591）与 `<768`（全屏 390，字段宽 261）用**同一形态**：单行省略。两档都不换行，实现与回归只有一套。
- **退出条件**：若设计侧要求「抽屉内展示完整可读地址」（例如两行截断 `-webkit-line-clamp: 2`），则本条作废，改 §7 与 §9 的截图基线。

### D3 组件落点：复用还是新增

| 档 | 做法 | 代价 | 结论 |
|---|---|---|---|
| 维持现状 | 无链接能力，等到「第三个抽屉需要外链」再抽 `ui/` 组件 | 零成本；issue 不闭环 | 否 |
| 方案 A 新增 `ui/ExternalLink` | 进 `ui/index.ts` → 22 个组件 | 要同步 4 处「21 个组件」表述（`ui-imports-check.mjs:30-52` 的 `EXPECTED`、`design/components.md:390`、`web/README.md:9,147,155`）+ 改界面契约 + 设计侧补组件条目与 refs；`DetailFields` 仍要接 ReactNode 才能用 | 否（面最大，收益最小） |
| 方案 B 调用方内联 `<a>` | 只在 `ProductDetailSheet.tsx` 里判定 + 渲染 | 改动 1 文件；判定与渲染分居两处（`url.ts` 判、页面渲染），项目抽屉以后要链接得再抄一遍；安全收口随页面走，容易漏 | 作为退路（D1 退出条件②） |
| **方案 C 给 `DetailField` 加 `link?: string`（选定）** | 渲染语义（`target` / `rel` / `title` / 降级 / 移动端命中区）全部收在 `DetailFields` 一处；调用方只声明「这条字段想当链接」 | `DetailFields` 从纯排版件长出「链接渲染 + 安全收口」职责（多一条不可退化的契约，需要一条注释与契约文档同步）；`ui/` 导出面**不变**（仍是 21 个；`DetailFields` 本来就在 `features/shell`，`design/components.md:388-396` 已把它定义为「屏内私有共用件」） | **是** |

- **为什么不把判定也放进 `DetailFields`**：判定是纯函数、要能单测、与 React 无关 → 留在 `web/src/lib/url.ts`；`DetailFields` 只做「拿到 candidate 后决定渲染成 `a` 还是文本」。这种分层让 `repoHref` 成为**唯一**判定入口（今天零调用方，接线后就是唯一消费者）。
- **退出条件**：若出现第三个消费方（如项目抽屉接 `repo_url`）并且要求一致的「外部链接」视觉（图标、`aria-label` 模板），再按 `design/components.md:395` 的既有升级条件抽 `ui/` 组件——那时同步清单已经写在契约里，不需要重新讨论。

### D4 安全口径收在哪一层

| 档 | 做法 | 代价 | 结论 |
|---|---|---|---|
| 维持现状 | 无链接，无该问题 | — | 否 |
| 只调用方过滤（`ProductDetailSheet` 先判、`DetailFields` 无脑渲染） | 判定在页面层 | `DetailFields` 变成「给什么 `href` 就渲染什么」，任何新调用方漏判即 XSS；契约不可自证 | 否 |
| **渲染组件内收口（选定）** | ① `link` 只是「意图声明」；② `DetailFields` 内部调 `repoHref` 决定 href；③ 命中才输出 `<a target="_blank" rel="noopener noreferrer">`，未命中输出原纯文本 | 组件多一层判定（微秒级，纯函数，无请求）；判定失败路径与「没传 link」等价 → 状态数不增加 | **是** |

- 安全属性逐条：
  - `target="_blank"`：新 tab。**没有** `rel` 时新页可通过 `window.opener` 反向控制原页（reverse tabnabbing）。
  - `rel="noopener noreferrer"`：`noopener` 断 `window.opener`；`noreferrer` 额外不发送 `Referer`（内部工具地址不外泄）。两条都写，不省略（与 `web/src/features/task-detail/attachment-preview.tsx:109` 的 `window.open(..., 'noopener,noreferrer')` 口径一致）。
  - 不写 `href` 在有值但非法的分支：不是「`href="#"` + `preventDefault`」——那类写法留下可点外观，违反「看着像链接点不动」的红线。
  - 链接文本 = 原值（只读 URL），不加「在新窗口打开」之类的可见文案（视觉噪音）；`title` 保留完整值，同时满足「截断兜底」。
- **为什么选这一层**：`repoHref` 今天零调用方（S3），「调用方先过滤」等于每个新调用方都要记得过滤，契约无法自证；渲染层收口把「忘记过滤」从**安全缺陷**降级为**视觉缺陷**（链接不出现），这是这类小改动里性价比最高的挡板。
- **退出条件**：若安全评审要求「链接必须由后端给出可信标记（例如后端返回 `git_url_kind: http|other`）」，则收口上移到契约层，本条作废并登记 ADR（会改 `/v1`）。

### D5 影响面边界：只改详情，还是全站统一

| 档 | 做法 | 代价 | 结论 |
|---|---|---|---|
| 维持现状 | 都不可点 | issue 不闭环 | 否 |
| **最小改动（选定）**：只改产品详情抽屉 | `ProductDetailSheet` + `DetailFields` + 1 条 CSS | 列表表格仍纯文本（同一产品在列表与抽屉里表现不同）；项目侧 `repo_url` 仍不展示 | **是** |
| 全站统一 | 列表「仓库」列也换成链接；项目抽屉补 `repo_url` 字段 | ① 列表行有 `onRowClick`（`ProductsPage.tsx:210`），链接必须 `stopPropagation` 才能「一次点击一个结果」，要重跑行点击/键盘回归；② `design/screens/products.md:7` 明文「操作列保持 1 枚按钮，行内不新增按钮」——行内新增链接需要设计侧改契约；③ 项目抽屉字段 9→10 项，`design/screens/projects.md`、`components.md:392`、视觉基线同步；④ 验收从 1 屏 1 字段变成 3 屏 N 字段 | 否（面变大、验收变多，且 issue 只指详情） |

- 剩余位置登记为未决项：`U-01`（谁定：产品负责人；见 §12.2）。
- **退出条件**：若产品负责人判定「必须全站一致才能收」，则本卡只交付「详情可点」，列表与项目侧另开 issue/项目，不在本卡默默扩。

### D6 design 基线同步

| 档 | 做法 | 代价 | 结论 |
|---|---|---|---|
| 维持现状 | 认为「没动界面就不管 design」 | 下个 dev 卡会照旧契约实现成纯文本，等于把这次改动静默回退 | 否 |
| 全量重生成 | 改 `design/screens/products.md` + 重跑 240 张 refs + 重算 `MANIFEST.sha256` | 截图脚本与 `MANIFEST` 生成口径当前不可复现（`design/README.md` 已登记）；单卡承担整套重生成不成比例 | 否 |
| **选定：登记偏差 + 最小改契约** | ① 改 `design/screens/products.md`「交互」一句：仓库字段在合法 http(s) 时为新 tab 链接、否则纯文本；② 改 `design/components.md` §「屏内私有详情抽屉」一句：`DetailField.link?` 与降级口径；③ `refs/` 与 `MANIFEST.sha256` **不重生成**，按既有「42 张 refs 偏差 + MANIFEST 待统一重算」同一跟踪项认账；④ 视觉回归以本卡验收截图为准 | 设计侧要认账两条既有偏差继续挂账；契约改动需要设计负责人确认（U-03） | **是** |

- 明确结论：**存在界面契约变化**（可点上/不可点上两种视觉状态，以及移动端字段行高 21→44），不是「无契约变化」的零影响改动；因此不能只写「不动 design」了事。
- **退出条件**：若设计负责人要求「先补 refs 再合代码」，本卡改判 §9 的执行前提，并把补图挂到 U-05。

---

## 6 失败与降级（逐条给用户可见表现）

| 场景 | 触发 | 表现（用户可见） | 是否新增态 |
|---|---|---|---|
| 值非法 / 非 http(s) | `repoHref` 返回 null | 仓库字段纯文本原值，无 `a`；不误点、不跳页 | 否（复用既有纯文本态） |
| 值为空 | `textOr` 命中 fallback | 显示 `未设置` | 否 |
| 值内部含空白（多链接串） | 判定 (3) 失败 | 纯文本原值 + `title` 完整串；用户可复制 | 否 |
| 目标站点 404 / 私有仓库需登录 | 目标侧 | 新 tab 里由目标站点给出 404 / 登录页；**本应用不感知**（跨域，且平台无上报端点） | 否 |
| 浏览器拦截弹窗 | 极端策略（把 `_blank` 视为弹窗） | 链接无反应或新 tab 被拦：浏览器地址栏出现拦截提示；应用不做兜底（不做 `window.open` 回退，避免与原生锚点的可访问性语义打架） | 否 |
| 链接文本被省略号截断 | 长 URL | 可见部分可点；完整值在 `title`；不出现横向滚动 | 否 |
| 移动端点按 | `<768` | 链接命中区 ≥44px 高（§7.2 的移动端口径），不出现「点不中」 | 是（新增移动端规则） |

---

## 7 展示与交互

### 7.1 视觉（全部复用既有令牌与全局规则，不新增颜色）

| 状态 | 表现 | 令牌/来源 |
|---|---|---|
| rest（可点） | `--action-primary-text` + 下划线（全局 `a` 规则已给）、等宽字体（仓库字段既有 `mono`） | `web/src/theme/base.css:144-146` |
| rest（不可点） | 字段正文色 `--text-primary`，无下划线 | `shell.css:460-468`（`.fx-detail-fields__value`） |
| hover | 保持下划线（全局锚点默认已有），不加额外位移/变色 | 同上 |
| focus-visible | 全局焦点环 `--focus-ring`，无需组件内重写 | `base.css:139-142` |
| 截断 | 单行省略（`nowrap` + `hidden` + `ellipsis`），`title` = 完整原值 | `shell.css:484-488`（既有 `[data-truncate='on']`） |

- **「看着像链接点得动 / 看着像文本点不动」的判据**：颜色 + 下划线是全局锚点语义（S10），不可点值**不进入锚点元素**，因此不存在「同色不同行为」的中间态；不使用 `role="link"` 伪造（屏幕阅读器会把可聚焦文本当链接，反而制造第二套语义）。
- 不加外链图标：`ui/icons.tsx` 无对应图标，新增图标要动 `ui/` 面（D3 成本）。

### 7.2 几何与触控

| 视口 | 抽屉宽 | 字段宽 | 链接盒 | 说明 |
|---|---|---|---|---|
| ≥768 桌面 | 720（`--ui-layout-sheet`） | 591（2026-11-22 在 945×973 视口实测） | 宽 = `min(内容宽, 字段宽)`，高 20（行高） | 桌面鼠标场景不强制 44 高；命中区 = 可见文本（省略号区域不可点，D2 已认账） |
| 1024×768 平板 | 720 | 591 | 同上 | 与桌面同规则，不另设断点 |
| <768 移动 | 全屏（=视口宽 390） | 261（实测） | 高 ≥44（`--ui-control-height-touch`），宽 = `min(内容宽, 字段宽)` | 复用面包屑链接的既有写法（S9）；字段行高 21→44，其余字段行不受影响 |

- 移动端实现口径（不写实现代码，只定契约）：在既有 `@media (max-width: 47.99em)` 块内，对「截断字段内的链接」给 `min-block-size: var(--ui-control-height-touch)` 并用 flex 居中；不新造断点、不写死 44。
- **副作用登记**：`<768` 时仓库行高变大 → 抽屉首屏可见字段数可能少 1 行；与 `design/screens/products.md` 的「6 字段一次可见」不构成契约冲突（该文件未规定首屏字段数）。

### 7.3 交互

| 操作 | 结果 |
|---|---|
| 点击链接（鼠标） | 新 tab 打开 `href`；当前 tab 的 hash / 抽屉状态 / 列表滚动位置均不变 |
| Tab 聚焦 | 焦点落到链接（行不抢焦点：`DataTable` 的行 `onKeyDown` 只在事件源是 `tr` 本身时响应，`ui/DataTable.tsx:100-115`） |
| Enter / Space | 浏览器默认锚点行为 = 新 tab 打开；不触发抽屉开关（同一条「一次点击一个结果」约束） |
| 点击纯文本分支 | 无任何反应（纯文本本就无行为），不冒泡成行点击（抽屉内没有行点击语义） |
| Esc / 遮罩点击 | 关闭抽屉（既有行为，不变） |

---

## 8 影响面（文件级；不含实现代码）

| 文件 | 改动性质 | 说明 |
|---|---|---|
| `web/src/lib/url.ts` | 改 | `repoHref` 补一条「内部含空白 → null」（§4.1 (3)）；返回值语义、白名单、try/catch 全保持 |
| `web/src/features/shell/DetailFields.tsx` | 改 | `DetailField` 新增 `link?: string`；`dd` 渲染分支：`link` 命中 → `<a target="_blank" rel="noopener noreferrer" title=…>`，未命中 → 原纯文本；`title` 口径不变（仅字符串值） |
| `web/src/features/products/ProductDetailSheet.tsx` | 改 | 仓库字段加 `link: product.git_url`（只声明意图，不自行判定） |
| `web/src/features/shell/shell.css` | 改 | `@media (max-width: 47.99em)` 内新增「截断字段内锚点」命中区 ≥44 规则（用 `data-*` 或既有容器选择器，避免新增未被引用的类名触发 `style-debt.mjs` ① 指标） |
| `design/screens/products.md` | 改（一句） | 「交互」段：仓库字段在合法 http(s) 时为 new-tab 链接，否则纯文本（D6） |
| `design/components.md` | 改（一句） | §「屏内私有详情抽屉」：`DetailField.link?` 与降级口径，明确 `ui/` 导出面仍 21 个（D6） |
| **不改** | — | `web/src/ui/**`（无新导出，21 个不变）、`web/src/api/**`（字段已齐）、`internal/**`、`migrations/**`、`design/refs/**`、`design/MANIFEST.sha256`、`internal/web/dist`（收口卡统一重生成） |

调用方影响：`DetailFields` 只有 2 个消费者（S12），`ProjectDetailSheet` 不传 `link` → 行为逐字节不变；这与「新字段可选」的设计一致。

---

## 9 接口与数据（如实写「无变更」）

- **接口契约**：无新增端点、无字段增删、无错误码变化。前端只消费既有 `GET /v1/products` 的 `git_url`（§3.3）。
- **数据模型**：无表结构变更、无迁移、无回填。`repo_url`（项目侧）已有后端派生，本卡不消费。
- **导出面**：`web/src/ui/index.ts` 21 个组件不变 → `web/scripts/ui-imports-check.mjs` 不需要动 `EXPECTED`。
- **design 基线**：见 D6；`refs/` 与 `MANIFEST.sha256` 不重生成，挂既有跟踪项。
- **构建产物**：`internal/web/dist` 是 tracked 产物，本卡不提交（项目上下文已规定由收口卡统一重生成）。

---

## 10 部署与回滚

### 10.1 部署

- 形态不变：单二进制（`go:embed internal/web/dist`）。前端改动走 `make web`（`style-debt.mjs` → `pnpm --dir web build`（含 `tsc --noEmit` + `check-size.mjs`）→ 同步 `internal/web/dist`），再 `make build` 出二进制，按既有镜像流程发布。
- 无数据迁移、无配置项、无环境变量变化、无灰度要求（纯渲染分支，不涉及请求与写入）。
- 发布顺序：与普通前端发版一致，无跨版本兼容约束（新旧二进制对同一份数据的行为差异仅限于「仓库是否可点」）。

### 10.2 回滚

| 层级 | 动作 | 影响 |
|---|---|---|
| 代码级 | `git revert <commit>`（或回到 `822c43e` 的前端文件后重新构建） | 仓库字段回纯文本；无数据残留 |
| 产物级 | 回滚镜像 tag 到上一个已发布版本，重新 `make web && make build` | 同代码级；`internal/web/dist` 随二进制一起回退 |
| 数据级 | 无 | 本卡不写库 |

**回滚验证**：回滚后重跑 P1-P4，预期回到「全部纯文本」= 基线行为（§3.2 截图可作对照）。

---

## 11 观测

### 11.1 现状约束

本平台没有远程上报端点（`docs/tech/web-ui-redo-v0.1.md:163` 已登记），前端没有 error boundary 上报链路，服务端也不记录前端渲染事件。因此本方案**不设计指标/告警**，只定义「证据怎么留」。

### 11.2 可观测的证据（验收与回归时执行）

| 观测点 | 手段 | 判据 |
|---|---|---|
| 判定表 17 类 | `node` 直接跑 `repoHref`（把函数体原样复制进临时脚本，不改源码） | 输出与 §4.2 表逐行一致；本卡已执行一次（§附录 A），dev 卡需在改后重跑并比对 13/14 两行由「可点」翻转为「不可点」 |
| DOM 断言 | 浏览器内 `document.querySelectorAll('[role=dialog] dd a')` 的 `href` / `target` / `rel` / `title` | P1-P4 |
| 新 tab | CDP `Target.targetCreated` + 新 target 的 `location.href` | P3 |
| 视觉 | 三视口截图（改前 / 改后） | 可点/不可点可区分；无横向滚动 |
| 门禁 | `node web/scripts/ui-imports-check.mjs`（期望 21 个通过）、`node web/scripts/style-debt.mjs`（6 项指标不退化）、`pnpm --dir web build`（含 `tsc --noEmit` 与体积门禁 344,056 B） | 全部退出码 0 |
| 触控命中区 | 仓库已有 CDP 三视口脚本 `web/scripts/ui-verify.mjs:535-570`（`checkTouchTargets`，元素集已含 `a[href]`、`<768` 阈值 44）；把产品抽屉的检查用例接进去后即可自动覆盖 §7.2 的移动端口径 | 打开抽屉后该链接命中区 ≥44×44，脚本退出码 0 |

### 11.3 明确未覆盖（如实登记，不写「加日志」糊过去）

| 未覆盖 | 原因 | 后果 |
|---|---|---|
| 线上点击成功/失败率、`_blank` 被拦比例 | 无上报端点；跨域目标站点无法回传 | 只能靠用户反馈发现；本方案已把「点击失败」的概率压到最低（协议白名单 + 新 tab） |
| 目标站点 404 / 登录墙 | 跨域，应用不可见 | 用户看到目标站点自己的错误页；不属本应用可观测面 |
| 判定函数的生产运行时异常 | `repoHref` 全函数 try/catch，异常 → 纯文本；无上报 | 若真出现异常，表现是「链接不出现」，不会打挂抽屉（`web/src` 无 ErrorBoundary，这点在 `url.ts:12-14` 注释里已写明） |
| 后端把非法 `git_url` 写入库 | 后端无协议校验（S4），属后端校验面 | 登记 U-02；前端不渲染成链接，因此不构成 XSS，只是脏数据长期存在 |

---

## 12 风险与未决项

### 12.1 风险

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| R1 | `DetailFields` 职责扩张后，未来有人把它当「通用链接组件」用（含 icon、aria-label 模板） | 契约漂移，`ui/` 与 feature 层边界变模糊 | `DetailField.link` 的注释写死「只表达意图 + 渲染层收口」，契约文档（D6）同口径；出现第三个消费方时按 `components.md:395` 的升级条件抽组件 |
| R2 | 移动端行高 21→44 与视觉基线短暂不一致 | 视觉回归 diff 增加 1 行高度 | D6 的偏差登记 + 验收截图对照；不重生成 refs |
| R3 | 「内部含空白不可点」被理解为 bug（用户贴了「URL + 说明」） | 少数登记值不可点 | `title` 保留完整串；U-04 记录「是否拆分/补全」的决策入口 |
| R4 | 链接与行点击/键盘事件的交互回归 | 出现「一次点击两个结果」 | P3-3 + `DataTable.tsx:100-115` 既有约束；dev 卡复跑行点击三条键盘路径 |

### 12.2 未决项（每条：谁定 / 缺什么信息 / 不定的后果）

| # | 未决项 | 谁定 | 缺什么信息 | 不定的后果 |
|---|---|---|---|---|
| U-01 | 产品**列表**表格仓库列、**项目**详情 `repo_url` 是否也改成可点 | 产品负责人（PM） | 是否接受「同一产品在列表纯文本、详情可点」的不一致；若统一，需设计侧确认「行内新增链接」与行点击的关系（`design/screens/products.md:7` 现行禁止行内新增按钮） | 本卡只交付详情可点；不一致长期存在，可能再报一次同类 issue（R1 的边界不在本卡扩） |
| U-02 | 后端是否给 `git_url` 加协议校验（或写入时归一化） | 后端负责人 + 产品负责人 | 存量数据里非 http(s) 值的清点结果；是否允许 `git@` 形式继续入库 | 脏数据继续入库；前端永远只能「不渲染」，无法在创建时提示 |
| U-03 | `design/screens/products.md` / `components.md` 的两句契约改动由谁落、按什么时点落 | 设计负责人 | 本卡是否允许「方案里写契约改动、实现卡里同时改 design 文件」 | 契约与实现分叉，下个 dev 卡照旧实现成纯文本（等于静默回退，D6 已列为首要风险） |
| U-04 | 是否要做 URL 补全（裸域名补 `https://`）或多链接拆分 | 产品负责人（+ 安全复核） | 脏数据样本分布；补全后是否算「改写用户登记值」 | 现状：裸域名/SSH 形式保持纯文本；补全属新范围，不做不影响 AC |
| U-05 | `design/refs/` 240 张与 `MANIFEST.sha256` 的重算时点 | 设计负责人 | 生成脚本口径不可复现（`design/README.md` 已登记） | 视觉基线继续以「42 张偏差 + MANIFEST 旧值」挂账；本卡验收截图作为临时对照 |

---

## 附录 A：本卡实测证据（2026-11-22，基线 `822c43e`）

1. **判定函数**：`node` 跑 `repoHref` 函数体（原样复制，未改源码）17 类输入，结果与 §4.2「当前实现」列一致；其中 `https://github.com/x/y https://github.com/a/b` 与 `https://github.com/x/y 附录` 返回**非 null**（即现行会渲染成一个可点链接），对应 §4.2 第 13/14 行。
2. **浏览器（改前）**：`http://echuan:8720/#/products` → 点击行 → `[role="dialog"] a` = 0；仓库 `dd[title]` = 完整 URL；桌面抽屉 720，字段 591×20；`<768` 全屏 390，字段 261×21。截图见 `docs/tech/evidence-117/before-product-detail.png`、`before-long-url-desktop.png`、`before-long-url-mobile.png`。
3. **后端不校验协议**：`POST /v1/products` 传 `git_url: "javascript:alert(1)"` → `201`；`POST` 传 `git_url: "git@github.com:Casper-Mars/echuan.git"` → `201`（与 `internal/app/product.go:293-302` 只有非空/长度校验一致）。
4. **探针清理**：3 条探针产品已 `DELETE`（各 `204`），`GET /v1/products` 复核只剩 `echuan`。
5. **未执行**：Go 侧测试未跑（本环境无 Go，`go.mod` 要求 1.26.1）；`pnpm --dir web build` 与两个门禁脚本本卡未执行（本卡不改代码，留到 dev 卡）。
