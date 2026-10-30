import { useEffect, useState, type ChangeEvent, type FormEvent } from 'react'
import {
  createProject,
  errorMessage,
  fetchMaterialText,
  isAbortError,
  updateProjectRequirement,
  updateProjectTitle,
} from '../api/client'
import type { ProjectDetail } from '../api/types'
import { formatBytes, PREVIEW_CHARS, previewText, stripExtension, validateMarkdownFile } from '../lib/upload'
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
  // 标题是否被用户手动改过：未改过时选文件可用文件名自动填充。
  const [titleEdited, setTitleEdited] = useState(false)
  const [autoTitle, setAutoTitle] = useState('')
  // 正文：create 模式来自所选文件，requirement 模式来自 material 拉取或替换文件。
  const [body, setBody] = useState('')
  const [loadedBody, setLoadedBody] = useState('')
  const [bodyLoading, setBodyLoading] = useState(mode === 'requirement' && Boolean(materialId))
  const [bodyError, setBodyError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [file, setFile] = useState<File | null>(null)
  const [fileError, setFileError] = useState<string | null>(null)
  const [fileReading, setFileReading] = useState(false)
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
        setLoadedBody(text)
        setBodyLoading(false)
      })
      .catch((err) => {
        if (isAbortError(err)) return
        setBodyError(errorMessage(err))
        setBodyLoading(false)
      })
    return () => controller.abort()
  }, [mode, materialId, reloadKey])

  // 正文读取中 / 读取失败时不允许编辑和提交；缺 material 不阻止上传首版。
  const bodyBlocked = mode === 'requirement' && (bodyLoading || Boolean(bodyError))
  // 文件校验失败（含读取失败）时禁止提交：否则非法文件会把正文清空/回退，
  // create 模式便会静默创建一条无需求项目，requirement 模式则会生成重复版本。
  const submitBlocked = submitting || fileReading || bodyBlocked || Boolean(fileError)
  // 文件选择本身不受 fileError 影响，否则用户无法重新选择或取消来解除阻塞。
  const fileInputBlocked = submitting || fileReading || bodyBlocked

  async function chooseFile(next: File | null) {
    setFileError(null)
    if (!next) {
      setFile(null)
      // 清除替换文件：create 模式清空预览，requirement 模式恢复拉取到的原文。
      setBody(mode === 'requirement' ? loadedBody : '')
      if (mode === 'create' && !titleEdited && title === autoTitle) {
        setTitle('')
        setAutoTitle('')
      }
      return
    }

    const validation = validateMarkdownFile(next)
    if (!validation.ok) {
      setFile(null)
      setFileError(validation.message)
      setBody(mode === 'requirement' ? loadedBody : '')
      // 旧文件的自动标题一并清除，避免「标题来自旧文件、实际无文档」。
      if (mode === 'create' && !titleEdited && title === autoTitle) {
        setTitle('')
        setAutoTitle('')
      }
      return
    }

    setFile(next)
    setFileReading(true)
    try {
      const text = await next.text()
      setBody(text)
      // 需求资料标题由后端写死为项目标题：用文件名去扩展名自动填标题，
      // 用户改过标题后不再覆盖。
      if (mode !== 'requirement' && (!titleEdited || !title.trim())) {
        const stem = stripExtension(next.name)
        setTitle(stem)
        setAutoTitle(stem)
      }
    } catch {
      setFile(null)
      setFileError('读取文件失败，请重试。')
      setBody(mode === 'requirement' ? loadedBody : '')
      if (mode === 'create' && !titleEdited && title === autoTitle) {
        setTitle('')
        setAutoTitle('')
      }
    } finally {
      setFileReading(false)
    }
  }

  function handleFileChange(event: ChangeEvent<HTMLInputElement>) {
    const next = event.target.files?.[0] ?? null
    // 清空 input，允许再次选择同一个文件。
    event.target.value = ''
    void chooseFile(next)
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting) return
    if (bodyLoading) {
      setError('需求正文尚未就绪，请稍后再试。')
      return
    }
    if (bodyError) {
      setError('需求正文读取失败，请重试。')
      return
    }
    if (fileError) {
      setError('文件未通过校验，请重新选择或清除后再提交。')
      return
    }
    if (mode !== 'requirement' && !title.trim()) {
      setError('项目标题不能为空。')
      return
    }
    if (mode === 'requirement' && !body.trim()) {
      setError('需求正文不能为空。')
      return
    }
    if (mode === 'create' && file && !body.trim()) {
      setError('所选文件内容为空，请换一个文件或清空选择。')
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      if (props.mode === 'create') {
        // 需求可选：没有选择文件时不发送 requirement_md，后端创建为需求待提交。
        await createProject(
          body.trim()
            ? { product_id: props.productId, title: title.trim(), requirement_md: body }
            : { product_id: props.productId, title: title.trim() },
        )
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
            <input
              value={title}
              onChange={(event) => {
                setTitle(event.target.value)
                setTitleEdited(true)
              }}
              disabled={submitting}
              autoFocus
            />
          </label>
        )}
        {mode !== 'title' && (
          <div className="field">
            <span>{mode === 'create' ? '需求文档（可选，.md / .markdown，最大 20 MiB）' : '选择文件替换正文（.md / .markdown，最大 20 MiB）'}</span>
            <input
              type="file"
              accept=".md,.markdown"
              className="file-input"
              aria-label="选择 Markdown 需求文档"
              onChange={handleFileChange}
              disabled={fileInputBlocked}
            />
            {file ? (
              <span className="file-meta">
                <strong className="ellipsis" title={file.name}>{file.name}</strong>
                <span className="muted">{formatBytes(file.size)}</span>
                <button type="button" className="ghost" onClick={() => void chooseFile(null)} disabled={submitting || fileReading}>
                  清除
                </button>
              </span>
            ) : (
              <span className="muted">
                {mode === 'create'
                  ? '不选择也可以创建；项目会落在「需求待提交」，之后可在项目详情里上传首版。'
                  : '选择文件会读取其内容并替换下方正文。'}
              </span>
            )}
            {fileReading && <span className="muted">读取中…</span>}
            {fileError && (
              <>
                <span className="field-error" role="alert">{fileError}</span>{' '}
                <button type="button" className="ghost" onClick={() => void chooseFile(null)} disabled={fileInputBlocked}>
                  清除
                </button>
              </>
            )}
          </div>
        )}
        {mode === 'requirement' && (
          <label className="field">
            <span>需求正文（必填，支持 Markdown）</span>
            <textarea
              rows={12}
              value={body}
              onChange={(event) => setBody(event.target.value)}
              disabled={submitting || bodyBlocked}
              placeholder={bodyLoading ? '读取中…' : undefined}
              autoFocus={!bodyBlocked}
            />
            {bodyLoading && <span className="muted">读取中…</span>}
            {!materialId && !file && (
              <span className="muted">该项目尚无需求文档（material_id 为空），可选择上方文件上传首版。</span>
            )}
          </label>
        )}
        {mode === 'create' && body && (
          <div className="field">
            <span>文档预览</span>
            <pre className="file-preview">{previewText(body)}</pre>
            {body.length > PREVIEW_CHARS && (
              <span className="muted">仅预览前 {PREVIEW_CHARS} 字符，创建时使用完整文件。</span>
            )}
          </div>
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
          <button type="submit" className="action action-primary" disabled={submitBlocked}>
            {submitting ? '提交中…' : mode === 'create' ? '创建' : '保存'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
