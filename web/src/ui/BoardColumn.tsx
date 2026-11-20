// ui/BoardColumn —— 看板单列容器（列头计数 + 折叠），design/components.md §ui/BoardColumn
//
// 结构：列头（状态名 · 计数 · 折叠按钮）→ 卡片栈（可滚动，ScrollArea）。
// 状态：rest / hover（列头折叠按钮 hover 才显形）/ active（折叠切换）/ focus-visible / loading（列内 2 个 SkeletonCard）。
// 可访问性：列头按钮 aria-expanded；折叠后仍保留「标题 + 计数」文案，读屏不会以为任务丢了。
import type { ReactNode } from 'react'
import { Paper, ScrollArea, UnstyledButton } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { ChevronDownIcon, ChevronRightIcon } from './icons'
import { SkeletonCard } from './SkeletonCard'
import { cx } from './internal'

export interface BoardColumnProps {
  title: string
  /** 状态名用于列头色条与语义色（in_review 列默认 accent） */
  status: string
  count: number
  collapsed?: boolean
  /** in_review 列：品牌描边 + 暖色渐变线 */
  accent?: boolean
  loading?: boolean
  /** 折叠/展开切换；不传则列头按钮禁用（纯展示） */
  onToggle?: (nextCollapsed: boolean) => void
  className?: string
  children?: ReactNode
}

export function BoardColumn({
  title,
  status,
  count,
  collapsed = false,
  accent = false,
  loading = false,
  onToggle,
  className,
  children,
}: BoardColumnProps) {
  const head = (
    <>
      <span className="ui-board-column__title">{title}</span>
      <span className="ui-board-column__count">{count}</span>
      <span className="ui-board-column__chevron" aria-hidden="true">
        {collapsed ? <ChevronRightIcon size={16} /> : <ChevronDownIcon size={16} />}
      </span>
    </>
  )

  if (collapsed) {
    return (
      <Paper
        className={cx('ui-board-column', 'ui-board-column--collapsed', className)}
        data-status={status}
        data-accent={accent || undefined}
        style={{ borderRadius: cssVar('radius-12') }}
      >
        <UnstyledButton
          className="ui-board-column__head"
          aria-expanded={false}
          aria-label={`${title}，${count} 项，已折叠`}
          disabled={!onToggle}
          onClick={() => onToggle?.(false)}
        >
          {head}
        </UnstyledButton>
      </Paper>
    )
  }

  return (
    <Paper
      className={cx('ui-board-column', className)}
      data-status={status}
      data-accent={accent || undefined}
      style={{ borderRadius: cssVar('radius-12') }}
    >
      <UnstyledButton
        className="ui-board-column__head"
        aria-expanded
        aria-label={`${title}，${count} 项，已展开`}
        disabled={!onToggle}
        onClick={() => onToggle?.(true)}
      >
        {head}
      </UnstyledButton>
      <ScrollArea className="ui-board-column__body" type="auto" offsetScrollbars>
        <div className="ui-board-column__stack">
          {loading ? (
            <>
              <SkeletonCard lines={2} />
              <SkeletonCard lines={2} />
            </>
          ) : (
            children
          )}
        </div>
      </ScrollArea>
    </Paper>
  )
}
