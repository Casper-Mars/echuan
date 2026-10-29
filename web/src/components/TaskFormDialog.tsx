import { useState, type FormEvent } from 'react'
import { createTask, errorMessage, updateTask } from '../api/client'
import type { Task } from '../api/types'
import { Modal } from './Modal'

interface TaskFormDialogProps {
  mode: 'create' | 'edit'
  projectId: string
  /** 编辑时必填；新增时可省略。 */
  task?: Task
  onClose: () => void
  /** 保存成功后由父级刷新看板。 */
  onSaved: () => void
}

/**
 * 新增/编辑任务卡的最小表单：标题、正文、优先级。
 * 只在任务草稿/任务评审/待完成阶段由看板打开；权限判断由父级负责。
 */
export function TaskFormDialog({ mode, projectId, task, onClose, onSaved }: TaskFormDialogProps) {
  const [title, setTitle] = useState(task?.title ?? '')
  const [body, setBody] = useState(task?.body_md ?? '')
  const [priority, setPriority] = useState(String(task?.priority ?? 0))
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting) return
    const trimmedTitle = title.trim()
    const trimmedBody = body.trim()
    const parsedPriority = Number(priority)
    if (!trimmedTitle) {
      setError('请填写任务标题。')
      return
    }
    if (!trimmedBody) {
      setError('请填写任务正文。')
      return
    }
    if (!Number.isInteger(parsedPriority) || parsedPriority < -1000 || parsedPriority > 1000) {
      setError('优先级必须是 -1000 到 1000 之间的整数。')
      return
    }

    setSubmitting(true)
    setError(null)
    try {
      if (mode === 'create') {
        await createTask(projectId, {
          title: trimmedTitle,
          body_md: trimmedBody,
          priority: parsedPriority,
        })
      } else if (task) {
        await updateTask(task.task_id, {
          title: trimmedTitle,
          body_md: trimmedBody,
          priority: parsedPriority,
        })
      }
      onSaved()
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal
      title={mode === 'create' ? '添加任务' : `编辑任务 ${task?.task_id ?? ''}`}
      onClose={onClose}
      closeDisabled={submitting}
    >
      <form className="dialog-form" onSubmit={submit}>
        <label className="field">
          <span>
            标题 <em className="required">（必填）</em>
          </span>
          <input
            type="text"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="一句话说明这张卡要做什么"
            autoFocus
            disabled={submitting}
          />
        </label>

        <label className="field">
          <span>
            正文 <em className="required">（必填）</em>
          </span>
          <textarea
            rows={8}
            value={body}
            onChange={(event) => setBody(event.target.value)}
            placeholder="Markdown 正文，描述验收标准与注意事项"
            disabled={submitting}
          />
        </label>

        <label className="field">
          <span>优先级</span>
          <input
            type="number"
            min={-1000}
            max={1000}
            step={1}
            value={priority}
            onChange={(event) => setPriority(event.target.value)}
            disabled={submitting}
          />
        </label>

        <p className="muted dialog-hint">数值越大越优先，范围 -1000 ~ 1000，默认 0。</p>

        {error && (
          <p className="dialog-error" role="alert">
            {error}
          </p>
        )}

        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={submitting}>
            取消
          </button>
          <button type="submit" className="action action-primary" disabled={submitting}>
            {submitting ? '提交中…' : mode === 'create' ? '添加' : '保存'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
