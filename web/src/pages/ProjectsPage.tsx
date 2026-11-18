import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Product, Project } from '../api/types'
import { Modal } from '../components/Modal'
import { formatTime } from '../lib/labels'

export function ProjectsPage() {
  const { productId = '' } = useParams()
  const [product, setProduct] = useState<Product | null>(null)
  const [items, setItems] = useState<Project[]>([])
  const [status, setStatus] = useState<'open' | 'closed'>('open')
  const [error, setError] = useState('')
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ name: '', context_md: '' })

  const load = () => {
    api.listProjects(productId, status).then(result => setItems(result.items)).catch(err => setError(message(err)))
  }
  useEffect(() => { api.getProduct(productId).then(setProduct).catch(() => setProduct(null)) }, [productId])
  useEffect(load, [productId, status])

  const create = async () => {
    try {
      await api.createProject({ product_id: productId, ...form })
      setOpen(false)
      setForm({ name: '', context_md: '' })
      load()
    } catch (err) { setError(message(err)) }
  }

  const close = async (project: Project, force: boolean) => {
    try { await api.closeProject(project.project_id, force); load() } catch (err) { setError(message(err)) }
  }

  return (
    <section>
      <header className="page-head">
        <div>
          <h1>{product?.name ?? productId} · 项目</h1>
          <p>项目只是容器：名称、背景上下文、open / closed。</p>
        </div>
        <div className="page-head-actions">
          <select className="filter-select" value={status} onChange={e => setStatus(e.target.value as 'open' | 'closed')}>
            <option value="open">open</option>
            <option value="closed">closed</option>
          </select>
          <button className="action action-primary" onClick={() => setOpen(true)}>新建项目</button>
        </div>
      </header>
      {error && <div className="notice notice-message">{error}</div>}
      <table className="v3-table">
        <thead><tr><th>项目</th><th>名称</th><th>状态</th><th>更新时间</th><th /></tr></thead>
        <tbody>
          {items.map(project => (
            <tr key={project.project_id}>
              <td className="mono">{project.project_id}</td>
              <td><Link className="link" to={`/projects/${project.project_id}/tasks`}>{project.name}</Link></td>
              <td><span className={`chip ${project.status === 'open' ? 'dot-ok' : ''}`}>{project.status}</span></td>
              <td className="muted">{formatTime(project.update_time)}</td>
              <td className="row-actions">
                <Link className="action action-compact" to={`/projects/${project.project_id}/tasks`}>任务</Link>
                {project.status === 'open'
                  ? <button className="action action-compact" onClick={() => close(project, true)}>关闭</button>
                  : <button className="action action-compact" onClick={() => api.reopenProject(project.project_id).then(load).catch(err => setError(message(err)))}>重开</button>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {items.length === 0 && <p className="muted">没有 {status} 项目。</p>}
      {open && (
        <Modal title="新建项目" onClose={() => setOpen(false)} footer={<button className="action action-primary" onClick={create}>创建</button>}>
          <div className="dialog-form">
            <label className="field">名称<input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} /></label>
            <label className="field">背景上下文<textarea rows={5} value={form.context_md} onChange={e => setForm({ ...form, context_md: e.target.value })} placeholder="issue 链接、仓库画像、约束、历史决策" /></label>
          </div>
        </Modal>
      )}
    </section>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
