import { useEffect, useState } from 'react'
import { api, ApiError, materialURL } from '../api/client'
import type { AttachmentRef, Task } from '../api/types'
import { formatTime, kindLabel, statusLabel } from '../lib/labels'
import { Markdown } from './Markdown'
import { Modal } from './Modal'

export function TaskDetail({ taskId, onClose, onChanged }: { taskId: string; onClose: () => void; onChanged: () => void }) {
  const [task, setTask] = useState<Task | null>(null)
  const [deps, setDeps] = useState<string[]>([])
  const [attachments, setAttachments] = useState<AttachmentRef[]>([])
  const [error, setError] = useState('')
  const [decision, setDecision] = useState<'approve' | 'reject'>('approve')
  const [reason, setReason] = useState('')
  const [materialId, setMaterialId] = useState('')

  const load = async () => {
    try {
      const current = await api.getTask(taskId)
      setTask(current)
      setDeps((await api.dependencies(taskId)).items)
      const refs = await api.attachments(taskId)
      setAttachments(refs.items)
      const output = refs.items.find(ref => ref.role === 'output')
      if (output) setMaterialId(output.material_id)
    } catch (err) { setError(message(err)) }
  }
  useEffect(() => { void load() }, [taskId])

  const decide = async () => {
    if (!task) return
    try {
      await api.decideTask({
        task_id: task.task_id,
        decision,
        material_id: materialId || undefined,
        decided_by: 'web',
        reason,
      })
      await load()
      onChanged()
    } catch (err) { setError(message(err)) }
  }

  return (
    <Modal title={`任务 ${taskId}`} onClose={onClose} wide>
      {error && <div className="notice notice-message">{error}</div>}
      {task && (
        <div className="v3-detail">
          <div className="v3-detail-grid">
            <div><span className="muted">类型</span><strong>{kindLabel(task.kind)}</strong></div>
            <div><span className="muted">状态</span><strong>{statusLabel(task.kind, task.status)}</strong></div>
            <div><span className="muted">尝试</span><strong>{task.attempt}/{task.max_attempts}</strong></div>
            <div><span className="muted">优先级</span><strong>{task.priority}</strong></div>
            <div><span className="muted">项目</span><strong className="mono">{task.project_id}</strong></div>
            <div><span className="muted">更新时间</span><strong>{formatTime(task.update_time)}</strong></div>
          </div>

          {(task.status === 'in_progress' || task.lease_id) && (
            <div className="chip chip-current">
              租约 {task.lease_id ?? '—'} · 到期 {formatTime(task.lease_until)} · worker {task.worker_id ?? '—'}
            </div>
          )}

          <section className="detail-section">
            <h3>任务描述</h3>
            <Markdown source={task.description_md} />
          </section>
          <section className="detail-section">
            <h3>任务上下文</h3>
            <Markdown source={task.context_md} />
          </section>
          {task.reason && (
            <section className="detail-section">
              <h3>最近一次驳回 / 失败原因</h3>
              <p className="reason-text">{task.reason}</p>
            </section>
          )}

          <section className="detail-section">
            <h3>前置依赖</h3>
            {deps.length === 0 ? <p className="muted">无</p> : <div className="chips">{deps.map(dep => <span className="chip mono" key={dep}>{dep}</span>)}</div>}
          </section>

          <section className="detail-section">
            <h3>附件</h3>
            {attachments.length === 0 ? <p className="muted">无</p> : (
              <ul className="list">
                {attachments.map(ref => (
                  <li key={`${ref.role}-${ref.material_id}`}>
                    <span className={`chip ${ref.role === 'output' ? 'dot-ok' : ''}`}>{ref.role}</span>{' '}
                    <a className="link" href={materialURL(ref.material_id)} target="_blank" rel="noreferrer">{ref.title || ref.material_id}</a>
                    <span className="muted"> · {ref.kind}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          {task.status === 'in_review' && (
            <section className="detail-section">
              <h3>评审</h3>
              <div className="dialog-form">
                <label className="field">结论
                  <select value={decision} onChange={e => setDecision(e.target.value as 'approve' | 'reject')}>
                    <option value="approve">approve</option>
                    <option value="reject">reject</option>
                  </select>
                </label>
                <label className="field">被评审 material_id<input value={materialId} onChange={e => setMaterialId(e.target.value)} /></label>
                <label className="field">原因（reject 必填）<textarea rows={3} value={reason} onChange={e => setReason(e.target.value)} /></label>
                <button className="action action-primary" onClick={decide}>提交结论</button>
              </div>
            </section>
          )}

          {task.status === 'failed' && (
            <button className="action" onClick={() => api.resetTask(task.task_id).then(load).then(onChanged).catch(err => setError(message(err)))}>重置为待处理</button>
          )}
          {(task.status === 'pending' || task.status === 'ready') && (
            <button className="action action-danger" onClick={() => api.deleteTask(task.task_id).then(onClose).then(onChanged).catch(err => setError(message(err)))}>删除任务</button>
          )}
        </div>
      )}
    </Modal>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
