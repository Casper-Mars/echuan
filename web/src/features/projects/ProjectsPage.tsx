// features/projects/ProjectsPage —— 某产品下的项目列表（`#/products/:productId/projects`）
//
// 规格对照：
//   design/screens/projects.md   页头（产品名 + 项目数 + 新建项目）→ 表格（序号 · 项目 · 状态 · 更新时间 · 操作）；
//                                四态 default / empty / loading / error；<768 卡片式行（data-label 字段名）；
//                                closed 项目操作保留（只读可看）；「交互」行点击 → 只读详情抽屉
//                                （9 字段、0 请求、字段取当前行），行内「打开看板」只导航不开抽屉；
//                                操作列 =「打开看板」（default）+「关闭 / 重开」（ghost）。
//   design/components.md         §ui/DataTable、§ui/StatusPill（圆点 + 文案，不只靠颜色）、§ui/EmptyState、
//                                §ui/SkeletonCard、§ui/Alert、§ui/Button、§ui/ConfirmModal。
//   design/flows.md              F5 关闭/重开项目（写操作，带二次确认）。
//   design/screens/projects.md   @82624c9（issue #133 倒序 + 状态筛选）：页头「按状态筛选」（`ui/SelectField`，
//                                全部状态 / 进行中 / 已关闭，默认全部状态）；表格按接口返回顺序呈现（`id DESC`，
//                                序号随 `seq_no` 递减，前端不再排序）；状态矩阵含「筛后为空」；筛选控件可见性
//                                判定式 `!首屏失败 && (见过行 || 产品项目数 > 0)`。
//   docs/tech/projects-sort-filter-133-v0.2.md §8.2（异常 / 部署窗口）+ §8.3（前端子态规则，逐条对应见下方
//                                「①…⑨」注释）、§5（换代键 = productId|status）；ADR-0002 §2.3（前端不排序 / 不按状态过滤）
//                                + §4 C3（`api.client` 里 1 行防御性 product_id 再过滤）。
//   docs/tech/web-ui-redo-v0.1.md §7   数据源：GET /v1/projects（按需带 product_id / status），产品名走 GET /v1/products?id=。
//   docs/tech/web-ui-102-interaction-fix-v0.1.md §7.2（D1 字段口径）/ §7.3（D2）/ §7.4（D3 入口只在行操作列）/
//                                §7.5（D4：先不带 force → 409 → 同一弹窗切「取消 N 个任务」→ force=1；**不做**
//                                `GET /v1/tasks?status=` 预检）/ §5.1（409 两种来源靠 message 子串区分）/ §7.8（一次点击一个结果）/
//                                §9.1（错误条：标题与说明中文，服务端原文只进 detail）。
//                                design/components.md §「屏内私有详情抽屉」——项目抽屉落在
//                                features/projects/ProjectDetailSheet，键值网格复用 features/shell/DetailFields。
//
// 分层：只 import ui / api / hooks / lib 与壳层骨架（ErrorBar），不直接 import Mantine（分层红线）；
// 样式见 projects.css（只消费令牌）。关闭 / 重开的确认弹窗是本屏私有件 ProjectWriteModal，
// 错误文案收敛在 projectWrite（与方案文本的一处实测偏差登记在 projectWrite.ts 头注释）。
//
// 不做（design/screens/projects.md「不做」）：抽屉只读（不做字段编辑）；不做批量关闭 / 重开；
// 「重开」不恢复已取消的任务。页头「新建项目」按 design/refs 展示但**不发写请求**（创建流程不在本卡范围）。
// 也不做（方案 §2 非目标 / §11 U5）：分页 / 加载更多、搜索与多条件筛选、筛后为空时的「清除筛选」按钮。
import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api } from '../../api/client'
import type { Project } from '../../api/types'
import { useAutoRefresh } from '../../hooks/useAutoRefresh'
import { REFRESH_INTERVALS } from '../../lib/refresh'
import { Alert, Button, DataTable, EmptyState, SelectField, SkeletonCard, StatusPill } from '../../ui'
import type { DataTableColumn } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'
import { ProjectDetailSheet, formatProjectTime } from './ProjectDetailSheet'
import { ProjectWriteModal } from './ProjectWriteModal'
import type { WriteFlow } from './ProjectWriteModal'
import {
  httpDetail,
  isUnfinishedConflict,
  messageOf,
  toWriteError,
  unfinishedCountOf,
  writeErrorTitle,
} from './projectWrite'
import type { ProjectWriteError } from './projectWrite'
import './projects.css'

/** 稳定的空数组：避免每渲染新建引用，也避免把空态误判成 loading。 */
const EMPTY_PROJECTS: Project[] = []

/** 骨架卡片数：design/screens/projects.md loading 态「3 个 SkeletonCard」 */
const SKELETON_CARDS = 3

/** 重拉中（换代键不匹配）表体的骨架行数：与表格默认加载行一致（ui/DataTable loadingRows 缺省 3） */
const SKELETON_ROWS = 3

/**
 * 筛选项（design/screens/projects.md：「筛选形态与 products 屏一致」，选项文本固定）。
 * U6：与 `ProductsPage` 的同名常量各自持有，不为此抽公共常量、不改产品页。
 */
type StatusFilter = 'all' | 'open' | 'closed'

const STATUS_FILTERS: Array<{ value: StatusFilter; label: string }> = [
  { value: 'all', label: '全部状态' },
  { value: 'open', label: '进行中' },
  { value: 'closed', label: '已关闭' },
]

/** 服务端只认 `open`/`closed`；`'all'` = 不传 `status`（方案 §7：`status` 缺省 = 全部状态）。 */
const serverStatusOf = (status: StatusFilter): 'open' | 'closed' | undefined => (status === 'all' ? undefined : status)

/** 下拉回传的是 `string | null`（Mantine 可清空）；非三个合法值一律回落「全部状态」，不把非法值发给服务端。 */
const asStatusFilter = (value: string | null): StatusFilter =>
  value === 'open' || value === 'closed' ? value : 'all'

/**
 * 一代响应（方案 §8.3 / §5）：`key` = 换代键 `productId|status`，`items` = 这一代的列表。
 * 用「响应自带 key」而不是「当前 state 反推」，才能判断手上这份数据属于哪一代。
 */
interface ProjectsGeneration {
  key: string
  items: Project[]
}

/**
 * 「新建项目」入口按 design/refs 展示，但本卡数据源只有三个 GET、不做写操作，
 * 所以按钮不带副作用（不发 POST、不弹表单）。创建流程不在本卡范围，见交付的「未覆盖项」。
 */
const CREATE_PROJECT_PLACEHOLDER = () => {}

export function ProjectsPage() {
  const { productId = '' } = useParams()
  const navigate = useNavigate()

  // ① 筛选值：组件内 state，缺省「全部状态」；不落 URL、不落 localStorage（方案 §8.3 第一行）。
  const [status, setStatus] = useState<StatusFilter>('all')

  // ⑧ 跨产品切换：路由元素复用（web/src/App.tsx 中 `/products/:productId/projects` 是同一个组件），
  //    新产品必须从「全部状态」开始。这里在渲染期同步归位（React 的「props 变化时调整 state」写法），
  //    而不是放 effect：effect 晚一帧，新产品会先带上一个产品的筛选值发一次请求，多打一次且短暂错表。
  const [statusOwner, setStatusOwner] = useState(productId)
  if (statusOwner !== productId) {
    setStatusOwner(productId)
    setStatus('all')
  }

  // ② 换代键 = productId|status，交给 useAutoRefresh 做换代与旧代丢弃；
  //    load 把「这一代是谁」写进返回值（key），供下面判断手上那份数据是否属于当前代。
  const scope = `${productId}|${status}`
  const { data, error, refresh } = useAutoRefresh<ProjectsGeneration>({
    intervalMs: REFRESH_INTERVALS.projects,
    scope,
    load: () =>
      api
        .listProjects({ productId, status: serverStatusOf(status) })
        .then((list) => ({ key: `${productId}|${status}`, items: list.items })),
  })

  // 产品名与 `project_count` 都来自同一次 `GET /v1/products?id=`：前者只服务页头文案
  // （design/refs：「驿传 Echuan · 3 个项目」），后者是筛选控件可见性的兜底判据（§8.3）。
  // 它不是本页数据源，取不到就回退显示 product_id、`project_count` 视同未知，不把整页打成错误态。
  const [productInfo, setProductInfo] = useState<{ name: string; projectCount: number | undefined }>({
    name: '',
    projectCount: undefined,
  })
  useEffect(() => {
    let alive = true
    setProductInfo({ name: '', projectCount: undefined })
    api
      .getProduct(productId)
      .then((product) => {
        if (alive) setProductInfo({ name: product.name, projectCount: product.project_count })
      })
      .catch(() => {
        if (alive) setProductInfo({ name: '', projectCount: undefined })
      })
    return () => {
      alive = false
    }
  }, [productId])

  // ⑤ 当前代：响应 key 与当前换代键不一致 ⇒ 这一代还在路上（重拉中），**不用**旧代数据充数。
  const current = data?.key === scope ? data.items : undefined
  const firstLoadLoading = data === undefined && error === ''
  const firstLoadError = data === undefined && error !== ''
  const owner = productInfo.name === '' ? productId : productInfo.name

  // ④ 控件可见性的「见过行」：绑定 productId，切产品立即失效（不靠 effect 事后清零，避免首帧闪出控件）。
  //    首次加载恒为 `'all'`（切产品的复位在渲染期同步做），所以「all 代成功且 ≥1 行」≡「该产品至少有一个项目」。
  const [seenRows, setSeenRows] = useState<{ productId: string; seen: boolean }>({ productId, seen: false })
  const hasEverSeenRows = seenRows.productId === productId && seenRows.seen
  useEffect(() => {
    if (hasEverSeenRows || status !== 'all' || current === undefined || current.length === 0) return
    setSeenRows({ productId, seen: true })
  }, [hasEverSeenRows, status, current, productId])

  // ④ 控件可见 = `!首屏失败 && (见过行 || 产品项目数 > 0)`（design 判定式），且**首屏 loading 期间一律不出现**：
  //    首屏两态都还没有可信数据，`project_count` 可能先于列表到达，不拦就会在 loading 态闪出控件。
  const filtersVisible =
    !firstLoadError && !firstLoadLoading && (hasEverSeenRows || (productInfo.projectCount ?? 0) > 0)

  // ⑦ 详情抽屉的打开状态只存 id（不复制行对象）：每次渲染按 id 从**当前代** `current` 反查，
  //    自动刷新 / 筛选换代后抽屉内容跟着当前行走，不会停在打开那一刻的快照上；
  //    行不在当前代（被筛掉 / 被关闭 / 刷新掉）→ 反查为 undefined → 抽屉自动关闭，不显示幽灵数据。
  const [detailId, setDetailId] = useState<string | null>(null)
  const detailProject = detailId === null ? undefined : current?.find((project) => project.project_id === detailId)

  // 关闭 / 重开（design/flows.md F5）：弹窗状态 + 提交中 + 失败错误条。
  // 写请求与 refresh('write') 都放在这里——两套状态会让「成功后就地刷新」漏掉一条分支。
  const [flow, setFlow] = useState<WriteFlow | null>(null)
  const [busy, setBusy] = useState(false)
  const [writeError, setWriteError] = useState<ProjectWriteError | null>(null)

  /**
   * 关闭项目：`force=false` 时先发不带 force 的请求（方案 §7.5 D4 选定 A，**不做**预检）。
   * - 成功 → 关弹窗 + `refresh('write')`，行状态与按钮跟着当前行数据切换（不手动刷新）；
   * - 409 且 message 能确认「有未完成任务」→ **同一个弹窗**切第二态（不写数据、不刷列表）；
   * - 其它错误（404 / 5xx / 断网 / 另一种 409）→ 关弹窗 + 错误条（可重试）。
   */
  const attemptClose = (project: Project, force: boolean) => {
    setBusy(true)
    setWriteError(null)
    api.closeProject(project.project_id, force).then(
      () => {
        setBusy(false)
        setFlow(null)
        refresh('write')
      },
      (err: unknown) => {
        setBusy(false)
        if (!force && isUnfinishedConflict(err)) {
          // 服务端给的是真值：能解析出 N 就写进文案，解析不到降级为「仍有未完成任务」（仍是同一弹窗）
          setFlow({
            kind: 'close',
            project,
            stage: { phase: 'force', unfinished: unfinishedCountOf(messageOf(err)), detail: httpDetail(err) },
          })
          return
        }
        setFlow(null)
        setWriteError(toWriteError('close', project, err))
      },
    )
  }

  /** 重开项目：一次确认、一次请求；成功后同样 `refresh('write')`（不复活已取消任务，后端只回项目状态） */
  const attemptReopen = (project: Project) => {
    setBusy(true)
    setWriteError(null)
    api.reopenProject(project.project_id).then(
      () => {
        setBusy(false)
        setFlow(null)
        refresh('write')
      },
      (err: unknown) => {
        setBusy(false)
        setFlow(null)
        setWriteError(toWriteError('reopen', project, err))
      },
    )
  }

  /** 弹窗确认：第一态发不带 force 的关闭；第二态才以 force=1 重发 */
  const confirmWrite = () => {
    if (flow === null) return
    if (flow.kind === 'reopen') {
      attemptReopen(flow.project)
      return
    }
    attemptClose(flow.project, flow.stage.phase === 'force')
  }

  /**
   * 错误条「重试」：按原动作重发。关闭**永远从「不带 force」开始**——
   * 重试时若项目又有了新的未完成任务，会重新走一次二次确认，不盲 force。
   */
  const retryWrite = () => {
    if (writeError === null) return
    if (writeError.kind === 'reopen') attemptReopen(writeError.project)
    else attemptClose(writeError.project, false)
  }

  const columns: Array<DataTableColumn<Project>> = [
    {
      key: 'seq_no',
      header: '序号',
      // design/refs 的序号是弱化的「#1」：ui/DataTable 会给首列加粗，这里按 ref 压回去
      render: (project) => <span className="pj-seq">#{project.seq_no}</span>,
    },
    {
      key: 'name',
      header: '项目',
      render: (project) => (
        <div className="pj-name">
          <span className="pj-name__title">{project.name}</span>
          <span className="pj-name__id">{project.project_id}</span>
        </div>
      ),
    },
    {
      key: 'status',
      header: '状态',
      // 状态是数据不是控件：圆点 + 中文文案（design/components.md §ui/StatusPill 反例=只用颜色）
      render: (project) =>
        project.status === 'open' ? (
          <StatusPill status="in_progress" label="进行中" />
        ) : (
          <StatusPill status="cancelled" label="已关闭" />
        ),
    },
    {
      key: 'update_time',
      header: '更新时间',
      render: (project) => <span className="pj-time">{formatProjectTime(project.update_time)}</span>,
    },
    {
      key: 'actions',
      header: '操作',
      // 不设 actions: true —— design/refs 移动端卡片里「操作」也带字段名（data-label）
      //
      // 入口位置（方案 §7.4 D3 选定 A）：关闭 / 重开**只**放在项目列表行操作列，
      // 看板页头不放（AC5：同一屏主操作只出现一次）。两枚按钮的视觉权重：
      // 「打开看板」= default 描边（行主操作），「关闭 / 重开」= ghost（弱化，不抢主操作）。
      render: (project) => (
        // 「一次点击一个结果」（docs/tech §7.8-1）：行内按钮必须阻断冒泡，否则 tr 的 onRowClick
        // 会同时把抽屉打开——点行内任一动作都只做那一件事，不开抽屉。
        <div className="pj-row-actions">
          <Button
            variant="default"
            size="sm"
            onClick={(event) => {
              event.stopPropagation()
              navigate(`/projects/${project.project_id}/tasks`)
            }}
          >
            打开看板
          </Button>
          {project.status === 'open' ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={(event) => {
                event.stopPropagation()
                setFlow({ kind: 'close', project, stage: { phase: 'confirm' } })
              }}
            >
              关闭
            </Button>
          ) : (
            <Button
              variant="ghost"
              size="sm"
              onClick={(event) => {
                event.stopPropagation()
                setFlow({ kind: 'reopen', project })
              }}
            >
              重开
            </Button>
          )}
        </div>
      ),
    },
  ]

  return (
    <section className="pj-page">
      <header className="pj-head">
        <div className="pj-head__main">
          <h1 className="pj-title">项目</h1>
          {/* ⑤ 数字 = 当前代行数；首屏 loading / error 不报数（§8.3）。
              产品下真无项目（all 且 0 行）沿用 design/refs 的「只有产品名」；筛后为空要报 0（AC「页头数字为 0」）。 */}
          <p className="pj-sub">{subtitleOf(firstLoadLoading, firstLoadError, owner, current?.length ?? 0, status !== 'all')}</p>
        </div>
        {/* ④ 筛选控件与「新建项目」同块：visible 判据见 filtersVisible（首屏两态 / 产品下 0 个项目都不出现）。
            「新建项目」原先只在有行时出现，现在跟着控件一起出现——筛后为空时仍能创建。 */}
        {filtersVisible ? (
          <div className="pj-actions">
            <SelectField
              className="pj-filter"
              label="按状态筛选"
              data={STATUS_FILTERS}
              value={status}
              onChange={(value) => setStatus(asStatusFilter(value))}
            />
            <Button variant="primary">新建项目</Button>
          </div>
        ) : null}
      </header>

      {/* 关闭 / 重开失败（方案 §9.1）：标题与说明是中文，服务端英文原文只进 detail；
          列表保留上次数据，不动行状态；「重试」按原动作重发（关闭仍从不带 force 开始）。 */}
      {writeError === null ? null : (
        <ErrorBar
          title={writeErrorTitle(writeError.kind)}
          message="操作没有生效，可以重试；如果反复失败，可能是项目已被他人修改。"
          detail={writeError.detail}
          onRetry={retryWrite}
          retrying={busy}
        />
      )}

      {/* 列表失败：首屏失败只有错误条（无表体，§8.3）；已拿到过数据后失败则保留上一次结果 + 错误条（§8.2）。 */}
      {error === '' ? null : (
        <Alert tone="danger" actionLabel="重试" onAction={() => refresh()}>
          {error}（GET /v1/projects）
        </Alert>
      )}

      {/* ③ 表体四分支：首屏 error → 只有错误条；首屏 loading → 3 个 SkeletonCard；
          重拉中（换代键不匹配）→ 表体骨架行、控件保留；否则按当前代渲染（空态分层见 §8.3）。 */}
      {firstLoadError ? null : firstLoadLoading ? (
        <div className="pj-card pj-card--skeleton">
          {Array.from({ length: SKELETON_CARDS }, (_, index) => (
            <SkeletonCard key={index} lines={2} />
          ))}
        </div>
      ) : current === undefined ? (
        <div className="pj-card">
          <DataTable
            ariaLabel="项目列表"
            columns={columns}
            rows={EMPTY_PROJECTS}
            getRowKey={(project) => project.project_id}
            loading
            loadingRows={SKELETON_ROWS}
          />
        </div>
      ) : status === 'all' && current.length === 0 ? (
        // ⑥ 空态分层：`all` 且 0 行 = 产品下真的没有项目 → 既有空态 + 「新建项目」
        <EmptyState
          title="这个产品还没有项目"
          description="项目是任务的容器。创建项目后，看板会出现在项目详情里。"
          actionLabel="新建项目"
          onAction={CREATE_PROJECT_PLACEHOLDER}
        />
      ) : (
        <div className="pj-card">
          <DataTable
            ariaLabel="项目列表"
            columns={columns}
            rows={current}
            getRowKey={(project) => project.project_id}
            onRowClick={(project) => setDetailId(project.project_id)}
            // ⑥ 非 `all` 且 0 行 = 筛后为空 → 走表内 `empty` 槽；不加「清除筛选」按钮（§11 U5），
            // 用户靠仍然保留的页头控件切回「全部状态」。
            empty="没有符合筛选条件的项目"
          />
        </div>
      )}

      {/* ⑦ 只读详情抽屉：内容按 id 从**当前代**行派生（0 请求），筛选换代后不在当前代的行走失即自动关闭 */}
      <ProjectDetailSheet project={detailProject} onClose={() => setDetailId(null)} />

      {/* 关闭 / 重开确认：同一个 ConfirmModal 承接「第一态 → 409 第二态」（方案 §7.5 D4） */}
      <ProjectWriteModal flow={flow} busy={busy} onConfirm={confirmWrite} onCancel={() => setFlow(null)} />
    </section>
  )
}

/**
 * 页头说明。
 * - 首屏 loading / error：不报数（数据还不可信）；
 * - 有行：`产品名 · N 个项目`；
 * - 0 行且是筛后（`zeroCounted`）：`产品名 · 0 个项目`——筛后为空必须能看到「0」，否则会误读成加载中；
 * - 0 行且「全部状态」：只有产品名（design/refs 的 `empty` 形态，产品下真没有项目时也由空态承接）。
 */
function subtitleOf(
  firstLoadLoading: boolean,
  firstLoadError: boolean,
  owner: string,
  count: number,
  zeroCounted: boolean,
): string {
  if (firstLoadLoading) return `${owner} · 加载中…`
  if (firstLoadError) return owner
  if (count === 0) return zeroCounted ? `${owner} · 0 个项目` : owner
  return `${owner} · ${count} 个项目`
}
