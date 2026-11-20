// ui/EmptyState —— 空列表 / 空附件 / 空队列，design/components.md §ui/EmptyState
//
// 结构：标题 → 一句说明（为什么空 + 下一步）→ 主操作（虚线描边容器）。
// 状态：仅 rest（除内部按钮）；不适用 disabled / loading / invalid。
// 可访问性：标题用 <p> + 视觉层级，不用装饰性插图（平台不存二进制资产）。
import type { ReactNode } from 'react'
import { Stack, Text } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { Button, type ButtonVariant } from './Button'
import { cssVars, cx } from './internal'

export interface EmptyStateProps {
  title: string
  /** 为什么空 + 下一步 */
  description?: string
  /** 主操作（如「新建任务」）；不传则只展示说明 */
  actionLabel?: string
  onAction?: () => void
  actionVariant?: ButtonVariant
  /** 错误态：标题换成错误文案时用 tone="danger" 给容器加语义描边 */
  tone?: 'neutral' | 'danger'
  /** 附加内容（如错误详情 / 兜底链接） */
  children?: ReactNode
  className?: string
}

export function EmptyState({
  title,
  description,
  actionLabel,
  onAction,
  actionVariant = 'primary',
  tone = 'neutral',
  children,
  className,
}: EmptyStateProps) {
  return (
    <Stack
      className={cx('ui-empty-state', className)}
      data-tone={tone}
      align="center"
      justify="center"
      gap={cssVar('space-3')}
      style={cssVars({ '--empty-state-radius': cssVar('radius-12') })}
    >
      <Text component="p" className="ui-empty-state__title">
        {title}
      </Text>
      {description ? (
        <Text component="p" className="ui-empty-state__description">
          {description}
        </Text>
      ) : null}
      {children}
      {actionLabel && onAction ? (
        <Button variant={actionVariant} size="md" onClick={onAction}>
          {actionLabel}
        </Button>
      ) : null}
    </Stack>
  )
}
