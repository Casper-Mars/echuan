import { useEffect, useState } from 'react'
import {
  ApiError,
  cancelProject,
  completeProject,
  getProject,
  submitProject,
} from '../api/client'
import type { Project, ProjectDecision } from '../api/types'
import {
  isDecidableProjectStatus,
  projectStatusLabel,
  unsettledWorkItemCount,
} from '../lib/format'
import { ConfirmDialog } from './ConfirmDialog'
import { DecisionDialog } from './DecisionDialog'
import type { ReviewedMaterial } from './ReviewedMaterial'

export type ProjectActionRequest =
  | { type: 'submit' }
  | { type: 'cancel' }
  | { type: 'complete' }
  | { type: 'decide'; decision: ProjectDecision }

interface ProjectActionButtonsProps {
  project: Project
  onAction: (request: ProjectActionRequest) => void
  className?: string
  /**
   * 是否渲染「完成项目」入口。只有详情抽屉打开它；列表行不暴露，
   * 避免在长列表里误点终态操作。仅 project.status === 6 时出现。
   */
  showComplete?: boolean
}

/**
 * 纯按钮组：只负责“能做什么”，不持有弹窗。
 * 列表把弹窗提升到页面层，行因筛选/轮询消失时弹窗不会被卸载。
 */
export function ProjectActionButtons({
  project,
  onAction,
  className,
  showComplete = false,
}: ProjectActionButtonsProps) {
  const canSubmit = project.status === 0 || project.status === 2
  // 开发任务评审（5）的决策在工作项看板完成，不能走项目级批准/驳回。
  const canDecide = isDecidableProjectStatus(project.status)
  const canCancel = project.status >= 0 && project.status <= 6
  // 6→7 的唯一入口：只有 pending_done 才谈得上「完成项目」。
  const canComplete = showComplete && project.status === 6
  const unsettled = unsettledWorkItemCount(project.work_item_by_state)

  if (!canSubmit && !canDecide && !canCancel && !canComplete) return null

  const trigger = (request: ProjectActionRequest) => (event: { stopPropagation: () => void }) => {
    event.stopPropagation()
    onAction(request)
  }

  return (
    <div className={`action-bar ${className ?? ''}`.trim()}>
      {canSubmit && (
        <button type="button" className="action action-primary" onClick={trigger({ type: 'submit' })}>
          提交评审
        </button>
      )}
      {canDecide && (
        <>
          <button
            type="button"
            className="action action-primary"
            onClick={trigger({ type: 'decide', decision: 'approve' })}
          >
            批准
          </button>
          <button
            type="button"
            className="action action-danger"
            onClick={trigger({ type: 'decide', decision: 'reject' })}
          >
            驳回
          </button>
        </>
      )}
      {canComplete && (
        <span className="action-complete">
          <button
            type="button"
            className="action action-primary"
            onClick={trigger({ type: 'complete' })}
            disabled={unsettled > 0}
            title={
              unsettled > 0
                ? `仍有 ${unsettled} 张任务未结清，结清后才能完成项目`
                : '确认任务已全部验收后，人工把项目置为已完成'
            }
          >
            完成项目
          </button>
          {unsettled > 0 && (
            <span className="action-hint" role="note">
              仍有 {unsettled} 张任务未结清
            </span>
          )}
        </span>
      )}
      {canCancel && (
        <button type="button" className="action" onClick={trigger({ type: 'cancel' })}>
          取消
        </button>
      )}
    </div>
  )
}

interface ProjectActionDialogsProps {
  project: Project
  request: ProjectActionRequest | null
  onClose: () => void
  onChanged: () => void
  onDecided?: (project: Project) => void
}

/**
 * 「完成项目」失败的可见文案：409 一律不自动重试，按 code 区分。
 *
 * - invalid_state：原样展示服务端 message（含未结清数量），随后 onStale 刷新项目；
 * - invalid_version：明确提示版本已过期（服务端原文带新旧版本号），随后刷新项目。
 */
function completeErrorMessage(error: unknown): unknown {
  if (error instanceof ApiError) {
    if (error.code === 'invalid_state') return new Error(error.message)
    if (error.code === 'invalid_version') {
      return new Error(`版本已过期，项目已刷新，请核对后重试。${error.message}`)
    }
  }
  return error
}

/** 与按钮分离的弹窗宿主：可渲染在页面层或抽屉内，生命周期不受列表行影响。 */
export function ProjectActionDialogs({
  project,
  request,
  onClose,
  onChanged,
  onDecided,
}: ProjectActionDialogsProps) {
  if (!request) return null

  if (request.type === 'submit') {
    return (
      <ConfirmDialog
        title="提交评审"
        message={
          <>
            确认将项目 <span className="code">{project.project_id}</span>
            「{project.title}」提交评审？需求草稿将进入需求评审，方案草稿将进入方案评审。
          </>
        }
        confirmLabel="确认提交"
        onClose={onClose}
        onConfirm={async () => {
          // 列表行不含 requirement 字段：需求草稿提交前先读详情做前置校验，
          // 与后端 409 的文案保持一致，缺需求时不发送 submit 请求。
          if (project.status === 0) {
            const detail = await getProject(project.project_id)
            if (!detail.requirement) {
              throw new Error('请先上传需求文档')
            }
          }
          const updated = await submitProject(project.project_id)
          onDecided?.(updated)
          onChanged()
        }}
        onStale={onChanged}
      />
    )
  }

  if (request.type === 'cancel') {
    return (
      <ConfirmDialog
        title="取消项目"
        message={
          <>
            确认取消项目 <span className="code">{project.project_id}</span>
            「{project.title}」？未完成的工作项会一并取消，此操作不可撤销。
          </>
        }
        confirmLabel="确认取消"
        danger
        onClose={onClose}
        onConfirm={async () => {
          const updated = await cancelProject(project.project_id)
          onDecided?.(updated)
          onChanged()
        }}
        onStale={onChanged}
      />
    )
  }

  if (request.type === 'complete') {
    return (
      <ConfirmDialog
        title="完成项目"
        message={
          <>
            确认人工完成项目 <span className="code">{project.project_id}</span>
            「{project.title}」？完成任务验收不会推进项目状态，这里才把项目置为「已完成」。
          </>
        }
        confirmLabel="确认完成"
        onClose={onClose}
        onConfirm={async () => {
          try {
            const updated = await completeProject(project.project_id, project.version)
            onDecided?.(updated)
            onChanged()
          } catch (error) {
            // 409 不自动重试：转成可读文案后交给 ConfirmDialog 展示，
            // onStale 再刷新一次项目，让用户看到最新状态与未结清数量。
            throw completeErrorMessage(error)
          }
        }}
        onStale={onChanged}
      />
    )
  }

  return (
    <ProjectDecisionDialog
      project={project}
      decision={request.decision}
      onClose={onClose}
      onChanged={onChanged}
      onDecided={onDecided}
    />
  )
}

/**
 * 项目审批弹窗：列表行不含 requirement/spec，所以先读一次详情，
 * 把当前状态对应的资料（需求或方案）作为「被审资料」传给 DecisionDialog。
 * 详情读取失败时仍可审批，只是不展示版本。
 */
function ProjectDecisionDialog({
  project,
  decision,
  onClose,
  onChanged,
  onDecided,
}: {
  project: Project
  decision: ProjectDecision
  onClose: () => void
  onChanged: () => void
  onDecided?: (project: Project) => void
}) {
  const [material, setMaterial] = useState<ReviewedMaterial | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    getProject(project.project_id)
      .then((detail) => {
        if (cancelled) return
        const reviewed =
          project.status === 1 ? detail.requirement : project.status === 3 ? detail.spec : null
        setMaterial(
          reviewed
            ? { materialId: reviewed.material_id, version: reviewed.version, title: reviewed.title }
            : null,
        )
      })
      .catch(() => {
        if (!cancelled) setMaterial(null)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [project.project_id, project.status])

  return (
    <DecisionDialog
      target={{
        kind: 'project',
        id: project.project_id,
        title: project.title,
        badgeClass: `status-${project.status}`,
        statusLabel: projectStatusLabel(project.status),
        version: project.version,
        material,
        materialLoading: loading,
      }}
      decision={decision}
      onClose={onClose}
      onChanged={onChanged}
      onDecided={onDecided}
    />
  )
}

interface ProjectActionsProps {
  project: Project
  onChanged: () => void
  onDecided?: (project: Project) => void
  className?: string
  /** 详情抽屉打开「完成项目」入口；列表行保持关闭（见 ProjectActionButtons）。 */
  showComplete?: boolean
}

/** 自包含组合（抽屉内使用）：按钮 + 弹窗。 */
export function ProjectActions({
  project,
  onChanged,
  onDecided,
  className,
  showComplete = false,
}: ProjectActionsProps) {
  const [request, setRequest] = useState<ProjectActionRequest | null>(null)

  return (
    <>
      <ProjectActionButtons
        project={project}
        onAction={setRequest}
        className={className}
        showComplete={showComplete}
      />
      <ProjectActionDialogs
        project={project}
        request={request}
        onClose={() => setRequest(null)}
        onChanged={onChanged}
        onDecided={onDecided}
      />
    </>
  )
}
