import type { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'

interface NavItem {
  to: string
  label: string
  icon: ReactNode
}

const iconProps = {
  width: 18,
  height: 18,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.8,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
} as const

/** 产品：分层堆叠，对应产品 → 项目 → 任务的下钻结构。 */
const productIcon = (
  <svg {...iconProps} aria-hidden="true">
    <path d="M3 7.5 12 3l9 4.5-9 4.5-9-4.5Z" />
    <path d="M3 12l9 4.5 9-4.5" />
    <path d="M3 16.5 12 21l9-4.5" />
  </svg>
)

/** Worker：芯片轮廓，对应执行节点。 */
const workerIcon = (
  <svg {...iconProps} aria-hidden="true">
    <rect x="7" y="7" width="10" height="10" rx="2" />
    <path d="M9.5 3v4M14.5 3v4M9.5 17v4M14.5 17v4M3 9.5h4M3 14.5h4M17 9.5h4M17 14.5h4" />
  </svg>
)

const items: NavItem[] = [
  { to: '/products', label: '产品', icon: productIcon },
  { to: '/workers', label: 'Worker', icon: workerIcon },
]

/**
 * 左侧主导航：仅两个入口，不带徽标/计数/状态灯。
 * 高亮完全交给 NavLink 的 isActive（/products 为前缀匹配，覆盖三级下钻）。
 * 窄屏（<1024px）由 CSS 收成 56px 图标栏，label 隐藏，title/aria-label 保留可读性。
 */
export function Sidebar() {
  return (
    <aside className="sidebar" aria-label="主导航">
      <nav className="sidebar-nav">
        {items.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            title={item.label}
            aria-label={item.label}
            className={({ isActive }) => (isActive ? 'sidebar-link active' : 'sidebar-link')}
          >
            <span className="sidebar-icon">{item.icon}</span>
            <span className="sidebar-label">{item.label}</span>
          </NavLink>
        ))}
      </nav>
    </aside>
  )
}
