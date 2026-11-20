// features/workers/WorkersPage —— 执行节点列表（`#/workers`）
//
// 规格对照：
//   design/screens/workers.md    页头（忙碌 / 空闲 / 过期计数 + 刷新）→ 表格（节点 · 状态 · 当前任务 · 心跳 · 操作）；
//                                五态 default / empty / loading / error / stale；<768 卡片式行（data-label 字段名）；
//                                stale 行 = 整行淡底（ui/DataTable 的 warn-row）+ 红色心跳徽标。
//   design/components.md         §ui/DataTable（warn-row 变体）、§ui/StatusPill（圆点 + 文案）、§ui/EmptyState、
//                                §ui/SkeletonCard、§ui/Alert、§ui/Button。
//   docs/tech/web-ui-redo-v0.1.md §7   数据源：GET /v1/workers（worker_id / state / current_task_id / last_seen）。
//
// 分层：只 import ui / api / hooks / lib，不直接 import Mantine（分层红线）；样式见 workers.css（只消费令牌）。
//
// 不做（design/screens/workers.md「不做」）：远程终止 worker、改 lease——属后端能力，页面不出现入口。
import { api } from '../../api/client'
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
 * 心跳过期阈值：3 个心跳周期（后端约定 30s 一次，见 /v1/workers 的 heartbeat_interval_ms）→ 90s。
 * 超过即判为过期：整行淡底 + 心跳换成红色徽标（design/screens/workers.md stale 态）。
 */
export const STALE_AFTER_MS = 90_000

/**
 * 「查看部署说明」入口按 design/refs 展示，但仓库里没有部署页路由（不在本卡范围），
 * 所以按钮不带副作用，见交付的「未覆盖项」。
 */
const DEPLOY_DOC_PLACEHOLDER = () => {}

export function WorkersPage() {
  const { data, error, refresh } = useAutoRefresh({
    intervalMs: REFRESH_INTERVALS.workers,
    load: () => api.listWorkers(),
  })

  const items = data?.items ?? EMPTY_WORKERS
  const loading = data === undefined && error === ''

  // 相对时间的基准：每次渲染取一次，自动刷新（15s）会带着它一起推进，不引入第二条计时器。
  const now = Date.now()
  const beats = items.map((worker) => heartbeatOf(worker.last_seen, now))
  const staleCount = beats.filter((beat) => beat.stale).length
  // 忙碌计数不含过期节点：过期节点在页头单独计数，否则「忙碌 + 空闲 + 过期」会大于表格行数。
  const busyCount = items.filter((worker, index) => worker.state === 'busy' && !beats[index].stale).length
  const idleCount = items.filter((worker, index) => worker.state !== 'busy' && !beats[index].stale).length

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
      key: 'current_task_id',
      header: '当前任务',
      render: (worker) =>
        worker.current_task_id === undefined || worker.current_task_id === null ? (
          <span className="wk-muted">—</span>
        ) : (
          <span className="wk-task">{String(worker.current_task_id)}</span>
        ),
    },
    {
      key: 'last_seen',
      header: '心跳',
      render: (worker) => {
        const beat = heartbeatOf(worker.last_seen, now)
        if (!beat.stale) {
          return (
            <span className="wk-beat" data-heartbeat="fresh">
              {beat.label}
            </span>
          )
        }
        // 过期：红色徽标 + 明写「心跳过期」——语义不只靠颜色（色盲用户与截图都能读出来）
        return (
          <span className="wk-beat" data-heartbeat="stale">
            <StatusPill status="failed" label={beat.label} />
            <span className="wk-beat__note">心跳过期</span>
          </span>
        )
      },
    },
    {
      key: 'actions',
      header: '操作',
      // 不设 actions: true —— design/refs 移动端卡片里「操作」也带字段名（data-label）。
      // 不跳转：/v1/workers 只给 current_task_id、不给 project_id，构造不出
      // `#/projects/:projectId/tasks/:taskId` 的真实路由（见本卡「未覆盖项」，不靠猜项目 id）。
      render: () => (
        <Button variant="default" size="sm" title="接口未提供项目上下文，暂不能直接打开任务详情">
          查看任务
        </Button>
      ),
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
            isWarnRow={(worker) => heartbeatOf(worker.last_seen, now).stale}
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

/** 心跳文案 + 是否过期（阈值见 STALE_AFTER_MS）。脏值不崩：给「—」并按不过期处理。 */
export function heartbeatOf(lastSeen: string, nowMs: number): { label: string; stale: boolean } {
  const parsed = Date.parse(lastSeen)
  if (Number.isNaN(parsed)) return { label: '—', stale: false }
  const elapsed = Math.max(0, nowMs - parsed)
  return { label: `${elapsedLabel(elapsed)} 前`, stale: elapsed > STALE_AFTER_MS }
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
