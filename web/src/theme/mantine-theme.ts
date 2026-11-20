// Mantine 8 theme ← design/tokens.json
//
// 这一层只做一件事：把生成物 theme/tokens.ts 的令牌映射成 Mantine theme，
// 让 Mantine 组件的默认字体/字号/间距/圆角/断点/颜色全部回到令牌上，组件层不再自带尺寸常量。
//
// 约定（重要）：
//   * 尺寸类令牌一律写成 var(--…) 引用，而不是内联 px —— 令牌层是运行时唯一来源，
//     Mantine 变量（--mantine-spacing-md 等）因此直接指向 tokens.css；
//   * 颜色分两路：Mantine 内部变量由 theme/base.css 的桥接块指向语义令牌（随亮暗切换），
//     需要「按语义色点名」的组件（Badge/ActionIcon/Button 等）由下面的 variantColorResolver 翻译成令牌；
//   * tokens.ts 里保存的是解析后的具体值（hex / px），只用于 Mantine 需要的确定性入参（色阶元组、断点）。

import {
  createTheme,
  defaultVariantColorsResolver,
  type MantineColorsTuple,
  type MantineThemeOverride,
  type VariantColorResolverResult,
  type VariantColorsResolver,
} from '@mantine/core'
import { breakpoint, cssVar, paletteDark } from './tokens'

// 设计令牌是 12 阶（1 最浅、12 最深），Mantine 需要 10 阶元组（索引 0 最浅）
const toTuple = (scale: Record<string, string>): MantineColorsTuple => {
  const values = Object.keys(scale)
    .sort((a, b) => Number(a) - Number(b))
    .map((step) => scale[step])
  return values.slice(0, 10) as unknown as MantineColorsTuple
}

// 断点：Mantine 用 em（媒体查询里不能用 var()），由令牌 px 值按 16px 根字号换算
const toEm = (px: number) => `${px / 16}em`

// 语义色别名：组件只允许点这些名字，具体值全部落到 var(--…)
export type SemanticAccent = 'primary' | 'danger' | 'warning' | 'success' | 'info' | 'neutral'

const ACCENT_SOLID: Record<string, string> = {
  primary: cssVar('action-primary'),
  danger: cssVar('feedback-danger'),
  warning: cssVar('feedback-warning'),
  success: cssVar('feedback-success'),
  info: cssVar('feedback-info'),
  neutral: cssVar('border-strong'),
}

const ACCENT_TEXT: Record<string, string> = {
  primary: cssVar('action-primary-text'),
  danger: cssVar('feedback-danger'),
  warning: cssVar('feedback-warning'),
  success: cssVar('feedback-success'),
  info: cssVar('feedback-info'),
  neutral: cssVar('text-muted'),
}

const accentOf = (color: string | undefined) => (color ? color.split('.')[0] : 'primary')
const solidOf = (color: string | undefined) => ACCENT_SOLID[accentOf(color)] ?? ACCENT_SOLID.primary
const textOf = (color: string | undefined) => ACCENT_TEXT[accentOf(color)] ?? ACCENT_TEXT.primary

const transparentBorder = `${cssVar('border-width')} solid transparent`
const mix = (color: string, percent: number, base = 'transparent') =>
  `color-mix(in srgb, ${color} ${percent}%, ${base})`

/**
 * 变体 → 令牌的翻译表。
 * 只翻译组件层实际用到的变体；其余（white / gradient 等）沿用 Mantine 默认实现，
 * 避免把 Mantine 的完整设计系统在项目里重写一遍。
 */
const variantColorResolver: VariantColorsResolver = (input): VariantColorResolverResult => {
  const { variant, color } = input
  const solid = solidOf(color)
  const soft = textOf(color)

  switch (variant) {
    case 'filled':
      return {
        background: solid,
        hover: accentOf(color) === 'primary' ? cssVar('action-primary-hover') : mix(solid, 88, cssVar('surface-canvas')),
        color: cssVar('text-on-action'),
        border: transparentBorder,
      }
    case 'light':
      return {
        background: mix(solid, 16),
        hover: mix(solid, 24),
        color: soft,
        border: transparentBorder,
      }
    case 'outline':
      return {
        background: 'transparent',
        hover: mix(solid, 14),
        color: soft,
        border: `${cssVar('border-width')} solid ${solid}`,
      }
    case 'subtle':
      return {
        background: 'transparent',
        hover: mix(solid, 12, cssVar('surface-overlay')),
        color: soft,
        border: transparentBorder,
      }
    case 'default':
      return {
        background: cssVar('surface-raised'),
        hover: cssVar('surface-overlay'),
        color: cssVar('text-primary'),
        border: `${cssVar('border-width')} solid ${cssVar('border-default')}`,
      }
    default:
      return defaultVariantColorsResolver(input)
  }
}

export const mantineTheme: MantineThemeOverride = createTheme({
  primaryColor: 'brand',
  // 品牌色 9 阶 = #2f5bea（亮）/ #3d6bff（暗），对应 10 阶元组的索引 8
  primaryShade: { light: 8, dark: 8 },
  colors: {
    brand: toTuple(paletteDark.brand),
    slate: toTuple(paletteDark.slate),
    jade: toTuple(paletteDark.jade),
    amber: toTuple(paletteDark.amber),
    red: toTuple(paletteDark.red),
    sky: toTuple(paletteDark.sky),
    dark: toTuple(paletteDark.slate),
  },
  white: paletteDark.white,
  black: paletteDark.black,
  variantColorResolver,

  fontFamily: cssVar('font-sans'),
  fontFamilyMonospace: cssVar('font-mono'),
  fontSizes: {
    xs: cssVar('text-caption-size'),
    sm: cssVar('text-small-size'),
    md: cssVar('text-body-size'),
    lg: cssVar('text-h3-size'),
    xl: cssVar('text-h2-size'),
  },
  lineHeights: {
    xs: cssVar('text-caption-line-height'),
    sm: cssVar('text-small-line-height'),
    md: cssVar('text-body-line-height'),
    lg: cssVar('text-h3-line-height'),
    xl: cssVar('text-h2-line-height'),
  },
  headings: {
    fontFamily: cssVar('font-sans'),
    fontWeight: cssVar('text-h1-weight'),
    sizes: {
      h1: { fontSize: cssVar('text-h1-size'), lineHeight: cssVar('text-h1-line-height') },
      h2: { fontSize: cssVar('text-h2-size'), lineHeight: cssVar('text-h2-line-height') },
      h3: { fontSize: cssVar('text-h3-size'), lineHeight: cssVar('text-h3-line-height') },
      h4: { fontSize: cssVar('text-body-strong-size'), lineHeight: cssVar('text-body-strong-line-height') },
      h5: { fontSize: cssVar('text-body-size'), lineHeight: cssVar('text-body-line-height') },
      h6: { fontSize: cssVar('text-small-size'), lineHeight: cssVar('text-small-line-height') },
    },
  },
  spacing: {
    xs: cssVar('space-2'),
    sm: cssVar('space-3'),
    md: cssVar('space-4'),
    lg: cssVar('space-5'),
    xl: cssVar('space-6'),
  },
  radius: {
    xs: cssVar('radius-2'),
    sm: cssVar('radius-4'),
    md: cssVar('radius-8'),
    lg: cssVar('radius-12'),
    xl: cssVar('radius-16'),
  },
  defaultRadius: 'md',
  shadows: {
    xs: cssVar('shadow-e1'),
    sm: cssVar('shadow-e2'),
    md: cssVar('shadow-e2'),
    lg: cssVar('shadow-e3'),
    xl: cssVar('shadow-e3'),
  },
  breakpoints: {
    // xs 由 sm 折半推导：令牌只定义 sm/md/lg/xl（design/tokens.json: breakpoint）
    xs: toEm(breakpoint.sm / 2),
    sm: toEm(breakpoint.sm),
    md: toEm(breakpoint.md),
    lg: toEm(breakpoint.lg),
    xl: toEm(breakpoint.xl),
  },
  focusRing: 'auto',
  cursorType: 'pointer',
})

export default mantineTheme
