// ui/Segmented —— 互斥二选一（评审「通过 / 驳回」），design/components.md §ui/Segmented
//
// 状态：rest / hover / active（选中项白底 + 轻阴影）/ focus-visible / disabled（提交中）/ invalid（意见为空时由 TextArea 标红）。
// 可访问性：radiogroup 语义 + 左右键切换（SegmentedControl 提供）。
import { SegmentedControl } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export interface SegmentedOption {
  value: string
  label: string
}

export interface SegmentedProps {
  options: SegmentedOption[]
  value: string
  onChange: (value: string) => void
  /** 提交中：整组禁用 */
  disabled?: boolean
  /** 提交中：禁用 + 外部按钮 loading（本组件只负责禁用态） */
  loading?: boolean
  /** danger：驳回态（红色语义，仅用于被驳回的选项） */
  tone?: 'default' | 'danger'
  /** radiogroup 的无障碍名称 */
  ariaLabel: string
  className?: string
}

export function Segmented({
  options,
  value,
  onChange,
  disabled = false,
  loading = false,
  tone = 'default',
  ariaLabel,
  className,
}: SegmentedProps) {
  return (
    <SegmentedControl
      className={cx('ui-segmented', `ui-segmented--${tone}`, className)}
      data={options}
      value={value}
      onChange={onChange}
      disabled={disabled || loading}
      aria-label={ariaLabel}
      radius="md"
      style={cssVars({
        '--sc-padding': cssVar('space-1'),
        '--sc-font-size': cssVar('text-small-size'),
      })}
    />
  )
}
