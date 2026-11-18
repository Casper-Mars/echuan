import { useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { Worker } from '../api/types'
import { formatTime } from '../lib/labels'

export function WorkersPage() {
  const [items, setItems] = useState<Worker[]>([])
  const [error, setError] = useState('')

  const load = () => {
    api.listWorkers().then(result => setItems(result.items)).catch(err => setError(message(err)))
  }
  useEffect(load, [])

  return (
    <section>
      <header className="page-head">
        <div><h1>执行节点</h1><p>worker 心跳与当前租约。</p></div>
        <div className="page-head-actions"><button className="action" onClick={load}>刷新</button></div>
      </header>
      {error && <div className="notice notice-message">{error}</div>}
      <table className="v3-table">
        <thead><tr><th>worker</th><th>状态</th><th>当前任务</th><th>租约</th><th>最后心跳</th></tr></thead>
        <tbody>
          {items.map(worker => (
            <tr key={worker.worker_id}>
              <td className="mono">{worker.worker_id}</td>
              <td><span className={`chip ${worker.state === 'busy' ? 'dot-error' : 'dot-ok'}`}>{worker.state}</span></td>
              <td className="mono">{worker.current_task_id ?? '—'}</td>
              <td className="mono">{worker.current_lease_id ?? '—'}</td>
              <td className="muted">{formatTime(worker.last_seen)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {items.length === 0 && <p className="muted">还没有 worker 注册。</p>}
    </section>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
