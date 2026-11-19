import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Product, Project, Task } from '../api/types'
import { Modal } from '../components/Modal'
import { useAutoRefresh } from '../hooks/useAutoRefresh'
import { formatTime } from '../lib/labels'
import { PROJECT_SUMMARY_EVERY, REFRESH_INTERVALS } from '../lib/refresh'

interface Summary { total: number; ready: number; review: number }

/** 首帧 / 无数据时的兜底：模块级常量，引用稳定，不每次渲染新建空数组、空对象。 */
const EMPTY_PROJECTS: Project[] = []
const EMPTY_SUMMARIES: Record<string, Summary> = {}

export function ProjectsPage() {
  const { productId = '' } = useParams()
  const [product, setProduct] = useState<Product | null>(null)
  const [status, setStatus] = useState<'open' | 'closed'>('open')
  const [error, setError] = useState('')
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ name: '', context_md: '' })

  // 轮次与计数缓存放 ref：`load` 每次渲染都是新闭包（hook 内部用 loadRef 转发到最新一份），
  // 跨次刷新要读写的可变状态只能挂 ref —— 模块级 let / 类字段在这里都会串页面。
  // 两者都**不随 scope（切换进行中/已关闭）清零**：换 tab 后靠「缺失 id 增量补算」
  // 与「按当前列表裁剪」自然处理，不必为换 tab 重置轮次、也不必多打一轮全量。
  const rounds = useRef(0)
  const summariesRef = useRef<Record<string, Summary>>({})

  /**
   * 一次刷新 = 列表 + 本轮该补的计数，同一次 run 内收口（受 hook 单飞保护），
   * 不出现「列表刷完 → 再各自瀑布 N 次」的独立触发器。
   *
   * 计数只在两种情况下发请求：
   * 1. 每 PROJECT_SUMMARY_EVERY（=6）轮全量重算一次（首轮 rounds === 0 必命中）；
   * 2. 列表里出现缓存没有的项目（新建 / 换 tab）→ 只补这批新增 id，
   *    不等第 6 轮，保证新卡片首次渲染就有计数，而不是停在「—」。
   *
   * 单项目计数失败沿用旧值（不写 0），也不把整轮判失败：本刷新对象是列表。
   * hook 契约：只取数并 resolve，不 setState、不 catch。
   */
  const load = async (): Promise<{ items: Project[]; summaries: Record<string, Summary> }> => {
    const { items } = await api.listProjects(productId, status)

    const round = rounds.current
    rounds.current += 1
    const cached = summariesRef.current
    const full = round % PROJECT_SUMMARY_EVERY === 0
    const targets = full ? items : items.filter(project => cached[project.project_id] === undefined)

    const entries = await Promise.all(
      targets.map(project =>
        api.listTasks({ project_id: project.project_id, limit: 500 })
          .then(result => [project.project_id, summarize(result.items)] as const)
          .catch(() => [project.project_id, cached[project.project_id]] as const)),
    )
    const fresh = new Map(entries)

    // 只保留当前列表里存在的 project_id：项目被删/换 tab 后不留幽灵计数，
    // 本轮没重算的项目沿用缓存里的旧值。
    const next: Record<string, Summary> = {}
    for (const project of items) {
      const summary = fresh.has(project.project_id) ? fresh.get(project.project_id) : cached[project.project_id]
      if (summary !== undefined) next[project.project_id] = summary
    }
    summariesRef.current = next
    return { items, summaries: next }
  }

  const { data, error: listError, refresh } = useAutoRefresh({
    intervalMs: REFRESH_INTERVALS.projects,
    scope: `${productId}|${status}`,
    load,
  })
  const items = data?.items ?? EMPTY_PROJECTS
  const summaries = data?.summaries ?? EMPTY_SUMMARIES

  useEffect(() => { api.getProduct(productId).then(setProduct).catch(() => setProduct(null)) }, [productId])

  const create = async () => {
    setError('')
    try {
      await api.createProject({ product_id: productId, ...form })
      setOpen(false)
      setForm({ name: '', context_md: '' })
      refresh('write')
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
      {listError && <div className="alert alert-error">{listError}</div>}
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
                    <button className="btn btn-ghost btn-sm" onClick={() => api.closeProject(project.project_id, true).then(() => refresh('write')).catch(err => setError(message(err)))}>关闭</button>
                  ) : (
                    <button className="btn btn-ghost btn-sm" onClick={() => api.reopenProject(project.project_id).then(() => refresh('write')).catch(err => setError(message(err)))}>重开</button>
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
