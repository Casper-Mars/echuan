// features/workers/WorkersPage —— 执行节点列表（`#/workers`）
//
// 规格对照：
//   design/screens/workers.md    页头（忙碌 / 空闲 / 过期计数 + 刷新）→ 表格（节点 · 状态 · 当前任务 · 心跳 · 操作）；
//                                五态 default / empty / loading / error / stale；<768 卡片式行（data-label 字段名）；
//                                stale 行 = 整行淡底（ui/DataTable 的 warn-row）+ 红色心跳徽标。
//   design/components.md         §ui/DataTable（warn-row 变体）、§ui/StatusPill（圆点 + 文案）、§ui/EmptyState、
//                                §ui/SkeletonCard、§ui/Alert、§ui/Button。
//   docs/tech/web-ui-redo-v0.1.md §7   数据源：GET /v1/workers（worker_id / state / current_work_item_id / last_seen / presence）。
//   字段口径以 internal/api/v3_handlers.go 的 v3WorkerResponse 为准（真正挂载的是 V3 路由）：
//   当前任务给的是 `t_...` 业务标识（string|null）。详情路由要 project_id，而 worker 行没有，
//   所以「查看任务」点一次先反查 GET /v1/tasks?id=<tid>，见 openWorkerTask。
//   过期判定只认服务端 `presence`（`active` / `offline`，由 `last_seen + worker_ttl` 推导），见 isStale。
//
// 分层：只 import ui / api / hooks / lib，不直接 import Mantine（分层红线）；样式见 workers.css（只消费令牌）。
//
// 不做（design/screens/workers.md「不做」）：远程终止 worker、改 lease——属后端能力，页面不出现入口。
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError, api } from '../../api/client'
import type { Worker } from '../../api/types'
import { useAutoRefresh } from '../../hooks/useAutoRefresh'
import { REFRESH_INTERVALS } from '../../lib/refresh'
import { Alert, Button, DataTable, EmptyState, SkeletonCard, StatusPill } from '../../ui'
import type { DataTableColumn } from '../../ui'
import './workers.css'

/** 稳定的空数组：避免每渲染新建引用，也避免把空态误判成 loading。 */
const EMPTY_WORKERS: Worker[] = []

/** 骨架卡片数：design/screens/workers.md loading 态「3 个 SkeletonCard」 */
const SKELETON_CARDS = 3

/**
 * 心跳过期判定：**只认服务端 `presence`**（`GET /v1/workers` 每项都返回，服务端按
 * `last_seen + worker_ttl` 推导，缺省 `worker_ttl=45s`）。
 *
 * 以前前端用 `last_seen` + 90s 常量自己算过期，与 `worker_ttl` 各说一套（R1）；现在服务端字段是唯一口径，
 * 前端不再保留阈值兜底：`presence` 缺失或取值非法（不是 `offline`）→ 不淡底、不红徽标、不计入过期。
 * 心跳列里的相对时长（`4m12s 前`）只是文案，不参与判定。
 */
export function isStale(worker: Worker): boolean {
  return worker.presence === 'offline'
}

/**
 * 「查看任务」的定位方式（issue #89 C2）：/v1/workers 只给 `current_work_item_id`（`t_...`），
 * 没有 project_id，`#/projects/:projectId/tasks/:taskId` 拼不出来。所以点一次先反查
 * `GET /v1/tasks?id=<tid>` 拿 project_id，再跳详情——一次点击恰好一次反查，后端契约不动。
 *
 * 反查失败（404 / 缺 project_id / 网络异常）或节点本来就没有当前任务时，按钮回到禁用 +
 * 行内可见原因，页面不跳走：宁可点不动，也不出现「点了没反应」或猜一个假 project_id。
 */
const NO_TASK_NOTE = '当前没有任务'

/** 反查在飞时的行内提示（同时按钮进入 loading，避免重复点击叠加请求）。 */
const LOOKUP_LOADING_NOTE = '正在定位任务…'

/** 一次「查看任务」反查的状态；只记当前这一次点击，失败原因随行内说明一起展示。 */
type TaskLookup =
  | { kind: 'loading' }
  | { kind: 'failed'; reason: string }

/**
 * 反查结果归属的行标识 =（`worker_id`，`current_work_item_id`），不是只按 `worker_id`（PR #100 review）。
 * 理由：/v1/workers 会随自动刷新变化，同一节点可能从任务 A 换到任务 B（或置空）。若只按节点关联，
 * A 的 `failed` 会被 B 继承 → 「查看任务」永久禁用，刷新也回不来。
 */
interface LookupEntry {
  workerId: string
  workItemId: string
  state: TaskLookup
}

/** 反查失败 → 行内可见原因（说清为什么点不动，而不是只说「失败」）。 */
function lookupFailureReason(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 404) return '反查任务失败：任务不存在或已删除（HTTP 404）'
    return `反查任务失败（HTTP ${err.status}）`
  }
  return '反查任务失败：网络异常，请稍后重试'
}

/**
 * 「查看部署说明」入口按 design/refs 展示，但仓库里没有部署页路由（不在本卡范围），
 * 所以按钮不带副作用，见交付的「未覆盖项」。
 */
const DEPLOY_DOC_PLACEHOLDER = () => {}

export function WorkersPage() {
  const navigate = useNavigate()
  const { data, error, refresh } = useAutoRefresh({
    intervalMs: REFRESH_INTERVALS.workers,
    load: () => api.listWorkers(),
  })

  // 「查看任务」的反查状态：只跟「这一行 + 这一行的当前任务」绑定（同一时刻只允许一个在飞，
  // 点别的行会顶掉上一个）；当前任务一变即作废，见下面的清理 effect。
  const [lookup, setLookup] = useState<LookupEntry | null>(null)

  /**
   * 反查的「换代」计数器（与 features/task-detail/use-task-detail 的 genRef、hooks/useAutoRefresh
   * 的 gen 同一套语义）：每次点击 +1，组件卸载时也 +1。响应回来时若代号已变，整笔丢弃——
   * 既不 setState（已卸载组件不再更新），也不 navigate。
   *
   * 一个计数器同时覆盖两件事：
   *   * 卸载：cleanup 里 +1 → 在飞响应不再是「最新」的（用户切到「产品」等页面不会被迟到跳转拽回来）；
   *   * 被新点击顶掉：新一次点击 +1 → 上一次仍在飞的响应作废，不会用旧行的 project_id 抢先跳转。
   */
  const lookupGenRef = useRef(0)

  useEffect(() => {
    return () => {
      // 卸载：让所有在飞反查失效（迟到响应不许 navigate / setState）
      lookupGenRef.current += 1
    }
  }, [])

  const items = data?.items ?? EMPTY_WORKERS
  const loading = data === undefined && error === ''

  /**
   * 当前任务变了 → 旧反查整笔作废（PR #100 review 的修复点之一）。
   *
   * 反查结果只对 `(worker_id, current_work_item_id)` 这一对成立。自动刷新（15s）/ 手动刷新会把
   * 同一节点的当前任务从 A 换成 B，或置空；A 的 `failed` 若继续挂着，B 会继承失败态 →
   * 「查看任务」永久禁用，刷新列表也回不来。这里在数据变化时比对一次：
   *   * 对得上 → 保留（同一任务重试失败，原因仍然成立）；
   *   * 对不上（换了任务 / 置空 / 节点消失）→ 清空状态，并把换代号 +1 让仍在飞的反查也一并失效，
   *     避免迟到的 A 响应把 B 行带进跳转。
   */
  useEffect(() => {
    if (lookup === null) return
    const row = items.find((worker) => worker.worker_id === lookup.workerId)
    const currentWorkItemId = row?.current_work_item_id ?? null
    if (currentWorkItemId === lookup.workItemId) return
    lookupGenRef.current += 1
    setLookup(null)
  }, [items, lookup])

  // 相对时间的基准：每次渲染取一次，自动刷新（15s）会带着它一起推进，不引入第二条计时器。
  const now = Date.now()
  const staleCount = items.filter(isStale).length
  // 忙碌计数不含过期节点：过期节点在页头单独计数，否则「忙碌 + 空闲 + 过期」会大于表格行数。
  const busyCount = items.filter((worker) => worker.state === 'busy' && !isStale(worker)).length
  const idleCount = items.filter((worker) => worker.state !== 'busy' && !isStale(worker)).length

  /**
   * 点「查看任务」：反查一次任务拿 project_id，然后跳详情抽屉。
   *
   * 一次点击只发一次反查（loading 期间按钮不可点）；拿到 project_id 才导航，
   * 缺 project_id 或反查失败都落到「禁用 + 行内原因」，不猜 id、不跳空白页。
   * 响应回来时先比代号：已卸载、已被新点击顶掉、或该节点的当前任务已变 → 整笔丢弃（不 setState、不 navigate）。
   */
  const openWorkerTask = async (worker: Worker) => {
    const workItemId = worker.current_work_item_id
    if (workItemId === undefined || workItemId === null || workItemId === '') return
    lookupGenRef.current += 1
    const gen = lookupGenRef.current
    setLookup({ workerId: worker.worker_id, workItemId, state: { kind: 'loading' } })
    try {
      const task = await api.getTask(workItemId)
      if (gen !== lookupGenRef.current) return // 期间已卸载 / 已换行 / 已换任务：迟到响应作废
      if (typeof task.project_id !== 'string' || task.project_id === '') {
        setLookup({
          workerId: worker.worker_id,
          workItemId,
          state: { kind: 'failed', reason: '反查任务失败：结果里没有项目上下文（project_id 缺失）' },
        })
        return
      }
      setLookup(null)
      navigate(`/projects/${task.project_id}/tasks/${task.task_id}`)
    } catch (err) {
      if (gen !== lookupGenRef.current) return // 同上：迟到失败也不许改已离开页面的状态
      setLookup({ workerId: worker.worker_id, workItemId, state: { kind: 'failed', reason: lookupFailureReason(err) } })
    }
  }

  const columns: Array<DataTableColumn<Worker>> = [
    {
      key: 'worker_id',
      header: '节点',
      render: (worker) => <span className="wk-id">{worker.worker_id}</span>,
    },
    {
      key: 'state',
      header: '状态',
      // 状态是数据不是控件：圆点 + 中文文案（design/components.md §ui/StatusPill 反例 = 只用颜色）
      render: (worker) =>
        worker.state === 'busy' ? (
          <StatusPill status="in_progress" label="忙碌" />
        ) : (
          <StatusPill status="done" label="空闲" />
        ),
    },
    {
      key: 'current_work_item_id',
      header: '当前任务',
      // 原样显示后端的任务业务标识（`t_...`）：不转数字、不做计数。空闲节点后端给 null → 占位「—」。
      render: (worker) => {
        const workItemId = worker.current_work_item_id
        if (workItemId === undefined || workItemId === null || workItemId === '') {
          return <span className="wk-muted">—</span>
        }
        return <span className="wk-task">{workItemId}</span>
      },
    },
    {
      key: 'last_seen',
      header: '心跳',
      render: (worker) => {
        const label = heartbeatLabel(worker.last_seen, now)
        if (!isStale(worker)) {
          return (
            <span className="wk-beat" data-heartbeat="fresh">
              {label}
            </span>
          )
        }
        // 过期（服务端 presence=offline）：红色徽标 + 明写「心跳过期」——语义不只靠颜色（色盲用户与截图都能读出来）
        return (
          <span className="wk-beat" data-heartbeat="stale">
            <StatusPill status="failed" label={label} />
            <span className="wk-beat__note">心跳过期</span>
          </span>
        )
      },
    },
    {
      key: 'actions',
      header: '操作',
      // 不设 actions: true —— design/refs 移动端卡片里「操作」也带字段名（data-label）。
      // 按钮语义（issue #89 C2）：有当前任务 → 可点，点一次反查 `GET /v1/tasks?id=<tid>` 拿
      // project_id 后跳详情；反查在飞 → loading 且不可重复点；反查失败 / 无当前任务 → 禁用 +
      // 行内可见原因（title 同一句，hover 与读屏都能读到），页面不跳走。
      // 反查状态按 (worker_id, current_work_item_id) 匹配（PR #100 review）：任务换了就取不到旧状态，
      // 旧任务的失败不会粘到新任务上。
      render: (worker) => {
        const workItemId = worker.current_work_item_id
        const hasTask = typeof workItemId === 'string' && workItemId !== ''
        const current =
          lookup !== null && lookup.workerId === worker.worker_id && lookup.workItemId === workItemId
            ? lookup.state
            : null
        const busy = current?.kind === 'loading'
        const disabled = !hasTask || busy || current?.kind === 'failed'
        const note = !hasTask
          ? NO_TASK_NOTE
          : current?.kind === 'failed'
            ? current.reason
            : busy
              ? LOOKUP_LOADING_NOTE
              : ''
        return (
          <span className="wk-actions-cell">
            <Button
              variant="default"
              size="sm"
              disabled={disabled}
              loading={busy}
              title={note === '' ? undefined : note}
              onClick={() => void openWorkerTask(worker)}
            >
              查看任务
            </Button>
            {note === '' ? null : <span className="wk-actions-note">{note}</span>}
          </span>
        )
      },
    },
  ]

  return (
    <section className="wk-page">
      <header className="wk-head">
        <div className="wk-head__main">
          <h1 className="wk-title">执行节点</h1>
          <p className="wk-sub">{subtitleOf(loading, error !== '', busyCount, idleCount, staleCount)}</p>
        </div>
        <div className="wk-actions">
          <Button variant="ghost" onClick={() => refresh()}>
            刷新
          </Button>
        </div>
      </header>

      {error !== '' ? (
        <Alert tone="danger" actionLabel="重试" onAction={() => refresh()}>
          {error}（GET /v1/workers）
        </Alert>
      ) : loading ? (
        <div className="wk-card wk-card--skeleton">
          {Array.from({ length: SKELETON_CARDS }, (_, index) => (
            <SkeletonCard key={index} lines={2} />
          ))}
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          title="还没有执行节点"
          description="worker 上线并心跳后会出现在这里。检查 worker 进程与 relay 连接。"
          actionLabel="查看部署说明"
          onAction={DEPLOY_DOC_PLACEHOLDER}
        />
      ) : (
        <div className="wk-card">
          <DataTable
            ariaLabel="执行节点列表"
            columns={columns}
            rows={items}
            getRowKey={(worker) => worker.worker_id}
            // 心跳过期行：整行淡底（ui/DataTable 的 warn-row 变体，行上有 data-warn）
            isWarnRow={isStale}
          />
        </div>
      )}
    </section>
  )
}

/** 页头说明：default =「N 个忙碌 · M 个空闲」；有过期节点再追加一段；loading / error 不报数。 */
function subtitleOf(loading: boolean, failed: boolean, busy: number, idle: number, stale: number): string {
  if (loading) return '加载中…'
  if (failed) return '数据加载失败'
  const base = `${busy} 个忙碌 · ${idle} 个空闲`
  return stale === 0 ? base : `${base} · ${stale} 个心跳过期`
}

/** 心跳文案（相对时长）；脏值不崩：给「—」。是否过期只看 {@link isStale}，与这里无关。 */
export function heartbeatLabel(lastSeen: string, nowMs: number): string {
  const parsed = Date.parse(lastSeen)
  if (Number.isNaN(parsed)) return '—'
  const elapsed = Math.max(0, nowMs - parsed)
  return `${elapsedLabel(elapsed)} 前`
}

/** 时长 → 「3s」/「21s」/「4m 12s」/「2h 5m」（design/refs 的心跳列写法）。 */
function elapsedLabel(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000)
  if (totalSeconds < 60) return `${totalSeconds}s`
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  if (minutes < 60) return seconds === 0 ? `${minutes}m` : `${minutes}m ${seconds}s`
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}
