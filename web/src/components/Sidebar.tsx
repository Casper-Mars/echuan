import type { ReactNode } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { recallMenuPath } from '../lib/menuMemory'

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
  { to: '/workers', label: '执行节点', icon: workerIcon },
]

/**
 * 左侧主导航：仅两个入口，不带徽标/计数/状态灯。
 *
 * - 点击回跳：已在本入口内时指向当前路径（不会把用户拉回列表），
 *   否则指向该入口最后停留的路径；记忆失效（产品/项目被删）时
 *   由页面自身的 404 回退负责，最终收敛到入口列表。
 * - 高亮：只看当前 pathname 的前缀，与“记忆停在哪”无关。
 *   因此用 Link + 字符串 className + 手动 aria-current：
 *   NavLink 会用自身 isActive 覆盖 aria-current 并自动追加 active 类，
 *   而 isActive 又是根据 `to`（可能来自记忆）算的，正好是要避免的耦合。
 * - 窄屏（<1024px）由 CSS 收成 56px 图标栏，label 隐藏，title/aria-label 保留可读性。
 */
export function Sidebar() {
  const { pathname } = useLocation()

  return (
    <aside className="sidebar" aria-label="主导航">
      <nav className="sidebar-nav">
        {items.map((item) => {
          const active = pathname === item.to || pathname.startsWith(`${item.to}/`)
          return (
            <Link
              key={item.to}
              to={active ? pathname : recallMenuPath(item.to, item.to)}
              title={item.label}
              aria-label={item.label}
              aria-current={active ? 'page' : undefined}
              className={active ? 'sidebar-link active' : 'sidebar-link'}
            >
              <span className="sidebar-icon">{item.icon}</span>
              <span className="sidebar-label">{item.label}</span>
            </Link>
          )
        })}
      </nav>
    </aside>
  )
}
