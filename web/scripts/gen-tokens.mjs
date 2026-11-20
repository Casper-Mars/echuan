// echuan 设计令牌生成器
//
// 唯一职责：把 design/tokens.json（DTCG）编译成两个产物，让「令牌值只有一个来源」成立：
//   web/src/theme/tokens.css   CSS 自定义属性层（暗色 = 默认解析，亮色用 html[data-theme='light'] 覆写）
//   web/src/theme/tokens.ts    TS 令牌对象（Mantine theme 映射 / JS 侧度量取用）
//
// 用法：
//   node web/scripts/gen-tokens.mjs            # 生成（幂等：同样的输入必然产出逐字节相同的产物）
//   node web/scripts/gen-tokens.mjs --check    # 只校验产物是否与当前提交一致（不写盘）
//
// 主题口径（重要，AC4/AC5）：
//   * 暗色即默认：本文件只写一个根块（伪类根选择器），里面是 tokens.json 的暗色解析值；
//   * 亮色不是第二套设计，而是同一批变量在 html[data-theme='light'] 下的重新解析；
//   * 因此令牌层里没有任何媒体查询块内的自定义属性定义（响应式由组件层负责）。
//   * 旧 web/src/styles.css（三代视觉叠加，多个根块）本卡不动，由 T11 收口；
//     「全局样式层只剩一个根块」在本卡的口径 = 新增令牌层内恰好一个根块。
//
// 取值规则：
//   * $value 里的 "{a.b.c}" 别名递归解析到 primitive；
//   * 暗色档取 $extensions['dsh.modes'].dark，缺省回落到亮色 $value（design/tokens.json 的既定约定）；
//   * 颜色只暴露 semantic 角色，primitive 不直接出现在 CSS 里（只经 semantic 间接进入）。
//
// 结构常量（唯一一处不走 tokens.json 的值）：
//   --border-width 是「描边宽度」这种物理结构量，DTCG 色板/间距里没有对应 primitive；
//   web/src/ui/** 与 web/src/theme/base.css 不允许出现 px 字面量，所以它必须在这里作为令牌出现。
//   除此之外本生成器不引入任何非 tokens.json 的视觉值。

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const WEB_DIR = dirname(dirname(fileURLToPath(import.meta.url)))
const REPO_DIR = dirname(WEB_DIR)
const DESIGN_TOKENS = join(REPO_DIR, 'design', 'tokens.json')
const OUT_CSS = join(WEB_DIR, 'src', 'theme', 'tokens.css')
const OUT_TS = join(WEB_DIR, 'src', 'theme', 'tokens.ts')

const STRUCTURAL = {
  'border-width': '1px',
}

const source = JSON.parse(readFileSync(DESIGN_TOKENS, 'utf8'))

/* ---------- DTCG 解析 ---------- */

// 取节点（剥掉文件级 $description / $extensions）
const tokenRoot = source

function lookup(path) {
  let node = tokenRoot
  for (const segment of path) {
    if (node === undefined || node === null || typeof node !== 'object') return undefined
    node = node[segment]
  }
  return node
}

const isToken = (node) => node !== undefined && node !== null && typeof node === 'object' && '$value' in node
const isAlias = (value) => typeof value === 'string' && /^\{[^{}]+\}$/.test(value)

// 值层面的别名解引用：typography / shadow 这类复合令牌的内部字段也可能是 "{type.mono}" 形式
function deref(value, mode, seen = []) {
  if (isAlias(value)) {
    const path = value.slice(1, -1).split('.')
    const key = path.join('.')
    if (seen.includes(key)) throw new Error(`别名环：${[...seen, key].join(' -> ')}`)
    const target = lookup(path)
    if (target === undefined) throw new Error(`别名指向不存在的令牌：${key}`)
    return resolveNode(target, mode, [...seen, key])
  }
  if (Array.isArray(value)) return value.map((item) => deref(item, mode, seen))
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, deref(item, mode, seen)]))
  }
  return value
}

function resolveNode(node, mode, seen = []) {
  if (!isToken(node)) throw new Error(`不是令牌节点：${JSON.stringify(node).slice(0, 80)}`)
  const modeValue = node.$extensions?.['dsh.modes']?.[mode]
  const raw = modeValue !== undefined ? modeValue : node.$value
  return deref(raw, mode, seen)
}

function resolveAt(path, mode) {
  return resolveNode(lookup(path.split('.')), mode)
}

// 遍历某个分组下的叶子令牌：返回 [{ path: 'surface.canvas', node }]
function leaves(group, prefix = []) {
  const out = []
  for (const [key, value] of Object.entries(group)) {
    if (key.startsWith('$')) continue
    if (isToken(value)) out.push({ path: [...prefix, key].join('.'), node: value })
    else if (value && typeof value === 'object') out.push(...leaves(value, [...prefix, key]))
  }
  return out
}

/* ---------- CSS 值格式化 ---------- */

const IDENT = /^[a-zA-Z0-9-]+$/
const quoteFont = (family) => (IDENT.test(family) ? family : `"${family}"`)
const fontStack = (families) => (Array.isArray(families) ? families.map(quoteFont).join(', ') : String(families))
const px = (value) => (typeof value === 'number' ? `${value}px` : String(value))
const cssEasing = (points) => `cubic-bezier(${points.join(', ')})`
const cssShadow = (shadow) =>
  [px(shadow.offsetX), px(shadow.offsetY), px(shadow.blur), px(shadow.spread), shadow.color].join(' ')

/* ---------- 颜色（只暴露 semantic） ---------- */

const cssName = (path) => path.replaceAll('.', '-')

function semanticColorVars(mode) {
  const vars = []
  for (const leaf of leaves(tokenRoot.semantic)) {
    // 组件层用扁平命名：--surface-canvas / --text-primary（AC5 判据按这两个名字取值）
    vars.push([cssName(leaf.path), resolveAt(`semantic.${leaf.path}`, mode)])
  }
  return vars
}

/* ---------- 其余令牌（与主题无关，双主题同值） ---------- */

function staticVars() {
  const out = []
  const push = (name, value) => out.push([name, value])

  const sans = fontStack(resolveAt('type.sans', 'dark'))
  const mono = fontStack(resolveAt('type.mono', 'dark'))
  push('font-sans', sans)
  push('font-mono', mono)

  // typography 复合令牌的 fontFamily 是指向字体族令牌的别名；解析后与 --font-sans / --font-mono 同值时复用它
  const familyRef = (family) => {
    const css = fontStack(family)
    if (css === sans) return 'var(--font-sans)'
    if (css === mono) return 'var(--font-mono)'
    return css
  }

  for (const leaf of leaves(tokenRoot.type.scale)) {
    const value = resolveAt(`type.scale.${leaf.path}`, 'dark')
    push(`text-${leaf.path}-size`, px(value.fontSize))
    push(`text-${leaf.path}-weight`, String(value.fontWeight))
    push(`text-${leaf.path}-line-height`, String(value.lineHeight))
    push(`text-${leaf.path}-letter-spacing`, String(value.letterSpacing))
    if (value.fontFamily !== undefined) push(`text-${leaf.path}-font-family`, familyRef(value.fontFamily))
  }

  for (const leaf of leaves(tokenRoot.space)) push(`space-${leaf.path}`, resolveAt(`space.${leaf.path}`, 'dark'))
  for (const leaf of leaves(tokenRoot.radius)) push(`radius-${leaf.path}`, resolveAt(`radius.${leaf.path}`, 'dark'))
  for (const leaf of leaves(tokenRoot.shadow)) {
    push(`shadow-${leaf.path}`, cssShadow(resolveAt(`shadow.${leaf.path}`, 'dark')))
  }
  for (const leaf of leaves(tokenRoot.motion.duration)) {
    push(`duration-${leaf.path}`, resolveAt(`motion.duration.${leaf.path}`, 'dark'))
  }
  for (const leaf of leaves(tokenRoot.motion.easing)) {
    push(`easing-${leaf.path}`, cssEasing(resolveAt(`motion.easing.${leaf.path}`, 'dark')))
  }
  for (const leaf of leaves(tokenRoot.breakpoint)) push(`breakpoint-${leaf.path}`, resolveAt(`breakpoint.${leaf.path}`, 'dark'))
  for (const leaf of leaves(tokenRoot.z)) push(`z-${leaf.path}`, String(resolveAt(`z.${leaf.path}`, 'dark')))

  for (const [name, value] of Object.entries(STRUCTURAL)) push(name, value)
  return out
}

/* ---------- 产物 1：tokens.css ---------- */

function renderCss() {
  const lines = []
  lines.push('/* 由 web/scripts/gen-tokens.mjs 从 design/tokens.json 生成，请勿手改。')
  lines.push(' * 生成命令：node web/scripts/gen-tokens.mjs')
  lines.push(' *')
  lines.push(' * 主题口径：暗色即默认——下方唯一的根块写的是 tokens.json 的暗色解析值；')
  lines.push(" * 亮色不是第二套设计，而是同一批变量的重新解析，写在 html[data-theme='light'] 里。")
  lines.push(' * 令牌层口径（AC4）：新增令牌层内恰好一个根块，且没有任何媒体查询块内的自定义属性定义。')
  lines.push(' * 旧 web/src/styles.css（三代视觉叠加，多个根块）本卡不动，由 T11 收口。')
  lines.push(' *')
  lines.push(' * 颜色只暴露 semantic 角色；primitive 只经 semantic 间接进入 CSS。')
  lines.push(' * --border-width 是结构常量（描边宽度，tokens.json 无对应 primitive），是本文件唯一非令牌值。')
  lines.push(' */')
  lines.push('')

  const dark = [...semanticColorVars('dark'), ...staticVars()]
  lines.push(':root {')
  lines.push('  /* semantic 颜色（暗色解析 = 默认） */')
  let inStatic = false
  for (const [name, value] of dark) {
    if (!inStatic && name === 'font-sans') {
      lines.push('')
      lines.push('  /* 字体与字阶 */')
      inStatic = true
    }
    lines.push(`  --${name}: ${value};`)
  }
  lines.push('}')
  lines.push('')

  lines.push("html[data-theme='light'] {")
  lines.push('  /* semantic 颜色（亮色解析） */')
  for (const [name, value] of semanticColorVars('light')) lines.push(`  --${name}: ${value};`)
  lines.push('}')
  lines.push('')
  return lines.join('\n')
}

/* ---------- 产物 2：tokens.ts ---------- */

const objectLiteral = (entries, indent) => {
  const pad = ' '.repeat(indent)
  const body = entries.map(([key, value]) => `${pad}  ${JSON.stringify(key)}: ${value},`).join('\n')
  return `{\n${body}\n${pad}}`
}

function renderTs() {
  const lines = []
  lines.push('// 由 web/scripts/gen-tokens.mjs 从 design/tokens.json 生成，请勿手改。')
  lines.push('// 生成命令：node web/scripts/gen-tokens.mjs')
  lines.push('//')
  lines.push('// 暗色即默认：dark* 是默认解析值，light* 是同一批令牌的亮色解析（供 html[data-theme=\'light\'] 与对照）。')
  lines.push('// CSS 侧一律走 tokens.css 的 var(--…)，本文件供 Mantine theme 映射与 JS 侧度量取用。')
  lines.push('')

  lines.push("export const DEFAULT_THEME_MODE = 'dark' as const")
  lines.push("export type ThemeMode = 'dark' | 'light'")
  lines.push('')

  const semanticEntries = (mode) =>
    semanticColorVars(mode).map(([name, value]) => [name, JSON.stringify(value)])
  lines.push('/** semantic 颜色角色（暗色解析，默认） */')
  lines.push(`export const semanticDark = ${objectLiteral(semanticEntries('dark'), 0)} as const`)
  lines.push('')
  lines.push('/** semantic 颜色角色（亮色解析） */')
  lines.push(`export const semanticLight = ${objectLiteral(semanticEntries('light'), 0)} as const`)
  lines.push('')

  const palette = (mode) => {
    const acc = {}
    for (const leaf of leaves(tokenRoot.color)) {
      const [group, step] = leaf.path.split('.')
      const value = JSON.stringify(resolveAt(`color.${leaf.path}`, mode))
      if (step === undefined) {
        acc[group] = value
        continue
      }
      acc[group] ??= {}
      acc[group][step] = value
    }
    return acc
  }
  const renderPalette = (mode) => {
    const data = palette(mode)
    const groups = Object.entries(data).map(([group, value]) =>
      typeof value === 'string' ? [group, value] : [group, objectLiteral(Object.entries(value), 2)],
    )
    return objectLiteral(groups, 0)
  }
  lines.push('/** primitive 色阶（暗色解析）：Mantine 色阶元组从这里取，页面层不得直接使用 */')
  lines.push(`export const paletteDark = ${renderPalette('dark')} as const`)
  lines.push('')
  lines.push('/** primitive 色阶（亮色解析） */')
  lines.push(`export const paletteLight = ${renderPalette('light')} as const`)
  lines.push('')

  const flatAt = (base, group) =>
    leaves(group).map((leaf) => [leaf.path, JSON.stringify(resolveAt(`${base}.${leaf.path}`, 'dark'))])

  lines.push('/** 字体族 */')
  lines.push(
    `export const fontFamily = ${objectLiteral(
      [
        ['sans', JSON.stringify(fontStack(resolveAt('type.sans', 'dark')))],
        ['mono', JSON.stringify(fontStack(resolveAt('type.mono', 'dark')))],
      ],
      0,
    )} as const`,
  )
  lines.push('')

  lines.push('/** 字阶 */')
  const scaleEntries = leaves(tokenRoot.type.scale).map((leaf) => {
    const value = resolveAt(`type.scale.${leaf.path}`, 'dark')
    const fields = [
      ['fontSize', JSON.stringify(px(value.fontSize))],
      ['fontWeight', String(value.fontWeight)],
      ['lineHeight', String(value.lineHeight)],
      ['letterSpacing', JSON.stringify(String(value.letterSpacing))],
    ]
    if (value.fontFamily !== undefined) {
      fields.push(['fontFamily', JSON.stringify(fontStack(value.fontFamily))])
    }
    return [leaf.path, objectLiteral(fields, 2)]
  })
  lines.push(`export const typeScale = ${objectLiteral(scaleEntries, 0)} as const`)
  lines.push('')

  lines.push('/** 间距 */')
  lines.push(`export const space = ${objectLiteral(flatAt('space', tokenRoot.space), 0)} as const`)
  lines.push('')
  lines.push('/** 圆角 */')
  lines.push(`export const radius = ${objectLiteral(flatAt('radius', tokenRoot.radius), 0)} as const`)
  lines.push('')
  lines.push('/** 阴影 */')
  lines.push(
    `export const shadow = ${objectLiteral(
      leaves(tokenRoot.shadow).map((leaf) => [
        leaf.path,
        JSON.stringify(cssShadow(resolveAt(`shadow.${leaf.path}`, 'dark'))),
      ]),
      0,
    )} as const`,
  )
  lines.push('')

  lines.push('/** 动效 */')
  const motionEntries = [
    ...leaves(tokenRoot.motion.duration).map((leaf) => [
      `duration-${leaf.path}`,
      JSON.stringify(resolveAt(`motion.duration.${leaf.path}`, 'dark')),
    ]),
    ...leaves(tokenRoot.motion.easing).map((leaf) => [
      `easing-${leaf.path}`,
      JSON.stringify(cssEasing(resolveAt(`motion.easing.${leaf.path}`, 'dark'))),
    ]),
  ]
  lines.push(`export const motion = ${objectLiteral(motionEntries, 0)} as const`)
  lines.push('')

  lines.push('/** 断点（px 数值：Mantine breakpoints 需要自己换算成 em） */')
  lines.push(
    `export const breakpoint = ${objectLiteral(
      leaves(tokenRoot.breakpoint).map((leaf) => [
        leaf.path,
        String(Number.parseInt(resolveAt(`breakpoint.${leaf.path}`, 'dark'), 10)),
      ]),
      0,
    )} as const`,
  )
  lines.push('')
  lines.push('/** z-index 层 */')
  lines.push(
    `export const zIndex = ${objectLiteral(
      leaves(tokenRoot.z).map((leaf) => [leaf.path, String(resolveAt(`z.${leaf.path}`, 'dark'))]),
      0,
    )} as const`,
  )
  lines.push('')

  lines.push('/** 结构常量（唯一非 tokens.json 值，见 gen-tokens.mjs 头部说明） */')
  lines.push(
    `export const structural = ${objectLiteral(
      Object.entries(STRUCTURAL).map(([key, value]) => [key, JSON.stringify(value)]),
      0,
    )} as const`,
  )
  lines.push('')

  lines.push('/** 令牌名 → CSS 变量名，供组件层拼 var() */')
  lines.push("export const cssVar = (name: string) => `var(--${name})`")
  lines.push('')
  return lines.join('\n')
}

/* ---------- 写盘 / 校验 ---------- */

const artifacts = [
  [OUT_CSS, renderCss()],
  [OUT_TS, renderTs()],
]

const checkOnly = process.argv.includes('--check')
let drift = 0

for (const [path, content] of artifacts) {
  if (checkOnly) {
    let current = ''
    try {
      current = readFileSync(path, 'utf8')
    } catch {
      current = ''
    }
    if (current !== content) {
      drift += 1
      console.error(`[tokens] 漂移：${path} 与 design/tokens.json 不一致`)
    } else {
      console.log(`[tokens] 一致：${path}`)
    }
  } else {
    mkdirSync(dirname(path), { recursive: true })
    writeFileSync(path, content, 'utf8')
    console.log(`[tokens] 写出：${path}`)
  }
}

if (checkOnly) {
  if (drift > 0) process.exit(1)
  console.log('[tokens] 产物与设计令牌源一致')
}
