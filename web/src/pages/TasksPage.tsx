import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Project, Task, TaskKind, TaskStatus } from '../api/types'
import { Modal } from '../components/Modal'
import { TaskDetail } from '../components/TaskDetail'
import { KINDS, STATUSES, formatTime, kindLabel, statusLabel } from '../lib/labels'

export function TasksPage() {
  const { projectId = '' } = useParams()
  const [project, setProject] = useState<Project | null>(null)
  const [items, setItems] = useState<Task[]>([])
  const [kind, setKind] = useState<TaskKind | ''>('')
  const [status, setStatus] = useState<TaskStatus | ''>('')
  const [error, setError] = useState('')
  const [selected, setSelected] = useState('')
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ kind: 'dev' as TaskKind, title: '', description_md: '', context_md: '', depends_on: '' })

  const load = () => {
    api.listTasks({
      project_id: projectId,
      kind: kind || undefined,
      status: status || undefined,
      limit: 200,
    }).then(result => setItems(result.items)).catch(err => setError(message(err)))
  }
  useEffect(() => { api.getProject(projectId).then(setProject).catch(() => setProject(null)) }, [projectId])
  useEffect(load, [projectId, kind, status])

  const create = async () => {
    try {
      await api.createTask({
        project_id: projectId,
        kind: form.kind,
        title: form.title,
        description_md: form.description_md,
        context_md: form.context_md,
        depends_on: form.depends_on.split(',').map(item => item.trim()).filter(Boolean),
      })
      setOpen(false)
      setForm({ kind: 'dev', title: '', description_md: '', context_md: '', depends_on: '' })
      load()
    } catch (err) { setError(message(err)) }
  }

  return (
    <section>
      <header className="page-head">
        <div>
          <h1>{project?.name ?? projectId} · 任务</h1>
          <p>四类任务共用七态；pending 等前置，ready 才可派发。</p>
        </div>
        <div className="page-head-actions">
          <Link className="action" to={`/products/${project?.product_id ?? ''}/projects`}>返回项目</Link>
          <button className="action action-primary" onClick={() => setOpen(true)}>新建任务</button>
        </div>
      </header>
      <div className="filter-bar">
        <select className="filter-select" value={kind} onChange={e => setKind(e.target.value as TaskKind | '')}>
          <option value="">全部类型</option>
          {KINDS.map(value => <option key={value} value={value}>{kindLabel(value)}</option>)}
        </select>
        <select className="filter-select" value={status} onChange={e => setStatus(e.target.value as TaskStatus | '')}>
          <option value="">全部状态</option>
          {STATUSES.map(value => <option key={value} value={value}>{value}</option>)}
        </select>
      </div>
      {error && <div className="notice notice-message">{error}</div>}
      <table className="v3-table">
        <thead><tr><th>任务</th><th>类型</th><th>标题</th><th>状态</th><th>尝试</th><th>更新时间</th></tr></thead>
        <tbody>
          {items.map(task => (
            <tr key={task.task_id} onClick={() => setSelected(task.task_id)} className="clickable">
              <td className="mono">{task.task_id}</td>
              <td><span className={`chip kind-${task.kind}`}>{kindLabel(task.kind)}</span></td>
              <td>{task.title}</td>
              <td>{statusLabel(task.kind, task.status)}</td>
              <td>{task.attempt}/{task.max_attempts}</td>
              <td className="muted">{formatTime(task.update_time)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {items.length === 0 && <p className="muted">没有任务。</p>}

      {open && (
        <Modal title="新建任务" onClose={() => setOpen(false)} footer={<button className="action action-primary" onClick={create}>创建</button>}>
          <div className="dialog-form">
            <label className="field">类型
              <select value={form.kind} onChange={e => setForm({ ...form, kind: e.target.value as TaskKind })}>
                {KINDS.map(value => <option key={value} value={value}>{kindLabel(value)}（{value}）</option>)}
              </select>
            </label>
            <label className="field">标题<input value={form.title} onChange={e => setForm({ ...form, title: e.target.value })} /></label>
            <label className="field">任务描述<textarea rows={4} value={form.description_md} onChange={e => setForm({ ...form, description_md: e.target.value })} /></label>
            <label className="field">任务上下文<textarea rows={3} value={form.context_md} onChange={e => setForm({ ...form, context_md: e.target.value })} /></label>
            <label className="field">前置任务（逗号分隔的 task_id）<input value={form.depends_on} onChange={e => setForm({ ...form, depends_on: e.target.value })} /></label>
          </div>
        </Modal>
      )}

      {selected !== '' && <TaskDetail taskId={selected} onClose={() => setSelected('')} onChanged={load} />}
    </section>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
