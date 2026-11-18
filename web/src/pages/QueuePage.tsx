import { useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { Task } from '../api/types'
import { TaskCard } from '../components/TaskCard'
import { TaskDetail } from '../components/TaskDetail'

export function QueuePage() {
  const [items, setItems] = useState<Task[]>([])
  const [error, setError] = useState('')
  const [selected, setSelected] = useState('')

  const load = () => {
    api.listTasks({ status: 'ready', limit: 300 }).then(result => setItems(result.items)).catch(err => setError(message(err)))
  }
  useEffect(load, [])

  const byProject = items.reduce<Record<string, Task[]>>((acc, task) => {
    (acc[task.project_id] ??= []).push(task)
    return acc
  }, {})

  return (
    <section className="page">
      <header className="page-hero">
        <div>
          <p className="eyebrow">DISPATCH QUEUE</p>
          <h1>派单队列</h1>
          <p className="muted">只展示 ready：依赖已满足、等待 worker 领取。</p>
        </div>
        <div className="hero-actions">
          <div className="stat-inline"><strong>{items.length}</strong><span>待派发</span></div>
          <button className="btn btn-ghost" onClick={load}>刷新</button>
        </div>
      </header>
      {error && <div className="alert alert-error">{error}</div>}
      {Object.entries(byProject).map(([project, tasks]) => (
        <section className="queue-group" key={project}>
          <header><h2 className="mono">{project}</h2><span className="muted">{tasks.length} 个任务</span></header>
          <div className="task-grid">
            {tasks.map(task => <TaskCard key={task.task_id} task={task} onClick={() => setSelected(task.task_id)} />)}
          </div>
        </section>
      ))}
      {items.length === 0 && <div className="empty-state">队列是空的，worker 可以休息一下。</div>}
      {selected !== '' && <TaskDetail taskId={selected} onClose={() => setSelected('')} onChanged={load} />}
    </section>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
