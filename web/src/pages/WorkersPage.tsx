import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { listWorkers } from '../api/client'
import type { Worker } from '../api/types'
import { useDrawer } from '../components/DrawerContext'
import {
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { TaskDetailDrawer } from '../components/TaskDetailDrawer'
import { usePolling } from '../hooks/usePolling'
import { formatDateTime, formatRelative } from '../lib/format'

/** 服务端单页上限 200；超过时按 next_cursor 继续翻页。 */
const WORKER_PAGE_LIMIT = 200
/** 防御性总上限，避免异常 next_cursor 造成无限翻页。 */
const WORKER_TOTAL_LIMIT = 1000

/** 拉全量 worker：在线/离线计数需要完整列表，不能只看第一页。 */
async function fetchWorkers(signal: AbortSignal): Promise<Worker[]> {
  const workers: Worker[] = []
  let cursor: string | undefined
  do {
    const page = await listWorkers({ limit: WORKER_PAGE_LIMIT, cursor, signal })
    workers.push(...page.items)
    cursor = page.next_cursor
  } while (cursor && workers.length < WORKER_TOTAL_LIMIT)
  return workers
}

/** 在线优先，其次 last_seen 倒序；同刻按 id 保证稳定。 */
function sortWorkers(workers: Worker[]): Worker[] {
  return [...workers].sort((a, b) => {
    const aOffline = a.presence === 'offline' ? 1 : 0
    const bOffline = b.presence === 'offline' ? 1 : 0
    if (aOffline !== bOffline) return aOffline - bOffline
    const delta = Date.parse(b.last_seen) - Date.parse(a.last_seen)
    if (delta !== 0) return delta
    return a.worker_id.localeCompare(b.worker_id)
  })
}

function workerState(worker: Worker): { label: string; className: string } {
  if (worker.presence === 'offline') {
    return { label: '离线', className: 'worker-state-offline' }
  }
  // 占用以 current_task_id 为准；state 只是 worker 的自报声明。
  if (worker.current_task_id || worker.state === 'busy') {
    return { label: '在线·执行中', className: 'worker-state-busy' }
  }
  return { label: '在线·空闲', className: 'worker-state-idle' }
}

function metadataEntries(metadata: Record<string, unknown>): [string, unknown][] {
  if (!metadata || typeof metadata !== 'object') return []
  return Object.entries(metadata).sort(([a], [b]) => a.localeCompare(b))
}

function renderMetadataValue(value: unknown): string {
  if (value === null) return 'null'
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

/** 非安全上下文 / 老浏览器下 navigator.clipboard 不可用时的兜底。 */
function fallbackCopy(text: string): boolean {
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '-1000px'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.select()
  try {
    return document.execCommand('copy')
  } finally {
    document.body.removeChild(area)
  }
}

/**
 * Worker 列表：在线状态、当前任务、自报元数据与心跳时间。
 * 5s 轮询复用 usePolling；在线/离线完全以服务端 presence 为准。
 */
export function WorkersPage() {
  const openDrawer = useDrawer()
  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(
    fetchWorkers,
    [],
  )
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(
    () => () => {
      if (copyTimerRef.current) clearTimeout(copyTimerRef.current)
    },
    [],
  )

  const workers = useMemo(() => sortWorkers(data ?? []), [data])
  const online = workers.filter((worker) => worker.presence === 'active').length

  const copyWorkerId = useCallback(async (workerId: string) => {
    let copied = false
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(workerId)
        copied = true
      }
    } catch {
      copied = false
    }
    if (!copied) {
      try {
        copied = fallbackCopy(workerId)
      } catch {
        copied = false
      }
    }
    if (!copied) return
    setCopiedId(workerId)
    if (copyTimerRef.current) clearTimeout(copyTimerRef.current)
    copyTimerRef.current = setTimeout(() => setCopiedId(null), 1500)
  }, [])

  const openTask = useCallback(
    (taskId: string) => {
      openDrawer({
        title: `任务 ${taskId}`,
        body: <TaskDetailDrawer taskId={taskId} onChanged={refresh} />,
      })
    },
    [openDrawer, refresh],
  )

  const head = (
    <header className="page-head">
      <div>
        <h1>执行节点</h1>
        <p className="muted">
          在线 {online} / 共 {workers.length}
          <span className="worker-ttl-hint">
            {' · 长时间无心跳后判定为离线（默认 45 秒，取决于服务端配置）'}
          </span>
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
  )

  return (
    <section className="page">
      {head}

      <ErrorNotice error={error} onRetry={refresh} />

      {!data && loading && <LoadingState label="正在加载执行节点…" />}
      {!data && !loading && error && (
        <EmptyState
          title="执行节点加载失败"
          hint="页面会自动重试；也可以点击上方“重试”。"
        />
      )}
      {data && workers.length === 0 && (
        <EmptyState
          title="还没有执行节点"
          hint="安装接入工具包并开启任务轮询后，执行节点会自动出现在这里。"
        />
      )}

      {workers.length > 0 && (
        <div className="worker-table" role="table" aria-label="执行节点列表">
          <div className="worker-row worker-table-head" role="row">
            <span role="columnheader">执行节点</span>
            <span role="columnheader">状态</span>
            <span role="columnheader">当前任务</span>
            <span role="columnheader">元数据</span>
            <span role="columnheader">时间</span>
          </div>
          {workers.map((worker) => {
            const state = workerState(worker)
            const offline = worker.presence === 'offline'
            const entries = metadataEntries(worker.metadata)
            return (
              <div
                key={worker.worker_id}
                className={`worker-row${offline ? ' worker-row-offline' : ''}`}
                role="row"
              >
                <div className="worker-cell worker-id-cell" role="cell">
                  <span className="mono worker-id" title={worker.worker_id}>
                    {worker.worker_id}
                  </span>
                  <button
                    type="button"
                    className="ghost worker-copy"
                    onClick={() => void copyWorkerId(worker.worker_id)}
                    aria-label={`复制 ${worker.worker_id}`}
                  >
                    {copiedId === worker.worker_id ? '已复制' : '复制'}
                  </button>
                </div>

                <div className="worker-cell" role="cell">
                  <span className={`worker-state ${state.className}`}>{state.label}</span>
                </div>

                <div className="worker-cell" role="cell">
                  {worker.current_task_id ? (
                    <button
                      type="button"
                      className="worker-task-link mono"
                      onClick={() => openTask(worker.current_task_id as string)}
                      title="查看任务详情"
                    >
                      {worker.current_task_id}
                    </button>
                  ) : (
                    <span className="muted">—</span>
                  )}
                </div>

                <div className="worker-cell" role="cell">
                  {entries.length > 0 ? (
                    <details className="worker-meta">
                      <summary>{entries.length} 项</summary>
                      <dl>
                        {entries.map(([key, value]) => {
                          const text = renderMetadataValue(value)
                          return (
                            <div key={key}>
                              <dt>{key}</dt>
                              <dd title={text}>{text}</dd>
                            </div>
                          )
                        })}
                      </dl>
                    </details>
                  ) : (
                    <span className="muted">—</span>
                  )}
                </div>

                <div className="worker-cell worker-time" role="cell">
                  <span title={formatDateTime(worker.last_seen)}>
                    心跳 {formatRelative(worker.last_seen)}
                  </span>
                  <span className="muted" title={formatDateTime(worker.first_seen)}>
                    首次 {formatDateTime(worker.first_seen)}
                  </span>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </section>
  )
}
