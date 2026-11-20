// ui 层内部小工具（不从 index.ts 导出，避免把实现细节漏给页面层）
import type { CSSProperties } from 'react'

/** 自定义 CSS 变量集合：Mantine 的样式变量入口，值必须是 var(--…) 或令牌推导式 */
export type CssVars = Record<`--${string}`, string>

/** 把令牌变量交给 Mantine 的 style / styles 入口（用户 style 在 Mantine 变量之后合并，故必然生效） */
export const cssVars = (vars: CssVars): CSSProperties => vars as CSSProperties

/** style 属性用的混合写法：既要设置 Mantine 的 CSS 变量，也要设置普通 CSS 属性 */
export const cssProps = (props: CssVars & CSSProperties): CSSProperties => props as CSSProperties

export const cx = (...parts: Array<string | false | null | undefined>): string =>
  parts.filter(Boolean).join(' ')
