import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Product, Project, Task } from '../api/types'
import { Modal } from '../components/Modal'
import { formatTime } from '../lib/labels'

interface Summary { total: number; ready: number; review: number }

export function ProjectsPage() {
  const { productId = '' } = useParams()
  const [product, setProduct] = useState<Product | null>(null)
  const [items, setItems] = useState<Project[]>([])
  const [status, setStatus] = useState<'open' | 'closed'>('open')
  const [summaries, setSummaries] = useState<Record<string, Summary>>({})
  const [error, setError] = useState('')
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ name: '', context_md: '' })

  const load = () => {
    api.listProjects(productId, status).then(result => setItems(result.items)).catch(err => setError(message(err)))
  }
  useEffect(() => { api.getProduct(productId).then(setProduct).catch(() => setProduct(null)) }, [productId])
  useEffect(load, [productId, status])

  useEffect(() => {
    let cancelled = false
    if (items.length === 0) { setSummaries({}); return }
    Promise.all(items.map(project => api.listTasks({ project_id: project.project_id, limit: 500 })
      .then(result => [project.project_id, summarize(result.items)] as const)
      .catch(() => [project.project_id, { total: 0, ready: 0, review: 0 }] as const)))
      .then(entries => { if (!cancelled) setSummaries(Object.fromEntries(entries)) })
    return () => { cancelled = true }
  }, [items])

  const create = async () => {
    try {
      await api.createProject({ product_id: productId, ...form })
      setOpen(false)
      setForm({ name: '', context_md: '' })
      load()
    } catch (err) { setError(message(err)) }
  }

  return (
    <section className="page">
      <nav className="crumbs">
        <Link to="/products">产品</Link><span>/</span>
        <span>{product?.name ?? productId}</span>
      </nav>
      <header className="page-hero">
        <div>
          <p className="eyebrow">PROJECTS</p>
          <h1>{product?.name ?? productId}</h1>
          <p className="muted mono">{product?.git_url ?? ''}</p>
        </div>
        <div className="hero-actions">
          <div className="tabs">
            <button className={status === 'open' ? 'active' : ''} onClick={() => setStatus('open')}>进行中</button>
            <button className={status === 'closed' ? 'active' : ''} onClick={() => setStatus('closed')}>已关闭</button>
          </div>
          <button className="btn btn-primary" onClick={() => setOpen(true)}>新建项目</button>
        </div>
      </header>
      {error && <div className="alert alert-error">{error}</div>}

      <div className="project-grid">
        {items.map(project => {
          const summary = summaries[project.project_id]
          return (
            <article className="project-card" key={project.project_id}>
              <header className="project-card-head">
                <span className={`pill ${project.status === 'open' ? 'pill-ok' : 'pill-muted'}`}>{project.status}</span>
                <span className="mono muted">{project.project_id}</span>
              </header>
              <h2>{project.name}</h2>
              <p className="project-context">{project.context_md || '暂无背景上下文'}</p>
              <div className="project-stats">
                <div><strong>{summary?.total ?? '—'}</strong><span>任务</span></div>
                <div><strong>{summary?.ready ?? '—'}</strong><span>待派发</span></div>
                <div><strong>{summary?.review ?? '—'}</strong><span>待审核</span></div>
              </div>
              <footer className="project-card-foot">
                <span className="muted">更新于 {formatTime(project.update_time)}</span>
                <div className="row-actions">
                  <Link className="btn btn-ghost btn-sm" to={`/projects/${project.project_id}/tasks`}>进入任务</Link>
                  {project.status === 'open' ? (
                    <button className="btn btn-ghost btn-sm" onClick={() => api.closeProject(project.project_id, true).then(load).catch(err => setError(message(err)))}>关闭</button>
                  ) : (
                    <button className="btn btn-ghost btn-sm" onClick={() => api.reopenProject(project.project_id).then(load).catch(err => setError(message(err)))}>重开</button>
                  )}
                </div>
              </footer>
            </article>
          )
        })}
        {items.length === 0 && <div className="empty-state">还没有 {status === 'open' ? '进行中' : '已关闭'} 的项目</div>}
      </div>

      {open && (
        <Modal title="新建项目" onClose={() => setOpen(false)} footer={<button className="btn btn-primary" onClick={create}>创建</button>}>
          <div className="form-stack">
            <label className="field">名称<input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} placeholder="一次有边界的工作" /></label>
            <label className="field">背景上下文<textarea rows={5} value={form.context_md} onChange={e => setForm({ ...form, context_md: e.target.value })} placeholder="issue 链接、仓库画像、约束、历史决策" /></label>
          </div>
        </Modal>
      )}
    </section>
  )
}

function summarize(tasks: Task[]): Summary {
  return {
    total: tasks.length,
    ready: tasks.filter(task => task.status === 'ready').length,
    review: tasks.filter(task => task.status === 'in_review').length,
  }
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
