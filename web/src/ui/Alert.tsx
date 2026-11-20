// ui/Alert —— 错误条 / 提示条，design/components.md §ui/Alert
//
// 变体：danger（可重试的错误）/ info（说明与降级提示）/ warning / success。
// 状态：rest / focus-visible（内部动作按钮）/ loading（动作按钮进入 loading，如「重试中」）。
// 可访问性：danger 用 role="alert"（读屏立即播报），其余 role="status"；动作按钮有明确文案。
import type { ReactNode } from 'react'
import { Alert as MantineAlert } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { Button } from './Button'
import { AlertIcon, InfoIcon } from './icons'
import { cssVars, cx } from './internal'

export type AlertTone = 'danger' | 'info' | 'warning' | 'success'

export interface AlertProps {
  tone?: AlertTone
  /** 标题（一句话说明发生了什么） */
  title?: ReactNode
  children?: ReactNode
  /** 动作按钮文案（「重试」「在新标签打开原文」） */
  actionLabel?: string
  onAction?: () => void
  actionLoading?: boolean
  className?: string
}

export function Alert({
  tone = 'info',
  title,
  children,
  actionLabel,
  onAction,
  actionLoading = false,
  className,
}: AlertProps) {
  return (
    <MantineAlert
      className={cx('ui-alert', `ui-alert--${tone}`, className)}
      data-tone={tone}
      role={tone === 'danger' ? 'alert' : 'status'}
      variant="light"
      color={tone}
      title={title}
      radius="md"
      icon={tone === 'danger' ? <AlertIcon size={18} /> : <InfoIcon size={18} />}
      style={cssVars({
        '--alert-radius': cssVar('radius-8'),
        '--alert-bd': `${cssVar('border-width')} solid ${cssVar('border-subtle')}`,
      })}
    >
      <div className="ui-alert__body">
        <div className="ui-alert__text">{children}</div>
        {actionLabel && onAction ? (
          <Button variant="default" size="sm" loading={actionLoading} onClick={onAction}>
            {actionLabel}
          </Button>
        ) : null}
      </div>
    </MantineAlert>
  )
}
