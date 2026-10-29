import { useState, type FormEvent } from 'react'
import { createProject, errorMessage, updateProjectRequirement, updateProjectTitle } from '../api/client'
import type { ProjectDetail } from '../api/types'
import { Modal } from './Modal'

type ProjectFormDialogProps = {
  onClose: () => void
  onSaved: () => void
} & (
  | { mode: 'create'; productId: string; project?: never }
  | { mode: 'title' | 'requirement'; project: ProjectDetail; productId?: never }
)

export function ProjectFormDialog(props: ProjectFormDialogProps) {
  const { mode, project, onClose, onSaved } = props
  const [title, setTitle] = useState(project?.title ?? '')
  const [body, setBody] = useState(project?.requirement_md ?? '')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const heading = mode === 'create' ? '新建需求项目' : mode === 'title' ? '编辑项目标题' : '编辑需求正文'

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting) return
    if (mode !== 'requirement' && !title.trim()) {
      setError('项目标题不能为空。')
      return
    }
    if (mode !== 'title' && !body.trim()) {
      setError('需求正文不能为空。')
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      if (props.mode === 'create') {
        await createProject({ product_id: props.productId, title: title.trim(), requirement_md: body })
      } else if (props.mode === 'title') {
        await updateProjectTitle(props.project.project_id, title.trim())
      } else {
        await updateProjectRequirement(props.project.project_id, body)
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
    <Modal title={heading} onClose={onClose} closeDisabled={submitting} wide>
      <form className="dialog-form" onSubmit={submit}>
        <p className="muted">{mode === 'create' ? `所属产品：${props.productId}` : `项目：${project?.project_id}`}</p>
        {mode !== 'requirement' && (
          <label className="field">
            <span>项目标题（必填）</span>
            <input value={title} onChange={(event) => setTitle(event.target.value)} disabled={submitting} autoFocus />
          </label>
        )}
        {mode !== 'title' && (
          <label className="field">
            <span>需求正文（必填，支持 Markdown）</span>
            <textarea rows={12} value={body} onChange={(event) => setBody(event.target.value)} disabled={submitting} autoFocus={mode === 'requirement'} />
          </label>
        )}
        {mode === 'requirement' && <p className="muted">保存后生成新版本，保留此前需求记录。</p>}
        {error && <p className="dialog-error" role="alert">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={submitting}>取消</button>
          <button type="submit" className="action action-primary" disabled={submitting}>
            {submitting ? '提交中…' : mode === 'create' ? '创建' : '保存'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
