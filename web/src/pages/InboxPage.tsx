import { useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { Task } from '../api/types'
import { TaskCard } from '../components/TaskCard'
import { TaskDetail } from '../components/TaskDetail'

export function InboxPage() {
  const [items, setItems] = useState<Task[]>([])
  const [error, setError] = useState('')
  const [selected, setSelected] = useState('')

  const load = () => {
    api.inbox().then(result => setItems(result.items)).catch(err => setError(message(err)))
  }
  useEffect(load, [])

  const review = items.filter(task => task.status === 'in_review')
  const ready = items.filter(task => task.status === 'ready')

  return (
    <section className="page">
      <header className="page-hero">
        <div>
          <p className="eyebrow">INBOX</p>
          <h1>收件箱</h1>
          <p className="muted">待审核优先处理；待领取由 worker 自动拉取。</p>
        </div>
        <div className="hero-actions">
          <div className="stat-inline"><strong>{review.length}</strong><span>待审核</span></div>
          <div className="stat-inline"><strong>{ready.length}</strong><span>待领取</span></div>
          <button className="btn btn-ghost" onClick={load}>刷新</button>
        </div>
      </header>
      {error && <div className="alert alert-error">{error}</div>}
      <section className="queue-group">
        <header><h2>待审核</h2><span className="muted">需要你拍板</span></header>
        <div className="task-grid">
          {review.map(task => <TaskCard key={task.task_id} task={task} showProject onClick={() => setSelected(task.task_id)} />)}
          {review.length === 0 && <p className="muted">没有待审核任务。</p>}
        </div>
      </section>
      <section className="queue-group">
        <header><h2>待领取</h2><span className="muted">worker 会按 kind 拉取</span></header>
        <div className="task-grid">
          {ready.map(task => <TaskCard key={task.task_id} task={task} showProject onClick={() => setSelected(task.task_id)} />)}
          {ready.length === 0 && <p className="muted">没有待领取任务。</p>}
        </div>
      </section>
      {selected !== '' && <TaskDetail taskId={selected} onClose={() => setSelected('')} onChanged={load} />}
    </section>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
