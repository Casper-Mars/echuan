// ui/Button —— 页面唯一按钮入口（design/components.md §ui/Button）
//
// 变体：primary（实心）/ default（描边）/ ghost（无底）/ danger（红描边）。
// 状态：rest / hover / active / focus-visible / disabled / loading 全部由 Mantine 状态属性 + 令牌承接；
// 几何量（高度、内边距、圆角、字号）用令牌变量覆盖 Mantine 内置尺寸表，见下方 cssVars。
import type { MouseEvent, ReactNode } from 'react'
import { Button as MantineButton } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export type ButtonVariant = 'primary' | 'default' | 'ghost' | 'danger'
export type ControlSize = 'sm' | 'md'

export interface ButtonProps {
  /** 按钮文案（必填；纯图标请用 IconButton） */
  children?: ReactNode
  variant?: ButtonVariant
  /** md = 36（桌面）/ 44（移动，由 .ui-button 的指针媒体规则提升）；sm = 32 */
  size?: ControlSize
  leftSection?: ReactNode
  rightSection?: ReactNode
  /** 主操作右侧计数（如「全部 12」） */
  count?: number
  loading?: boolean
  disabled?: boolean
  fullWidth?: boolean
  type?: 'button' | 'submit' | 'reset'
  /** 打开弹层时自动聚焦：同时给 Mantine FocusTrap 的 data-autofocus 目标 */
  autoFocus?: boolean
  title?: string
  onClick?: (event: MouseEvent<HTMLButtonElement>) => void
  className?: string
}

const VARIANT_MAP: Record<ButtonVariant, { variant: string; color?: string }> = {
  primary: { variant: 'filled', color: 'primary' },
  default: { variant: 'default' },
  ghost: { variant: 'subtle', color: 'primary' },
  danger: { variant: 'outline', color: 'danger' },
}

export function Button({
  children,
  variant = 'primary',
  size = 'md',
  leftSection,
  rightSection,
  count,
  loading = false,
  disabled = false,
  fullWidth,
  type = 'button',
  autoFocus,
  title,
  onClick,
  className,
}: ButtonProps) {
  const mapped = VARIANT_MAP[variant]
  return (
    <MantineButton
      className={cx('ui-button', `ui-button--${variant}`, className)}
      data-ui-size={size}
      variant={mapped.variant}
      color={mapped.color}
      type={type}
      autoFocus={autoFocus}
      data-autofocus={autoFocus || undefined}
      title={title}
      loading={loading}
      disabled={disabled}
      fullWidth={fullWidth}
      leftSection={leftSection}
      rightSection={count !== undefined ? <span className="ui-button__count">{count}</span> : rightSection}
      aria-busy={loading || undefined}
      onClick={onClick}
      style={cssVars({
        '--button-height': size === 'sm' ? cssVar('ui-control-height-sm') : cssVar('ui-control-height-md'),
        '--button-padding-x': cssVar('space-4'),
        '--button-fz': size === 'sm' ? cssVar('text-small-size') : cssVar('text-body-size'),
        '--button-radius': cssVar('radius-8'),
      })}
    >
      {children}
    </MantineButton>
  )
}
