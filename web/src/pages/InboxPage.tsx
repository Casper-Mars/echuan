import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Task } from '../api/types'
import { TaskDetail } from '../components/TaskDetail'
import { kindLabel, statusLabel } from '../lib/labels'

export function InboxPage() {
  const navigate = useNavigate()
  const [items, setItems] = useState<Task[]>([])
  const [error, setError] = useState('')
  const [selected, setSelected] = useState('')

  const load = () => {
    api.inbox().then(result => setItems(result.items)).catch(err => setError(message(err)))
  }
  useEffect(load, [])

  return (
    <section>
      <header className="page-head">
        <div>
          <h1>收件箱</h1>
          <p>待领取（ready）与待审核（in_review）的任务。</p>
        </div>
        <div className="page-head-actions"><button className="action" onClick={load}>刷新</button></div>
      </header>
      {error && <div className="notice notice-message">{error}</div>}
      <table className="v3-table">
        <thead><tr><th>任务</th><th>项目</th><th>类型</th><th>标题</th><th>状态</th></tr></thead>
        <tbody>
          {items.map(task => (
            <tr key={task.task_id} className="clickable" onClick={() => setSelected(task.task_id)}>
              <td className="mono">{task.task_id}</td>
              <td className="mono"><button className="link" onClick={event => { event.stopPropagation(); navigate(`/projects/${task.project_id}/tasks`) }}>{task.project_id}</button></td>
              <td><span className={`chip kind-${task.kind}`}>{kindLabel(task.kind)}</span></td>
              <td>{task.title}</td>
              <td>{statusLabel(task.kind, task.status)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {items.length === 0 && <p className="muted">收件箱是空的。</p>}
      {selected !== '' && <TaskDetail taskId={selected} onClose={() => setSelected('')} onChanged={load} />}
    </section>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
