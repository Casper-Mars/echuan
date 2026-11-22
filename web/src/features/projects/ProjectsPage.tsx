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
import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api } from '../../api/client'
import type { Project } from '../../api/types'
import { useAutoRefresh } from '../../hooks/useAutoRefresh'
import { REFRESH_INTERVALS } from '../../lib/refresh'
import { Alert, Button, DataTable, EmptyState, SkeletonCard, StatusPill } from '../../ui'
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

/**
 * 资产库 pin（只读徽标）：本仓 `web/` 消费的共享库版本，来源是仓库根 `.agent/assets.yaml` 的
 * `library: "0.1.1"`。本屏只做「看得见版本」的只读落点——不是控件（span、无 onClick / tabIndex /
 * 导航语义），也不随列表四态消失；升级 pin 时同步改这一处（构建期注入 YAML 超出本卡范围）。
 */
const ASSET_LIBRARY_PIN = '资产库 0.1.1'

/**
 * 「新建项目」入口按 design/refs 展示，但本卡数据源只有三个 GET、不做写操作，
 * 所以按钮不带副作用（不发 POST、不弹表单）。创建流程不在本卡范围，见交付的「未覆盖项」。
 */
const CREATE_PROJECT_PLACEHOLDER = () => {}

export function ProjectsPage() {
  const { productId = '' } = useParams()
  const navigate = useNavigate()

  const { data, error, refresh } = useAutoRefresh({
    intervalMs: REFRESH_INTERVALS.projects,
    scope: productId,
    load: () => api.listProjects(productId),
  })

  // 产品名只服务于页头文案（design/refs：「驿传 Echuan · 3 个项目」）。
  // 它不是本页数据源，取不到就回退显示 product_id，不把整页打成错误态。
  const [productName, setProductName] = useState('')
  useEffect(() => {
    let alive = true
    setProductName('')
    api
      .getProduct(productId)
      .then((product) => {
        if (alive) setProductName(product.name)
      })
      .catch(() => {
        if (alive) setProductName('')
      })
    return () => {
      alive = false
    }
  }, [productId])

  const items = data?.items ?? EMPTY_PROJECTS
  const loading = data === undefined && error === ''
  const owner = productName === '' ? productId : productName

  // 详情抽屉的打开状态只存 id（不复制行对象）：每次渲染按 id 从**当前** items 反查，
  // 自动刷新 / 数据变化后抽屉内容跟着当前行走，不会停在打开那一刻的快照上；
  // 行不存在（被关闭后被筛掉 / 刷新掉）→ 反查为 undefined → 抽屉自动关闭，不显示幽灵数据。
  const [detailId, setDetailId] = useState<string | null>(null)
  const detailProject = detailId === null ? undefined : items.find((project) => project.project_id === detailId)

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
          <div className="pj-head__title">
            <h1 className="pj-title">项目</h1>
            {/* 放在 __main 里而非 __actions：徽标与「新建项目」的显隐条件解耦，四态都在页头。 */}
            <span className="pj-asset-pin">{ASSET_LIBRARY_PIN}</span>
          </div>
          <p className="pj-sub">{subtitleOf(loading, error !== '', owner, items.length)}</p>
        </div>
        {/* 加载中 / 错误态页头不出现新建（design/refs 三种状态一致） */}
        {!loading && error === '' && items.length > 0 ? (
          <div className="pj-actions">
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

      {error !== '' ? (
        <Alert tone="danger" actionLabel="重试" onAction={() => refresh()}>
          {error}（GET /v1/projects）
        </Alert>
      ) : loading ? (
        <div className="pj-card pj-card--skeleton">
          {Array.from({ length: SKELETON_CARDS }, (_, index) => (
            <SkeletonCard key={index} lines={2} />
          ))}
        </div>
      ) : items.length === 0 ? (
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
            rows={items}
            getRowKey={(project) => project.project_id}
            onRowClick={(project) => setDetailId(project.project_id)}
          />
        </div>
      )}

      {/* 只读详情抽屉：内容按 id 从当前 items 派生（0 请求），列表刷新后仍在的行走新数据 */}
      <ProjectDetailSheet project={detailProject} onClose={() => setDetailId(null)} />

      {/* 关闭 / 重开确认：同一个 ConfirmModal 承接「第一态 → 409 第二态」（方案 §7.5 D4） */}
      <ProjectWriteModal flow={flow} busy={busy} onConfirm={confirmWrite} onCancel={() => setFlow(null)} />
    </section>
  )
}

/** 页头说明：default =「产品名 · N 个项目」；empty = 只有产品名；loading / error 不报数。 */
function subtitleOf(loading: boolean, failed: boolean, owner: string, count: number): string {
  if (loading) return `${owner} · 加载中…`
  if (failed) return owner
  if (count === 0) return owner
  return `${owner} · ${count} 个项目`
}
