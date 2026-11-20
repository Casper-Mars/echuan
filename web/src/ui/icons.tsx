// ui 层内联图标（design/components.md：禁止用 ✕ 等字符冒充图标，避免字体缺失变豆腐块）
// 图标尺寸走数值属性（SVG 单位），颜色继承 currentColor，因此自动跟随令牌的文字色。
import type { ReactNode } from 'react'

interface GlyphProps {
  /** 图标边长（SVG 用户单位，非 CSS 长度） */
  size?: number
  className?: string
}

const Glyph = ({ size = 20, className, children }: GlyphProps & { children: ReactNode }) => (
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
    className={className}
  >
    {children}
  </svg>
)

export const ChevronDownIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="m6 9 6 6 6-6" />
  </Glyph>
)

export const ChevronRightIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="m9 6 6 6-6 6" />
  </Glyph>
)

export const CloseIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="M6 6l12 12M18 6 6 18" />
  </Glyph>
)

export const RefreshIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="M20 11a8 8 0 1 0-2.6 5.9" />
    <path d="M20 5v6h-6" />
  </Glyph>
)

export const AlertIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="M12 8v5" />
    <path d="M12 16.5v.5" />
    <path d="M12 3.5 21 20H3z" />
  </Glyph>
)

export const InfoIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M12 11v5" />
    <path d="M12 8v.5" />
  </Glyph>
)

export const BoardIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <rect x="3" y="4" width="5" height="16" rx="1.5" />
    <rect x="10" y="4" width="5" height="11" rx="1.5" />
    <rect x="17" y="4" width="4" height="7" rx="1.5" />
  </Glyph>
)

export const InboxIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="M4 13h4l2 3h4l2-3h4" />
    <path d="M4 13 6.5 5h11L20 13v5a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18z" />
  </Glyph>
)

export const ProductIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="m12 3 8 4.5v9L12 21l-8-4.5v-9z" />
    <path d="m4 7.5 8 4.5 8-4.5" />
    <path d="M12 12v9" />
  </Glyph>
)

export const MoreIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <circle cx="5" cy="12" r="1.4" />
    <circle cx="12" cy="12" r="1.4" />
    <circle cx="19" cy="12" r="1.4" />
  </Glyph>
)

export const AttachmentIcon = (props: GlyphProps) => (
  <Glyph {...props}>
    <path d="M15.5 7 8.7 13.8a3 3 0 0 0 4.2 4.2l7-7a5 5 0 0 0-7-7l-7 7" />
  </Glyph>
)
