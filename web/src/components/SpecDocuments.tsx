import { useEffect, useState, type ChangeEvent, type FormEvent } from 'react'
import {
  errorMessage,
  fetchMaterialText,
  getSpec,
  isAbortError,
  listSpecs,
  materialDownloadUrl,
  saveSpec,
} from '../api/client'
import type { SpecDocument } from '../api/types'
import { usePolling } from '../hooks/usePolling'
import { formatDateTime } from '../lib/format'
import { formatBytes, PREVIEW_CHARS, previewText, stripExtension, validateMarkdownFile } from '../lib/upload'
import { Markdown } from './Markdown'
import { Modal } from './Modal'
import { ErrorNotice, LoadingState } from './States'

function SpecEditor({ projectId, spec, onClose, onSaved }: {
  projectId: string; spec?: SpecDocument; onClose: () => void; onSaved: () => void
}) {
  const materialId = spec?.material.material_id ?? null
  const editing = Boolean(spec)
  const [title, setTitle] = useState(spec?.title ?? '')
  const [titleEdited, setTitleEdited] = useState(false)
  const [body, setBody] = useState(spec?.body_md ?? '')
  const [loadedBody, setLoadedBody] = useState(spec?.body_md ?? '')
  const [bodyLoading, setBodyLoading] = useState(Boolean(materialId))
  const [bodyError, setBodyError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [file, setFile] = useState<File | null>(null)
  const [fileError, setFileError] = useState<string | null>(null)
  const [fileReading, setFileReading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!materialId) return
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
  }, [materialId, reloadKey])

  const bodyBlocked = bodyLoading || Boolean(bodyError)
  const submitBlocked = submitting || fileReading || bodyBlocked

  async function chooseFile(next: File | null) {
    setFileError(null)
    if (!next) {
      setFile(null)
      setBody(loadedBody)
      return
    }
    const validation = validateMarkdownFile(next)
    if (!validation.ok) {
      setFile(null)
      setFileError(validation.message)
      setBody(loadedBody)
      return
    }
    setFile(next)
    setFileReading(true)
    try {
      const text = await next.text()
      setBody(text)
      // 标题默认取文件名（去扩展名）；用户手动改过标题后不再覆盖。
      if (!titleEdited || !title.trim()) {
        setTitle(stripExtension(next.name))
      }
    } catch {
      setFile(null)
      setFileError('读取文件失败，请重试。')
      setBody(loadedBody)
    } finally {
      setFileReading(false)
    }
  }

  function handleFileChange(event: ChangeEvent<HTMLInputElement>) {
    const next = event.target.files?.[0] ?? null
    event.target.value = ''
    void chooseFile(next)
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting) return
    if (bodyLoading) { setError('方案正文尚未就绪，请稍后再试。'); return }
    if (bodyError) { setError('方案正文读取失败，请重试。'); return }
    if (!title.trim() || !body.trim()) { setError('请填写方案标题和正文。'); return }
    setSubmitting(true); setError(null)
    try {
      await saveSpec(projectId, title.trim(), body, spec)
      onSaved()
    } catch (err) { setError(errorMessage(err)) }
    finally { setSubmitting(false) }
  }

  return (
    <Modal title={editing ? '编辑技术方案' : '新建技术方案'} onClose={onClose} closeDisabled={submitting} wide>
      <form className="dialog-form" onSubmit={submit}>
        <p className="muted">{spec ? `${spec.spec_id} · 第 ${spec.version} 版` : `所属项目：${projectId}`}</p>
        <label className="field"><span>方案标题（必填）</span>
          <input
            value={title}
            onChange={(event) => { setTitle(event.target.value); setTitleEdited(true) }}
            disabled={submitting}
            autoFocus
          />
        </label>
        <div className="field">
          <span>{editing ? '选择文件替换正文（.md / .markdown，最大 20 MiB）' : '方案文档（必填，.md / .markdown，最大 20 MiB）'}</span>
          <input
            type="file"
            accept=".md,.markdown"
            className="file-input"
            aria-label="选择 Markdown 方案文档"
            onChange={handleFileChange}
            disabled={submitBlocked}
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
              {editing ? '不选择文件时直接编辑下方正文。' : '标题默认取文件名（去扩展名），可手动修改。'}
            </span>
          )}
          {fileReading && <span className="muted">读取中…</span>}
          {fileError && <span className="field-error" role="alert">{fileError}</span>}
        </div>
        {editing ? (
          <label className="field"><span>方案正文（必填，支持 Markdown）</span>
            <textarea
              rows={14}
              value={body}
              onChange={(event) => setBody(event.target.value)}
              disabled={submitting || bodyBlocked}
              placeholder={bodyLoading ? '读取中…' : undefined}
            />
            {bodyLoading && <span className="muted">读取中…</span>}
          </label>
        ) : (
          body && (
            <div className="field">
              <span>文档预览</span>
              <pre className="file-preview">{previewText(body)}</pre>
              {body.length > PREVIEW_CHARS && (
                <span className="muted">仅预览前 {PREVIEW_CHARS} 字符，保存时使用完整文件。</span>
              )}
            </div>
          )
        )}
        {bodyError && (
          <div className="dialog-error" role="alert">
            <span>{bodyError}</span>{' '}
            <button type="button" className="ghost" onClick={() => setReloadKey((key) => key + 1)}>
              重试
            </button>
          </div>
        )}
        <p className="muted">每份方案独立保存历史版本；项目统一评审全部当前方案。</p>
        {error && <p className="dialog-error" role="alert">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={submitting}>取消</button>
          <button type="submit" className="action action-primary" disabled={submitBlocked}>{submitting ? '保存中…' : '保存方案'}</button>
        </div>
      </form>
    </Modal>
  )
}

function SpecReader({ id, editable, onClose, onEdit }: {
  id: string; editable: boolean; onClose: () => void; onEdit: (spec: SpecDocument) => void
}) {
  const [materialId, setMaterialId] = useState('')
  const [data, setData] = useState<SpecDocument | null>(null)
  const [error, setError] = useState<unknown>(null)
  useEffect(() => {
    const controller = new AbortController()
    setData(null); setError(null)
    getSpec(id, materialId, controller.signal).then(setData).catch((err) => {
      if (!isAbortError(err)) setError(err)
    })
    return () => controller.abort()
  }, [id, materialId])
  return (
    <Modal title={data?.title ?? '技术方案全文'} onClose={onClose} wide>
      <ErrorNotice error={error} />
      {!data && !error && <LoadingState label="正在加载方案全文…" />}
      {data && <div className="detail">
        <p className="muted">{data.spec_id} · 第 {data.version} 版 · {formatDateTime(data.material.create_time)}</p>
        <label className="field"><span>阅读版本</span>
          <select value={data.material.material_id} onChange={(event) => setMaterialId(event.target.value)}>
            {[...(data.versions ?? [])].reverse().map((version, index) => (
              <option key={version.material_id} value={version.material_id}>
                第 {version.version} 版{index === 0 ? '（当前）' : ''} · {version.title}
              </option>
            ))}
          </select>
        </label>
        <div className="detail-actions">
          <a className="action" href={materialDownloadUrl(data.material.material_id)} download>下载本版方案</a>
          {editable && data.material.material_id === data.versions?.at(-1)?.material_id && (
            <button type="button" className="action action-primary" onClick={() => onEdit(data)}>编辑当前方案</button>
          )}
        </div>
        <Markdown source={data.body_md} empty="暂无方案正文" />
      </div>}
    </Modal>
  )
}

export function SpecDocuments({ projectId, status, onChanged }: {
  projectId: string; status: number; onChanged: () => void
}) {
  const { data, error, loading, refresh } = usePolling((signal) => listSpecs(projectId, signal), [projectId])
  const [reading, setReading] = useState<string | null>(null)
  const [editing, setEditing] = useState<{ spec?: SpecDocument } | null>(null)
  const editable = status === 2 || status === 3
  return (
    <section className="detail-section">
      <h3>技术方案文档（{data?.items.length ?? 0} 份）</h3>
      <p className="muted">各方案独立修订，项目统一评审全部当前方案。</p>
      {editable && <button type="button" className="action action-primary" onClick={() => setEditing({})}>新建技术方案</button>}
      {!editable && <p className="muted">仅在规格评审或规划中可新建、编辑方案。</p>}
      <ErrorNotice error={error} onRetry={refresh} />
      {loading && !data && <LoadingState label="正在加载方案…" />}
      {data?.items.length === 0 && <p className="muted">暂无技术方案</p>}
      <ul className="chain-list">
        {data?.items.map((spec) => <li className="chain-item" key={spec.spec_id}>
          <span className="code">{spec.spec_id}</span><strong>{spec.title}</strong>
          <span className="muted">第 {spec.version} 版</span>
          <button type="button" className="ghost" onClick={() => setReading(spec.spec_id)}>阅读全文</button>
        </li>)}
      </ul>
      {reading && !editing && <SpecReader id={reading} editable={editable} onClose={() => setReading(null)} onEdit={(spec) => setEditing({ spec })} />}
      {editing && <SpecEditor projectId={projectId} spec={editing.spec} onClose={() => setEditing(null)} onSaved={() => {
        setEditing(null); setReading(null); refresh(); onChanged()
      }} />}
    </section>
  )
}
