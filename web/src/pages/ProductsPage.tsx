import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Product, Project } from '../api/types'
import { Modal } from '../components/Modal'

export function ProductsPage() {
  const [items, setItems] = useState<Product[]>([])
  const [projects, setProjects] = useState<Project[]>([])
  const [error, setError] = useState('')
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ product_id: '', name: '', git_url: '' })

  const load = () => {
    api.listProducts().then(result => setItems(result.items)).catch(err => setError(message(err)))
    api.listAllProjects().then(result => setProjects(result.items)).catch(() => setProjects([]))
  }
  useEffect(load, [])

  const submit = async () => {
    try {
      await api.createProduct(form)
      setOpen(false)
      setForm({ product_id: '', name: '', git_url: '' })
      load()
    } catch (err) { setError(message(err)) }
  }

  return (
    <section className="page">
      <header className="page-hero">
        <div>
          <p className="eyebrow">WORKSPACE</p>
          <h1>产品</h1>
          <p className="muted">一个产品对应一个仓库；项目与任务都挂在产品下。</p>
        </div>
        <div className="hero-actions">
          <div className="stat-inline"><strong>{items.length}</strong><span>产品</span></div>
          <div className="stat-inline"><strong>{projects.filter(project => project.status === 'open').length}</strong><span>进行中项目</span></div>
          <button className="btn btn-primary" onClick={() => setOpen(true)}>新建产品</button>
        </div>
      </header>
      {error && <div className="alert alert-error">{error}</div>}
      <div className="product-grid">
        {items.map(product => {
          const owned = projects.filter(project => project.product_id === product.product_id)
          return (
            <article className="product-card" key={product.product_id}>
              <div className="product-mark">{product.name.slice(0, 1)}</div>
              <div className="product-body">
                <header>
                  <h2>{product.name}</h2>
                  <span className="mono muted">{product.product_id}</span>
                </header>
                <p className="mono ellipsis">{product.git_url}</p>
                <div className="product-stats">
                  <span><strong>{owned.length}</strong> 项目</span>
                  <span><strong>{owned.filter(project => project.status === 'open').length}</strong> 进行中</span>
                </div>
              </div>
              <Link className="btn btn-ghost btn-sm" to={`/products/${product.product_id}/projects`}>查看项目</Link>
            </article>
          )
        })}
        {items.length === 0 && <div className="empty-state">还没有产品，先建一个吧。</div>}
      </div>
      {open && (
        <Modal title="新建产品" onClose={() => setOpen(false)} footer={<button className="btn btn-primary" onClick={submit}>创建</button>}>
          <div className="form-stack">
            <label className="field">产品编号<input value={form.product_id} onChange={e => setForm({ ...form, product_id: e.target.value })} placeholder="APP" /></label>
            <label className="field">名称<input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} /></label>
            <label className="field">仓库地址<input value={form.git_url} onChange={e => setForm({ ...form, git_url: e.target.value })} placeholder="git@github.com:org/repo.git" /></label>
          </div>
        </Modal>
      )}
    </section>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
