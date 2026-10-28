/**
 * 契约漂移闸门（任务-08 A）。
 *
 * 目的：`api/openapi.yaml` 是唯一契约来源，`src/client.ts` 的 `TOOL_OPERATIONS`
 * 是客户端对契约的「声明」。两者一旦漂移（改路径、改 method、改 operationId、
 * 多加/少加一个动作），这里必须立刻变红。
 *
 * 覆盖：
 * 1. `TOOL_OPERATIONS` 恰好 7 条，字段与白名单逐项一致（顺序也固定）；
 * 2. 每条 `METHOD /path` 在 yaml `paths` 段中真实存在且唯一；
 * 3. 每条 `operationId` 与 yaml 中该 method 的实际值一致；
 * 4. `TOOL_METHODS` 恰好 7 条且与 `TOOL_OPERATIONS` 顺序一一对应；
 * 5. 注册工具数 = 7（3 写 + 4 读）、名字唯一；
 * 6. worker 心跳端点 `POST /v1/workers/heartbeat`（heartbeatWorker）也在契约里；
 * 7. 物料读取返回**原始正文**：`content-type` 非 JSON 时不做 JSON 解析。
 *
 * 解析器说明：仓库没有 `yaml` 依赖，且本任务禁止新增依赖，因此这里用
 * 「按缩进切段」的保守解析器（只认 `paths:` 段下的 2/4/6 空格层级）。
 * 解析失败（找不到段、命中数不是 7）会直接抛错，不会静默通过。
 */
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  TOOL_METHODS,
  TOOL_OPERATIONS,
  createClient,
  createWorkerClient,
  type FetchLike,
} from '../src/client.js'
import { resolveConfig } from '../src/config.js'
import { TOOL_NAMES, createAllEchuanTools } from '../src/tools/index.js'

const kitRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const openapiPath = path.resolve(kitRoot, '..', 'api', 'openapi.yaml')
const openapiYaml = readFileSync(openapiPath, 'utf8')

// ---------------------------------------------------------------------------
// 极简 YAML paths 解析（无依赖）
// ---------------------------------------------------------------------------

interface YamlOperation {
  path: string
  method: string
  operationId: string
}

/**
 * 解析 `paths:` 段，返回 `{ path, method, operationId }` 列表。
 *
 * 只依赖本仓库 openapi.yaml 的稳定缩进：path 键 2 空格、method 键 4 空格、
 * `operationId` 6 空格。遇到下一个顶层键（行首非空白）即结束。
 */
function parsePathOperations(yaml: string): YamlOperation[] {
  const lines = yaml.split(/\r?\n/)
  const start = lines.findIndex(line => /^paths:\s*$/.test(line))
  if (start < 0) throw new Error('openapi.yaml 缺少顶层 paths 段，无法校验契约')

  const operations: YamlOperation[] = []
  let currentPath: string | undefined
  let currentMethod: string | undefined

  for (let index = start + 1; index < lines.length; index += 1) {
    const line = lines[index] as string
    if (line.trim() === '') continue
    // 下一个顶层键：paths 段结束。
    if (/^\S/.test(line)) break
    // 段内注释（`  # xxx`）忽略。
    if (/^ {2}#/.test(line)) continue

    const pathMatch = /^ {2}(\/[^:\s]*):\s*$/.exec(line)
    if (pathMatch?.[1] !== undefined) {
      currentPath = pathMatch[1]
      currentMethod = undefined
      continue
    }

    const methodMatch = /^ {4}(get|post|put|patch|delete|head|options):\s*$/i.exec(line)
    if (methodMatch?.[1] !== undefined) {
      currentMethod = methodMatch[1].toUpperCase()
      continue
    }

    const operationMatch = /^ {6}operationId:\s*(\S+)\s*$/.exec(line)
    if (operationMatch?.[1] !== undefined) {
      if (currentPath === undefined || currentMethod === undefined) {
        throw new Error(`openapi.yaml 解析异常：operationId 出现在无 host 的位置：${line}`)
      }
      operations.push({ path: currentPath, method: currentMethod, operationId: operationMatch[1] })
    }
  }

  return operations
}

const yamlOperations = parsePathOperations(openapiYaml)

function findYamlOperation(method: string, requestPath: string): YamlOperation | undefined {
  return yamlOperations.find(op => op.method === method && op.path === requestPath)
}

// ---------------------------------------------------------------------------
// 客户端声明（真实值，逐字抄自 src/client.ts；改契约必须先改这里）
// ---------------------------------------------------------------------------

/**
 * 客户端被允许使用的 7 个 operation，**顺序即 `TOOL_OPERATIONS` 顺序**。
 *
 * 序号/内容与 `api/openapi.yaml` 的 `operationId` 完全一致；多一个、少一个、
 * 换顺序、换路径都会让下面的用例变红。
 */
const EXPECTED_OPERATIONS = [
  { tool: 'claimTask', method: 'POST', path: '/v1/tasks/claim', operationId: 'claimTask' },
  { tool: 'renewLease', method: 'POST', path: '/v1/tasks/heartbeat', operationId: 'heartbeatTask' },
  { tool: 'reportTask', method: 'POST', path: '/v1/tasks/report', operationId: 'reportTask' },
  { tool: 'getInbox', method: 'GET', path: '/v1/inbox', operationId: 'listInbox' },
  { tool: 'listProjects', method: 'GET', path: '/v1/projects', operationId: 'listOrGetProjects' },
  { tool: 'listTasks', method: 'GET', path: '/v1/tasks', operationId: 'listOrGetTasks' },
  { tool: 'getMaterials', method: 'GET', path: '/v1/materials', operationId: 'downloadMaterial' },
] as const

// ---------------------------------------------------------------------------
// 假平台
// ---------------------------------------------------------------------------

interface RecordedCall {
  url: string
  init: RequestInit
  body: string | undefined
}

function createPlatform(respond: (url: string) => Response): {
  calls: RecordedCall[]
  fetchImpl: FetchLike
} {
  const calls: RecordedCall[] = []
  const fetchImpl: FetchLike = async (url, init = {}) => {
    calls.push({ url, init, body: init.body === undefined ? undefined : String(init.body) })
    return respond(url)
  }
  return { calls, fetchImpl }
}

const config = resolveConfig({ baseUrl: 'http://platform.test' })

// ---------------------------------------------------------------------------
// 1. 客户端声明的 7 个 operation
// ---------------------------------------------------------------------------

describe('契约闸门：客户端声明的 operation 恰好 7 个', () => {
  it('TOOL_OPERATIONS 与白名单逐字段一致（含顺序）', () => {
    expect(TOOL_OPERATIONS).toHaveLength(7)
    expect(TOOL_OPERATIONS).toEqual(EXPECTED_OPERATIONS)
  })

  it('operationId 序列稳定，正好是契约里的 7 个动作', () => {
    expect(TOOL_OPERATIONS.map(entry => entry.operationId)).toEqual([
      'claimTask',
      'heartbeatTask',
      'reportTask',
      'listInbox',
      'listOrGetProjects',
      'listOrGetTasks',
      'downloadMaterial',
    ])
  })

  it('每条 METHOD /path 都在 openapi.yaml 的 paths 段中真实存在且唯一', () => {
    // 解析器自检：yaml 里确实有大量 operation，不是解析失败返回空数组。
    expect(yamlOperations.length).toBeGreaterThan(20)

    // 这里遍历的是**客户端实际声明** `TOOL_OPERATIONS`（不是上面那份白名单）：
    // 代码里写错路径，即使白名单没改，也必须在这里变红。
    for (const entry of TOOL_OPERATIONS) {
      const hits = yamlOperations.filter(
        op => op.method === entry.method && op.path === entry.path,
      )
      expect(
        hits,
        `${entry.tool} 期望 ${entry.method} ${entry.path} 在 yaml 中恰好出现一次`,
      ).toHaveLength(1)
    }
  })

  it('每条 operationId 与 yaml 中该 method 的实际值一致', () => {
    for (const entry of TOOL_OPERATIONS) {
      const hit = findYamlOperation(entry.method, entry.path)
      expect(hit, `${entry.tool} 的 ${entry.method} ${entry.path} 未在 yaml 找到`).toBeDefined()
      expect(hit?.operationId, `${entry.tool} operationId 漂移`).toBe(entry.operationId)
    }
  })

  it('TOOL_METHODS 恰好 7 条，且与 TOOL_OPERATIONS 顺序一一对应', () => {
    expect(TOOL_METHODS).toHaveLength(7)
    expect([...TOOL_METHODS]).toEqual(EXPECTED_OPERATIONS.map(entry => entry.tool))
  })
})

// ---------------------------------------------------------------------------
// 2. 注册的工具数
// ---------------------------------------------------------------------------

describe('契约闸门：注册工具数 = 7', () => {
  it('createAllEchuanTools 产出 7 个定义，名字唯一且与 TOOL_NAMES 一致', () => {
    const anyClient = createClient(config, { fetchImpl: async () => new Response('{}') })
    const tools = createAllEchuanTools({ client: anyClient, identity: { workerId: undefined } })

    expect(tools).toHaveLength(7)
    expect(tools.map(tool => tool.name)).toEqual([...TOOL_NAMES])
    expect(new Set(TOOL_NAMES).size).toBe(7)
  })

  it('3 写 + 4 读的分组没变（写工具不会混进只读集合）', async () => {
    const { READ_TOOL_NAMES, WRITE_TOOL_NAMES } = await import('../src/tools/index.js')
    expect([...WRITE_TOOL_NAMES]).toHaveLength(3)
    expect([...READ_TOOL_NAMES]).toHaveLength(4)
    expect([...WRITE_TOOL_NAMES, ...READ_TOOL_NAMES]).toEqual([...TOOL_NAMES])
  })
})

// ---------------------------------------------------------------------------
// 3. worker 心跳端点
// ---------------------------------------------------------------------------

describe('契约闸门：worker 心跳端点', () => {
  it('createWorkerClient 打 POST /v1/workers/heartbeat，且该端点 operationId 为 heartbeatWorker', async () => {
    const platform = createPlatform(() => new Response(
      JSON.stringify({ worker: { worker_id: 'w_TEST' }, commands: [] }),
      { status: 200, headers: { 'content-type': 'application/json' } },
    ))
    const worker = createWorkerClient(config, { fetchImpl: platform.fetchImpl })

    await worker.heartbeat({ state: 'idle', metadata: {} })

    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]?.init.method).toBe('POST')
    expect(new URL(platform.calls[0]!.url).pathname).toBe('/v1/workers/heartbeat')

    const hit = findYamlOperation('POST', '/v1/workers/heartbeat')
    expect(hit?.operationId).toBe('heartbeatWorker')
  })
})

// ---------------------------------------------------------------------------
// 4. 物料读取：原始正文
// ---------------------------------------------------------------------------

describe('契约闸门：物料读取返回原始正文', () => {
  it('content-type 为 text/markdown 时按原文返回，不当 JSON 解析', async () => {
    const markdown = '# 需求正文\n\n- 第一项\n- 第二项\n'
    const platform = createPlatform(() => new Response(markdown, {
      status: 200,
      headers: {
        'content-type': 'text/markdown; charset=utf-8',
        etag: '"v1"',
        'x-material-id': 'M-1',
        'x-material-version': '3',
      },
    }))
    const client = createClient(config, { fetchImpl: platform.fetchImpl })

    const result = await client.getMaterials({ id: 'M-1' })

    expect(result.kind).toBe('ok')
    if (result.kind !== 'ok') throw new Error('unreachable')
    // 逐字节一致：换行、结尾空白都不能被 trim 或重新序列化。
    expect(result.data.content).toBe(markdown)
    expect(result.data.contentType).toBe('text/markdown; charset=utf-8')
    expect(result.data.materialId).toBe('M-1')
    expect(result.data.materialVersion).toBe(3)
    expect(result.data.etag).toBe('"v1"')
  })

  it('正文长得像 JSON 也原样返回（不 JSON.parse、不抛 invalid_response）', async () => {
    const body = '{"code":"not_found","message":"这不是平台错误体，只是资料正文"}'
    const platform = createPlatform(() => new Response(body, {
      status: 200,
      headers: { 'content-type': 'text/plain' },
    }))
    const client = createClient(config, { fetchImpl: platform.fetchImpl })

    const result = await client.getMaterials({ id: 'M-2' })

    expect(result.kind).toBe('ok')
    if (result.kind !== 'ok') throw new Error('unreachable')
    expect(result.data.content).toBe(body)
    expect(typeof result.data.content).toBe('string')
  })
})
