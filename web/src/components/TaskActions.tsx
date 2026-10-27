import { useState } from 'react'
import type { Task, TaskDecision } from '../api/types'
import { taskStatusLabel } from '../lib/format'
import { DecisionDialog } from './DecisionDialog'

interface TaskActionButtonsProps {
  task: Task
  onAction: (decision: TaskDecision) => void
  className?: string
}

/**
 * 纯按钮组（按状态）：
 * - 待验收（3）：验收 / 返工
 * - 已失败（5）：重排 / 放弃
 */
export function TaskActionButtons({ task, onAction, className }: TaskActionButtonsProps) {
  const canAccept = task.status === 3
  const canRetry = task.status === 5
  if (!canAccept && !canRetry) return null

  const trigger = (decision: TaskDecision) => (event: { stopPropagation: () => void }) => {
    event.stopPropagation()
    onAction(decision)
  }

  return (
    <div className={`action-bar ${className ?? ''}`.trim()}>
      {canAccept && (
        <>
          <button type="button" className="action action-primary" onClick={trigger('accept')}>
            验收
          </button>
          <button type="button" className="action action-danger" onClick={trigger('rework')}>
            返工
          </button>
        </>
      )}
      {canRetry && (
        <>
          <button type="button" className="action action-primary" onClick={trigger('retry')}>
            重排
          </button>
          <button type="button" className="action action-danger" onClick={trigger('abandon')}>
            放弃
          </button>
        </>
      )}
    </div>
  )
}

interface TaskActionDialogsProps {
  task: Task
  decision: TaskDecision | null
  onClose: () => void
  onChanged: () => void
}

/** 弹窗宿主：由页面或抽屉持有，卡片移动/卸载不会影响正在填写的 reason。 */
export function TaskActionDialogs({ task, decision, onClose, onChanged }: TaskActionDialogsProps) {
  if (!decision) return null
  return (
    <DecisionDialog
      target={{
        kind: 'task',
        id: task.task_id,
        title: task.title,
        status: task.status,
        statusLabel: taskStatusLabel(task.status),
        version: task.version,
      }}
      decision={decision}
      onClose={onClose}
      onChanged={onChanged}
    />
  )
}

interface TaskActionsProps {
  task: Task
  onChanged: () => void
  className?: string
}

/** 自包含组合（抽屉内使用）。 */
export function TaskActions({ task, onChanged, className }: TaskActionsProps) {
  const [decision, setDecision] = useState<TaskDecision | null>(null)

  return (
    <>
      <TaskActionButtons task={task} onAction={setDecision} className={className} />
      <TaskActionDialogs
        task={task}
        decision={decision}
        onClose={() => setDecision(null)}
        onChanged={onChanged}
      />
    </>
  )
}
