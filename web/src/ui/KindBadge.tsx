// ui/KindBadge —— 任务类型（需求/方案/拆解/开发），design/components.md §ui/KindBadge
//
// 类型不是状态：四种 kind 配色统一（描边 + 中性色），不给语义色，避免与状态色抢注意力。
import { Badge } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export type TaskKind = 'requirement' | 'design' | 'breakdown' | 'dev'

export const KIND_LABEL: Record<TaskKind, string> = {
  requirement: '需求',
  design: '方案',
  breakdown: '拆解',
  dev: '开发',
}

export interface KindBadgeProps {
  kind: TaskKind
  /** 覆盖默认两字标签 */
  label?: string
  className?: string
}

export function KindBadge({ kind, label, className }: KindBadgeProps) {
  return (
    <Badge
      component="span"
      className={cx('ui-kind-badge', className)}
      data-kind={kind}
      variant="default"
      radius="sm"
      style={cssVars({
        '--badge-height': cssVar('ui-pill-height'),
        '--badge-fz': cssVar('text-caption-size'),
        '--badge-padding-x': cssVar('space-2'),
        '--badge-radius': cssVar('radius-4'),
        '--badge-bg': cssVar('surface-overlay'),
        '--badge-color': cssVar('text-muted'),
        '--badge-bd': `${cssVar('border-width')} solid ${cssVar('border-subtle')}`,
      })}
    >
      {label ?? KIND_LABEL[kind]}
    </Badge>
  )
}
