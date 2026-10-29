import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage, getSpec, isAbortError, listSpecs, materialDownloadUrl, saveSpec } from '../api/client'
import type { SpecDocument } from '../api/types'
import { usePolling } from '../hooks/usePolling'
import { formatDateTime } from '../lib/format'
import { Markdown } from './Markdown'
import { Modal } from './Modal'
import { ErrorNotice, LoadingState } from './States'

function SpecEditor({ projectId, spec, onClose, onSaved }: {
  projectId: string; spec?: SpecDocument; onClose: () => void; onSaved: () => void
}) {
  const [title, setTitle] = useState(spec?.title ?? '')
  const [body, setBody] = useState(spec?.body_md ?? '')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting) return
    if (!title.trim() || !body.trim()) { setError('请填写方案标题和正文。'); return }
    setSubmitting(true); setError(null)
    try {
      await saveSpec(projectId, title.trim(), body, spec)
      onSaved()
    } catch (err) { setError(errorMessage(err)) }
    finally { setSubmitting(false) }
  }
  return (
    <Modal title={spec ? '编辑技术方案' : '新建技术方案'} onClose={onClose} closeDisabled={submitting} wide>
      <form className="dialog-form" onSubmit={submit}>
        <p className="muted">{spec ? `${spec.spec_id} · 第 ${spec.version} 版` : `所属项目：${projectId}`}</p>
        <label className="field"><span>方案标题（必填）</span>
          <input value={title} onChange={(event) => setTitle(event.target.value)} disabled={submitting} autoFocus />
        </label>
        <label className="field"><span>方案正文（必填，支持 Markdown）</span>
          <textarea rows={14} value={body} onChange={(event) => setBody(event.target.value)} disabled={submitting} />
        </label>
        <p className="muted">每份方案独立保存历史版本；项目统一评审全部当前方案。</p>
        {error && <p className="dialog-error" role="alert">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={submitting}>取消</button>
          <button type="submit" className="action action-primary" disabled={submitting}>{submitting ? '保存中…' : '保存方案'}</button>
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
  const editable = status === 1 || status === 2
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
