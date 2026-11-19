import type { Task } from '../api/types'
import { formatTime, kindLabel, statusLabel } from '../lib/labels'

export function TaskCard({ task, onClick, showProject = false }: { task: Task; onClick: () => void; showProject?: boolean }) {
  const lease = task.status === 'in_progress' && task.lease_until
  return (
    <button className="task-card" onClick={onClick}>
      <div className="task-card-top">
        <span className={`kind-badge kind-${task.kind}`}>{kindLabel(task.kind)}</span>
        {task.priority > 0 && <span className="task-priority">P{task.priority}</span>}
        <span className={`status-dot status-${task.status}`}>{statusLabel(task.kind, task.status)}</span>
      </div>
      <h3 className="task-card-title">{task.title}</h3>
      <p className="task-card-id mono">{task.task_id}{showProject ? ` · ${task.project_id}` : ''}</p>
      <div className="task-card-foot">
        <span>尝试 {task.attempt}/{task.max_attempts}</span>
        {lease ? <span className="lease-chip">租约至 {formatTime(task.lease_until)}</span> : <span>{formatTime(task.update_time)}</span>}
      </div>
      {task.reason && <p className="task-card-reason">{task.reason}</p>}
    </button>
  )
}
