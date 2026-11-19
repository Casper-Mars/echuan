import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Project, Task, TaskKind, TaskStatus } from '../api/types'
import { TaskCard } from '../components/TaskCard'
import { TaskDetail } from '../components/TaskDetail'
import { Modal } from '../components/Modal'
import { KINDS, STATUSES, kindLabel } from '../lib/labels'
import { REFRESH_INTERVALS } from '../lib/refresh'
import { useAutoRefresh } from '../hooks/useAutoRefresh'

const COLUMNS: TaskStatus[] = ['pending', 'ready', 'in_progress', 'in_review', 'done', 'failed', 'cancelled']

/** 无数据时的稳定空数组：避免每渲染新建引用，也避免把空态当成 loading 态。 */
const EMPTY_TASKS: Task[] = []

export function TasksPage() {
  const { projectId = '' } = useParams()
  const [project, setProject] = useState<Project | null>(null)
  const [kind, setKind] = useState<TaskKind | ''>('')
  const [status, setStatus] = useState<TaskStatus | ''>('')
  // 页面写操作（新建任务）的错误单独存一份：列表刷新错误由 hook 管，
  // 两条互不覆盖，错误条按「列表错误在前、写错误在后」渲染。
  const [error, setError] = useState('')
  const [selected, setSelected] = useState('')
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ kind: 'dev' as TaskKind, title: '', description_md: '', context_md: '', depends_on: '' })

  // 筛选组合即换代边界：projectId / kind / status 任一变化 = 新 scope，
  // hook 负责清定时器、失败计数归零、立即重拉，并丢弃旧代响应。
  const { data, error: listError, refresh } = useAutoRefresh({
    intervalMs: REFRESH_INTERVALS.tasks,
    scope: `${projectId}|${kind}|${status}`,
    load: () => api.listTasks({
      project_id: projectId, kind: kind || undefined, status: status || undefined, limit: 300,
    }),
  })
  const items = data?.items ?? EMPTY_TASKS

  useEffect(() => { api.getProject(projectId).then(setProject).catch(() => setProject(null)) }, [projectId])

  const grouped = useMemo(() => {
    const map = new Map<TaskStatus, Task[]>()
    for (const column of COLUMNS) map.set(column, [])
    for (const task of items) map.get(task.status)?.push(task)
    return map
  }, [items])

  const create = async () => {
    // 写路径自己负责这条写错误条的生命周期：发起时清掉上一轮的陈旧写错误。
    setError('')
    try {
      await api.createTask({
        project_id: projectId, kind: form.kind, title: form.title,
        description_md: form.description_md, context_md: form.context_md,
        depends_on: form.depends_on.split(',').map(item => item.trim()).filter(Boolean),
      })
      setOpen(false)
      setForm({ kind: 'dev', title: '', description_md: '', context_md: '', depends_on: '' })
      // 写后补拉走 hook（'write'）：在飞则排队、本轮收口补跑 1 次，绝不与自动刷新叠发。
      refresh('write')
    } catch (err) { setError(message(err)) }
  }

  return (
    <section className="page">
      <nav className="crumbs">
        <Link to="/products">产品</Link><span>/</span>
        <Link to={`/products/${project?.product_id ?? ''}/projects`}>{project?.product_id ?? '项目'}</Link><span>/</span>
        <span>{project?.name ?? projectId}</span>
      </nav>
      <header className="page-hero">
        <div>
          <p className="eyebrow">PROJECT TASKS</p>
          <h1>{project?.name ?? projectId}</h1>
          <p className="muted">{projectId} · {items.length} 个任务</p>
        </div>
        <div className="hero-actions">
          <select className="select" value={kind} onChange={e => setKind(e.target.value as TaskKind | '')}>
            <option value="">全部类型</option>
            {KINDS.map(value => <option key={value} value={value}>{kindLabel(value)}</option>)}
          </select>
          <select className="select" value={status} onChange={e => setStatus(e.target.value as TaskStatus | '')}>
            <option value="">全部状态</option>
            {STATUSES.map(value => <option key={value} value={value}>{value}</option>)}
          </select>
          <button className="btn btn-primary" onClick={() => setOpen(true)}>新建任务</button>
        </div>
      </header>
      {listError && <div className="alert alert-error">{listError}</div>}
      {error && <div className="alert alert-error">{error}</div>}

      <div className="board">
        {COLUMNS.map(column => {
          const tasks = grouped.get(column) ?? []
          return (
            <div className={`board-col col-${column}`} key={column}>
              <header className="board-col-head">
                <span>{column}</span>
                <strong>{tasks.length}</strong>
              </header>
              <div className="board-col-body">
                {tasks.map(task => <TaskCard key={task.task_id} task={task} onClick={() => setSelected(task.task_id)} />)}
                {tasks.length === 0 && <p className="board-empty">暂无任务</p>}
              </div>
            </div>
          )
        })}
      </div>

      {open && (
        <Modal title="新建任务" onClose={() => setOpen(false)} footer={<button className="btn btn-primary" onClick={create}>创建</button>}>
          <div className="form-stack">
            <label className="field">类型
              <select value={form.kind} onChange={e => setForm({ ...form, kind: e.target.value as TaskKind })}>
                {KINDS.map(value => <option key={value} value={value}>{kindLabel(value)}（{value}）</option>)}
              </select>
            </label>
            <label className="field">标题<input value={form.title} onChange={e => setForm({ ...form, title: e.target.value })} placeholder="一句话说明要做什么" /></label>
            <label className="field">任务描述<textarea rows={4} value={form.description_md} onChange={e => setForm({ ...form, description_md: e.target.value })} /></label>
            <label className="field">任务上下文<textarea rows={3} value={form.context_md} onChange={e => setForm({ ...form, context_md: e.target.value })} /></label>
            <label className="field">前置任务（逗号分隔的 task_id）<input value={form.depends_on} onChange={e => setForm({ ...form, depends_on: e.target.value })} /></label>
          </div>
        </Modal>
      )}
      {selected !== '' && <TaskDetail taskId={selected} onClose={() => setSelected('')} onChanged={() => refresh('write')} />}
    </section>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
