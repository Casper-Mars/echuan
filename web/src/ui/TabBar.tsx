// ui/TabBar —— 移动端一级导航（看板 / 收件箱 / 产品 / 更多），design/components.md §ui/TabBar（移动专属）
//
// 结构：图标 + 文字（必须都有文字，不允许只给图标）；当前项品牌色 + 加粗 + aria-current="page"。
// 状态：rest / active / focus-visible；hover 不适用（触屏），disabled / loading 不适用。
// 可访问性：单项点击区 ≥44×44（由令牌度量 ui-control-height-touch 保证）；底部留 safe-area-inset-bottom。
import type { ReactNode } from 'react'
import { Tabs } from '@mantine/core'
import { cx } from './internal'

export interface TabBarItem {
  key: string
  label: string
  icon: ReactNode
}

export interface TabBarProps {
  items: TabBarItem[]
  activeKey: string
  onChange: (key: string) => void
  /** 导航区的无障碍名称 */
  ariaLabel?: string
  className?: string
}

export function TabBar({ items, activeKey, onChange, ariaLabel = '主导航', className }: TabBarProps) {
  return (
    <Tabs
      className={cx('ui-tab-bar', className)}
      value={activeKey}
      onChange={(value) => onChange(value ?? items[0]?.key ?? '')}
      variant="unstyled"
      aria-label={ariaLabel}
    >
      <Tabs.List className="ui-tab-bar__list">
        {items.map((item) => (
          <Tabs.Tab
            key={item.key}
            value={item.key}
            className="ui-tab-bar__tab"
            leftSection={item.icon}
            aria-current={item.key === activeKey ? 'page' : undefined}
          >
            {item.label}
          </Tabs.Tab>
        ))}
      </Tabs.List>
    </Tabs>
  )
}
