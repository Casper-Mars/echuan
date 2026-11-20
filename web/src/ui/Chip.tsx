// ui/Chip —— mono 碎片（任务 id、租约信息），design/components.md §ui/Chip
//
// 仅 rest 一种状态；可点击时继承 focus-visible 焦点环。长 id 省略尾部并给 title（读屏/悬停可读全）。
import type { ReactNode } from 'react'
import { Pill } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export interface ChipProps {
  children: ReactNode
  /** 完整值（省略显示时告诉用户原文） */
  title?: string
  /** 是否等宽字体（任务 id / 租约用 true，普通碎片用 false） */
  mono?: boolean
  onClick?: () => void
  className?: string
}

export function Chip({ children, title, mono = true, onClick, className }: ChipProps) {
  return (
    <Pill
      className={cx('ui-chip', mono && 'ui-chip--mono', onClick && 'ui-chip--action', className)}
      title={title}
      onClick={onClick}
      style={cssVars({
        '--pill-height': cssVar('ui-pill-height'),
        '--pill-fz': cssVar('text-caption-size'),
        '--pill-radius': cssVar('radius-pill'),
      })}
    >
      {children}
    </Pill>
  )
}
