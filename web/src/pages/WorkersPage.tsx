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
    <section className="page">
      <header className="page-hero">
        <div>
          <p className="eyebrow">WORKERS</p>
          <h1>执行节点</h1>
          <p className="muted">心跳、当前任务与租约。</p>
        </div>
        <div className="hero-actions"><button className="btn btn-ghost" onClick={load}>刷新</button></div>
      </header>
      {error && <div className="alert alert-error">{error}</div>}
      <div className="worker-grid">
        {items.map(worker => (
          <article className="worker-card" key={worker.worker_id}>
            <header>
              <span className={`dot ${worker.state === 'busy' ? 'dot-busy' : 'dot-ok'}`} />
              <h2 className="mono">{worker.worker_id}</h2>
              <span className="pill">{worker.state}</span>
            </header>
            <dl>
              <div><dt>当前任务</dt><dd className="mono">{worker.current_task_id ?? '—'}</dd></div>
              <div><dt>租约</dt><dd className="mono">{worker.current_lease_id ?? '—'}</dd></div>
              <div><dt>最后心跳</dt><dd>{formatTime(worker.last_seen)}</dd></div>
            </dl>
          </article>
        ))}
        {items.length === 0 && <div className="empty-state">还没有 worker 注册。</div>}
      </div>
    </section>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
