/**
 * 测试卫生闸门（任务-08 C）。
 *
 * 这个文件回答一个问题：**凭什么相信「测试全程不需要真实平台与网络」**。
 *
 * 1. 源码级：`src` 下只有 `client.ts` 调 `fetch(`；写盘 API 只允许出现在
 *    `src/tools/package.ts`（资料包下载，唯一例外）；`node:fs` 的导入只允许只读函数
 *    （package.ts 额外允许 `writeFileSync` / `unlinkSync`）；`node:fs/promises`（纯写侧）
 *    一个文件都不允许导入 —— 会话 cwd 就是工作区根目录，不再 create 目录；`config.ts` 不读
 *    `process.env`。模块导入检查同时覆盖单/双引号与 `import(...)` 动态导入形式。
 * 2. 运行时级：把 `globalThis.fetch` 换成一被调用就抛错的哨兵，再跑
 *    ① 23 个工具的完整链路 ② worker 心跳链路 → 哨兵必须 0 次调用。
 * 3. 运行时级：用 `vi.mock('node:fs', { spy: true })` 把 `node:fs` 的导出换成
 *    会记账的 spy（具名导入的 `import { writeFileSync }` 也能拦到），跑两轮
 *    worker 心跳 → 0 次写文件（worker_id 只在内存）。监视器先做一次自证
 *    （真的能抓到写入），避免「spy 没挂上」导致的假绿。23 个工具链路里只有
 *    `echuan_project_package` 允许写 1 次 tmpdir，且失败时 unlink。
 */
import * as fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Agent } from '@deepseek-ai/dsh-agent'
import type { ToolDefinition } from '@deepseek-ai/dsh-tools'
import { createClient, createWorkerClient, type FetchLike } from '../src/client.js'
import { resolveConfig } from '../src/config.js'
import { createAllEchuanTools } from '../src/tools/index.js'
import type { EchuanToolOptions } from '../src/tools/index.js'
import { createWorkerLoop, type WorkerTimerApi } from '../src/worker.js'
import { buildZip } from './helpers/zip.js'

// 整个文件都跑在「fs 导出被 spy 包裹」的环境里：具名导入与属性调用都能记账，
// 但实现仍是真实 fs（readFileSync / readdirSync 照常工作）。
vi.mock('node:fs', { spy: true })

const kitRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const srcDir = path.join(kitRoot, 'src')

// ---------------------------------------------------------------------------
// 源码扫描工具
// ---------------------------------------------------------------------------

function listTsFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) out.push(...listTsFiles(full))
    else if (entry.isFile() && entry.name.endsWith('.ts')) out.push(full)
  }
  return out.sort()
}

/** 去掉块注释与整行 `//` 注释，避免注释里的示例代码造成误报。 */
function stripComments(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

/** 字符串字面量片段：单/双引号都接受（转义正则元字符，模块名可含 `/`）。 */
function quoted(specifier: string): string {
  return `['"]${specifier.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}['"]`
}

/**
 * 具名导入的名字列表，覆盖两种写法：
 *
 * - 静态：`import { a, b as c } from 'spec'`
 * - 动态：`const { a, b as c } = await import('spec')`（`await` 可省略）
 */
function namedImportNames(code: string, specifier: string): string[] {
  const q = quoted(specifier)
  const patterns = [
    new RegExp(`import\\s*\\{([^}]*)\\}\\s*from\\s*${q}`, 'g'),
    new RegExp(
      `(?:const|let|var)\\s*\\{([^}]*)\\}\\s*=\\s*(?:await\\s*)?import\\s*\\(\\s*${q}\\s*\\)`,
      'g',
    ),
  ]
  const names: string[] = []
  for (const pattern of patterns) {
    let match: RegExpExecArray | null
    while ((match = pattern.exec(code)) !== null) {
      for (const part of (match[1] ?? '').split(',')) {
        const name = part.trim().split(/\s+as\s+/)[0]?.trim() ?? ''
        if (name !== '') names.push(name)
      }
    }
  }
  return names
}

/** 是否导入了某个模块（静态 `from` 或动态 `import()`，单/双引号均可）。 */
function importsModule(code: string, specifier: string): boolean {
  const q = quoted(specifier)
  return new RegExp(`(?:from\\s*${q})|(?:import\\s*\\(\\s*${q}\\s*\\))`).test(code)
}

/** 导入语句条数（静态 + 动态），用于「整个模块只允许导入一次」的断言。 */
function importOccurrences(code: string, specifier: string): number {
  const q = quoted(specifier)
  const pattern = new RegExp(`(?:from\\s*${q})|(?:import\\s*\\(\\s*${q}\\s*\\))`, 'g')
  return (code.match(pattern) ?? []).length
}

/** 是否存在具名导入（含动态解构），否则视为默认 / 命名空间导入。 */
function hasNamedImport(code: string, specifier: string): boolean {
  const q = quoted(specifier)
  return new RegExp(
    `(?:import\\s*\\{[^}]*\\}\\s*from\\s*${q})`
      + `|(?:(?:const|let|var)\\s*\\{[^}]*\\}\\s*=\\s*(?:await\\s*)?import\\s*\\(\\s*${q}\\s*\\))`,
  ).test(code)
}

interface SourceFile {
  /** 相对 kit 根目录的路径，例如 `src/tools/read.ts`。 */
  relative: string
  code: string
}

const sourceFiles: SourceFile[] = listTsFiles(srcDir).map((file) => ({
  relative: path.relative(kitRoot, file),
  code: stripComments(fs.readFileSync(file, 'utf8')),
}))

function sourceOf(relative: string): string {
  const hit = sourceFiles.find(file => file.relative === relative)
  if (hit === undefined) throw new Error(`源码清单缺少 ${relative}`)
  return hit.code
}

// ---------------------------------------------------------------------------
// 1. 源码级：网络出口唯一
// ---------------------------------------------------------------------------

describe('源码级：fetch 出口唯一', () => {
  it('src 下只有 client.ts 调用 fetch（其余文件 0 次）', () => {
    const offenders = sourceFiles
      .filter(file => file.relative !== 'src/client.ts')
      .filter(file => /\bfetch\s*\(/.test(file.code))
      .map(file => file.relative)

    expect(offenders).toEqual([])
    // 反向自证：client.ts 确实有（否则上面的过滤是空转）。
    expect(/\bfetch\s*\(/.test(sourceOf('src/client.ts'))).toBe(true)
  })

  it('工具层 / worker / runtime 都不直接 import http 客户端模块', () => {
    const offenders = sourceFiles
      .filter(file => file.relative !== 'src/client.ts')
      .filter(file => /\bfrom\s+'node:(http|https|net|tls)'/.test(file.code))
      .map(file => file.relative)

    expect(offenders).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// 2. 源码级：不写盘
// ---------------------------------------------------------------------------

/** 任何「写文件 / 建目录 / 删文件」的名字都不该出现在 src 里。 */
const WRITE_APIS = [
  'writeFileSync',
  'writeFile',
  'appendFileSync',
  'appendFile',
  'createWriteStream',
  'mkdirSync',
  'mkdtempSync',
  'rmSync',
  'rmdirSync',
  'unlinkSync',
  'truncateSync',
  'copyFileSync',
  'renameSync',
]

/** 唯一允许写盘的源码文件：资料包下载（且只能写 os.tmpdir()）。 */
const WRITE_EXCEPTION_FILE = 'src/tools/package.ts'

/** `node:fs` 允许的只读具名导入（非 package.ts）。 */
const FS_READ_ALLOWED_NAMES = [
  'readFileSync',
  'readFile',
  'existsSync',
  'statSync',
  'readdirSync',
  'createReadStream',
]
/** `package.ts` 额外允许独占写与失败清理。 */
const FS_PACKAGE_EXTRA_NAMES = ['writeFileSync', 'unlinkSync']

/**
 * 纯函数规则：`node:fs` 的具名导入只允许只读函数（package.ts 额外允许写/清理）。
 *
 * 覆盖静态 `import {…} from 'node:fs'` 与动态 `const {…} = await import('node:fs')`，
 * 单/双引号都匹配。抽成纯函数便于用合成源码断言规则本身不是空转。
 */
function scanFsNamedImports(
  files: ReadonlyArray<{ relative: string; code: string }>,
): string[] {
  const violations: string[] = []
  for (const file of files) {
    const allowedHere = new Set(
      file.relative === WRITE_EXCEPTION_FILE
        ? [...FS_READ_ALLOWED_NAMES, ...FS_PACKAGE_EXTRA_NAMES]
        : FS_READ_ALLOWED_NAMES,
    )
    for (const name of namedImportNames(file.code, 'node:fs')) {
      if (!allowedHere.has(name)) violations.push(`${file.relative}: ${name}`)
    }
  }
  return violations
}

describe('源码级：不写盘（package.ts 是唯一例外）', () => {
  it('除 package.ts 外不出现任何写盘 API', () => {
    const hits: string[] = []
    for (const file of sourceFiles) {
      if (file.relative === WRITE_EXCEPTION_FILE) continue
      for (const api of WRITE_APIS) {
        if (new RegExp(`\\b${api}\\s*\\(`).test(file.code)) hits.push(`${file.relative}: ${api}`)
      }
    }
    expect(hits).toEqual([])
    // 自证：例外文件确实写盘，否则「唯一例外」无从谈起。
    expect(sourceOf(WRITE_EXCEPTION_FILE)).toContain('writeFileSync')
    expect(sourceOf(WRITE_EXCEPTION_FILE)).toContain('unlinkSync')
    expect(sourceOf(WRITE_EXCEPTION_FILE)).toContain('os.tmpdir()')
  })

  it('node:fs 的具名导入只允许只读函数（package.ts 额外允许独占写与清理）', () => {
    expect(scanFsNamedImports(sourceFiles)).toEqual([])
    // skill.ts 确实用只读方式读技能正文（证明上面的白名单不是空转）。
    expect(sourceOf('src/skill.ts')).toContain('readFileSync')
  })

  it('规则不是空转：双引号 / 动态 import 的写 API 在 config.ts 都会被抓', () => {
    // 单引号写法能抓，双引号同样不能漏（否则换个引号就绕过白名单）。
    expect(scanFsNamedImports([
      { relative: 'src/config.ts', code: 'import { writeFileSync } from "node:fs"' },
    ])).toEqual(['src/config.ts: writeFileSync'])

    // 动态解构导入同样按具名导入处理。
    expect(scanFsNamedImports([
      { relative: 'src/config.ts', code: 'const { writeFileSync } = await import("node:fs")' },
    ])).toEqual(['src/config.ts: writeFileSync'])

    // 合法只读导入（双引号）不误报。
    expect(scanFsNamedImports([
      { relative: 'src/config.ts', code: 'import { readFileSync } from "node:fs"' },
    ])).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// 2b. 源码级：node:fs/promises 一律不导入
// ---------------------------------------------------------------------------

const PROMISES_MODULE = 'node:fs/promises'

/** 是否出现 `node:fs/promises` 的导入语句（静态 / 动态，单/双引号）。 */
function importsPromises(code: string): boolean {
  return importsModule(code, PROMISES_MODULE)
}

/** 解析 `node:fs/promises` 的具名导入（不含别名；含动态解构）。 */
function promisesImportNames(code: string): string[] {
  return namedImportNames(code, PROMISES_MODULE)
}

/**
 * 纯函数规则：`node:fs/promises` 是**纯写侧**模块（mkdir / rm / writeFile…），
 * 插件源码一个都不允许导入 —— 会话 cwd 就是工作区根目录，不再创建任何目录。
 *
 * 抽成纯函数是为了让「规则本身」也能被断言（防止它变成永远为空的空转检查）。
 */
function scanPromisesImports(
  files: ReadonlyArray<{ relative: string; code: string }>,
): string[] {
  const violations: string[] = []
  for (const file of files) {
    if (!importsPromises(file.code)) continue
    if (!hasNamedImport(file.code, PROMISES_MODULE)) {
      violations.push(`${file.relative}: 非具名导入`)
      continue
    }
    const names = promisesImportNames(file.code)
    violations.push(
      ...(names.length === 0 ? [`${file.relative}: 整个模块`] : names.map(name => `${file.relative}: ${name}`)),
    )
  }
  return violations
}

describe('源码级：node:fs/promises 一律不导入', () => {
  it('全量扫描：没有任何文件导入 node:fs/promises（含 session.ts）', () => {
    expect(scanPromisesImports(sourceFiles)).toEqual([])
    expect(sourceFiles.filter(file => importsPromises(file.code))).toEqual([])
    // 反向自证：session.ts 现在只用只读的 `stat` 探测目录存在性。
    expect(sourceOf('src/session.ts')).toContain('statSync')
  })

  it('规则不是空转：任何文件、任何函数、任何写法都会被抓出来', () => {
    expect(scanPromisesImports([
      { relative: 'src/tools/read.ts', code: "import { mkdir } from 'node:fs/promises'" },
    ])).toEqual(['src/tools/read.ts: mkdir'])

    // 会话层自己也不行：cwd 不再建目录。
    expect(scanPromisesImports([
      { relative: 'src/session.ts', code: "import { mkdir } from 'node:fs/promises'" },
    ])).toEqual(['src/session.ts: mkdir'])

    expect(scanPromisesImports([
      { relative: 'src/session.ts', code: "import { mkdir, rm } from 'node:fs/promises'" },
    ])).toEqual(['src/session.ts: mkdir', 'src/session.ts: rm'])

    expect(scanPromisesImports([
      { relative: 'src/session.ts', code: "import fsp from 'node:fs/promises'" },
    ])).toEqual(['src/session.ts: 非具名导入'])
  })

  it('规则不是空转：双引号与动态 import 同样按同一套规则处理', () => {
    expect(scanPromisesImports([
      { relative: 'src/config.ts', code: 'import { mkdir } from "node:fs/promises"' },
    ])).toEqual(['src/config.ts: mkdir'])

    expect(scanPromisesImports([
      { relative: 'src/session.ts', code: 'const { mkdir } = await import("node:fs/promises")' },
    ])).toEqual(['src/session.ts: mkdir'])

    expect(scanPromisesImports([
      { relative: 'src/session.ts', code: "const fsp = await import('node:fs/promises')" },
    ])).toEqual(['src/session.ts: 非具名导入'])
  })
})

// ---------------------------------------------------------------------------
// 3. 源码级：不读环境变量
// ---------------------------------------------------------------------------

describe('源码级：不读 process.env', () => {
  it('config.ts 不读 process.env（配置只来自宿主注入）', () => {
    expect(sourceOf('src/config.ts')).not.toContain('process.env')
  })

  it('src 全量也不读 process.env（没有隐藏的环境开关）', () => {
    const offenders = sourceFiles
      .filter(file => file.code.includes('process.env'))
      .map(file => file.relative)
    expect(offenders).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// 假平台 / 假时钟
// ---------------------------------------------------------------------------

interface RecordedCall {
  url: URL
  method: string
  body: string | undefined
}

function createPlatform(respond: (call: RecordedCall) => Response): {
  calls: RecordedCall[]
  fetchImpl: FetchLike
} {
  const calls: RecordedCall[] = []
  const fetchImpl: FetchLike = async (input, init = {}) => {
    const call: RecordedCall = {
      url: new URL(input),
      method: init.method ?? 'GET',
      body: init.body === undefined ? undefined : String(init.body),
    }
    calls.push(call)
    return respond(call)
  }
  return { calls, fetchImpl }
}

function json(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

const CLAIM_PAYLOAD = {
  task: {
    id: 'APP-1-T0001',
    project_id: 'APP-1',
    title: '实现登录接口',
    body_md: '# 任务正文',
    attempt: 1,
    priority: 10,
  },
  lease: { lease_id: 'l_9f8b', until: '2026-10-27T16:49:25.157Z', ttl_seconds: 90 },
  context: {
    spec_version: 1,
    spec_digest: 'sha256:bb23',
    product: { id: 'APP', name: '驿传示例产品', git_url: 'git@example.com:APP.git' },
  },
}

const respondByPath = (call: RecordedCall): Response => {
  switch (call.url.pathname) {
    case '/v1/tasks/claim':
      return json(CLAIM_PAYLOAD)
    case '/v1/tasks/heartbeat':
      return json({ lease_id: 'l_next', until: '2026-10-27T16:51:00.000Z', ttl_seconds: 90 })
    case '/v1/tasks/report':
      return json({ task_id: 'APP-1-T0001', status_name: '待验收' })
    case '/v1/inbox':
      return json({
        items: [],
        count: 0,
        review_stale_hours: 24,
        generated_at: '2026-10-28T00:00:00.000Z',
      })
    case '/v1/products':
      if (call.method === 'POST') {
        return json({ product_id: 'APPX', name: '示例产品', git_url: 'git@example.com:APPX.git' })
      }
      return json({ items: [] })
    case '/v1/projects':
      if (call.method === 'POST') {
        return json({
          project_id: 'APP-3',
          product_id: 'APP',
          title: '新项目',
          status: 0,
          status_name: '需求草稿',
          version: 1,
        })
      }
      if (call.method === 'PATCH') {
        return json({ project_id: 'APP-2', title: '新项目名', status: 2, status_name: '方案草稿', version: 4 })
      }
      return call.url.searchParams.has('id')
        ? json({ project_id: 'APP-2', title: '示例项目' })
        : json({ items: [] })
    case '/v1/projects/requirement':
      return json({
        project_id: 'APP-2',
        requirement: { material_id: 'M-REQ-2', version: 2, title: '需求', size: 128 },
      })
    case '/v1/projects/submit':
      return json({ project_id: 'APP-2', status: 1, status_name: '需求评审', version: 2 })
    case '/v1/projects/plans':
      return json({
        project_id: 'APP-2',
        status: 5,
        status_name: '任务评审',
        project_version: 6,
        tasks: [{
          id: 'APP-2-T0001',
          seq_no: 1,
          title: '卡 1',
          priority: 0,
          status: 0,
          status_name: '待办',
          depends_on: [],
        }],
      })
    case '/v1/specs':
      return json({
        spec_id: 'S-1',
        project_id: 'APP-2',
        title: '方案 A',
        version: call.method === 'PATCH' ? 4 : 1,
        material: { material_id: 'M-SPEC-1' },
      })
    case '/v1/tasks':
      if (call.method === 'POST') {
        return json({
          task_id: 'APP-2-T0003',
          project_id: 'APP-2',
          seq_no: 3,
          title: '卡 3',
          body_md: '正文 3',
          priority: 7,
          status: 0,
          status_name: '待办',
          depends_on: ['APP-2-T0001'],
        })
      }
      if (call.method === 'PATCH') {
        return json({ task_id: 'APP-2-T0003', title: '新标题', priority: 1, status: 0, status_name: '待办' })
      }
      if (call.method === 'DELETE') {
        return new Response(null, { status: 204 })
      }
      return call.url.searchParams.has('id')
        ? json({ task_id: 'APP-1-T0007', title: '示例任务' })
        : json({ items: [] })
    case '/v1/projects/package':
      return new Response(buildZip([
        { name: '需求-v1-示例.md', content: '# 需求' },
        { name: '方案-S0001-v1-示例.md', content: '# 方案' },
      ]), {
        status: 200,
        headers: { 'content-type': 'application/zip' },
      })
    case '/v1/materials':
      return new Response('# 资料正文\n', {
        status: 200,
        headers: { 'content-type': 'text/markdown' },
      })
    default:
      return json({})
  }
}

/** worker 心跳回包：第一轮给一条 run_task，验证派单路径也不碰网络/磁盘。 */
function workerPayload(call: RecordedCall): Response {
  const body = call.body === undefined ? {} : (JSON.parse(call.body) as Record<string, unknown>)
  if (body['worker_id'] === undefined) {
    return json({
      worker: { worker_id: 'w_TEST01ABCDEFGHIJKLMN', heartbeat_interval_ms: 15_000 },
      commands: [],
    })
  }
  return json({
    worker: { worker_id: 'w_TEST01ABCDEFGHIJKLMN', heartbeat_interval_ms: 15_000 },
    commands: [{ kind: 'run_task', ...CLAIM_PAYLOAD }],
  })
}

const config = resolveConfig({ baseUrl: 'http://platform.test' })
const pollConfig = resolveConfig({
  baseUrl: 'http://platform.test',
  worker: { poll: { enable: true }, metadata: { host: 'ci' } },
})

const silentLogger = { info: () => {}, warn: () => {}, debug: () => {} }

const exec = { signal: new AbortController().signal } as unknown as Parameters<
  ToolDefinition['execute']
>[1]

async function flush(times = 4): Promise<void> {
  for (let index = 0; index < times; index += 1) {
    await new Promise<void>(resolve => setImmediate(resolve))
  }
}

interface FakeTimer {
  api: WorkerTimerApi
  /** 让所有挂起的定时器各触发一次（模拟时钟前进）。 */
  fireAll(): void
  pending(): number
}

function createFakeTimer(): FakeTimer {
  let seq = 0
  const timers = new Map<number, () => void>()
  return {
    api: {
      setTimeout(handler: () => void): unknown {
        seq += 1
        timers.set(seq, handler)
        return seq
      },
      clearTimeout(handle: unknown): void {
        timers.delete(handle as number)
      },
    },
    fireAll(): void {
      const handlers = [...timers.values()]
      timers.clear()
      for (const handler of handlers) handler()
    },
    pending(): number {
      return timers.size
    },
  }
}

/** 跑完 23 个工具的完整调用链（`echuan_worker_info` 零 HTTP），返回 package 落盘路径。 */
async function runWholeToolChain(fetchImpl: FetchLike): Promise<string[]> {
  const options: EchuanToolOptions = {
    client: createClient(config, { fetchImpl }),
    identity: { workerId: 'w_TEST01ABCDEFGHIJKLMN' },
  }
  const tools = new Map(createAllEchuanTools(options).map(tool => [tool.name, tool]))

  const calls: Array<[string, Record<string, unknown>]> = [
    ['echuan_task_claim', { product: 'APP' }],
    ['echuan_task_renew', { task_id: 'APP-1-T0001', lease_id: 'l_9f8b' }],
    ['echuan_task_report', { task_id: 'APP-1-T0001', lease_id: 'l_9f8b', summary_md: '四段式报告' }],
    ['echuan_project_create', { product_id: 'APP', title: '新项目' }],
    ['echuan_project_requirement_write', { project_id: 'APP-2', body_md: '# 需求正文' }],
    ['echuan_project_submit', { project_id: 'APP-2' }],
    ['echuan_spec_create', { project_id: 'APP-2', title: '方案 A', body_md: '# 方案正文' }],
    ['echuan_spec_revise', { spec_id: 'S-1', version: 1, title: '方案 A', body_md: '# 方案正文 v2' }],
    ['echuan_plan_submit', {
      project_id: 'APP-2',
      tasks: [{ title: '卡 1', body_md: '正文 1' }],
    }],
    ['echuan_task_create', {
      project_id: 'APP-2',
      title: '卡 3',
      body_md: '正文 3',
      priority: 7,
      depends_on: ['APP-2-T0001'],
    }],
    ['echuan_task_update', { task_id: 'APP-2-T0003', title: '新标题' }],
    ['echuan_task_delete', { task_id: 'APP-2-T0003' }],
    ['echuan_project_update', { project_id: 'APP-2', title: '新项目名' }],
    ['echuan_product_create', {
      product_id: 'APPX',
      name: '示例产品',
      git_url: 'git@example.com:APPX.git',
    }],
    ['echuan_project_package', { project_id: 'APP-2' }],
    ['echuan_inbox', { limit: 5 }],
    ['echuan_product_list', { limit: 5 }],
    ['echuan_project_list', { product_id: 'APP' }],
    ['echuan_task_list', { project_id: 'APP-1' }],
    ['echuan_project_get', { id: 'APP-2' }],
    ['echuan_task_get', { id: 'APP-1-T0007' }],
    ['echuan_material_get', { id: 'M-1' }],
    // 只读诊断：零 HTTP、零写盘。
    ['echuan_worker_info', {}],
  ]

  const savedPaths: string[] = []
  for (const [name, args] of calls) {
    const tool = tools.get(name)
    if (tool === undefined) throw new Error(`工具 ${name} 未注册`)
    const value = await tool.execute(args, exec) as unknown
    if (name === 'echuan_project_package') {
      savedPaths.push((value as { saved_path: string }).saved_path)
    }
  }
  return savedPaths
}

interface WorkerRun {
  loop: ReturnType<typeof createWorkerLoop>
  calls: RecordedCall[]
  timer: FakeTimer
  delivered: string[]
}

/** 项目会话桩：`id` 即项目号（APP-1），投递按 `task.project_id` 命中它。 */
const ROOT_AGENT = { id: 'APP-1', status: 'running' } as unknown as Agent

/** 跑 `rounds` 轮 worker 心跳（每轮都由假定时器触发：首轮也要推进过 5s 延迟）。 */
async function runWorkerRounds(rounds = 2): Promise<WorkerRun> {
  const platform = createPlatform(workerPayload)
  const timer = createFakeTimer()
  const delivered: string[] = []

  const loop = createWorkerLoop({
    config: pollConfig,
    client: createWorkerClient(config, { fetchImpl: platform.fetchImpl }),
    sessions: { get: (id: string) => (id === ROOT_AGENT.id ? ROOT_AGENT : undefined) },
    ensureSession: async (projectId: string) => (
      projectId === ROOT_AGENT.id ? { agent: ROOT_AGENT } : undefined
    ),
    logger: silentLogger,
    timer: timer.api,
    deliver: (_agent, text) => delivered.push(text),
  })

  loop.start()
  await flush()
  // 首轮心跳延迟 FIRST_HEARTBEAT_DELAY_MS 才发出，因此第 1 轮同样由定时器触发。
  timer.fireAll()
  await flush()

  for (let round = 1; round < rounds; round += 1) {
    timer.fireAll()
    await flush()
  }

  return { loop, calls: platform.calls, timer, delivered }
}

afterEach(() => {
  vi.clearAllMocks()
})

// ---------------------------------------------------------------------------
// 4. 运行时级：哨兵 fetch
// ---------------------------------------------------------------------------

interface Sentinel {
  calls: number
  restore(): void
}

/**
 * 把 `globalThis.fetch` 换成「一被调用就记数并抛错」的哨兵。
 *
 * 任何没走注入 `fetchImpl` 的代码都会立刻炸掉并留下计数，测试不可能「静默打网」。
 */
function installFetchSentinel(): Sentinel {
  const original = globalThis.fetch
  const sentinel = { calls: 0 }
  globalThis.fetch = (() => {
    sentinel.calls += 1
    throw new Error('测试打到了真实网络：globalThis.fetch 被调用')
  }) as unknown as typeof fetch
  return {
    get calls() {
      return sentinel.calls
    },
    restore(): void {
      globalThis.fetch = original
    },
  } as Sentinel
}

describe('运行时级：不触达真实 fetch', () => {
  it('23 个工具完整链路：哨兵 0 次调用，假平台收到 22 个请求（worker_info 零 HTTP）', async () => {
    const platform = createPlatform(respondByPath)
    const sentinel = installFetchSentinel()
    const savedPaths: string[] = []
    try {
      savedPaths.push(...await runWholeToolChain(platform.fetchImpl))
      expect(sentinel.calls).toBe(0)
    } finally {
      sentinel.restore()
      for (const saved of savedPaths) fs.unlinkSync(saved)
    }

    expect(platform.calls).toHaveLength(22)
  })

  it('worker 心跳两轮（含派单投递）：哨兵 0 次调用', async () => {
    const sentinel = installFetchSentinel()
    let run: WorkerRun | undefined
    try {
      run = await runWorkerRounds(2)
      expect(sentinel.calls).toBe(0)
    } finally {
      sentinel.restore()
    }

    if (run === undefined) throw new Error('worker 心跳未跑完')
    expect(run.calls).toHaveLength(2)
    expect(run.delivered).toHaveLength(1)
    run.loop.dispose()
  })
})

// ---------------------------------------------------------------------------
// 5. 运行时级：不写盘
// ---------------------------------------------------------------------------

type Spied = { mock: { calls: unknown[][] } }

/** 五个写盘函数（含具名导入的调用，因为整个 fs 模块已被 spy 包裹）。 */
function writeSpies(): Spied[] {
  return [
    fs.writeFileSync,
    fs.writeFile,
    fs.appendFileSync,
    fs.appendFile,
    fs.createWriteStream,
  ] as unknown as Spied[]
}

describe('运行时级：不写盘', () => {
  it('worker 心跳两轮：0 次写文件（worker_id 只在内存）', async () => {
    const spies = writeSpies()
    // 自证：spy 真的挂上了（否则「0 次」毫无意义）。
    expect(vi.isMockFunction(fs.writeFileSync)).toBe(true)

    const probe = path.join(os.tmpdir(), `echuan-hygiene-${process.pid}-${Date.now()}.txt`)
    fs.writeFileSync(probe, 'probe')
    expect(spies[0]!.mock.calls.length).toBe(1)
    fs.unlinkSync(probe)
    for (const spy of spies) spy.mock.calls.length = 0

    const run = await runWorkerRounds(2)
    run.loop.dispose()

    for (const spy of spies) expect(spy.mock.calls).toEqual([])
    // worker_id 只在内存：第二轮请求体带的是第一轮回包里的 id。
    const secondBody = run.calls[1]!.body as string
    expect(JSON.parse(secondBody)['worker_id']).toBe('w_TEST01ABCDEFGHIJKLMN')
  })

  it('23 个工具完整链路：只有资料包写 1 次 tmpdir，其余 0 次写盘', async () => {
    const spies = writeSpies()
    const platform = createPlatform(respondByPath)

    const savedPaths = await runWholeToolChain(platform.fetchImpl)
    try {
      // 唯一允许的写入：writeFileSync，且路径必须在 os.tmpdir() 下。
      expect(savedPaths).toHaveLength(1)
      expect(savedPaths[0]!.startsWith(os.tmpdir())).toBe(true)

      const writeCalls = spies[0]!.mock.calls
      expect(writeCalls).toHaveLength(1)
      expect(String(writeCalls[0]![0]).startsWith(os.tmpdir())).toBe(true)
      // flag 必须是 wx（不覆盖已有文件）。
      expect((writeCalls[0]![2] as { flag?: string } | undefined)?.flag).toBe('wx')
      for (const spy of spies.slice(1)) expect(spy.mock.calls).toEqual([])
      expect(platform.calls).toHaveLength(22)
    } finally {
      for (const saved of savedPaths) fs.unlinkSync(saved)
    }
  })
})
