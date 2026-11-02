import { useState, type FormEvent } from 'react'
import { ApiError, errorMessage, reviewTasks } from '../api/client'
import type { Project } from '../api/types'
import { Modal } from './Modal'
import { ConfirmDialog } from './ConfirmDialog'

interface TaskReviewDialogProps {
  project: Project
  decision: 'approve' | 'reject'
  /** 本次待评审的卡片数量（用于二次确认文案）。 */
  count: number
  onClose: () => void
  /** 提交成功或数据冲突后通知父级重拉看板。 */
  onChanged: () => void
}

/**
 * 任务批次评审弹窗。
 * - approve：二次确认「通过 N 张任务卡？」；
 * - reject：原因必填，失败展示后端错误体（409 时同时刷新看板）。
 */
export function TaskReviewDialog({
  project,
  decision,
  count,
  onClose,
  onChanged,
}: TaskReviewDialogProps) {
  if (decision === 'approve') {
    return (
      <ConfirmDialog
        title="全部通过"
        message={
          <>
            通过 <strong>{count}</strong> 张任务卡？通过后这些卡片会进入「待开发」，
            项目流程继续推进。
          </>
        }
        confirmLabel="确认通过"
        onClose={onClose}
        onConfirm={async () => {
          await reviewTasks(project.project_id, 'approve')
          onChanged()
        }}
        onStale={onChanged}
      />
    )
  }

  return (
    <RejectForm
      project={project}
      onClose={onClose}
      onChanged={onChanged}
    />
  )
}

function RejectForm({
  project,
  onClose,
  onChanged,
}: {
  project: Project
  onClose: () => void
  onChanged: () => void
}) {
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting) return
    if (!reason.trim()) {
      setError('请填写驳回原因后重试。')
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      await reviewTasks(project.project_id, 'reject', reason.trim())
      onChanged()
      onClose()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setError(`${errorMessage(err)} 已刷新看板，请核对后重试。`)
        onChanged()
      } else {
        setError(errorMessage(err))
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal title="全部驳回" onClose={onClose} closeDisabled={submitting}>
      <form className="dialog-form" onSubmit={submit}>
        <div className="dialog-target">
          <div className="dialog-target-title">
            <span className="code">{project.project_id}</span>
            <strong>{project.title || '（未命名项目）'}</strong>
          </div>
          <div className="dialog-target-meta">
            <span>驳回只记录意见，不改动卡片与项目状态。</span>
          </div>
        </div>

        <label className="field">
          <span>
            驳回原因 <em className="required">（必填）</em>
          </span>
          <textarea
            rows={4}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            placeholder="请说明任务清单需要修改的地方"
            autoFocus
            disabled={submitting}
          />
        </label>

        {!reason.trim() && (
          <p className="muted dialog-hint">原因会显示在看板提示条上，改卡后自动清除。</p>
        )}

        {error && (
          <p className="dialog-error" role="alert">
            {error}
          </p>
        )}

        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={submitting}>
            取消
          </button>
          <button type="submit" className="action action-danger" disabled={submitting}>
            {submitting ? '提交中…' : '确定驳回'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
