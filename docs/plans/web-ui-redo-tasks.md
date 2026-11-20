# Web UI 重做 · 任务拆分总表（12 卡）

- 来源：`notes/web-ui-redo-plan.json`（与上游拆分物料 `AGQ7O3JTFEUB3ZC4POMSMWTHJY` 同源，12 项）
- 生成方式：逐项搬运 `seq_no` / `title` / `depends_on` / `priority` / `body_md`；各卡正文文字未改动，仅把原 `## ` 小节标题下调一级为 `### ` 以适配本表层级
- 上游依据：ADR-0001（`docs/decisions/ADR-0001-web-ui-stack.md`）、技术方案（`docs/tech/web-ui-redo-v0.1.md`）、设计实例 v0.3（`design/`）
- T1 已把上述设计与决策物料入库；后续 dev 卡的 `design_ref` 指仓库内 commit，取值见 `design/README.md` 的 design_ref 段

## 依赖总表

| seq | 标题 | 依赖 | 优先级 |
|---|---|---|---|
| 1 | 设计契约与决策文档入库（design_ref 落库） | — | 90 |
| 2 | 接入 Mantine 8 与产物体积门禁 | T1 | 85 |
| 3 | 设计系统落地：令牌层 + 21 个 ui 组件 + 暗色默认 | T2 | 80 |
| 4 | 应用外壳与路由：桌面/平板/移动三形态 + 详情可分享路由 | T3 | 75 |
| 5 | 看板重做（主路径）：7 列 + 折叠 + 移动端横向切列 | T4 | 70 |
| 6 | 任务详情重做：可分享路由 + 评审区（material_id 自动带入） | T4 | 70 |
| 7 | 附件 markdown 应用内预览与四类降级 | T6 | 65 |
| 8 | 收件箱与派单队列重做（入口合并、路由保留） | T4 | 60 |
| 9 | 产品 / 项目 / 执行节点三屏重做 | T4 | 55 |
| 10 | 移动端三条专项验收 + UI 回归脚本三视口扩展 | T5、T6、T7、T8、T9 | 50 |
| 11 | 样式债出清与产物体积门禁收口 | T10 | 40 |
| 12 | 收尾合并 PR：feature/echuan-1 → 默认分支（人工点合并） | T11 | 30 |

## T1 设计契约与决策文档入库（design_ref 落库）

- 依赖：—
- 优先级：90

### 目标
把三份已评审产物提交进仓库，成为后续所有 dev 卡的唯一对照源；此后每张卡的 design_ref 指仓库内 commit。

### 范围
- `docs/decisions/ADR-0001-web-ui-stack.md`、`docs/tech/web-ui-redo-v0.1.md`、`docs/plans/web-ui-redo-tasks.md`
- `design/`：tokens.json、components.md、flows.md、screens/*.md、mock/（可跑原型）、refs/（240 张：暗色 120 为验收基准、亮色 120 对照）、README、MANIFEST.sha256
- 只入库，不改任何产品源码

### 任务级验收（可观察）
1. 仓库内 `design/refs` 下 PNG 数量为 240，且暗色/亮色各 120
2. `node <design-kit>/scripts/check.mjs design` 退出码 0（0 error）
3. `design/README.md` 与 `docs/tech/web-ui-redo-v0.1.md` 中记录 design_ref = 本卡合并后的 commit sha
4. `git status` 干净；本卡不出现 `web/src`、`internal/`、`migrations/`、`api/` 的任何改动

### 不做
不接依赖、不改构建、不写业务代码。

### 前置
- 上游 design 物料 `AGQ7KVSIUHG2OIVZXRYJUBG4XI`（v0.3）
- 设计实例当前在工作区 `/workspace/design-echuan-ui/design/`，design_ref = `2b306fbe4441ccec730144a6738745788064d89f5117906cf965f7de9f60c80d`

## T2 接入 Mantine 8 与产物体积门禁

- 依赖：T1
- 优先级：85

### 目标
把选型落地为可构建的依赖，并让「产物体积超标」在构建期失败，而不是靠人记。

### 范围
- 运行期依赖只加 2 个：`@mantine/core@^8.3.18`、`@mantine/hooks@^8.3.18`
- 提交 `web/pnpm-lock.yaml`（镜像构建走 `pnpm install --frozen-lockfile`，不提交则 docker 阶段直接失败）
- 体积门禁：构建后打印 JS/CSS 的 raw 与 gzip，JS gzip 超「基线 88,056 B + 250 KB」即非 0 退出
- 把门禁串进前端构建入口（`make web` 路径）

### 任务级验收（可观察）
1. `pnpm --dir web build` 退出码 0
2. `pnpm --dir web install --frozen-lockfile` 退出码 0（验证 lock 已提交）
3. 门禁脚本输出包含 JS 与 CSS 的 raw/gzip 两行数值
4. 临时把阈值调到 1 KB，门禁必须非 0 退出并打印超标项（验证门禁真的会失败，验证后改回）
5. `web/package.json` 的 dependencies 中只新增了这 2 个 UI 包

### 不做
不引图标库/日期库/通知库，不引 Tailwind，不改 React 版本，不写页面。

## T3 设计系统落地：令牌层 + 21 个 ui 组件 + 暗色默认

- 依赖：T2
- 优先级：80

### 目标
把 `design/tokens.json` 变成代码里唯一的样式来源，落出 `ui/` 封装层与全局视觉底座，默认暗色主题。

### 范围
- 令牌层：把 DTCG 令牌翻译为运行期主题（Mantine theme 映射）与 CSS 变量，**不允许**在组件里写裸色值/裸像素
- `ui/` 21 个组件（20 节，TextField 与 TextArea 同族）：Button、IconButton、TaskCard、StatusPill、KindBadge、BoardColumn、DataTable、TextField、TextArea、SelectField、Alert、EmptyState、SkeletonCard、ConfirmModal、DetailSheet、TabBar、SidebarNav、MarkdownViewer、AttachmentRow、Segmented、Chip（与 design/components.md 的总览表一一对应）
- 全局视觉底座：reset、排版、氛围光、细网格、玻璃面板、渐变主按钮、状态色条、数字等宽
- 主题默认暗色（`data-theme="dark"`），亮色保留可切换

### 任务级验收（可观察）
1. `pnpm --dir web build` 退出码 0
2. 组件清单与 `design/components.md` 总览表一一对应：21 个组件全部可 import（TextField/TextArea 计两个）
3. `grep -rn "@mantine/core"` 在页面层目录为空（页面只能经 `ui/`）
4. 全局样式层只剩**一个** `:root` 令牌源，且无媒体查询内的令牌覆写
5. 暗色默认生效：页面根节点 `data-theme="dark"`，令牌解析出的 surface/text 值为暗色档
6. 构建产物体积数值记录进卡内（供 T11 对比）

### 不做
不重做业务页面（留给 T5–T9）；不删旧样式文件（T11 统一收口，避免中间态误导）。
注：组件的视觉逐项核对在各页面卡完成，本卡只保证结构与令牌正确。

## T4 应用外壳与路由：桌面/平板/移动三形态 + 详情可分享路由

- 依赖：T3
- 优先级：75

### 目标
让「看板是壳、详情是路由」成立：三形态导航与可分享的详情 URL。

### 范围
- 桌面 `≥1280`：左栏 240px、4 个一级入口且有文字
- 平板 `768–1279`：左栏收 64px 图标栏，文字进 Tooltip（不允许「无文字且无替代」）
- 移动 `<768`：无侧栏，底部 TabBar 4 项（看板/收件箱/产品/更多），留 safe-area
- 路由：`#/` 重定向到上次项目看板；`#/projects/:pid/tasks/:tid` 详情可分享；无 taskId 时回看板
- 页级错误边界与统一错误条骨架

### 任务级验收（可观察）
1. 1440 / 834 / 390 三视口壳层截图与 `design/refs/board__{desktop,tablet,mobile}__dark__default.png` 的导航与顶栏区域一致
2. 平板视口导航项有 Tooltip 文案，移动视口底部 4 个 Tab 均为「图标+文字」
3. 直接打开一个详情 URL 能进入详情态；去掉 taskId 回看板
4. 三视口 `document.scrollWidth === clientWidth`
5. 主操作按钮全屏只出现一次（顶栏不重复）

### design_ref
design_ref = 入库后仓库内 commit sha（T1 建立）；必须覆盖状态：default、loading。

## T5 看板重做（主路径）：7 列 + 折叠 + 移动端横向切列

- 依赖：T4
- 优先级：70

### 目标
按设计重做看板，使其成为真正的主路径：一屏看清推进态，移动端能横向看完 7 列。

### 范围
- 7 列（pending/ready/in_progress/in_review/done/failed/cancelled），列头 sticky + 计数，列内按 priority 降序
- `done`/`cancelled` 默认折叠为 56px 竖条，偏好持久化
- 点卡片进详情路由；筛选（kind/status）进 URL query
- 15s 自动刷新不打断滚动与输入

### 任务级验收（可观察）
1. 6 个状态三视口截图对照 `design/refs/board__{desktop,tablet,mobile}__dark__{default,empty,loading,error,collapsed,long-list}.png`
2. 移动端 M1：看板容器 `scrollWidth > clientWidth`；滚到最右后最后一列完整可见（右边界 ≤ 视口 +1px）；列宽 ≥ `min(86vw,320px)`
3. 折叠后刷新页面，折叠状态保持（localStorage）
4. 列表失败时保留上次数据并出现错误条，不整屏转空

### 必须覆盖的状态
default / empty / loading / error / collapsed / long-list

### 不做
不做拖拽改状态、不做列自定义排序、不做看板内联编辑。

## T6 任务详情重做：可分享路由 + 评审区（material_id 自动带入）

- 依赖：T4
- 优先级：70

### 目标
把详情从「抽屉 state」升级为可分享路由，并修掉评审手输 material_id 的缺陷。

### 范围
- 桌面 720px 右侧抽屉（保留看板上下文），移动全屏 sheet + 底部固定操作条，均绑定 `#/projects/:pid/tasks/:tid`
- 分区顺序：元信息 → 租约条 → 评审区（仅 in_review）→ 附件 → 描述 → 上下文 → 依赖 → 驳回/失败原因
- 评审「被评审附件」从 attachments 的 output 项自动带入（下拉可选），默认选第一个；驳回意见必填
- 提交走既有 `POST /v1/tasks/decide`，失败显示服务端 message

### 任务级验收（可观察）
1. 6 个状态三视口截图对照 `design/refs/task-detail__{desktop,tablet,mobile}__dark__{default,loading,error,leased,failed,rejected}.png`
2. 详情 URL 可直接打开；关闭后回到原看板
3. 评审区没有可自由输入的 material_id 文本框；被评审附件为下拉且默认已选 output
4. 移动端底部操作条固定可见，按钮 ≥44×44
5. 评审冲突（非 2xx）时出现错误条且详情自动重拉

### 必须覆盖的状态
default / loading / error / leased / failed / rejected

### 不做
不做字段内联编辑、不在详情里新建任务。

## T7 附件 markdown 应用内预览与四类降级

- 依赖：T6
- 优先级：65

### 目标
在应用内读附件正文，任何失败都有明确文案与兜底，不出现白屏或静默空白。

### 范围
- 点附件 → `GET /v1/materials?id=`（超时 8s）→ 读 `Content-Length`；>512 KB 不自动渲染
- 正文用既有 marked + DOMPurify 渲染，正文容器标记 `data-testid=markdown-viewer`
- 桌面在详情内二级面板（左列表 280px），移动全屏二级 sheet
- 降级：404 / 413 / 超时 / 为空 / 内嵌图片占位 / 渲染异常降级为原文
- 同 material_id 并发去重，不新开标签

### 任务级验收（可观察）
1. 6 个状态三视口截图对照 `design/refs/attachment-preview__{desktop,tablet,mobile}__dark__{default,empty,loading,error,oversized,image-placeholder}.png`
2. 点附件后同页出现 `[data-testid=markdown-viewer]`，且无新窗口/新标签导航
3. 四类降级各复现一次：404、超大（>512KB）、空正文、正文内嵌图片显式占位文案
4. 返回详情后滚动位置保持

### 必须覆盖的状态
default / empty / loading / error / oversized / image-placeholder

### 不做
不做图片预览（平台无二进制存储），不做下载另存，不做全文搜索。

## T8 收件箱与派单队列重做（入口合并、路由保留）

- 依赖：T4
- 优先级：60

### 目标
一屏交代「要我拍板的」与「worker 会拉走的」，把派单队列入口并入收件箱而不删路由。

### 范围
- 收件箱三区：待审核 / 待领取 / 派单队列
- `#/queue` 路由保留（兼容旧书签），主入口并入收件箱第三区
- 卡片网格自适应；空态、加载、错误齐备

### 任务级验收（可观察）
1. `inbox` 4 状态 + `queue` 5 状态三视口截图对照 `design/refs/inbox__*__dark__*.png`、`design/refs/queue__*__dark__*.png`
2. 侧栏/TabBar 不再单独列「派单队列」入口；直接打开 `#/queue` 仍可用
3. 分区计数与数据条数一致

### 必须覆盖的状态
inbox：default / empty / loading / error；queue：default / empty / loading / error / long-list

### 不做
不做批量审核、批量领取。

## T9 产品 / 项目 / 执行节点三屏重做

- 依赖：T4
- 优先级：55

### 目标
把三张列表页做成品类一致、移动端可读的形态。

### 范围
- 产品页：产品名 + 仓库（等宽）+ 项目数 + 进入项目
- 项目页：序号 + 名称 + open/closed 状态 + 更新时间 + 打开看板
- 执行节点：节点/状态/当前任务/心跳/操作；心跳过期行整行淡底 + 红色心跳徽标
- 移动端表格转卡片行（字段名可见）

### 任务级验收（可观察）
1. products 4 / projects 4 / workers 5 状态三视口截图对照 `design/refs/{products,projects,workers}__*__dark__*.png`
2. 390 视口下表格为卡片行且每行带字段名
3. 心跳过期行有可辨识的差异（背景 + 徽标），不只靠颜色

### 必须覆盖的状态
products/projects：default / empty / loading / error；workers：default / empty / loading / error / stale

### 不做
不做产品编辑/删除、不做项目 close/reopen、不做远程终止 worker。

## T10 移动端三条专项验收 + UI 回归脚本三视口扩展

- 依赖：T5、T6、T7、T8、T9
- 优先级：50

### 目标
把「移动端好用」从口头变成可重复执行的断言，并把回归从人工看图变成脚本。

### 范围
- 扩展现有 CDP 自检脚本为三视口（1440 / 834 / 390），覆盖 M1–M3 与通用项
- M1 横向切列、M2 详情读附件、M3 推进任务状态
- 无水平溢出、正文字号 ≥15、触控目标 ≥44

### 任务级验收（可观察）
1. 一次运行输出 M1–M3 全 PASS 的日志（贴原始输出，不许只写「通过」）
2. 三视口 `document.scrollWidth === clientWidth`
3. 所有可点元素 boundingBox ≥44×44（枚举输出不达标项，应为空）
4. 断言失败时脚本非 0 退出（人为改坏一个选择器验证一次）

### 不做
不引入测试框架（无前端测试基建），不写单元测试。

## T11 样式债出清与产物体积门禁收口

- 依赖：T10
- 优先级：40

### 目标
证明「不是把丑挪了个位置」：样式债与体积都被量化收敛，且门禁常驻构建。

### 范围
- 样式债脚本落地并进构建/CI：未引用类名、重复选择器、令牌源、样式文件行数、断点数、死类子系统
- 删除旧视觉层与死类（`.board-column*`、`.row-*`、`.notice-*`、`.state-panel`、`.spinner`）
- `make web` 体积门禁接好并打印数值

### 任务级验收（可观察）
1. 样式债脚本输出：未引用类名占比 <20%、重复定义选择器 = 0、顶层令牌源 = 1 且媒体查询内覆写 = 0、全局样式 ≤300 行、断点 1 套
2. 死类子系统 `grep` 结果为空
3. `make web` 与 `make build` 均退出码 0，且打印 JS/CSS 体积；JS gzip ≤ 基线 +250 KB
4. 8 屏 × 状态的三视口对照截图逐项打勾（对照 `design/refs`），无未覆盖状态

### 不做
不建合并 PR（见 T12）、不改后端与 `/v1`、不改 CI 配置。

## T12 收尾合并 PR：feature/echuan-1 → 默认分支（人工点合并）

- 依赖：T11
- 优先级：30

### 目标
把整条 feature 分支交成一个可人审的合并 PR：改动范围、验收证据、回滚方式写清楚；**合并动作由人执行**。

### 范围
- 创建 PR：base = 仓库默认分支，head = `feature/echuan-1`
- PR 描述三块：① 改动范围（前端 8 屏 + `ui/` 封装层 + 设计系统；后端 0 文件、接口 0 变更、数据库 0 变更）② 验收证据（样式债数值、体积数值、移动端 M1–M3 输出摘要、8 屏状态对照图清单）③ 回滚步骤（revert + 重建镜像，或切回上一版不可变镜像 tag）
- 只建 PR 与写描述，不改任何代码

### 任务级验收（可观察）
1. PR 已创建且 base/head 正确，链接可访问，状态为 open（未被 agent 合并）
2. PR 描述包含上述三块内容，且引用了 `design/refs` 的对照图命名规则
3. `git diff --stat origin/main...feature/echuan-1` 只含预期路径（`web/`、`docs/`、`design/`，必要时 `Makefile`），**不含** `internal/`、`migrations/`、`api/`
4. `/version` 的 commit 与本次合并前的 feature HEAD 一致（部署校验口径写进 PR 描述）

### 不做
不点击合并、不删除分支、不推 `main`（全部是人的动作）。

---

本表为 dev 卡唯一对照源，design_ref 见 `design/README.md`。
