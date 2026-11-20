// echuan 前端「样式债」量化门禁
//
// 只做一件事：扫 web/src 下的全部 CSS 与引用方，算 6 项指标 → 逐项判阈值 → 打印 → 退出码。
// 已串进 `make web`（在 `pnpm --dir web build` 前跑），因此「删旧视觉层」这件事有可复跑的判据，
// 不靠 review 时的肉眼印象。体积门禁（dist 产物体积）在 scripts/check-size.mjs，两者不重叠。
//
// 用法：
//   node web/scripts/style-debt.mjs        # 6 项指标 + 阈值判定
//   STYLE_DEBT_UNUSED_MAX=25 node web/scripts/style-debt.mjs   # 覆盖「未引用类名占比」阈值（演示失败路径）
//
// 口径（6 项指标的统计范围与判据，逐条写死在这里）：
//   扫描面 = web/src/**/*.css（删掉旧 web/src/styles.css 后只剩 theme/*.css + ui/ui.css + features/*/*.css）；
//   引用面 = web/src/**/*.{ts,tsx} + web/index.html（类名在这些文本里「出现过」即算被引用；
//            动态拼接的类名（cx(...) / 模板串）同样能被这条口径覆盖，代价是它偏宽松——宁可漏判死类，
//            不把活类误判成死类）。
//
//   ① 未引用类名占比 = 未被引用类名数 / 已定义类名总数，阈值 < 20%
//   ② 重复定义选择器 = 同一「层叠上下文」（顶层，或同一个 @media 块）内同一条选择器出现 > 1 次的条数，阈值 = 0
//   ③ 顶层 :root 令牌源 = 不在任何 @media / @supports 内的 `:root` 规则数，阈值 = 1；
//      媒体查询内令牌覆写 = **令牌层**（theme/*.css）的 @media 块内出现自定义属性（--x: …）声明的条数，阈值 = 0；
//      组件/页面在 @media 内调自己的局部变量（--button-height / --bp-column-w 等）不算令牌覆写，单列展示
//   ④ 全局样式层行数 = theme/*.css 的物理行数之和，阈值 ≤ 300 行。
//      为什么「全局样式层」只算 theme/*.css：它是页面级底座（令牌 + 元素级规则 + 画布/氛围光/网格），
//      不绑定任何组件或页面；ui/ui.css 是组件层、features/*/*.css 是页面层，两者按文件单列在输出里
//      （给出行数但不计入本门禁，否则「8 个页面 + 21 个组件的样式」不可能压在 300 行内）。
//   ⑤ 断点套数 = 全部 @media 断点值的去重集合必须落在唯一一套里（design/tokens.json 的
//      breakpoint.sm 768 = 47.99em、breakpoint.md 1024 = 63.99em 这两个 CSS 侧的取值），阈值 = 1 套
//   ⑥ 死类子系统 grep = `.board-column* / .row-* / .notice-* / .state-panel / .spinner` 在扫描面里的命中数，阈值 = 0
//
// 退出码：0 全部达标；1 有指标超标（逐条打印当前值与阈值）；2 用法/环境错误（扫描面为空等）。

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const WEB_DIR = dirname(dirname(fileURLToPath(import.meta.url)))
const SRC_DIR = join(WEB_DIR, 'src')

const UNUSED_RATIO_MAX = readRatio('STYLE_DEBT_UNUSED_MAX', 0.2)
const GLOBAL_CSS_LINE_MAX = 300
const DEAD_CLASS_PATTERNS = [
  { name: 'board-column', re: /\.board-column/g },
  { name: 'row-', re: /\.row-/g },
  { name: 'notice-', re: /\.notice-/g },
  { name: 'state-panel', re: /\.state-panel/g },
  { name: 'spinner', re: /\.spinner/g },
]
/** 允许的断点集合：tokens 的 sm/md 在 CSS 侧的取值（768 → 47.99em、1024 → 63.99em），单位 em */
const ALLOWED_BREAKPOINTS = new Set(['47.99em', '63.99em'])

function readRatio(name, fallback) {
  const raw = process.env[name]
  if (raw === undefined || raw === '') return fallback
  const value = Number(raw)
  if (!Number.isFinite(value) || value < 0 || value > 100) fail(2, `${name} 必须是 0–100 的百分比数值（收到 "${raw}"）`)
  return value / 100
}

function fail(code, message) {
  console.error(`[style-debt] 错误：${message}`)
  process.exit(code)
}

function walk(dir, filter) {
  const out = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...walk(full, filter))
    else if (entry.isFile() && filter(full)) out.push(full)
  }
  return out
}

const cssFiles = walk(SRC_DIR, (file) => file.endsWith('.css')).sort()
const refFiles = [...walk(SRC_DIR, (file) => /\.(ts|tsx)$/.test(file)), join(WEB_DIR, 'index.html')].sort()
if (cssFiles.length === 0) fail(2, `扫描面为空：${relative(process.cwd(), SRC_DIR)} 下没有 CSS`)

const stripComments = (css) => css.replace(/\/\*[\s\S]*?\*\//g, '')
const refHaystack = refFiles.map((file) => readFileSync(file, 'utf8')).join('\n')

/** 逐文件遍历 CSS：区分顶层规则与 at-rule（@media / @supports）内的规则 */
function analyze(file) {
  const raw = readFileSync(file, 'utf8')
  const css = stripComments(raw)
  const rules = []
  const mediaTokens = []
  const breakpoints = []
  let buffer = ''
  const stack = []
  for (const char of css) {
    if (char === '{') {
      const prelude = buffer.trim().replace(/\s+/g, ' ')
      stack.push(prelude)
      if (prelude.startsWith('@media')) {
        breakpoints.push(...[...prelude.matchAll(/\(\s*(?:min|max)-width:\s*([^)\s]+)\s*\)/g)].map((m) => m[1]))
      }
      buffer = ''
      continue
    }
    if (char === '}') {
      const prelude = stack.pop() ?? ''
      // 最近的外层 at-rule（@media / @supports）就是这条规则的「层叠上下文」
      const context = [...stack].reverse().find((item) => item.startsWith('@')) ?? null
      if (!prelude.startsWith('@')) {
        rules.push({ selector: prelude, context })
        // 媒体查询块内的自定义属性声明 = 令牌覆写
        if (context !== null && /--[\w-]+\s*:/.test(buffer)) mediaTokens.push(prelude)
      }
      buffer = ''
      continue
    }
    buffer += char
  }
  return { file, rules, mediaTokens, breakpoints, lines: raw.split('\n').length - (raw.endsWith('\n') ? 1 : 0) }
}

const analyzed = cssFiles.map(analyze)
const allRules = analyzed.flatMap((item) => item.rules.map((rule) => ({ ...rule, file: item.file })))

// ---- ① 未引用类名 ---------------------------------------------------------
// \`mantine-*\` 是组件库自己渲染的类名（ui/** 只做定向覆写），永远不可能出现在我们的源码里，
// 因此不计入分母（否则分母里塞进一堆库类，占比会被人为压低、失去意义）。
const LIBRARY_CLASS_PREFIX = 'mantine-'
const definedClasses = new Set()
let libraryClasses = 0
for (const rule of allRules) {
  for (const match of rule.selector.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) {
    if (match[1].startsWith(LIBRARY_CLASS_PREFIX)) libraryClasses += 1
    else definedClasses.add(match[1])
  }
}
const unusedClasses = [...definedClasses].filter((name) => !refHaystack.includes(name)).sort()
const unusedRatio = definedClasses.size === 0 ? 0 : unusedClasses.length / definedClasses.size

// ---- ② 重复定义选择器 -----------------------------------------------------
const seen = new Map()
for (const rule of allRules) {
  const key = `${rule.context ?? '<top>'}\u0000${rule.selector}`
  seen.set(key, (seen.get(key) ?? 0) + 1)
}
const duplicated = [...seen.entries()].filter(([, count]) => count > 1).map(([key, count]) => ({ key, count }))

// ---- ③ 顶层 :root / 媒体查询内令牌覆写 ------------------------------------
// 「令牌」= theme 层（tokens.css + base.css）里的自定义属性；组件/页面内部自己的局部变量
// （例：ui.css 的 --button-height、board.css 的 --bp-column-w）按断点调值是正常写法，不算令牌覆写，
// 因此单列成 componentMediaVars 只做信息展示，不计入门禁。
const isThemeLayer = (item) => relative(SRC_DIR, item.file).split(/[\\/]/)[0] === 'theme'
const topRoots = allRules.filter((rule) => rule.context === null && rule.selector === ':root')
const mediaTokenRules = analyzed.filter(isThemeLayer).flatMap((item) => item.mediaTokens)
const componentMediaVars = analyzed.filter((item) => !isThemeLayer(item)).flatMap((item) => item.mediaTokens)

// ---- ④ 全局样式层行数 -----------------------------------------------------
const isTheme = isThemeLayer
const globalLines = analyzed.filter(isTheme).reduce((sum, item) => sum + item.lines, 0)

// ---- ⑤ 断点套数 -----------------------------------------------------------
const breakpoints = [...new Set(analyzed.flatMap((item) => item.breakpoints))].sort()
const foreignBreakpoints = breakpoints.filter((value) => !ALLOWED_BREAKPOINTS.has(value))
const breakpointSets = breakpoints.length === 0 || foreignBreakpoints.length > 0 ? breakpoints.length || 1 : 1

// ---- ⑥ 死类子系统 ---------------------------------------------------------
const cssText = cssFiles.map((file) => stripComments(readFileSync(file, 'utf8'))).join('\n')
const deadHits = DEAD_CLASS_PATTERNS.map((pattern) => ({
  name: pattern.name,
  count: [...cssText.matchAll(pattern.re)].length,
}))
const deadTotal = deadHits.reduce((sum, item) => sum + item.count, 0)

// ---- 输出 ----------------------------------------------------------------
const pad = (value, width) => String(value).padEnd(width)
const results = []
function report(label, ok, detail) {
  results.push(ok)
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${label} — ${detail}`)
}

console.log(`[style-debt] 扫描面 ${cssFiles.length} 个 CSS（${relative(WEB_DIR, SRC_DIR)}/**/*.css），引用面 ${refFiles.length} 个文件`)
for (const item of analyzed) {
  const layer = isTheme(item) ? '全局层(theme)' : relative(SRC_DIR, item.file)
  console.log(`[style-debt]   ${pad(layer, 34)} ${String(item.lines).padStart(5)} 行  ${item.rules.length} 条顶层规则`)
}

const pct = (value) => `${(value * 100).toFixed(1)}%`
console.log('')
report(
  '① 未引用类名占比 < 20%',
  unusedRatio < UNUSED_RATIO_MAX,
  `${unusedClasses.length}/${definedClasses.size} = ${pct(unusedRatio)}（阈值 < ${pct(UNUSED_RATIO_MAX)}）` +
    `${unusedClasses.length === 0 ? '' : `；未引用：${unusedClasses.slice(0, 12).join(', ')}${unusedClasses.length > 12 ? ' …' : ''}`}`,
)
report(
  '② 重复定义选择器 = 0',
  duplicated.length === 0,
  `${duplicated.length} 条` + (duplicated.length === 0 ? '' : `；${duplicated.slice(0, 6).map((item) => item.key.replace('\u0000', ' @ ')).join(' | ')}`),
)
report(
  '③ 顶层 :root = 1 / 媒体查询内令牌覆写 = 0',
  topRoots.length === 1 && mediaTokenRules.length === 0,
  `顶层 :root ${topRoots.length} 个（${topRoots.map((rule) => relative(WEB_DIR, rule.file)).join(', ') || '无'}）；` +
    `媒体查询内令牌覆写 ${mediaTokenRules.length} 个；组件局部变量在媒体查询内定义 ${componentMediaVars.length} 处（不计入门禁）`,
)
report(
  `④ 全局样式层（theme/*.css）≤ ${GLOBAL_CSS_LINE_MAX} 行`,
  globalLines <= GLOBAL_CSS_LINE_MAX,
  `${globalLines} 行；组件层 ui/ui.css ${analyzed.filter((item) => !isTheme(item) && item.file.endsWith('ui.css')).reduce((sum, item) => sum + item.lines, 0)} 行、` +
    `页面层 features/*/*.css ${analyzed.filter((item) => !isTheme(item) && !item.file.endsWith('ui.css')).reduce((sum, item) => sum + item.lines, 0)} 行（单列，不计入本门禁）`,
)
report(
  '⑤ 断点 1 套（47.99em / 63.99em）',
  breakpointSets === 1,
  `${breakpoints.length} 个取值：${breakpoints.join(', ') || '无'}` + (foreignBreakpoints.length === 0 ? '' : `；越界：${foreignBreakpoints.join(', ')}`),
)
report(
  '⑥ 死类子系统 grep = 0',
  deadTotal === 0,
  deadHits.map((item) => `.${item.name}=${item.count}`).join(' '),
)

const failed = results.filter((ok) => !ok).length
console.log(`\n[style-debt] ${results.length - failed}/${results.length} 项达标`)
if (failed > 0) {
  console.log('[style-debt] 结论：样式债超标（见上面 FAIL 行）')
  process.exit(1)
}
console.log('[style-debt] 结论：样式债已出清（6/6 项达标）')
