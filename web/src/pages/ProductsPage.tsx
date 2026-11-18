import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Product, Project } from '../api/types'
import { Modal } from '../components/Modal'
import { ProductDetail } from '../components/ProductDetail'

/**
 * 点击事件是否落在可交互元素内（链接/按钮/输入框）。
 * 命中时交给元素自身处理，不触发卡片级行为（否则「查看项目」会同时打开抽屉）。
 * 只有这一处调用方，按 6 行内联，不额外建 lib。
 */
function isInteractiveTarget(target: EventTarget | null): boolean {
  if (typeof HTMLElement === 'undefined' || !(target instanceof HTMLElement)) return false
  return Boolean(target.closest('a, button, input, textarea, select'))
}

export function ProductsPage() {
  const [items, setItems] = useState<Product[]>([])
  const [projects, setProjects] = useState<Project[]>([])
  const [error, setError] = useState('')
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ product_id: '', name: '', git_url: '' })
  // 详情抽屉：记录被点开的产品（纯展示态，关闭即清空，不影响列表数据与滚动位置）。
  const [detail, setDetail] = useState<{ product: Product; projectCount: number } | null>(null)

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
          const openProduct = () => setDetail({ product, projectCount: owned.length })
          return (
            <article
              className="product-card"
              key={product.product_id}
              // 不给容器加 role="button"：卡片内还有「查看项目」链接，套按钮角色会让
              // 链接变成按钮内的伪元素，可达性反而更差。只补 tabIndex + 键盘处理。
              tabIndex={0}
              onClick={event => {
                if (isInteractiveTarget(event.target)) return
                openProduct()
              }}
              onKeyDown={event => {
                // 只处理卡片自身获得焦点时的按键；内部链接/按钮的键盘事件交给它们。
                if (event.target !== event.currentTarget) return
                if (event.key === 'Enter') {
                  openProduct()
                  return
                }
                if (event.key === ' ') {
                  // 空格默认会滚动页面，先拦掉再打开抽屉。
                  event.preventDefault()
                  openProduct()
                }
              }}
            >
              <div className="product-mark">{product.name.slice(0, 1)}</div>
              <div className="product-body">
                <header>
                  <h2 className="card-title">{product.name}</h2>
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
      {detail && (
        <ProductDetail
          productId={detail.product.product_id}
          fallbackProjectCount={detail.projectCount}
          onClose={() => setDetail(null)}
        />
      )}
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
