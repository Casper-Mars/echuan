import { useEffect, useState, type FormEvent } from 'react'
import {
  createProject,
  errorMessage,
  fetchMaterialText,
  isAbortError,
  updateProjectRequirement,
  updateProjectTitle,
} from '../api/client'
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
  const materialId = mode === 'requirement' ? project?.requirement?.material_id ?? null : null
  const [title, setTitle] = useState(project?.title ?? '')
  // 详情接口不再返回正文：create 模式从空串开始，requirement 模式由 material 拉取填充。
  const [body, setBody] = useState('')
  const [bodyLoading, setBodyLoading] = useState(mode === 'requirement' && Boolean(materialId))
  const [bodyError, setBodyError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const heading = mode === 'create' ? '新建需求项目' : mode === 'title' ? '编辑项目标题' : '编辑需求正文'

  useEffect(() => {
    if (mode !== 'requirement' || !materialId) return
    const controller = new AbortController()
    setBodyLoading(true)
    setBodyError(null)
    fetchMaterialText(materialId, controller.signal)
      .then((text) => {
        setBody(text)
        setBodyLoading(false)
      })
      .catch((err) => {
        if (isAbortError(err)) return
        setBodyError(errorMessage(err))
        setBodyLoading(false)
      })
    return () => controller.abort()
  }, [mode, materialId, reloadKey])

  const missingRequirement = mode === 'requirement' && !materialId
  // 正文未就绪（缺 material / 读取中 / 读取失败）时禁止编辑与提交。
  const bodyBlocked = mode === 'requirement' && (missingRequirement || bodyLoading || Boolean(bodyError))

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting) return
    if (bodyBlocked) {
      setError(missingRequirement ? '该项目缺少需求资料，无法保存。' : '需求正文尚未就绪，请稍后再试。')
      return
    }
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
            <textarea
              rows={12}
              value={body}
              onChange={(event) => setBody(event.target.value)}
              disabled={submitting || bodyLoading || missingRequirement || Boolean(bodyError)}
              placeholder={bodyLoading ? '读取中…' : undefined}
              autoFocus={mode === 'requirement' && !bodyBlocked}
            />
            {bodyLoading && <span className="muted">读取中…</span>}
            {missingRequirement && (
              <span className="muted">该项目缺少需求资料（material_id 为空），无法读取正文。</span>
            )}
          </label>
        )}
        {bodyError && (
          <div className="dialog-error" role="alert">
            <span>{bodyError}</span>{' '}
            <button type="button" className="ghost" onClick={() => setReloadKey((key) => key + 1)}>
              重试
            </button>
          </div>
        )}
        {mode === 'requirement' && !bodyBlocked && <p className="muted">保存后生成新版本，保留此前需求记录。</p>}
        {error && <p className="dialog-error" role="alert">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={submitting}>取消</button>
          <button type="submit" className="action action-primary" disabled={submitting || bodyBlocked}>
            {submitting ? '提交中…' : mode === 'create' ? '创建' : '保存'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
