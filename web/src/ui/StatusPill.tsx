// ui/StatusPill —— 7 种任务状态的唯一视觉表达（design/components.md §ui/StatusPill）
//
// 结构：圆点 + 文案（不只靠颜色区分，色盲用户靠文案）；非交互，无 hover/active/disabled 状态。
// 变体：neutral（pending/cancelled）/ info（ready/in_progress）/ warning（in_review）/ success（done）/ danger（failed）。
import { Badge } from '@mantine/core'
import { statusLabel } from '../lib/labels'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export type TaskStatus =
  | 'pending'
  | 'ready'
  | 'in_progress'
  | 'in_review'
  | 'done'
  | 'failed'
  | 'cancelled'

export type StatusTone = 'neutral' | 'info' | 'warning' | 'success' | 'danger'

/** 状态 → 语义色（token 名，见 theme/mantine-theme.ts 的 variantColorResolver） */
export const STATUS_TONE: Record<TaskStatus, StatusTone> = {
  pending: 'neutral',
  ready: 'info',
  in_progress: 'info',
  in_review: 'warning',
  done: 'success',
  failed: 'danger',
  cancelled: 'neutral',
}

export interface StatusPillProps {
  status: TaskStatus
  /**
   * 覆盖文案（项目「进行中 / 已关闭」、执行节点「忙碌 / 空闲」这类非任务状态语义）。
   *
   * 不传时走 `lib/labels.ts` 的单一状态表 —— 任务状态文案只有一份真源，
   * 本组件不再自带第二张默认表（返工 D1/S1：两套文案分叉就是从这里开始的）。
   */
  label?: string
  className?: string
}

export function StatusPill({ status, label, className }: StatusPillProps) {
  const tone = STATUS_TONE[status]
  return (
    <Badge
      component="span"
      className={cx('ui-status-pill', `ui-status-pill--${tone}`, className)}
      data-status={status}
      variant="light"
      color={tone}
      radius="pill"
      leftSection={<span className="ui-status-pill__dot" aria-hidden="true" />}
      style={cssVars({
        '--badge-height': cssVar('ui-pill-height'),
        '--badge-fz': cssVar('text-caption-size'),
        '--badge-padding-x': cssVar('space-3'),
        '--badge-radius': cssVar('radius-pill'),
      })}
    >
      {label ?? statusLabel(status)}
    </Badge>
  )
}
