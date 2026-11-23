// 「当前版本」跨端一致性门禁 —— 方案 v0.2 §7.1.2 第 3 道锁
//
// 做什么：用仓库已有的 esbuild（vite 的传递依赖，不写进 package.json）真实打包
//   web/src/lib/current-versions.ts，import 打包产物（不复制函数体），
//   再拿 Go 侧同一份夹具 internal/currentver/testdata/current-versions-cases.json
//   逐条跑 currentTaskVersions，断言 material_id 序列与 want_material_ids 完全一致。
//
// 为什么用「同一份夹具 + 真 import」：
//   * 夹具是双端唯一的真相来源：Go 的 TestCurrentFixtures 读它，本脚本也读它。
//     改规则只改一侧（Go 改规则不改 Web，或反过来）时，这条脚本或 Go 测试必有一侧红；
//   * 复制函数体进脚本的话，Web 实现回退（比如 R-d 改成按 id 记账）脚本依旧全绿，
//     等于没有回归保护——所以这里只 import，不复制。
//
// 用法：
//   node web/scripts/current-versions-parity.mjs     # 退出码 0 = 夹具全组双端一致
//
// 退出码：
//   0 通过（夹具每一组的期望都与 Web 实现一致）
//   1 有组不一致（打印每组的期望、实际与用例名）
//   2 环境或打包失败（esbuild 不可用 / 打包报错 / 夹具缺失或不可解析）

import { createRequire } from 'node:module'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { readFile } from 'node:fs/promises'
import { dirname, join, relative } from 'node:path'

const WEB_DIR = dirname(dirname(fileURLToPath(import.meta.url)))
const REPO_DIR = dirname(WEB_DIR)
const ENTRY = join(WEB_DIR, 'src', 'lib', 'current-versions.ts')
const FIXTURE = join(REPO_DIR, 'internal', 'currentver', 'testdata', 'current-versions-cases.json')

// 夹具下限：R-a…R-f 每条路径至少一组（与 Go 侧 TestCurrentFixtures 同口径）
const MIN_CASES = 8

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
  console.error('[currentver] 错误：找不到 esbuild（vite 的传递依赖）。请先在 web/ 执行 pnpm install')
  process.exit(2)
}

let bundled
try {
  bundled = await esbuild.build({
    entryPoints: [ENTRY],
    bundle: true,
    // write:false：只做真实打包，不落盘（门禁脚本不留产物）
    write: false,
    outdir: join(WEB_DIR, '.current-versions-parity'),
    format: 'esm',
    platform: 'neutral',
    target: 'es2022',
    logLevel: 'silent',
  })
} catch (error) {
  console.error('[currentver] 错误：web/src/lib/current-versions.ts 打包失败')
  for (const message of error.errors ?? []) {
    console.error(`[currentver]   ${relative(WEB_DIR, message.location?.file ?? ENTRY)}: ${message.text}`)
  }
  if (!error.errors?.length) console.error(`[currentver]   ${error.message}`)
  process.exit(2)
}

const jsOutput = bundled.outputFiles?.find((file) => file.path.endsWith('.js'))
if (!jsOutput) {
  console.error('[currentver] 错误：打包没有产出 JavaScript 输出')
  process.exit(2)
}

// 用 data URL import 打包产物：不写临时文件，也不让脚本自己实现一遍规则
let currentTaskVersions
try {
  const module_ = await import(`data:text/javascript;base64,${Buffer.from(jsOutput.contents).toString('base64')}`)
  currentTaskVersions = module_.currentTaskVersions
} catch (error) {
  console.error(`[currentver] 错误：打包产物无法 import：${error.message}`)
  process.exit(2)
}
if (typeof currentTaskVersions !== 'function') {
  console.error('[currentver] 错误：web/src/lib/current-versions.ts 没有导出 currentTaskVersions')
  process.exit(2)
}

let fixture
try {
  fixture = JSON.parse(await readFile(FIXTURE, 'utf8'))
} catch (error) {
  console.error(`[currentver] 错误：读不到共享夹具 ${relative(REPO_DIR, FIXTURE)}：${error.message}`)
  process.exit(2)
}
const cases = Array.isArray(fixture?.cases) ? fixture.cases : null
if (!cases) {
  console.error(`[currentver] 错误：夹具 ${relative(REPO_DIR, FIXTURE)} 缺少 cases 数组`)
  process.exit(2)
}
if (cases.length < MIN_CASES) {
  console.error(`[currentver] 错误：夹具只有 ${cases.length} 组，至少要 ${MIN_CASES} 组（R-a…R-f 覆盖）`)
  process.exit(1)
}

const sameSequence = (got, want) => got.length === want.length && got.every((id, index) => id === want[index])

let failed = 0
for (const testCase of cases) {
  const name = testCase?.name ?? '<未命名用例>'
  const refs = Array.isArray(testCase?.refs) ? testCase.refs : []
  const want = Array.isArray(testCase?.want_material_ids) ? testCase.want_material_ids : []
  const got = currentTaskVersions(refs).map((ref) => ref.material_id)
  const ok = sameSequence(got, want)
  if (!ok) failed += 1
  console.log(`${ok ? 'OK  ' : 'FAIL'} ${name}`)
  if (!ok) {
    console.log(`     期望 [${want.join(', ')}]`)
    console.log(`     实际 [${got.join(', ')}]`)
  }
}

console.log(`[currentver] 夹具 ${cases.length} 组，通过 ${cases.length - failed} 组，失败 ${failed} 组`)
if (failed > 0) {
  console.error('[currentver] 失败：Web 侧 currentTaskVersions 与共享夹具不一致（Go 侧同夹具见 internal/currentver 测试）')
  process.exit(1)
}
console.log('[currentver] 通过：Web 实现与 Go 侧共享夹具逐组一致')
