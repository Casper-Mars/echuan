// features/shell/nav —— 一级导航定义（侧栏 4 项 + 移动 TabBar 4 项）与当前项判定
//
// 结构来源：design/refs/board__{desktop,tablet,mobile}__dark__default.png
//   侧栏（桌面/平板）：看板 / 收件箱 / 产品 / 执行节点 —— 底部一行版本备注。
//   移动 TabBar：看板 / 收件箱 / 产品 / 更多（「更多」把侧栏放不下的入口收进二级层，
//   见 design/components.md §ui/TabBar「图标 + 文字（必须都有文字）」）。
//
// 图标为什么写在这里：ui/index.ts 只导出 21 个组件（scripts/ui-imports-check.mjs 会断言导出集合），
// 内联图标（ui/icons.tsx）不在导出面内，壳层不能从 ui 内部文件 import。故壳层自带同一套描边字形，
// 颜色一律 currentColor，跟随令牌文字色。已列入「未覆盖项」交 T11 收口（图标下沉到 ui 导出面）。
import type { ReactNode } from 'react'

interface GlyphProps {
  size?: number
}

const Glyph = ({ size = 18, children }: GlyphProps & { children: ReactNode }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth={1.75}
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    {children}
  </svg>
)

/** 看板：三列纵条，对应 7 状态列的信息架构 */
export const BoardIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <rect x="3" y="4" width="5" height="16" rx="1.5" />
    <rect x="10" y="4" width="5" height="11" rx="1.5" />
    <rect x="17" y="4" width="4" height="16" rx="1.5" />
  </Glyph>
)

/** 收件箱：托盘轮廓，对应「需要我处理的事」 */
export const InboxIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="M4 13.5V6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v7.5" />
    <path d="M4 13.5h4l1.5 2.5h5l1.5-2.5h4" />
    <path d="M4 13.5V18a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4.5" />
  </Glyph>
)

/** 产品：分层堆叠，对应产品 → 项目 → 工作项的下钻结构 */
export const ProductsIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="M3 7.5 12 3l9 4.5-9 4.5-9-4.5Z" />
    <path d="M3 12l9 4.5 9-4.5" />
    <path d="M3 16.5 12 21l9-4.5" />
  </Glyph>
)

/** 执行节点：芯片轮廓 */
export const WorkersIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <rect x="7" y="7" width="10" height="10" rx="2" />
    <path d="M9.5 3v4M14.5 3v4M9.5 17v4M14.5 17v4M3 9.5h4M3 14.5h4M17 9.5h4M17 14.5h4" />
  </Glyph>
)

/** 更多：三点省略（移动 TabBar 第 4 项） */
export const MoreIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <circle cx="5" cy="12" r="1.4" />
    <circle cx="12" cy="12" r="1.4" />
    <circle cx="19" cy="12" r="1.4" />
  </Glyph>
)

/** 刷新：环形箭头（顶栏手动刷新，见 design/refs 顶栏右上角「刷新」） */
export const RefreshIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="M20 11a8 8 0 1 0-2.6 5.9" />
    <path d="M20 5v6h-6" />
  </Glyph>
)

export type NavKey = 'board' | 'inbox' | 'products' | 'workers' | 'more'

export interface NavDescriptor {
  key: NavKey
  label: string
  icon: ReactNode
}

/** 侧栏（桌面 / 平板）四项，顺序与 design/refs 一致 */
export const SIDEBAR_NAV: NavDescriptor[] = [
  { key: 'board', label: '看板', icon: <BoardIcon /> },
  { key: 'inbox', label: '收件箱', icon: <InboxIcon /> },
  { key: 'products', label: '产品', icon: <ProductsIcon /> },
  { key: 'workers', label: '执行节点', icon: <WorkersIcon /> },
]

/** 移动 TabBar 四项：第 4 项是「更多」（design/components.md §ui/TabBar） */
export const TAB_NAV: NavDescriptor[] = [
  { key: 'board', label: '看板', icon: <BoardIcon /> },
  { key: 'inbox', label: '收件箱', icon: <InboxIcon /> },
  { key: 'products', label: '产品', icon: <ProductsIcon /> },
  { key: 'more', label: '更多', icon: <MoreIcon /> },
]

/** 「更多」二级层里的入口（侧栏有、TabBar 放不下） */
export const MORE_NAV: NavDescriptor[] = [
  { key: 'workers', label: '执行节点', icon: <WorkersIcon /> },
]

/** 路径 → 高亮项。前缀匹配与旧侧栏一致；`/queue` 保留给旧书签，不在新导航里高亮。 */
export function activeKeyOf(pathname: string): NavKey | '' {
  if (pathname === '/inbox' || pathname.startsWith('/inbox/')) return 'inbox'
  if (pathname === '/products' || pathname.startsWith('/products/')) return 'products'
  if (pathname === '/workers' || pathname.startsWith('/workers/')) return 'workers'
  if (pathname === '/projects' || pathname.startsWith('/projects/')) return 'board'
  return ''
}
