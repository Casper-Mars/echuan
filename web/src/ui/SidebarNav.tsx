// ui/SidebarNav —— 桌面/平板侧栏导航，design/components.md §ui/SidebarNav
//
// 底层：AppShell.Navbar + NavLink。必须在 <AppShell> 内使用（Mantine 安全上下文约束）。
// 结构：桌面 240（有文字标签）；≤1024 收 64 图标栏，文字进 Tooltip（不允许只删文字不给替代）。
// 状态：rest / hover（淡底）/ active（左侧品牌竖条 + 淡底 + 加粗）/ focus-visible。
import type { ReactNode } from 'react'
import { AppShell, NavLink, Tooltip } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cx } from './internal'

export interface SidebarNavItem {
  key: string
  label: string
  icon: ReactNode
  /** 右侧计数（收件箱未处理数等） */
  count?: number
}

export interface SidebarNavProps {
  items: SidebarNavItem[]
  activeKey: string
  onSelect: (key: string) => void
  /** ≤1024 收起为图标栏；文字进 Tooltip */
  collapsed?: boolean
  /** 侧栏顶部品牌/标题区 */
  header?: ReactNode
  /** 侧栏底部（版本号、主题切换等） */
  footer?: ReactNode
  ariaLabel?: string
  className?: string
}

export function SidebarNav({
  items,
  activeKey,
  onSelect,
  collapsed = false,
  header,
  footer,
  ariaLabel = '侧边导航',
  className,
}: SidebarNavProps) {
  return (
    <AppShell.Navbar
      className={cx('ui-sidebar-nav', collapsed && 'ui-sidebar-nav--collapsed', className)}
      aria-label={ariaLabel}
      p={cssVar('space-3')}
      style={{
        backgroundColor: cssVar('surface-raised'),
        borderRight: `${cssVar('border-width')} solid ${cssVar('border-subtle')}`,
      }}
    >
      {header ? <div className="ui-sidebar-nav__header">{header}</div> : null}
      <nav className="ui-sidebar-nav__list">
        {items.map((item) => {
          const active = item.key === activeKey
          const link = (
            <NavLink
              key={item.key}
              className="ui-sidebar-nav__link"
              active={active}
              label={collapsed ? undefined : item.label}
              leftSection={item.icon}
              rightSection={!collapsed && item.count !== undefined ? String(item.count) : undefined}
              aria-label={collapsed ? item.label : undefined}
              aria-current={active ? 'page' : undefined}
              onClick={() => onSelect(item.key)}
              variant="subtle"
              color="primary"
            />
          )
          // 折叠态：文字进 Tooltip（design/components.md 反例：折叠后彻底无文字提示）
          return collapsed ? (
            <Tooltip key={item.key} label={item.label} position="right" withArrow>
              {link}
            </Tooltip>
          ) : (
            link
          )
        })}
      </nav>
      {footer ? <div className="ui-sidebar-nav__footer">{footer}</div> : null}
    </AppShell.Navbar>
  )
}
