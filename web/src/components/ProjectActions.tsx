import { useState } from 'react'
import { acceptProject, cancelProject } from '../api/client'
import type { Project, ProjectDecision } from '../api/types'
import { isPendingProjectStatus, projectStatusLabel } from '../lib/format'
import { ConfirmDialog } from './ConfirmDialog'
import { DecisionDialog } from './DecisionDialog'

export type ProjectActionRequest =
  | { type: 'accept' }
  | { type: 'cancel' }
  | { type: 'decide'; decision: ProjectDecision }

interface ProjectActionButtonsProps {
  project: Project
  onAction: (request: ProjectActionRequest) => void
  className?: string
}

/**
 * 纯按钮组：只负责“能做什么”，不持有弹窗。
 * 列表把弹窗提升到页面层，行因筛选/轮询消失时弹窗不会被卸载。
 */
export function ProjectActionButtons({ project, onAction, className }: ProjectActionButtonsProps) {
  const canAccept = project.status === 0
  const canDecide = isPendingProjectStatus(project.status)
  const canCancel = project.status >= 0 && project.status <= 5

  if (!canAccept && !canDecide && !canCancel) return null

  const trigger = (request: ProjectActionRequest) => (event: { stopPropagation: () => void }) => {
    event.stopPropagation()
    onAction(request)
  }

  return (
    <div className={`action-bar ${className ?? ''}`.trim()}>
      {canAccept && (
        <button type="button" className="action action-primary" onClick={trigger({ type: 'accept' })}>
          立项
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

/** 与按钮分离的弹窗宿主：可渲染在页面层或抽屉内，生命周期不受列表行影响。 */
export function ProjectActionDialogs({
  project,
  request,
  onClose,
  onChanged,
  onDecided,
}: ProjectActionDialogsProps) {
  if (!request) return null

  const target = {
    kind: 'project' as const,
    id: project.project_id,
    title: project.title,
    status: project.status,
    statusLabel: projectStatusLabel(project.status),
    version: project.version,
  }

  if (request.type === 'accept') {
    return (
      <ConfirmDialog
        title="立项确认"
        message={
          <>
            确认将项目 <span className="code">{project.project_id}</span>
            「{project.title}」立项？立项后进入规格评审。
          </>
        }
        confirmLabel="确认立项"
        onClose={onClose}
        onConfirm={async () => {
          const updated = await acceptProject(project.project_id)
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
            「{project.title}」？未完成的任务会一并取消，此操作不可撤销。
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

  return (
    <DecisionDialog
      target={target}
      decision={request.decision}
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
}

/** 自包含组合（抽屉内使用）：按钮 + 弹窗。 */
export function ProjectActions({ project, onChanged, onDecided, className }: ProjectActionsProps) {
  const [request, setRequest] = useState<ProjectActionRequest | null>(null)

  return (
    <>
      <ProjectActionButtons project={project} onAction={setRequest} className={className} />
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
