// ui/IconButton —— 无语义文案的操作（关闭抽屉、返回、刷新），design/components.md §ui/IconButton
//
// 状态：rest / hover / focus-visible / disabled；aria-label 强制必填。
// 点击区：sm 32 / md 40 / touch 44（移动端触控目标），全部由令牌推导。
import type { MouseEvent, ReactNode } from 'react'
import { ActionIcon } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export type IconButtonVariant = 'ghost' | 'default' | 'danger'
export type IconButtonSize = 'sm' | 'md' | 'touch'

export interface IconButtonProps {
  /** 无障碍名称（必填，读屏唯一入口） */
  label: string
  icon: ReactNode
  variant?: IconButtonVariant
  size?: IconButtonSize
  loading?: boolean
  disabled?: boolean
  title?: string
  className?: string
  onClick?: (event: MouseEvent<HTMLButtonElement>) => void
}

const VARIANT_MAP: Record<IconButtonVariant, { variant: string; color?: string }> = {
  ghost: { variant: 'subtle', color: 'primary' },
  default: { variant: 'default' },
  danger: { variant: 'outline', color: 'danger' },
}

const SIZE_MAP: Record<IconButtonSize, string> = {
  sm: cssVar('ui-control-height-sm'),
  md: cssVar('ui-icon-box'),
  touch: cssVar('ui-control-height-touch'),
}

export function IconButton({
  label,
  icon,
  variant = 'ghost',
  size = 'md',
  loading = false,
  disabled = false,
  title,
  className,
  onClick,
}: IconButtonProps) {
  const mapped = VARIANT_MAP[variant]
  return (
    <ActionIcon
      className={cx('ui-icon-button', `ui-icon-button--${variant}`, className)}
      variant={mapped.variant}
      color={mapped.color}
      aria-label={label}
      title={title}
      loading={loading}
      disabled={disabled}
      onClick={onClick}
      style={cssVars({
        '--ai-size': SIZE_MAP[size],
        '--ai-radius': cssVar('radius-8'),
      })}
    >
      {icon}
    </ActionIcon>
  )
}
