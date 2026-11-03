import { useEffect, useMemo, useState } from 'react'
import { listWorkItemsByFilter, listWorkers } from '../api/client'
import type { WorkItem, WorkItemKind, Worker } from '../api/types'
import {
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { usePolling } from '../hooks/usePolling'
import { WORK_ITEM_KINDS, formatDuration, workItemKindLabel } from '../lib/format'

/** 工作项接口单页上限 200。 */
const PAGE_LIMIT = 200
/** 分页拉全的防御上限：异常 next_cursor 最多翻 10 页。 */
const MAX_PAGES = 10
/** worker 单页上限；队列页只统计首屏 200 个执行节点。 */
const WORKER_LIMIT = 200

const KIND_ORDER: WorkItemKind[] = [...WORK_ITEM_KINDS]

interface QueueSnapshot {
  items: WorkItem[]
  workers: Worker[]
  /** 是否因 10 页防护而截断。 */
  truncated: boolean
}

interface KindStat {
  queued: number
  leased: number
  onlineWorkers: number
  /** 该 kind 所有 leased 中最早的 lease.until（毫秒时间戳）。 */
  earliestLease: number | null
}

/** 跨项目拉取排队/开发中的工作项，并同时拉执行节点。 */
async function fetchQueue(signal: AbortSignal): Promise<QueueSnapshot> {
  const [pending, workerPage] = await Promise.all([
    (async () => {
      const items: WorkItem[] = []
      let cursor: string | undefined
      for (let page = 0; page < MAX_PAGES; page += 1) {
        const result = await listWorkItemsByFilter({
          states: ['queued', 'leased'],
          limit: PAGE_LIMIT,
          cursor,
          signal,
        })
        items.push(...result.items)
        cursor = result.next_cursor
        if (!cursor) return { items, truncated: false }
      }
      return { items, truncated: true }
    })(),
    listWorkers({ limit: WORKER_LIMIT, signal }),
  ])

  return { items: pending.items, workers: workerPage.items, truncated: pending.truncated }
}

function emptyStat(): KindStat {
  return { queued: 0, leased: 0, onlineWorkers: 0, earliestLease: null }
}

/** 每秒本地 tick：租约剩余由时间派生，不需要额外请求。 */
function useSecondTick(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  return now
}

function leaseText(until: number | null, now: number): { text: string; overdue: boolean } {
  if (until === null || Number.isNaN(until)) return { text: '—', overdue: false }
  const remaining = until - now
  if (remaining <= 0) return { text: '已到期', overdue: true }
  return { text: `最短 ${formatDuration(remaining)}`, overdue: false }
}

/**
 * 派单队列：按 kind 固定四张卡片展示排队数、开发中数、在线 worker 数与租约剩余。
 *
 * - 跨项目拉 state=queued,leased（分页拉全，最多 10 页防护），5s 轮询；
 * - 在线 worker 以服务端 presence=active 为准；
 * - 某 kind 无在线 worker 且仍有排队/开发中时高亮，提示队列会静默卡住。
 */
export function QueuePage() {
  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(
    fetchQueue,
    [],
  )
  const now = useSecondTick()

  const stats = useMemo(() => {
    const map = new Map<WorkItemKind, KindStat>()
    for (const kind of KIND_ORDER) map.set(kind, emptyStat())
    if (!data) return map

    for (const item of data.items) {
      const stat = map.get(item.kind)
      if (!stat) continue
      if (item.state === 'queued') stat.queued += 1
      if (item.state === 'leased') {
        stat.leased += 1
        if (item.lease?.until) {
          const until = Date.parse(item.lease.until)
          if (!Number.isNaN(until) && (stat.earliestLease === null || until < stat.earliestLease)) {
            stat.earliestLease = until
          }
        }
      }
    }

    for (const worker of data.workers) {
      if (worker.presence !== 'active') continue
      for (const kind of worker.kinds) {
        const stat = map.get(kind)
        if (stat) stat.onlineWorkers += 1
      }
    }
    return map
  }, [data])

  const totals = useMemo(() => {
    let queued = 0
    let leased = 0
    for (const kind of KIND_ORDER) {
      const stat = stats.get(kind)
      if (!stat) continue
      queued += stat.queued
      leased += stat.leased
    }
    return { queued, leased }
  }, [stats])

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>派单队列</h1>
          <p className="muted">
            {data
              ? `共 ${totals.queued} 个排队、${totals.leased} 个开发中`
              : '按类型查看排队、开发中与租约剩余'}
          </p>
        </div>
        <div className="page-head-actions">
          <RefreshBar
            lastUpdated={lastUpdated}
            refreshing={refreshing}
            error={error}
            onRefresh={refresh}
          />
        </div>
      </header>

      <ErrorNotice error={error} onRetry={refresh} />

      {!data && loading && <LoadingState label="正在加载派单队列…" />}
      {!data && !loading && error && (
        <EmptyState title="派单队列加载失败" hint="页面会自动重试；也可以点击上方「重试」。" />
      )}

      {data && data.truncated && (
        <div className="queue-warning" role="status">
          已翻到第 {MAX_PAGES} 页，队列可能还有更多工作项；当前统计仅覆盖已拉取部分。
        </div>
      )}

      {data && (
        <div className="queue-grid">
          {KIND_ORDER.map((kind) => {
            const stat = stats.get(kind) ?? emptyStat()
            const stalled = stat.onlineWorkers === 0 && stat.queued + stat.leased > 0
            const lease = leaseText(stat.earliestLease, now)
            return (
              <article
                key={kind}
                className={stalled ? 'queue-card is-stalled' : 'queue-card'}
                aria-label={`${workItemKindLabel(kind)}队列`}
              >
                <header className="queue-card-head">
                  <span className={`badge kind-${kind}`}>{workItemKindLabel(kind)}</span>
                  {stalled && <span className="queue-badge-alert">无在线 worker</span>}
                </header>

                <dl className="queue-stats">
                  <div className="queue-stat">
                    <dt>排队数</dt>
                    <dd>{stat.queued}</dd>
                  </div>
                  <div className="queue-stat">
                    <dt>开发中数</dt>
                    <dd>{stat.leased}</dd>
                  </div>
                  <div className="queue-stat">
                    <dt>在线 worker</dt>
                    <dd
                      className={
                        stat.onlineWorkers === 0 && stat.queued + stat.leased > 0
                          ? 'is-zero'
                          : undefined
                      }
                    >
                      {stat.onlineWorkers}
                    </dd>
                  </div>
                  <div className="queue-stat queue-stat-lease">
                    <dt>租约剩余</dt>
                    <dd className={lease.overdue ? 'is-overdue' : undefined}>{lease.text}</dd>
                  </div>
                </dl>

                {stalled && (
                  <p className="queue-alert">该 kind 无在线 worker，队列会静默卡住</p>
                )}
              </article>
            )
          })}
        </div>
      )}
    </section>
  )
}
