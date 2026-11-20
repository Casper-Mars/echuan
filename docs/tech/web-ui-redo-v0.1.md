# Web UI 重做 技术方案 v0.1

- 状态：已确认（2026-11-20 随本卡入库）
- 作者：CTO
- 日期：2026-11-19
- 输入：ADR-0001 `<docs/decisions/ADR-0001-web-ui-stack.md>`；代码 `<Casper-Mars/echuan@3cdc89e>`
- 上游：[issue #76](https://github.com/Casper-Mars/echuan/issues/76) + 决策人 2026-11-19 三项范围答复
- design_ref：`<本卡 design 入库 commit，见紧随其后的回填提交>`（design/ 物料入库 commit；对照图见 design/refs）

## 1 结论

**在 Mantine 8 之上建一层 `web/src/ui/` 封装，重做 6 个页面的信息架构与样式层：看板成为主入口、任务详情升级为可分享路由、附件支持应用内 markdown 预览、移动端按「读 + 轻操作」三条落地。**

改动范围：前端 6 个页面 + 8 个组件 + 新增 `ui/`、`theme/` 两层；**0 个接口改动、0 张表、0 个后端文件**；新增 3 个 npm 依赖。

最大风险：Mantine 主题表达力不足导致「认不出来」做不到 → §11 R-01。

> 工期按约定不写；顺序与依赖见 §9「发布顺序」与 §2 非目标的边界。

## 2 目标与非目标

目标：

- G1 主路径可在 3 次点击内完成「打开看板 → 定位任务 → 看附件 → 出评审结论」（现状：附件需跳浏览器新标签看原始文本，❌）。
- G2 移动端（≥360px 宽）完成三条动作：横向切列看完整个看板；打开详情读附件 markdown；推进任务状态。
- G3 样式债出清：`styles.css` 未引用类名占比从 **55%（148/269）** 降到 **< 20%**；重复定义选择器从 **39** 降到 **0**；令牌来源从 **2 个 `:root`** 收敛为 **1 个**。
- G4 体积不失控：JS gz 增量 ≤ **250 KB**（ADR E1），当前基线 88 KB gz。

非目标（本期明确不做，理由）：

- **图片预览**：平台无法存二进制（`POST /v1/materials` 只收 `body_md`；`materials` 表无 MIME 列；`materialContentType()` 硬编码 `text/markdown`）。决策人 2026-11-19 明确出清。
- **后端与契约变更**：`internal/`、`migrations/`、`/v1` 一律不动。附件预览是只读消费既有接口。
- **升级 React 19**：ADR §5 已锁定 8.x。
- **任务拖拽移动端手势库**：除非原型证明 Drawer 不够（ADR A2），否则不引 `vaul`。
- **前端远程错误上报**：无后端端点，本期只做本地错误边界与用户可见错误条（见 §10）。

## 3 AC 覆盖表

| AC | 原文 / 来源 | 模块 | 接口 | 数据 | 验证方式 |
|---|---|---|---|---|---|
| AC1 | 看板推进度为主路径（决策人答复 2） | `features/board` | `GET /v1/tasks` | 无 | 三视口截图 + CDP 断言看板 7 列渲染 |
| AC2 | 附件在线浏览 markdown（答复 2，图片已出清） | `ui/MarkdownViewer`、`features/task-detail` | `GET /v1/materials?id=` | 无 | 点附件出现正文；断网/404 出现错误条 + 兜底链接 |
| AC3 | 移动端三条（决策人答复「算数」） | `ui/AppShell`、`features/board`、`features/task-detail` | 同上 + `POST /v1/tasks/decide` | 无 | 390px 视口 CDP 走通三条；触控目标 ≥44px |
| AC4 | 样式债：死类 <20%、重复选择器 0、单一令牌 | `theme/`、`styles.css` | 无 | 无 | 静态统计脚本（复用 ADR §3 口径） |
| AC5 | 体积 ≤ 基线 +250 KB gz | 构建 | 无 | 无 | `make web` 后打印 gz 体积 |
| AC6 | 页面层不得直连 UI 库 | `web/src/features/**` | 无 | 无 | `grep -r "@mantine/core" web/src/features` 为空 |
| AC7 | 详情可分享 | 路由 | 无 | 无 | 复制详情 URL → 新标签打开 → 同一任务详情 |

## 4 现状与改动点

| 文件 / 模块 | 现状 | 改成什么 | 影响面 |
|---|---|---|---|
| `web/src/styles.css`（2613 行） | 三代视觉叠加、2 个 `:root`、39 个重复选择器、148 个死类 | 拆为 1 个令牌层 + `ui/` 内部样式；删死类 | 全站视觉 |
| `web/src/pages/*.tsx`（6 个） | 每页自拼 hero/卡片/空态，样式靠类名字符串 | 迁到 `features/`，只用 `ui/` 组件 | 全部页面 |
| `web/src/components/TaskDetail.tsx`（138 行） | 右侧抽屉；附件是 `<a target=_blank>` 裸链 | 桌面 Drawer / 移动端全屏 sheet；附件改为应用内预览；升为路由 | 主路径 |
| `web/src/components/Sidebar.tsx` | `≤1024px` 收成图标栏，文字全隐藏 | 桌面侧栏（有标签）；`<768px` 换底部 TabBar（图标 + 文字） | 导航 |
| `web/src/components/Markdown.tsx` | marked + DOMPurify，已可用 | 保留实现，包装进 `ui/MarkdownViewer`；新增内嵌图片占位策略 | 需求/方案/附件正文 |
| `web/src/api/client.ts` | 同源 fetch，无 AbortSignal | 新增 `fetchMaterialText(id, signal)`；错误映射为可读文案 | 附件预览 |
| `web/src/hooks/useAutoRefresh.ts` | 已有 scope 换代 + 失败计数 | 保留不动，仅补媒体查询 hook | 全站刷新 |
| `web/package.json` | React 18.3.1 + Vite 6 | 增 `@mantine/core@8.3.x`、`@mantine/hooks@8.3.x`、`@mantine/modals`（按需） | 构建 |

本次不改：`internal/**`、`migrations/**`、`api/openapi.yaml`、`kit/**`、`cmd/**`；`scripts/ui-verify.mjs` 只扩展断言，不重写。

## 5 总体设计

```text
features/*  ──只用──▶  ui/*  ──调用──▶  @mantine/core
    │                    │
    │                    └──使用──▶ theme/tokens.ts（唯一令牌来源）
    └──调用──▶ api/client.ts ──fetch──▶ /v1/*（同源，契约不变）
```

| 组件 | 职责 | 同步/异步 | 失败后的行为 |
|---|---|---|---|
| `theme/tokens.ts` | 色板、字号、间距、圆角、阴影、断点的唯一来源；映射为 Mantine theme | 同步（构建期常量） | 无运行时失败路径 |
| `ui/*` | 基础组件（Button/Card/Chip/Pill/Field/Select/Alert/EmptyState/Toolbar）+ 复合组件（BoardShell/BoardColumn/TaskCard/DetailSheet/MarkdownViewer/AttachmentPreview） | 同步渲染 | 渲染异常由页级错误边界兜住（§10） |
| `features/board` | 看板：7 列、列折叠、按优先级排序、点卡片跳详情 | 同步渲染 + 自动刷新 | 列表错误走错误条，保留上次数据 |
| `features/task-detail` | 详情路由页：元信息、Markdown 正文、附件预览、评审动作 | 同步渲染 + 异步取附件 | 附件失败降级为兜底链接 |
| `api/client.ts` | 同源请求、错误映射 | 异步 | 抛 `ApiError`，由调用方转错误条 |

分层红线：`features/*` 不得 import `@mantine/core`（AC6）。这一条把 ADR 的「部分不可逆」压成「可替换」。

## 6 数据模型

**本期无数据模型变更。** 不新增表、不改字段、不写迁移。

依赖的既有结构（只读）：

- `materials(id, material_id, kind, version, title, sha256, size, path, ...)` — 附件预览按 `material_id` 取正文。
- `attachments(task_id, material_id, role, sort_no)` — 详情页附件列表来源。
- 结论：`migrations/` 零改动，回滚不涉及数据。

## 7 接口契约

**不新增接口，不改契约。** 前端消费以下既有端点（签名以 `internal/api/router_v3.go` 为准）：

| 端点 | 用途 | 本期变化 |
|---|---|---|
| `GET /v1/products`、`GET /v1/projects` | 产品/项目列表 | 无 |
| `GET /v1/tasks?project_id=&kind=&status=&limit=` | 看板数据源 | 无 |
| `GET /v1/tasks?id=` | 详情 | 无 |
| `GET /v1/tasks/dependencies?task_id=`、`GET /v1/tasks/attachments?task_id=` | 详情分区 | 无 |
| **`GET /v1/materials?id=`** | **附件正文（本期新消费）** | 前端首次在应用内读取 |
| `POST /v1/tasks/decide` | 评审通过/驳回 | 无 |
| `POST /v1/tasks/reset`、`DELETE /v1/tasks` | 失败重置 / 删除未开工任务 | 无 |
| `GET /v1/inbox`、`GET /v1/workers` | 收件箱 / 执行节点 | 无 |

`GET /v1/materials` 的行为是本期设计的硬依赖（已核对源码）：

- 响应 `Content-Type: text/markdown; charset=utf-8`（硬编码）；`ETag:"<sha256>"`；`Cache-Control: public, max-age=31536000, immutable`；支持 `Range` 与 `HEAD`。
- 因此**附件正文可被浏览器 HTTP 缓存直接复用**，前端不需要自建缓存层；只做请求去重（同一 `material_id` 并发只发一次）。
- 错误映射：`404` → 「附件不存在或已删除」+ 兜底链接；`413` → 「附件过大」；其他 → 原文提示。

## 8 关键流程

正常路径（看板推进度）：

1. 用户进入看板（`#/projects/:pid/tasks`）→ `GET /v1/tasks` 拉 300 条上限 → 按 `status` 分 7 列，列内按 `priority` 降序、`update_time` 降序。
2. 点卡片 → 前端路由切到 `#/projects/:pid/tasks/:taskId` → `GET /v1/tasks?id=` + 依赖 + 附件 并发拉取。
3. 详情内点附件 → `GET /v1/materials?id=` → `text()` → `MarkdownViewer` 渲染（marked + DOMPurify 不变）。
4. `in_review` 状态下提交评审结论 → `POST /v1/tasks/decide` → 成功关闭 sheet 并触发 `refresh('write')`（复用现有 hook 语义，不与自动刷新叠发）。

异常路径：

| 场景 | 检测方式 | 处理 | 最终状态 |
|---|---|---|---|
| 看板列表超时 | fetch 无响应（沿用现有：无显式超时） | 保留上次数据 + 错误条；自动刷新下一轮自愈 | 界面旧数据，明示错误 |
| 附件 404 | HTTP 404 | 错误条 + 「在新标签打开原文」兜底 | 附件区降级，不阻塞详情其他分区 |
| 附件过大 413 | HTTP 413 | 错误条提示，不给预览 | 同上 |
| 评审并发冲突 | `POST /v1/tasks/decide` 非 2xx（任务已被他人处理） | 错误条显示服务端 message + 立即重拉详情 | 用户看到最新状态 |
| 移动端误触删除 | 破坏性动作 | 二次确认；仅 `pending`/`ready` 可删（沿用现有约束） | 取消则无变化 |
| markdown 内嵌图片 | `img` 加载失败 | **显式占位**（alt 文本 + 断链标记），禁止静默空白（ADR R4） | 正文其余部分正常 |

## 9 部署与配置

新增依赖（运行期 2 + 可选 1）：

| 包 | 版本约束 | 作用 |
|---|---|---|
| `@mantine/core` | `^8.3.18`（**锁定 8.x，不得跨大版本**） | 组件与样式底座 |
| `@mantine/hooks` | `^8.3.18` | `useMediaQuery` 等 |
| `@mantine/modals` | `^8.3.18`（可选，仅当封装层需要集中式确认弹窗） | 二次确认 |

**无新增环境变量**；`VITE_ACTOR` 沿用现状。

发布顺序：1) `pnpm --dir web install` 2) `make web`（构建并同步到 `internal/web/dist`）3) `make build`（嵌入二进制）4) `make docker-build` → 替换容器。

灰度：单二进制内网部署，**不做流量灰度**；替代方案是保留上一版镜像 tag，回滚即切回（这是本项目的既有发布形态，不是新引入的机制）。

回滚：

- 代码回滚：`git revert <merge>` → `make web` → `make build` → 重新部署容器。
- 数据回滚：无（本期无迁移）。
- 回滚验证点：回滚后 `/version` commit 与镜像 tag 一致，且 6 个页面可打开。

## 10 可观测性

本方案是纯前端 + 既有后端，**不新增服务端指标**。可执行的观测量如下（不写「加日志」凑数）：

- **构建期门禁（主要手段）**：`make web` 后打印 `dist/assets/*.{js,css}` 的原始与 gzip 体积；JS gz 超过基线 +250 KB 即判定违反 ADR E1，构建失败。这是 AC5 的自动化落点。
- **静态债门禁**：统计 `styles.css` 未引用类名占比与重复选择器数，写入构建输出；阈值 AC4（<20%、0）。这是防止「重做后又长回去」的唯一机制。
- **运行期错误边界**：页级 React error boundary → 渲染错误卡片并提供「重试 / 返回看板」；所有 `ApiError` 统一转为用户可读错误条（含 HTTP 状态与服务端 message）。
- **不做的观测**：远程错误上报（无端点）、性能埋点（无采集端）、附件预览成功率（无服务端日志口径）。这些列入非目标，不假装已实现。

UI 回归：扩展 `web/scripts/ui-verify.mjs`（现有 CDP 脚本）为三视口断言（1440 / 834 / 390），覆盖 AC1/AC3 的关键路径。

## 11 风险与未决

| # | 风险 | 触发信号 | 影响 | 缓解 | 回滚 |
|---|---|---|---|---|---|
| R-01 | Mantine 主题表达力不足，「认不出来」做不到 | 原型阶段 ≥3 个页面需大量 hack 主题（ADR E2） | 视觉目标落空 | 先用看板单页做原型验证 A1 | 转 ADR 选项 B（Radix+Tailwind） |
| R-02 | 树摇后体积超限 | 构建期 gz 超 250 KB 增量（ADR E1） | 移动端加载变慢 | 按需引入子包、避免整包 import | 同上 |
| R-03 | 移动端写操作（评审/删除）误触 | 小屏操作路径缩短 | 误驳回、误删 | 二次确认 + 底部操作条与滚动区分离 | 无需回滚，交互层修正 |
| R-04 | 详情升为路由后，旧行为（无路由参数的抽屉）被破坏 | 用户从收藏/历史进入 | 详情打不开 | 无 `taskId` 时保持走板页；路由为增量而非替换 | 路由回退为纯 state |
| R-05 | 无前端测试框架，重做期回归靠 CDP 脚本 | 脚本覆盖不足 | 隐性回归 | 扩展现有脚本，不为本需求引入框架 | — |
| R-06 | 附件正文含内嵌图片时出现断链 | 预览中出现占位 | 用户误以为内容缺失 | 显式占位 + 说明文案（ADR R4） | — |

未决项：

| # | 问题 | 谁定 | 何时 | 未定前默认动作 |
|---|---|---|---|---|
| U-01 | 详情路由用 `#/projects/:pid/tasks/:taskId` 还是保留纯抽屉 state | 决策人 | 原型验收时 | 按本方案走路由（可分享，AC7） |
| U-02 | 看板 7 列是否默认折叠 `done`/`cancelled` | 决策人 | 原型验收时 | 默认折叠这两列，可展开 |
| U-03 | 附件正文大于多少字节时改为「点击后加载」而非自动渲染 | 技术（§11 R-06 关联） | 实现前 | 默认 ≤256 KB 自动渲染，超过按需加载 |
| U-04 | 移动端是否保留「新建任务」入口 | 决策人 | 原型验收时 | 保留，但收进二级菜单（主路径是推进不是创建） |

## 12 设计契约引用

设计与决策物料已随本卡入库，`design/` 是后续所有 dev 卡的唯一视觉对照源；本方案其余章节不变，实现细节以设计物料为准。

**design_ref**：见文首元信息行。`design_ref` 指向 `design/` 物料入库的仓库 commit——在该 commit 下 `design/screens/*.md`、`design/tokens.json`、`design/components.md`、`design/flows.md` 与 `design/refs/` 的 240 张截图可逐字节检出（`git cat-file -e <design_ref>^{commit}` 可解析）。PR 合并后由人工合并产生的新 sha 不再回写本行。

**8 屏目录**（`design/screens/`）：

| 屏 | 规格文件 | 状态矩阵状态数 |
|---|---|---|
| `products` | `design/screens/products.md` | 4 |
| `projects` | `design/screens/projects.md` | 4 |
| `board` | `design/screens/board.md` | 6 |
| `task-detail` | `design/screens/task-detail.md` | 6 |
| `attachment-preview` | `design/screens/attachment-preview.md` | 6 |
| `inbox` | `design/screens/inbox.md` | 4 |
| `workers` | `design/screens/workers.md` | 5 |
| `queue` | `design/screens/queue.md` | 5 |

**refs 命名规则**：`design/refs/<screen>__<viewport>__<theme>__<state>.png`

- `<screen>`：上表 8 个目录名；
- `<viewport>`：`desktop`（1440×900）/ `tablet`（1024×768）/ `mobile`（390×844）；
- `<theme>`：`dark` / `light`；
- `<state>`：对应 `design/screens/<screen>.md` 状态矩阵里的状态名（如 `default` `empty` `loading` `error` `long-list`）。

**数量口径**：40 个「屏 × 状态」组合 × 3 视口 × 2 主题 = 240 张，其中暗色 120 张、亮色 120 张。

**暗色为验收基准**：默认主题 = 暗色（`design/README.md`「视觉方向」），dev 实现与视觉回归以 `refs/<screen>__<viewport>__dark__<state>.png` 为准；亮色 120 张为同套对照，不作为验收判据。

**契约校验**（本卡入库前必须 0 error，后续改设计物料时同口径复跑）：

```bash
node <design-kit>/scripts/check.mjs design
```

五类检查：①样式文件禁 hex/px 字面量 ②semantic 只能别名到 color primitive ③正文语义色 APCA Lc ≥ 60 ④无 `{{...}}` / `TODO:` 残留 ⑤`screens/*.md` 状态矩阵里每个状态 × 三视口都有对应截图（`light` 命名）。通过时输出 `[check] 通过（0 个警告）`，退出码 0。
