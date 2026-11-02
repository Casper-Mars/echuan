import { useState } from 'react'
import type { WorkItem, WorkItemDecision } from '../api/types'
import { workItemStateLabel } from '../lib/format'
import { DecisionDialog } from './DecisionDialog'

/** 卡片上的一个动作：决策值 + 展示文案 + 是否必填原因。 */
export interface WorkItemActionRequest {
  decision: WorkItemDecision
  label: string
  requiresReason: boolean
  danger: boolean
}

interface WorkItemActionButtonsProps {
  item: WorkItem
  onAction: (request: WorkItemActionRequest) => void
  className?: string
}

/**
 * 纯按钮组（按 state）：
 * - submitted（待验收）：验收 → accepted / 返工 → queued
 * - failed（已失败）：重排 → queued
 */
export function WorkItemActionButtons({ item, onAction, className }: WorkItemActionButtonsProps) {
  const canAccept = item.state === 'submitted'
  const canRetry = item.state === 'failed'
  if (!canAccept && !canRetry) return null

  const trigger = (request: WorkItemActionRequest) => (event: { stopPropagation: () => void }) => {
    event.stopPropagation()
    onAction(request)
  }

  return (
    <div className={`action-bar ${className ?? ''}`.trim()}>
      {canAccept && (
        <>
          <button
            type="button"
            className="action action-primary"
            onClick={trigger({ decision: 'approve', label: '验收', requiresReason: false, danger: false })}
          >
            验收
          </button>
          <button
            type="button"
            className="action action-danger"
            onClick={trigger({ decision: 'reject', label: '返工', requiresReason: true, danger: true })}
          >
            返工
          </button>
        </>
      )}
      {canRetry && (
        <button
          type="button"
          className="action action-primary"
          onClick={trigger({ decision: 'reject', label: '重排', requiresReason: false, danger: false })}
        >
          重排
        </button>
      )}
    </div>
  )
}

interface WorkItemActionDialogsProps {
  item: WorkItem
  request: WorkItemActionRequest | null
  onClose: () => void
  onChanged: () => void
}

/** 弹窗宿主：由页面或抽屉持有，卡片移动/卸载不会影响正在填写的 reason。 */
export function WorkItemActionDialogs({
  item,
  request,
  onClose,
  onChanged,
}: WorkItemActionDialogsProps) {
  if (!request) return null
  return (
    <DecisionDialog
      target={{
        kind: 'work_item',
        id: item.work_id,
        title: item.title,
        badgeClass: `state-${item.state}`,
        statusLabel: workItemStateLabel(item.state),
        version: item.version,
      }}
      decision={request.decision}
      label={request.label}
      requiresReason={request.requiresReason}
      danger={request.danger}
      onClose={onClose}
      onChanged={onChanged}
    />
  )
}

interface WorkItemActionsProps {
  item: WorkItem
  onChanged: () => void
  className?: string
}

/** 自包含组合（抽屉内使用）。 */
export function WorkItemActions({ item, onChanged, className }: WorkItemActionsProps) {
  const [request, setRequest] = useState<WorkItemActionRequest | null>(null)

  return (
    <>
      <WorkItemActionButtons item={item} onAction={setRequest} className={className} />
      <WorkItemActionDialogs
        item={item}
        request={request}
        onClose={() => setRequest(null)}
        onChanged={onChanged}
      />
    </>
  )
}
