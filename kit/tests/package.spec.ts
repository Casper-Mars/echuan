/**
 * 打包与分发契约单测（真实执行 `npm pack --dry-run`，不 mock）。
 *
 * 覆盖验收项：
 * 1. 包内含运行期必需文件：`lib/index.js`、技能正文、`cordis.patch.yml`、`package.json`；
 * 2. 包内含类型声明（至少一个 `lib/types/*.d.ts`）；
 * 3. 包内不含 `src/`、`tests/`、`node_modules/`，也不含 `*.tgz`；
 * 4. `files` 白名单与约定字符串完全一致（防止回退到包含 src 的旧配置）；
 * 5. `dsh.bundle.patch` 指向的 patch 文件确实随包发布，且 `exports` 子路径有效；
 * 6. `prepack` 钩子存在，保证干净 clone 下打包前先产出 `lib/`。
 */
import { execFileSync } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { beforeAll, describe, expect, it } from 'vitest'

const kitRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

interface PackageJson {
  files?: string[]
  scripts?: Record<string, string>
  exports?: Record<string, unknown>
  dsh?: { bundle?: { patch?: string } }
}

const pkg = JSON.parse(readFileSync(path.join(kitRoot, 'package.json'), 'utf8')) as PackageJson

/** 约定白名单：只含运行期必需产物，不含源码与测试。 */
const EXPECTED_FILES = [
  'lib/**/*.js',
  'lib/types/**/*.d.ts',
  'lib/**/*.md',
  'cordis.patch.yml',
]

/**
 * 执行 `npm pack --dry-run --json` 并返回包内文件路径清单。
 *
 * 注意：`prepack`（`pnpm build`）的构建日志也会写到 stdout，因此 JSON 之前
 * 可能混有非 JSON 行，需要从最后一个顶层数组起始处开始解析。
 */
function listPackedFiles(): string[] {
  const raw = execFileSync('npm', ['pack', '--dry-run', '--json'], {
    cwd: kitRoot,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'ignore'],
  })

  const marker = '\n[\n'
  const start = raw.lastIndexOf(marker)
  if (start < 0) {
    throw new Error(`npm pack --json 输出无法定位 JSON 起点：\n${raw}`)
  }

  const parsed = JSON.parse(raw.slice(start + 1)) as Array<{
    files?: Array<{ path: string }>
    entryCount?: number
  }>
  const entry = parsed[0]
  if (!entry?.files) {
    throw new Error(`npm pack --json 输出缺少 files 字段：\n${raw}`)
  }
  return entry.files.map((file) => file.path)
}

let packedFiles: string[] = []

beforeAll(() => {
  packedFiles = listPackedFiles()
}, 60_000)

describe('包内文件清单', () => {
  it('含运行期必需产物：入口、技能正文、patch、package.json', () => {
    expect(packedFiles).toContain('lib/index.js')
    expect(packedFiles).toContain('lib/assets/echuan-task-execution.md')
    expect(packedFiles).toContain('cordis.patch.yml')
    expect(packedFiles).toContain('package.json')
  })

  it('含类型声明（至少一个 lib/types/*.d.ts）', () => {
    const declarations = packedFiles.filter((file) => /^lib\/types\/.*\.d\.ts$/.test(file))
    expect(declarations.length).toBeGreaterThan(0)
  })

  it('不含源码、测试、依赖目录', () => {
    const leaked = packedFiles.filter(
      (file) =>
        file.startsWith('src/') ||
        file.startsWith('tests/') ||
        file.startsWith('node_modules/'),
    )
    expect(leaked).toEqual([])
  })

  it('不含打包产物 *.tgz', () => {
    expect(packedFiles.filter((file) => file.endsWith('.tgz'))).toEqual([])
  })
})

describe('打包配置', () => {
  it('files 白名单与约定一致（不得包含 src）', () => {
    expect(pkg.files).toEqual(EXPECTED_FILES)
  })

  it('prepack 钩子在打包前重新构建', () => {
    expect(pkg.scripts?.prepack).toBe('pnpm build')
  })

  it('dsh.bundle.patch 指向随包发布的 patch 文件', () => {
    expect(pkg.dsh?.bundle?.patch).toBe('./cordis.patch.yml')
    expect(packedFiles).toContain('cordis.patch.yml')
  })

  it('exports 暴露的 patch 子路径在包内有效', () => {
    expect(pkg.exports?.['./cordis.patch.yml']).toBe('./cordis.patch.yml')
    const patchOnDisk = path.join(kitRoot, 'cordis.patch.yml')
    expect(existsSync(patchOnDisk)).toBe(true)
    expect(packedFiles).toContain(path.relative(kitRoot, patchOnDisk))
  })
})
