import { useEffect, useState } from 'react'
import { api, ApiError, materialURL } from '../api/client'
import type { AttachmentRef, Task } from '../api/types'
import { formatTime, kindLabel, statusLabel } from '../lib/labels'
import { Drawer } from './Drawer'
import { Markdown } from './Markdown'

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
        task_id: task.task_id, decision, material_id: materialId || undefined,
        decided_by: 'web', reason,
      })
      await load()
      onChanged()
    } catch (err) { setError(message(err)) }
  }

  return (
    <Drawer title={task?.title ?? taskId} onClose={onClose} footer={
      task ? (
        <div className="drawer-actions">
          {(task.status === 'pending' || task.status === 'ready') && (
            <button className="btn btn-ghost btn-danger" onClick={() => api.deleteTask(task.task_id).then(onClose).then(onChanged).catch(err => setError(message(err)))}>删除</button>
          )}
          {task.status === 'failed' && (
            <button className="btn btn-ghost" onClick={() => api.resetTask(task.task_id).then(load).then(onChanged).catch(err => setError(message(err)))}>重置为待处理</button>
          )}
        </div>
      ) : undefined
    }>
      {error && <div className="alert alert-error">{error}</div>}
      {task && (
        <div className="detail-stack">
          <div className="detail-hero">
            <div className="detail-hero-main">
              <span className={`kind-badge kind-${task.kind}`}>{kindLabel(task.kind)}</span>
              <span className={`status-pill status-${task.status}`}>{statusLabel(task.kind, task.status)}</span>
            </div>
            <p className="mono muted">{task.task_id} · {task.project_id}</p>
            <div className="detail-meta">
              <div><span>尝试</span><strong>{task.attempt}/{task.max_attempts}</strong></div>
              <div><span>优先级</span><strong>{task.priority}</strong></div>
              <div><span>更新时间</span><strong>{formatTime(task.update_time)}</strong></div>
            </div>
            {task.lease_id && (
              <div className="lease-banner">
                <span className="dot dot-ok" />
                <span className="mono">{task.lease_id}</span>
                <span>到期 {formatTime(task.lease_until)}</span>
                <span className="mono">{task.worker_id}</span>
              </div>
            )}
          </div>

          <section className="detail-block">
            <h3>任务描述</h3>
            <Markdown source={task.description_md} />
          </section>
          <section className="detail-block">
            <h3>任务上下文</h3>
            <Markdown source={task.context_md} />
          </section>
          {task.reason && (
            <section className="detail-block detail-reason">
              <h3>最近一次驳回 / 失败原因</h3>
              <p>{task.reason}</p>
            </section>
          )}

          <section className="detail-block">
            <h3>前置依赖</h3>
            {deps.length === 0 ? <p className="muted">无前置依赖</p> : (
              <div className="chip-row">{deps.map(dep => <span className="chip mono" key={dep}>{dep}</span>)}</div>
            )}
          </section>

          <section className="detail-block">
            <h3>附件</h3>
            {attachments.length === 0 ? <p className="muted">暂无附件</p> : (
              <ul className="attachment-list">
                {attachments.map(ref => (
                  <li key={`${ref.role}-${ref.material_id}`}>
                    <span className={`role-tag role-${ref.role}`}>{ref.role}</span>
                    <a href={materialURL(ref.material_id)} target="_blank" rel="noreferrer">{ref.title || ref.material_id}</a>
                    <span className="muted">{ref.kind}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          {task.status === 'in_review' && (
            <section className="detail-block review-block">
              <h3>评审</h3>
              <div className="segmented">
                <button className={decision === 'approve' ? 'active' : ''} onClick={() => setDecision('approve')}>通过</button>
                <button className={decision === 'reject' ? 'active danger' : ''} onClick={() => setDecision('reject')}>驳回</button>
              </div>
              <label className="field">被评审 material_id<input value={materialId} onChange={e => setMaterialId(e.target.value)} /></label>
              <label className="field">评审意见{decision === 'reject' ? '（必填）' : '（可选）'}
                <textarea rows={3} value={reason} onChange={e => setReason(e.target.value)} />
              </label>
              <button className="btn btn-primary" onClick={decide}>提交结论</button>
            </section>
          )}
        </div>
      )}
    </Drawer>
  )
}

function message(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}
