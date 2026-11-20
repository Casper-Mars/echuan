// ui/StatusPill —— 7 种任务状态的唯一视觉表达（design/components.md §ui/StatusPill）
//
// 结构：圆点 + 文案（不只靠颜色区分，色盲用户靠文案）；非交互，无 hover/active/disabled 状态。
// 变体：neutral（pending/cancelled）/ info（ready/in_progress）/ warning（in_review）/ success（done）/ danger（failed）。
import { Badge } from '@mantine/core'
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

/** 默认中文文案；页面按 kind 定制时用 label 覆盖（与 lib/labels.ts 的 kind 维度保持一致） */
export const STATUS_LABEL: Record<TaskStatus, string> = {
  pending: '待领取',
  ready: '待开工',
  in_progress: '进行中',
  in_review: '待审核',
  done: '已完成',
  failed: '失败',
  cancelled: '已取消',
}

export interface StatusPillProps {
  status: TaskStatus
  /** 覆盖默认文案（如按 kind 显示「待设计」） */
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
      {label ?? STATUS_LABEL[status]}
    </Badge>
  )
}
