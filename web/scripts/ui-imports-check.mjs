// ui 封装层「可 import」门禁（AC2）
//
// 做什么：用仓库已有的 esbuild（vite 的传递依赖，不写进 package.json）真实打包
//   web/src/ui/index.ts
// 并对产物元数据断言：导出的组件名集合 == design/components.md 总览表里的 21 个组件。
// 缺一个、多个、或某个组件文件解析失败（import 不到 Mantine / React）都会非 0 退出。
//
// 为什么用 --bundle + external：
//   * bundle 会真的走一遍每个组件的模块图，能抓出「文件存在但 import 链断了」的情况；
//   * Mantine / React / react-router-dom 标记 external，只验证我们自己的模块图，
//     不把 UI 库源码打进来（这里不是产物体积门禁，体积门禁在 scripts/check-size.mjs）。
//
// 用法：
//   node web/scripts/ui-imports-check.mjs     # 退出码 0 = 21 个组件全部可 import
//
// 退出码：
//   0 通过（导出名集合与 21 个组件完全一致）
//   1 导出不匹配（缺 / 多）
//   2 环境或打包失败（esbuild 不可用 / 打包报错）

import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const WEB_DIR = dirname(dirname(fileURLToPath(import.meta.url)))
const ENTRY = join(WEB_DIR, 'src', 'ui', 'index.ts')

// design/components.md「总览」表的 21 个自有组件（顺序与该表一致）
const EXPECTED = [
  'Button',
  'IconButton',
  'TaskCard',
  'StatusPill',
  'KindBadge',
  'BoardColumn',
  'DataTable',
  'TextField',
  'TextArea',
  'SelectField',
  'Alert',
  'EmptyState',
  'SkeletonCard',
  'ConfirmModal',
  'DetailSheet',
  'TabBar',
  'SidebarNav',
  'MarkdownViewer',
  'AttachmentRow',
  'Segmented',
  'Chip',
]

const EXTERNAL = [
  '@mantine/core',
  '@mantine/core/*',
  '@mantine/hooks',
  'react',
  'react/*',
  'react-dom',
  'react-dom/*',
  'react-router-dom',
  'dompurify',
  'marked',
]

// esbuild 是 vite 的传递依赖：pnpm 严格布局下不能从 web/ 直接解析，
// 因此从 vite 包的解析上下文里取（仍然只用仓库已安装的依赖，不新增 package.json 条目）。
async function loadEsbuild() {
  const require = createRequire(import.meta.url)
  const attempts = [
    () => require.resolve('esbuild'),
    () => createRequire(require.resolve('vite/package.json')).resolve('esbuild'),
    () => createRequire(require.resolve('@vitejs/plugin-react/package.json')).resolve('esbuild'),
  ]
  for (const attempt of attempts) {
    try {
      const path = attempt()
      return await import(pathToFileURL(path).href)
    } catch {
      // 换下一个解析上下文
    }
  }
  return null
}

const esbuild = await loadEsbuild()
if (!esbuild) {
  console.error('[ui] 错误：找不到 esbuild（vite 的传递依赖）。请先在 web/ 执行 pnpm install')
  process.exit(2)
}

let result
try {
  result = await esbuild.build({
    entryPoints: [ENTRY],
    bundle: true,
    // write:false + outdir：只做真实打包（顺带把 ui.css 也过一遍 esbuild 的 CSS 解析），不落盘
    write: false,
    outdir: join(WEB_DIR, '.ui-imports-check'),
    format: 'esm',
    platform: 'browser',
    target: 'es2022',
    jsx: 'automatic',
    logLevel: 'silent',
    metafile: true,
    external: EXTERNAL,
  })
} catch (error) {
  console.error('[ui] 错误：web/src/ui/index.ts 打包失败，组件不可 import')
  for (const message of error.errors ?? []) {
    console.error(`[ui]   ${relative(WEB_DIR, message.location?.file ?? ENTRY)}: ${message.text}`)
  }
  if (!error.errors?.length) console.error(`[ui]   ${error.message}`)
  process.exit(2)
}

const outputKey = Object.keys(result.metafile.outputs).find((key) => key.endsWith('.js'))
if (!outputKey) {
  console.error('[ui] 错误：打包没有产出 JavaScript 输出')
  process.exit(2)
}
const exportsList = [...(result.metafile.outputs[outputKey]?.exports ?? [])].sort()

// 只保留组件名（index.ts 还导出了 props 类型与常量，类型在 ESM 里不产生运行时导出）
const runtimeExports = exportsList.filter((name) => !name.startsWith('__')) // esbuild 在无导出时会写空
const expectedSorted = [...EXPECTED].sort()
const missing = expectedSorted.filter((name) => !runtimeExports.includes(name))
const extra = runtimeExports.filter((name) => !expectedSorted.includes(name))

for (const name of EXPECTED) {
  const ok = runtimeExports.includes(name)
  console.log(`${ok ? 'OK  ' : 'FAIL'} ${name}`)
}
console.log(`[ui] 导出总数：${runtimeExports.length} / 期望 ${EXPECTED.length}`)

if (runtimeExports.length > 0) {
  const jsOutput = result.outputFiles?.find((file) => file.path.endsWith('.js'))
  const cssOutput = result.outputFiles?.find((file) => file.path.endsWith('.css'))
  console.log(`[ui] 打包产物（external 掉 UI 库后）：js ${jsOutput?.contents?.length ?? 0} B，css ${cssOutput?.contents?.length ?? 0} B`)
}

if (missing.length > 0 || extra.length > 0) {
  if (missing.length > 0) console.error(`[ui] 缺少导出：${missing.join(', ')}`)
  if (extra.length > 0) console.error(`[ui] 多出导出（需同步文档或删除）：${extra.join(', ')}`)
  process.exit(1)
}

console.log(`[ui] 通过：${EXPECTED.length} 个组件全部可 import`)
