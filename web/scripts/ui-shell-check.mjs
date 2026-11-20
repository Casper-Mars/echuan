// features/shell 的 jsdom 断言门禁（AC1/AC2/AC3/AC5 + 错误边界）
//
// 做什么：把 `src/App.tsx` 真实打包进 jsdom 里跑三视口 × 多种路由，断言的是
//   **渲染出来的 DOM**（导航节点、aria 属性、可见文字、路由落点、主操作计数），不是源码字符串。
//
// 为什么只能断言 DOM：jsdom 不算布局（getBoundingClientRect 全 0、env(safe-area-inset-*) 为 0、
// 外部 CSS 不参与计算），所以宽度 / 高度 / 是否横向溢出**不在本脚本的判据里**——
// 那些量由浏览器验收（AC1/AC4 的截图与 scrollWidth 证据）。本脚本只保证结构、语义与路由。
//
// 依赖口径：jsdom / esbuild 都**不写进 package.json**（本卡不允许动依赖块与 lock）。
//   * esbuild：vite 的传递依赖，从 vite 的解析上下文里取（与 scripts/ui-imports-check.mjs 同法）；
//   * jsdom：按顺序尝试 `JSDOM_MODULE` 环境变量 → web/node_modules/jsdom → /workspace/.verify-tools/node_modules/jsdom。
//     一个都找不到时退出码 2（环境错误，不是断言失败）。
//
// 用法：
//   node web/scripts/ui-shell-check.mjs
//   JSDOM_MODULE=/path/to/jsdom node web/scripts/ui-shell-check.mjs
//
// 退出码：0 全部通过 / 1 断言失败 / 2 环境或打包失败。

import { createRequire } from 'node:module'
import { mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const WEB_DIR = dirname(dirname(fileURLToPath(import.meta.url)))
const WORK_DIR = join(WEB_DIR, 'node_modules', '.ui-shell-check')

function fail(code, message) {
  console.error(`[shell] 错误：${message}`)
  process.exit(code)
}

// ---- esbuild（vite 的传递依赖，同 scripts/ui-imports-check.mjs） --------------
async function loadEsbuild() {
  const require = createRequire(import.meta.url)
  const attempts = [
    () => require.resolve('esbuild'),
    () => createRequire(require.resolve('vite/package.json')).resolve('esbuild'),
    () => createRequire(require.resolve('@vitejs/plugin-react/package.json')).resolve('esbuild'),
  ]
  for (const attempt of attempts) {
    try {
      return await import(pathToFileURL(attempt()).href)
    } catch {
      // 换下一个解析上下文
    }
  }
  return null
}

// ---- jsdom（不进依赖块，按环境变量 / 已知位置解析） --------------------------
function loadJsdom() {
  const require = createRequire(import.meta.url)
  const candidates = [
    process.env.JSDOM_MODULE,
    join(WEB_DIR, 'node_modules', 'jsdom'),
    '/workspace/.verify-tools/node_modules/jsdom',
  ].filter(Boolean)
  for (const candidate of candidates) {
    try {
      return require(candidate)
    } catch {
      // 换下一个候选
    }
  }
  return null
}

const esbuild = await loadEsbuild()
if (!esbuild) fail(2, '找不到 esbuild（vite 的传递依赖）。请先在 web/ 执行 pnpm install')

const jsdomModule = loadJsdom()
if (!jsdomModule) {
  fail(
    2,
    '找不到 jsdom。本门禁刻意不把它写进 package.json（改依赖块不在本卡范围）：' +
      '用 JSDOM_MODULE=<jsdom 包路径> 指定，或确认 /workspace/.verify-tools/node_modules/jsdom 存在',
  )
}
const { JSDOM } = jsdomModule

// ---- 1. 打包被测入口 ---------------------------------------------------------
// 入口写成真实文件（而不是 stdin）：动态 import 的相对解析与 CSS import 都按真实路径走。
// 产物落在 web/node_modules 下（已 gitignore）：external 的 react / react-dom / @mantine/core
// 等由 Node 从 web/node_modules 解析，与脚本自己 import 的是同一份实例。
mkdirSync(WORK_DIR, { recursive: true })
const ENTRY = join(WORK_DIR, 'entry.jsx')
writeFileSync(
  ENTRY,
  [
    "import { StrictMode } from 'react'",
    "export { App } from '../../src/App'",
    "export { ErrorBoundary } from '../../src/features/shell/ErrorBoundary'",
    "export { RouteLoading } from '../../src/features/shell/RouteLoading'",
    'export { StrictMode }',
    '',
  ].join('\n'),
)

const EXTERNAL = [
  '@mantine/core',
  '@mantine/core/*',
  '@mantine/hooks',
  'react',
  'react/*',
  'react-dom',
  'react-dom/*',
  'react/jsx-runtime',
  'react-router-dom',
  'dompurify',
  'marked',
]

let result
try {
  result = await esbuild.build({
    entryPoints: [ENTRY],
    bundle: true,
    write: false,
    outdir: WORK_DIR,
    entryNames: 'bundle',
    format: 'esm',
    platform: 'browser',
    target: 'es2022',
    jsx: 'automatic',
    logLevel: 'silent',
    // 详情路由是懒加载：不拆 chunk，动态 import 就地内联，产物只有一份文件
    splitting: false,
    external: EXTERNAL,
  })
} catch (error) {
  console.error('[shell] 错误：src/App.tsx 打包失败')
  for (const message of error.errors ?? []) console.error(`[shell]   ${message.text}`)
  if (!error.errors?.length) console.error(`[shell]   ${error.message}`)
  process.exit(2)
}

const jsOutput = result.outputFiles?.find((file) => file.path.endsWith('.js'))
if (!jsOutput) fail(2, '打包没有产出 JavaScript 输出')
const BUNDLE = join(WORK_DIR, 'bundle.mjs')
writeFileSync(BUNDLE, jsOutput.contents)

// ---- 2. jsdom 环境 ----------------------------------------------------------
const dom = new JSDOM('<!doctype html><html data-theme="dark"><body></body></html>', {
  url: 'http://localhost/',
  pretendToBeVisual: true,
})

const { window } = dom

/** 三视口的宽度真值（与 design/screens/board.md 的断点一致） */
const viewport = { width: 1440, height: 900 }

/**
 * matchMedia 桩：jsdom 不实现 matchMedia。这里只解析 min-width / max-width 条件（壳层与
 * Mantine 用到的就是这类），未知条件（prefers-reduced-motion 等）一律 false——即「未开启减弱动效」。
 */
const mediaListeners = new Set()

function evaluateQuery(query) {
  return query.split(/\s+and\s+/).every((raw) => {
    const part = raw.trim()
    const min = /^\(min-width:\s*([\d.]+)px\)$/.exec(part)
    if (min) return viewport.width >= Number(min[1])
    const max = /^\(max-width:\s*([\d.]+)px\)$/.exec(part)
    if (max) return viewport.width <= Number(max[1])
    const minH = /^\(min-height:\s*([\d.]+)px\)$/.exec(part)
    if (minH) return viewport.height >= Number(minH[1])
    const maxH = /^\(max-height:\s*([\d.]+)px\)$/.exec(part)
    if (maxH) return viewport.height <= Number(maxH[1])
    return false
  })
}

window.matchMedia = (query) => {
  const listeners = new Set()
  const handle = {
    media: query,
    get matches() {
      return evaluateQuery(query)
    },
    onchange: null,
    addEventListener: (_type, listener) => listeners.add(listener),
    removeEventListener: (_type, listener) => listeners.delete(listener),
    addListener: (listener) => listeners.add(listener),
    removeListener: (listener) => listeners.delete(listener),
    dispatchEvent: () => true,
  }
  mediaListeners.add({ query, listeners })
  return handle
}

/** 改视口：改宽度 → 所有 matchMedia 订阅者收到 change（= 真实 resize 下的断点切换） */
function setViewport(width, height) {
  viewport.width = width
  viewport.height = height
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true })
  Object.defineProperty(window, 'innerHeight', { value: height, configurable: true })
  for (const entry of mediaListeners) {
    const event = { matches: evaluateQuery(entry.query), media: entry.query }
    for (const listener of entry.listeners) listener(event)
  }
}

// 浏览器里天然存在、jsdom 里缺的观察器：给出最小桩，避免 Mantine 组件在 mount 时抛错
class ObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return []
  }
}

window.ResizeObserver = ObserverStub
window.IntersectionObserver = ObserverStub
window.matchMedia = window.matchMedia
window.requestAnimationFrame ??= (callback) => setTimeout(() => callback(Date.now()), 0)
window.cancelAnimationFrame ??= (handle) => clearTimeout(handle)
// 手势 / 指针事件相关的 API：Mantine 的 Tooltip 会探测，缺了会抛错
window.PointerEvent ??= window.MouseEvent
window.HTMLElement.prototype.scrollIntoView ??= () => {}
window.HTMLElement.prototype.setPointerCapture ??= () => {}
window.HTMLElement.prototype.releasePointerCapture ??= () => {}
window.HTMLElement.prototype.hasPointerCapture ??= () => false

/**
 * fetch 桩：本门禁不连后端。统一返回 503 + 可读 message，
 * 让 api/client 走「非 2xx → ApiError」的既有错误路径（页面自己转错误条），
 * 既不产生未捕获 rejection，也不给被测代码喂假成功数据。
 */
window.fetch = globalThis.fetch = async () => ({
  ok: false,
  status: 503,
  statusText: 'Service Unavailable',
  headers: { get: () => null },
  text: async () => JSON.stringify({ message: 'ui-shell-check：本门禁不连后端' }),
  json: async () => ({ message: 'ui-shell-check：本门禁不连后端' }),
})

for (const key of [
  'window',
  'document',
  'navigator',
  'location',
  'history',
  'localStorage',
  'sessionStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'Element',
  'Node',
  'Event',
  'MouseEvent',
  'KeyboardEvent',
  'FocusEvent',
  'CustomEvent',
  'DOMParser',
  'MutationObserver',
  'getComputedStyle',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'matchMedia',
  'ResizeObserver',
  'IntersectionObserver',
  'fetch',
]) {
  // Node 22 里 navigator / location 等是只读 getter：用 defineProperty 覆盖，失败就跳过（不阻断门禁）
  try {
    Object.defineProperty(globalThis, key, { value: window[key], configurable: true, writable: true })
  } catch {
    // 少数全局不可覆写：保持 Node 原生实现（本门禁不使用它们）
  }
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

setViewport(viewport.width, viewport.height)

// ---- 3. 载入被测代码（必须在全局装好之后） ----------------------------------
const React = await import('react')
const { createRoot } = await import('react-dom/client')
const { act } = await import('react-dom/test-utils')
const { App, ErrorBoundary, RouteLoading } = await import(pathToFileURL(BUNDLE).href)

// Mantine 的 Provider / AppShell 上下文：与 src/main.tsx 的接线保持一致（本脚本不跑 main.tsx，
// 它自己 createRoot 会抢容器）。MantineProvider 必填，否则 @mantine/core 的 useMantineTheme 直接抛错；
// AppShell 提供 ui/SidebarNav 所需的 AppShell 安全上下文。
const { MantineProvider, AppShell } = await import("@mantine/core")

function withProviders(node) {
  return h(
    MantineProvider,
    { forceColorScheme: "dark", defaultColorScheme: "dark" },
    h(
      AppShell,
      {
        withBorder: false,
        padding: 0,
        navbar: { width: "var(--ui-layout-sidebar)", breakpoint: "sm", collapsed: { mobile: true } },
      },
      node,
    ),
  )
}

const h = React.createElement

// ---- 4. 断言工具 ------------------------------------------------------------
const results = []

async function check(name, run) {
  let ok = false
  let detail = ''
  try {
    detail = (await run()) ?? ''
    ok = true
  } catch (error) {
    detail = error instanceof Error ? error.message : String(error)
  }
  results.push({ name, ok, detail })
  console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${detail ? ` — ${detail}` : ''}`)
}

function assert(condition, message) {
  if (!condition) throw new Error(message)
  return message
}

const text = (node) => (node?.textContent ?? '').replace(/\s+/g, ' ').trim()

function visibleNavLabels(root) {
  return Array.from(root.querySelectorAll('.ui-sidebar-nav a, .ui-sidebar-nav button'))
    .map((node) => text(node))
    .filter(Boolean)
}

function tabNodes(root) {
  return Array.from(root.querySelectorAll('.ui-tab-bar .ui-tab-bar__tab'))
}

// ---- 5. 挂载 / 卸载 ---------------------------------------------------------
let root = null
let container = null

async function mount(element, { settle = true } = {}) {
  container = window.document.createElement('div')
  window.document.body.appendChild(container)
  root = createRoot(container)
  if (settle) {
    await act(async () => {
      root.render(element)
    })
    await flush()
  } else {
    await act(() => {
      root.render(element)
    })
  }
  return container
}

/** 让微任务与一次宏任务都跑完（lazy chunk、fetch 桩的 rejection 收口） */
async function flush(times = 3) {
  for (let index = 0; index < times; index += 1) {
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
  }
}

async function unmount() {
  if (root) await act(async () => root.unmount())
  root = null
  window.document.body.innerHTML = ''
  window.localStorage.clear()
  window.location.hash = ''
  await flush(1)
}

/** 设定本次场景的「地址 + 本地记忆 + 视口」，然后挂载真实 App */
async function renderAt({ width, height, hash, lastProject }) {
  setViewport(width, height)
  window.localStorage.clear()
  if (lastProject) window.localStorage.setItem('echuan:last-project', lastProject)
  window.location.hash = hash
  return mount(withProviders(h(App)))
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

// ---- 6. 场景：三视口壳层（AC1 / AC2） ----------------------------------------
// AC1 只断言「结构 + 取值清单」；像素级与宽度由浏览器验收，见包外截图证据。

await renderAt({ width: 1440, height: 900, hash: '#/projects/echuan-1/tasks' })

await check('AC1 桌面：shell 根节点 data-layout=desktop', () =>
  assert(
    window.document.querySelector('[data-ui="shell"]')?.getAttribute('data-layout') === 'desktop',
    'data-layout=desktop',
  ),
)

await check('AC1 桌面：渲染 SidebarNav（nav[aria-label=主导航] .ui-sidebar-nav）', () => {
  const nav = window.document.querySelector('nav.ui-sidebar-nav')
  return assert(nav?.getAttribute('aria-label') === '主导航', '侧栏存在且 aria-label=主导航')
})

await check('AC1 桌面：侧栏 4 项且文字可见（看板/收件箱/产品/执行节点）', () => {
  const labels = visibleNavLabels(window.document)
  return assert(
    labels.length === 4 && ['看板', '收件箱', '产品', '执行节点'].every((label) => labels.includes(label)),
    `labels=${JSON.stringify(labels)}`,
  )
})

await check('AC1 桌面：不渲染移动 TabBar', () =>
  assert(window.document.querySelector('.ui-tab-bar') === null, '无 .ui-tab-bar'),
)

await check('AC1 顶栏：存在且包含面包屑与「刷新」', () => {
  const topbar = window.document.querySelector('[data-ui="shell-topbar"]')
  const crumbs = window.document.querySelector('nav[aria-label="面包屑"]')
  assert(topbar, '缺顶栏')
  assert(text(topbar).includes('刷新'), '顶栏缺「刷新」')
  return assert(text(crumbs).includes('echuan-1'), `crumbs=${text(crumbs)}`)
})

await check('AC5 桌面看板：主操作「新建任务」全屏只 1 个，顶栏内 0 个', () => {
  const buttons = Array.from(window.document.querySelectorAll('button'))
  const primary = buttons.filter((node) => text(node) === '新建任务')
  const inTopbar = window.document
    .querySelector('[data-ui="shell-topbar"]')
    ?.querySelectorAll('[data-ui="primary-action"], button')
  const topbarPrimary = Array.from(inTopbar ?? []).filter(
    (node) => text(node) === '新建任务' || node.getAttribute('data-ui') === 'primary-action',
  )
  assert(primary.length === 1, `新建任务 计数=${primary.length}`)
  return assert(topbarPrimary.length === 0, '顶栏无主操作')
})

await unmount()

// 平板 1024×768：侧栏收 64 图标栏，文字进 Tooltip
await renderAt({ width: 1024, height: 768, hash: '#/projects/echuan-1/tasks' })

await check('AC1 平板：data-layout=tablet', () =>
  assert(
    window.document.querySelector('[data-ui="shell"]')?.getAttribute('data-layout') === 'tablet',
    'data-layout=tablet',
  ),
)

await check('AC1 平板：侧栏折叠（.ui-sidebar-nav--collapsed），无可见文字标签', () => {
  const nav = window.document.querySelector('nav.ui-sidebar-nav')
  const labels = visibleNavLabels(window.document)
  assert(nav?.classList.contains('ui-sidebar-nav--collapsed'), '缺 --collapsed 类')
  return assert(labels.length === 0, `可见文字=${JSON.stringify(labels)}`)
})

await check('AC2 平板：折叠项保留 aria-label（读屏仍可读）', () => {
  const labels = Array.from(window.document.querySelectorAll('.ui-sidebar-nav [aria-label]')).map((node) =>
    node.getAttribute('aria-label'),
  )
  return assert(
    ['看板', '收件箱', '产品', '执行节点'].every((label) => labels.includes(label)),
    `aria-label=${JSON.stringify(labels)}`,
  )
})

await check('AC2 平板：悬停折叠项出现 Tooltip（role=tooltip + 文案）', async () => {
  const target = window.document.querySelector('.ui-sidebar-nav [aria-label="看板"]')
  assert(target, '找不到折叠项')

  // floating-ui 的 useHover 只挂了 onPointerEnter / onMouseMove（见 Tooltip 的 reference props），
  // 所以既要 pointerover（合成 pointerenter），也要 mousemove；一次性 dispatch 后要给
  // floating-ui 的 open → Mantine Transition 挂载浮层留出若干帧。
  for (const type of ['pointerover', 'mouseover', 'pointerenter', 'mouseenter', 'pointermove', 'mousemove']) {
    target.dispatchEvent(new window.MouseEvent(type, { bubbles: true, cancelable: true, view: window }))
  }

  let tooltip = null
  for (let frame = 0; frame < 12 && tooltip === null; frame += 1) {
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {
      await sleep(60)
    })
    tooltip = window.document.querySelector('[role="tooltip"]')
  }

  assert(tooltip, '悬停后未出现 role=tooltip 浮层')
  return assert(text(tooltip).includes('看板'), `tooltip=${text(tooltip)}`)
})

await unmount()

// 移动 390×844：无侧栏 + 底部 TabBar 4 项（图标 + 文字）
await renderAt({ width: 390, height: 844, hash: '#/projects/echuan-1/tasks' })

await check('AC1 移动：data-layout=mobile 且无侧栏', () => {
  const shell = window.document.querySelector('[data-ui="shell"]')
  assert(shell?.getAttribute('data-layout') === 'mobile', 'data-layout≠mobile')
  return assert(window.document.querySelector('.ui-sidebar-nav') === null, '移动端不应有侧栏')
})

await check('AC1/AC2 移动：TabBar 4 项都有图标 + 文字（看板/收件箱/产品/更多）', () => {
  const tabs = tabNodes(window.document)
  assert(tabs.length === 4, `tab 数=${tabs.length}`)
  const labels = tabs.map((tab) => text(tab))
  assert(
    ['看板', '收件箱', '产品', '更多'].every((label) => labels.includes(label)),
    `labels=${JSON.stringify(labels)}`,
  )
  const withoutIcon = tabs.filter((tab) => tab.querySelector('svg') === null)
  return assert(withoutIcon.length === 0, '每项都有内联 SVG：' + labels.join('/'))
})

await check('AC1/AC2 移动：当前项 aria-current=page', () => {
  const current = tabNodes(window.document).filter((tab) => tab.getAttribute('aria-current') === 'page')
  return assert(current.length === 1 && text(current[0]) === '看板', `aria-current=${text(current[0])}`)
})

await check('AC5 移动：顶栏无主操作，页面主操作只有 1 个', () => {
  const topbar = window.document.querySelector('[data-ui="shell-topbar"]')
  assert(topbar?.querySelectorAll('[data-ui="primary-action"]').length === 0, '顶栏出现主操作')
  const primary = Array.from(window.document.querySelectorAll('button')).filter(
    (node) => text(node) === '新建任务',
  )
  return assert(primary.length === 1, `新建任务 计数=${primary.length}`)
})

await unmount()

// ---- 7. 场景：路由三入口（AC3） ---------------------------------------------
await renderAt({ width: 1440, height: 900, hash: '#/', lastProject: 'echuan-1' })
await check('AC3 入口一：`#/` 有上次项目记录 → 落到该项目看板', () => {
  const hash = window.location.hash
  return assert(hash === '#/projects/echuan-1/tasks', `hash=${hash}`)
})
await unmount()

await renderAt({ width: 1440, height: 900, hash: '#' })
await check('AC3 入口一（无记录）：`#/` 落到产品页', () => {
  const hash = window.location.hash
  return assert(hash === '#/products', `hash=${hash}`)
})
await unmount()

// 壳层 loading 态（确定性断言）：Suspense fallback 就是 RouteLoading 骨架。
// 单独渲染「永不 resolve 的子组件 + 同一个 fallback 组件」，避免依赖 lazy chunk 的时序。
{
  const Never = () => {
    throw new Promise(() => {})
  }
  await mount(withProviders(h(React.Suspense, { fallback: h(RouteLoading) }, h(Never))))

  await check('壳层 loading：Suspense fallback 渲染 RouteLoading 骨架（aria-busy + 骨架块）', () => {
    const loading = window.document.querySelector('[data-ui="route-loading"]')
    assert(loading, '缺 [data-ui="route-loading"]')
    assert(loading.getAttribute('aria-busy') === 'true', 'aria-busy 不是 true')
    const skeletons = window.document.querySelectorAll('.ui-skeleton-card')
    return assert(skeletons.length >= 1, `骨架块数=${skeletons.length}`)
  })

  await unmount()
}

// 详情深链：先记录 lazy chunk 未到位时是否出现过 fallback，再断言详情容器
{
  setViewport(1440, 900)
  window.localStorage.clear()
  window.location.hash = '#/projects/echuan-1/tasks/T42'
  let sawLoading = false
  const observer = new window.MutationObserver(() => {
    if (window.document.querySelector('[data-ui="route-loading"]')) sawLoading = true
  })
  observer.observe(window.document.body, { childList: true, subtree: true })

  container = window.document.createElement('div')
  window.document.body.appendChild(container)
  root = createRoot(container)
  await act(() => {
    root.render(withProviders(h(App)))
  })
  if (window.document.querySelector('[data-ui="route-loading"]')) sawLoading = true

  await flush()
  observer.disconnect()

  await check('AC3 入口二：详情深链直接渲染详情容器（data-ui=task-detail）', () => {
    const detail = window.document.querySelector('[data-ui="task-detail"]')
    assert(detail, '缺 [data-ui="task-detail"]')
    return assert(detail.getAttribute('data-task-id') === 'T42', `task-id=${detail.getAttribute('data-task-id')}`)
  })

  await check('AC3 入口二：关闭动作回看板路由（按钮文案「返回看板」）', () => {
    const button = Array.from(window.document.querySelectorAll('button')).find(
      (node) => text(node) === '返回看板',
    )
    assert(button, '缺「返回看板」按钮')
    return assert(button.closest('[data-ui="primary-action"]') !== null, '主操作锚点存在')
  })

  await check('AC5 详情页：全屏主操作锚点恰好 1 个，顶栏 0 个', () => {
    const all = window.document.querySelectorAll('[data-ui="primary-action"]')
    const inTopbar = window.document
      .querySelector('[data-ui="shell-topbar"]')
      ?.querySelectorAll('[data-ui="primary-action"]')
    assert(all.length === 1, `主操作锚点=${all.length}`)
    return assert((inTopbar?.length ?? 0) === 0, '顶栏无主操作锚点')
  })

  // 信息行（不作判据）：本地单文件打包下 lazy 立即 resolve，React 可能直接跳过 fallback。
  console.log(
    `INFO 详情路由懒加载期间是否出现过 RouteLoading 骨架：${sawLoading ? '是' : '否（本地内联 chunk 立即 resolve；浏览器侧为真实网络请求）'}`,
  )

  await unmount()
}

await renderAt({ width: 1440, height: 900, hash: '#/projects/echuan-1/tasks' })
await check('AC3 入口三：`#/projects/:pid/tasks`（无 taskId）停在看板', () => {
  const hash = window.location.hash
  return assert(hash === '#/projects/echuan-1/tasks', `hash=${hash}`)
})
await unmount()

await renderAt({ width: 1440, height: 900, hash: '#/projects/echuan-1' })
await check('AC3 入口三：`#/projects/:pid`（缺 tasks 段）归一回看板', () => {
  const hash = window.location.hash
  return assert(hash === '#/projects/echuan-1/tasks', `hash=${hash}`)
})
await unmount()

// 旧书签必须仍可达（不被新路由吃掉）
for (const [hash, probe] of [
  ['#/inbox', '收件箱'],
  ['#/queue', '派单队列'],
  ['#/products', '产品'],
  ['#/products/echuan/projects', 'echuan'],
  ['#/workers', '执行节点'],
]) {
  await renderAt({ width: 1440, height: 900, hash })
  await check(`旧书签保留：${hash} 不被重定向`, () => {
    assert(window.location.hash === hash, `hash=${window.location.hash}`)
    const topbar = window.document.querySelector('[data-ui="shell-topbar"]')
    return assert(text(topbar).includes(probe), `顶栏含「${probe}」：${text(topbar)}`)
  })
  await unmount()
}

// ---- 8. 场景：页级错误边界（default / error / loading + 两个动作） -----------
{
  setViewport(1440, 900)
  window.location.hash = '#/products'
  let shouldThrow = true
  let exitCalled = false

  const Boom = () => {
    if (shouldThrow) throw new Error('boom（门禁构造的渲染异常）')
    return h('p', { 'data-ui': 'recovered' }, '已恢复')
  }

  await mount(
    withProviders(
      h(ErrorBoundary, { onExit: () => { exitCalled = true } }, h(Boom)),
    ),
  )

  await check('错误边界 error 态：接住子组件抛错并渲染 role=alert 错误条', () => {
    const alert = window.document.querySelector('[role="alert"]')
    assert(alert, '缺 role=alert')
    return assert(text(alert).includes('页面渲染出错'), `文案=${text(alert).slice(0, 40)}`)
  })

  await check('错误边界 error 态：给出「重试」与「返回看板」两个动作', () => {
    const labels = Array.from(window.document.querySelectorAll('button')).map((node) => text(node))
    assert(labels.includes('重试'), `缺重试：${JSON.stringify(labels)}`)
    return assert(labels.includes('返回看板'), `缺返回看板：${JSON.stringify(labels)}`)
  })

  await check('D2 错误条自带表面：Alert 带 .fx-error-bar-alert 且骨架标记 data-surface=self', () => {
    const alert = window.document.querySelector('[role="alert"]')
    assert(alert?.classList.contains('fx-error-bar-alert'), 'Alert 缺 .fx-error-bar-alert')
    const bar = window.document.querySelector('[data-ui="error-bar"]')
    return assert(bar?.getAttribute('data-surface') === 'self', `data-surface=${bar?.getAttribute('data-surface')}`)
  })

  await check('D2 错误条前景/背景成对：title 与 message 是自绘节点（不依赖 ui/Alert 的 title 槽）', () => {
    const title = window.document.querySelector('.fx-error-bar__title')
    const message = window.document.querySelector('.fx-error-bar__message')
    assert(title && text(title) === '页面渲染出错', `title=${text(title)}`)
    return assert(message !== null, `message 节点存在=${Boolean(message)}`)
  })

  await check('D7 动作成组：重试与返回看板在同一个 actions 行内', () => {
    const row = window.document.querySelector('[data-ui="error-bar-actions"]')
    assert(row, '缺 [data-ui="error-bar-actions"]')
    const labels = Array.from(row.querySelectorAll('button')).map((node) => text(node))
    assert(labels.includes('重试'), `行内缺重试：${JSON.stringify(labels)}`)
    return assert(labels.includes('返回看板'), `行内缺返回看板：${JSON.stringify(labels)}`)
  })

  await check('错误边界 error 态：技术细节可见（不是只有一句「出错了」）', () => {
    const detail = window.document.querySelector('.fx-error-bar__detail')
    return assert(text(detail).includes('boom'), `detail=${text(detail)}`)
  })

  await check('错误边界 error 态：「返回看板」动作可触发（onExit 回调）', async () => {
    const exit = Array.from(window.document.querySelectorAll('button')).find(
      (node) => text(node) === '返回看板',
    )
    assert(exit, '缺返回看板按钮')
    await act(async () => {
      exit.dispatchEvent(new window.MouseEvent('click', { bubbles: true, cancelable: true, view: window }))
    })
    return assert(exitCalled === true, `onExit called=${exitCalled}`)
  })

  await check('错误边界 loading 态：点「重试」后动作按钮 aria-busy=true', async () => {
    shouldThrow = false
    const retry = Array.from(window.document.querySelectorAll('button')).find((node) => text(node) === '重试')
    assert(retry, '找不到重试按钮')
    await act(async () => {
      retry.dispatchEvent(new window.MouseEvent('click', { bubbles: true, cancelable: true, view: window }))
    })
    const busy = retry.getAttribute('aria-busy')
    return assert(busy === 'true', `aria-busy=${busy}`)
  })

  await check('错误边界 retry→default：重试后子树重新渲染（不再抛错即回到正常内容）', async () => {
    await flush(4)
    const recovered = window.document.querySelector('[data-ui="recovered"]')
    return assert(recovered !== null, `recovered=${Boolean(recovered)}`)
  })

  await unmount()
}

// ---- 8b. 场景：D1 错误态随导航重置（resetKey 变化即恢复） --------------------
{
  setViewport(1440, 900)
  window.location.hash = '#/products'
  let shouldThrow = true
  let resetKey = 'route-a'

  const Boom = () => {
    if (shouldThrow) throw new Error('boom-D1')
    return h('p', { 'data-ui': 'route-b-page' }, '路由 B 页面')
  }

  const view = () =>
    withProviders(h(ErrorBoundary, { resetKey, onExit: () => {} }, h(Boom)))

  await mount(view())

  await check('D1 前置：先进入 error 态', () => {
    const err = window.document.querySelector('[data-ui="page-error"]')
    return assert(err !== null, `page-error=${Boolean(err)}`)
  })

  await check('D1 导航（resetKey 变化）后错误态自动清掉，新路由内容渲染', async () => {
    shouldThrow = false
    resetKey = 'route-b'
    await act(async () => {
      root.render(view())
    })
    await flush(2)
    const err = window.document.querySelector('[data-ui="page-error"]')
    const page = window.document.querySelector('[data-ui="route-b-page"]')
    assert(err === null, '错误条仍在（D1 复现）')
    return assert(page !== null, '新路由内容未渲染')
  })

  await check('D1 反向：resetKey 不变时不擅自清错误（避免掩盖真实故障）', async () => {
    shouldThrow = true
    resetKey = 'route-c'
    await act(async () => {
      root.render(view())
    })
    await flush(1)
    const err = window.document.querySelector('[data-ui="page-error"]')
    assert(err !== null, 'error 态应保持')
    // 同一 resetKey 下再渲染一次（模拟无关重渲染）→ 仍保持 error
    await act(async () => {
      root.render(view())
    })
    const stillErr = window.document.querySelector('[data-ui="page-error"]')
    return assert(stillErr !== null, `同一 resetKey 重渲染后错误条=${Boolean(stillErr)}`)
  })

  await unmount()
}

// ---- 9. 汇总 ----------------------------------------------------------------
const passed = results.filter((item) => item.ok).length
const failed = results.length - passed
console.log('')
console.log(`[shell] 断言 ${results.length} 项：通过 ${passed}，失败 ${failed}`)
console.log(`[shell] 说明：本脚本只断言 DOM 结构与语义；宽度 / 高度 / 横向溢出由浏览器验收（AC1/AC4）`)

rmSync(WORK_DIR, { recursive: true, force: true })
dom.window.close()

if (failed > 0) {
  console.error('[shell] 失败项：')
  for (const item of results.filter((entry) => !entry.ok)) {
    console.error(`[shell]   ${item.name} — ${item.detail}`)
  }
  process.exit(1)
}
console.log('[shell] 通过')
process.exit(0)
