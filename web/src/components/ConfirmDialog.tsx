import { useState, type ReactNode } from 'react'
import { errorMessage } from '../api/client'
import { Modal } from './Modal'

interface ConfirmDialogProps {
  title: string
  message: ReactNode
  confirmLabel: string
  danger?: boolean
  onClose: () => void
  onConfirm: () => Promise<unknown>
  /** 操作失败（如 409 invalid_state）后刷新父级数据。 */
  onStale?: () => void
}

/**
 * 二次确认弹窗（立项/取消这类无 reason 的动作）。
 * 失败时展示含 code / request_id 的提示，并触发一次刷新。
 */
export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  danger = false,
  onClose,
  onConfirm,
  onStale,
}: ConfirmDialogProps) {
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function confirm() {
    if (submitting) return
    setSubmitting(true)
    setError(null)
    try {
      await onConfirm()
      onClose()
    } catch (err) {
      setError(errorMessage(err))
      onStale?.()
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal title={title} onClose={onClose} closeDisabled={submitting}>
      <div className="dialog-form">
        <div className="dialog-message">{message}</div>
        {error && (
          <p className="dialog-error" role="alert">
            {error}
          </p>
        )}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={submitting}>
            取消
          </button>
          <button
            type="button"
            className={`action ${danger ? 'action-danger' : 'action-primary'}`}
            onClick={confirm}
            disabled={submitting}
          >
            {submitting ? '提交中…' : confirmLabel}
          </button>
        </div>
      </div>
    </Modal>
  )
}
