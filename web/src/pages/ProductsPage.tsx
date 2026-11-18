import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Product } from '../api/types'
import { Modal } from '../components/Modal'

export function ProductsPage() {
  const [items, setItems] = useState<Product[]>([])
  const [error, setError] = useState('')
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ product_id: '', name: '', git_url: '' })

  const load = () => {
    api.listProducts().then(result => setItems(result.items)).catch(err => setError(message(err)))
  }
  useEffect(load, [])

  const submit = async () => {
    try {
      await api.createProduct(form)
      setOpen(false)
      setForm({ product_id: '', name: '', git_url: '' })
      load()
    } catch (err) {
      setError(message(err))
    }
  }

  return (
    <section>
      <header className="page-head">
        <div>
          <h1>产品</h1>
          <p>一个产品对应一个仓库；项目与任务都挂在产品下。</p>
        </div>
        <div className="page-head-actions">
          <button className="action action-primary" onClick={() => setOpen(true)}>新建产品</button>
        </div>
      </header>
      {error && <div className="notice notice-message">{error}</div>}
      <div className="product-grid">
        {items.map(product => (
          <article className="product-card" key={product.product_id}>
            <h2>{product.name}</h2>
            <p className="mono">{product.product_id}</p>
            <p className="muted ellipsis">{product.git_url}</p>
            <Link className="action" to={`/products/${product.product_id}/projects`}>查看项目</Link>
          </article>
        ))}
        {items.length === 0 && <p className="muted">还没有产品。</p>}
      </div>
      {open && (
        <Modal title="新建产品" onClose={() => setOpen(false)} footer={
          <button className="action action-primary" onClick={submit}>创建</button>
        }>
          <div className="dialog-form">
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
