import { useState, type FormEvent } from 'react'
import {
  ApiError,
  decideProject,
  decideWorkItem,
  errorMessage,
  getProject,
  getWorkItem,
} from '../api/client'
import type { Project, ProjectDecision, WorkItemDecision } from '../api/types'
import { projectStatusLabel, workItemStateLabel } from '../lib/format'
import { Modal } from './Modal'
import { ReviewedMaterialLine, type ReviewedMaterial } from './ReviewedMaterial'

export type DecisionValue = ProjectDecision | WorkItemDecision

export interface DecisionTarget {
  kind: 'project' | 'work_item'
  id: string
  title: string
  /** 徽标样式类：项目用 status-N，工作项用 state-<state>。 */
  badgeClass: string
  statusLabel: string
  version: number
  /** 本次审批所依据的资料；项目按当前状态取 requirement/spec，单卡取 result。 */
  material?: ReviewedMaterial | null
  /** 详情请求尚未返回时置 true，避免把「加载中」误显示为「未绑定」。 */
  materialLoading?: boolean
}

const DECISION_LABEL: Record<DecisionValue, string> = {
  approve: '批准',
  reject: '驳回',
}

/** 后端同样要求 reason 的项目驳回；前端先做必填拦截。 */
export function decisionRequiresReason(decision: DecisionValue): boolean {
  return decision === 'reject'
}

interface DecisionDialogProps {
  target: DecisionTarget
  decision: DecisionValue
  /** 动作文案（批准/驳回/验收/返工/重排）；缺省按决策值推导。 */
  label?: string
  /** 是否强制填写原因；缺省时 reject 必填、approve 可选。 */
  requiresReason?: boolean
  /** 提交按钮是否使用危险样式；缺省时 reject 为危险色。 */
  danger?: boolean
  onClose: () => void
  /** 提交成功或数据冲突后通知父级立即重拉列表。 */
  onChanged: () => void
  /** 项目裁决成功后的最新项目，供列表按新状态调整筛选。 */
  onDecided?: (project: Project) => void
}

/**
 * 决策弹窗：展示对象编号/标题、当前状态与本次 If-Match 版本；
 * 验收/返工/重排/驳回等动作复用同一套提交与 409 处理。
 *
 * 409 处理：
 * - invalid_version → 提示“数据已变化”，自动重拉最新版本，保留已填 reason；
 * - invalid_state → 提示当前状态不允许该操作，并刷新最新状态。
 */
export function DecisionDialog({
  target,
  decision,
  label: labelProp,
  requiresReason: requiresReasonProp,
  danger: dangerProp,
  onClose,
  onChanged,
  onDecided,
}: DecisionDialogProps) {
  const [current, setCurrent] = useState<DecisionTarget>(target)
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const label = labelProp ?? DECISION_LABEL[decision]
  const requiresReason = requiresReasonProp ?? decisionRequiresReason(decision)
  const danger = dangerProp ?? decision === 'reject'

  async function reloadLatest() {
    try {
      if (current.kind === 'project') {
        const latest = await getProject(current.id)
        const reviewed = latest.status === 3 ? latest.spec : latest.requirement
        setCurrent({
          kind: 'project',
          id: latest.project_id,
          title: latest.title,
          badgeClass: `status-${latest.status}`,
          statusLabel: projectStatusLabel(latest.status),
          version: latest.version,
          material: reviewed
            ? { materialId: reviewed.material_id, version: reviewed.version, title: reviewed.title }
            : null,
        })
      } else {
        const latest = await getWorkItem(current.id)
        setCurrent({
          kind: 'work_item',
          id: latest.work_id,
          title: latest.title,
          badgeClass: `state-${latest.state}`,
          statusLabel: workItemStateLabel(latest.state),
          version: latest.version,
          material: latest.result
            ? {
                materialId: latest.result.material_id,
                version: latest.result.version,
                title: latest.result.title,
              }
            : null,
        })
      }
    } catch {
      // 重拉失败时保留原 target；错误提示已经展示给用户。
    }
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting) return
    if (requiresReason && !reason.trim()) {
      setError('请填写原因后重试。')
      return
    }
    setSubmitting(true)
    setError(null)
    setNotice(null)
    try {
      if (current.kind === 'project') {
        const updated = await decideProject(
          current.id,
          decision as ProjectDecision,
          reason.trim(),
          current.version,
        )
        onDecided?.(updated)
      } else {
        await decideWorkItem(current.id, decision as WorkItemDecision, reason.trim())
      }
      onChanged()
      onClose()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        if (err.code === 'invalid_version') {
          setError(`${errorMessage(err)} 已重新加载最新数据，请核对后再次提交。`)
        } else if (err.code === 'invalid_state') {
          setError(`${errorMessage(err)} 已刷新最新状态。`)
        } else {
          setError(errorMessage(err))
        }
        // 保留 reason，不清空用户输入；只把状态与版本换成最新。
        await reloadLatest()
        onChanged()
        setNotice('已自动刷新最新数据，你填写的原因仍然保留。')
        return
      }
      setError(errorMessage(err))
    } finally {
      setSubmitting(false)
    }
  }

  const targetName = current.kind === 'project' ? '项目' : '工作项'

  return (
    <Modal title={`${label}${targetName}`} onClose={onClose} closeDisabled={submitting}>
      <form className="dialog-form" onSubmit={submit}>
        <div className="dialog-target">
          <div className="dialog-target-title">
            <span className="code">{current.id}</span>
            <strong>{current.title || '（未命名）'}</strong>
          </div>
          <div className="dialog-target-meta">
            <span className={`badge ${current.badgeClass}`}>{current.statusLabel}</span>
            <span className="mono">本次提交依据第 {current.version} 版</span>
          </div>
          <ReviewedMaterialLine
            material={current.material ?? target.material ?? null}
            loading={current.materialLoading ?? target.materialLoading ?? false}
          />
        </div>

        <label className="field">
          <span>
            原因 {requiresReason ? <em className="required">（必填）</em> : <em className="optional">（可选）</em>}
          </span>
          <textarea
            rows={4}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            placeholder={
              requiresReason ? `请说明${label}的原因，会记录在对象上` : '可补充说明（可留空）'
            }
            autoFocus
            disabled={submitting}
          />
        </label>

        {requiresReason && !reason.trim() && (
          <p className="muted dialog-hint">请说明{label}的原因，方便后续查看记录。</p>
        )}
        {notice && (
          <p className="dialog-notice" role="status">
            {notice}
          </p>
        )}
        {error && (
          <p className="dialog-error" role="alert">
            {error}
          </p>
        )}

        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={submitting}>
            取消
          </button>
          <button
            type="submit"
            className={`action ${danger ? 'action-danger' : 'action-primary'}`}
            disabled={submitting}
          >
            {submitting ? '提交中…' : `确定${label}`}
          </button>
        </div>
      </form>
    </Modal>
  )
}
