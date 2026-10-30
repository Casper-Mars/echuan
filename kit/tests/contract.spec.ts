/**
 * 契约漂移闸门（任务-08 A）。
 *
 * 目的：`api/openapi.yaml` 是唯一契约来源，`src/client.ts` 的 `TOOL_OPERATIONS`
 * 是客户端对契约的「声明」。两者一旦漂移（改路径、改 method、改 operationId、
 * 多加/少加一个动作），这里必须立刻变红。
 *
 * 覆盖：
 * 1. `TOOL_OPERATIONS` 恰好 14 条，字段与白名单逐项一致（顺序也固定）；
 * 2. 每条 `METHOD /path` 在 yaml `paths` 段中真实存在且唯一；
 * 3. 每条 `operationId` 与 yaml 中该 method 的实际值一致；
 * 4. `TOOL_METHODS` 恰好 14 条且与 `TOOL_OPERATIONS` 顺序一一对应；
 * 5. 注册工具数 = 16（9 写 + 7 读）、名字唯一；
 * 6. worker 心跳端点 `POST /v1/workers/heartbeat`（heartbeatWorker）也在契约里；
 * 7. 物料读取返回**原始正文**：`content-type` 非 JSON 时不做 JSON 解析。
 *
 * 解析器说明（保持手写，不新增 `yaml` 依赖）
 * ------------------------------------------------
 * 本文件用「按缩进切段」的极简解析器读取 `api/openapi.yaml`，只服务于上面的
 * 14 条 operation 校验。它**假设**契约始终是下面这套固定结构：
 *
 * 1. 顶层键 `paths:` 独立成行（行首无缩进，冒号后为空）；
 * 2. 二级是 path 键：2 空格缩进 + `/v1/...:`（冒号后为空，不跟其他键值）；
 * 3. 三级是 method 键：4 空格缩进 + `get|post|put|patch|delete|head|options:`；
 * 4. 四级是 `operationId:`：6 空格缩进 + 单个非空白值（在同一 operation 块内）。
 *
 * 明确**不支持**的写法（真出现时断言会变红，而不是静默通过）：
 * - flow style（`paths: {}`、`{/v1/x: {...}}`）：找不到独立的 `paths:` 行 →
 *   解析器直接抛错；
 * - 多行折叠 / 块标量（如 `operationId: >`）：取不到值，该 operation 从结果里
 *   消失 →「恰好出现一次」断言失败；
 * - `$ref` 间接（path / operation 定义在别的文件或 `components` 里）：对应的
 *   `METHOD /path` 查不到 → 断言失败；
 * - 锚点 / 别名（`<<: *base`）：被引用的键不在字面文本里 → 断言失败。
 *
 * 改契约时的同步点：
 * - 改 `paths` 段结构（缩进层级、path 键写法、method 改成 `$ref` 等）→ 必须
 *   同步改下面的 `parsePathOperations`，否则解析结果为空或不完整；
 * - 改 `operationId` / path / method → 对应断言变红，按失败信息更新
 *   `src/client.ts` 的 `TOOL_OPERATIONS`（以及本文件的 `EXPECTED_OPERATIONS`）。
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

/**
 * 读取某个 operation 的 `requestBody` 契约：
 * - `requestBodyRequired`：requestBody 本身是否 `required: true`；
 * - `schemaRequired`：`content.application/json.schema.required` 的字段名。
 *
 * 只服务于「请求体必填字段」这类轻量断言（D1/D3 落点），仍不引入 yaml 依赖；
 * 结构若被改写（例如 schema 换成 `$ref`、required 改成多行块），这里会拿不到值，
 * 由调用方断言变红，而不是静默通过。
 */
interface RequestBodyContract {
  requestBodyRequired: boolean
  schemaRequired: string[]
}

function parseRequestBodyContract(operationId: string): RequestBodyContract | undefined {
  const lines = openapiYaml.split(/\r?\n/)
  const anchorLine = `      operationId: ${operationId}`
  const start = lines.findIndex(line => line === anchorLine)
  if (start < 0) return undefined

  // 向后扫到下一个 method 键（4 空格）/ path 键 / 顶层键为止。
  let end = lines.length
  for (let index = start + 1; index < lines.length; index += 1) {
    const line = lines[index] as string
    if (line.trim() === '') continue
    if (/^ {4}(get|post|put|patch|delete|head|options):\s*$/i.test(line)) {
      end = index
      break
    }
    if (/^ {2}\//.test(line) || /^\S/.test(line)) {
      end = index
      break
    }
  }

  let requestBodyRequired = false
  let schemaRequired: string[] | undefined
  let inRequestBody = false
  let inJsonContent = false
  let inSchema = false

  for (let index = start + 1; index < end; index += 1) {
    const line = lines[index] as string
    if (/^ {6}requestBody:\s*$/.test(line)) {
      inRequestBody = true
      inJsonContent = false
      inSchema = false
      continue
    }
    if (!inRequestBody) continue

    if (/^ {8}required:\s*true\s*$/.test(line)) {
      requestBodyRequired = true
      continue
    }
    if (/^ {10}application\/json:\s*$/.test(line)) {
      inJsonContent = true
      inSchema = false
      continue
    }
    if (inJsonContent && /^ {12}schema:\s*$/.test(line)) {
      inSchema = true
      continue
    }
    if (inSchema) {
      const requiredMatch = /^ {14}required:\s*\[(.*)\]\s*$/.exec(line)
      if (requiredMatch?.[1] !== undefined) {
        schemaRequired = requiredMatch[1]
          .split(',')
          .map(item => item.trim())
          .filter(item => item !== '')
        break
      }
    }
  }

  if (schemaRequired === undefined) return undefined
  return { requestBodyRequired, schemaRequired }
}

// ---------------------------------------------------------------------------
// 客户端声明（真实值，逐字抄自 src/client.ts；改契约必须先改这里）
// ---------------------------------------------------------------------------

/**
 * 客户端被允许使用的 14 个 operation，**顺序即 `TOOL_OPERATIONS` 顺序**。
 *
 * 序号/内容与 `api/openapi.yaml` 的 `operationId` 完全一致；多一个、少一个、
 * 换顺序、换路径都会让下面的用例变红。
 */
const EXPECTED_OPERATIONS = [
  { tool: 'claimTask', method: 'POST', path: '/v1/tasks/claim', operationId: 'claimTask' },
  { tool: 'renewLease', method: 'POST', path: '/v1/tasks/heartbeat', operationId: 'heartbeatTask' },
  { tool: 'reportTask', method: 'POST', path: '/v1/tasks/report', operationId: 'reportTask' },
  { tool: 'createProject', method: 'POST', path: '/v1/projects', operationId: 'createProject' },
  { tool: 'writeRequirement', method: 'POST', path: '/v1/projects/requirement', operationId: 'writeProjectRequirement' },
  { tool: 'submitProject', method: 'POST', path: '/v1/projects/submit', operationId: 'submitProject' },
  { tool: 'createSpec', method: 'POST', path: '/v1/specs', operationId: 'createSpecDocument' },
  { tool: 'reviseSpec', method: 'PATCH', path: '/v1/specs', operationId: 'reviseSpecDocument' },
  { tool: 'submitPlan', method: 'POST', path: '/v1/projects/plans', operationId: 'submitProjectPlan' },
  { tool: 'getInbox', method: 'GET', path: '/v1/inbox', operationId: 'listInbox' },
  { tool: 'listProducts', method: 'GET', path: '/v1/products', operationId: 'listOrGetProducts' },
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
// 1. 客户端声明的 8 个 operation
// ---------------------------------------------------------------------------

describe('契约闸门：客户端声明的 operation 恰好 14 个', () => {
  it('TOOL_OPERATIONS 与白名单逐字段一致（含顺序）', () => {
    expect(TOOL_OPERATIONS).toHaveLength(14)
    expect(TOOL_OPERATIONS).toEqual(EXPECTED_OPERATIONS)
  })

  it('operationId 序列稳定，正好是契约里的 14 个动作', () => {
    expect(TOOL_OPERATIONS.map(entry => entry.operationId)).toEqual([
      'claimTask',
      'heartbeatTask',
      'reportTask',
      'createProject',
      'writeProjectRequirement',
      'submitProject',
      'createSpecDocument',
      'reviseSpecDocument',
      'submitProjectPlan',
      'listInbox',
      'listOrGetProducts',
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

  it('TOOL_METHODS 恰好 14 条，且与 TOOL_OPERATIONS 顺序一一对应', () => {
    expect(TOOL_METHODS).toHaveLength(14)
    expect([...TOOL_METHODS]).toEqual(EXPECTED_OPERATIONS.map(entry => entry.tool))
  })
})

// ---------------------------------------------------------------------------
// 2. 注册的工具数
// ---------------------------------------------------------------------------

describe('契约闸门：注册工具数 = 16', () => {
  it('createAllEchuanTools 产出 16 个定义，名字唯一且与 TOOL_NAMES 一致', () => {
    const anyClient = createClient(config, { fetchImpl: async () => new Response('{}') })
    const tools = createAllEchuanTools({ client: anyClient, identity: { workerId: undefined } })

    expect(tools).toHaveLength(16)
    expect(tools.map(tool => tool.name)).toEqual([...TOOL_NAMES])
    expect(new Set(TOOL_NAMES).size).toBe(16)
  })

  it('9 写 + 7 读的分组没变（写工具不会混进只读集合）', async () => {
    const { READ_TOOL_NAMES, WRITE_TOOL_NAMES } = await import('../src/tools/index.js')
    expect([...WRITE_TOOL_NAMES]).toHaveLength(9)
    expect([...READ_TOOL_NAMES]).toHaveLength(7)
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

// ---------------------------------------------------------------------------
// 5. 10 个工具 → 8 个 operation 的映射
// ---------------------------------------------------------------------------

/**
 * 工具名 → operationId。16 个工具共用 14 个 operation：
 * 项目/任务各由一个 GET 同时承担 list 与 get。
 */
const TOOL_TO_OPERATION = {
  echuan_task_claim: 'claimTask',
  echuan_task_renew: 'heartbeatTask',
  echuan_task_report: 'reportTask',
  echuan_project_create: 'createProject',
  echuan_project_requirement_write: 'writeProjectRequirement',
  echuan_project_submit: 'submitProject',
  echuan_spec_create: 'createSpecDocument',
  echuan_spec_revise: 'reviseSpecDocument',
  echuan_plan_submit: 'submitProjectPlan',
  echuan_inbox: 'listInbox',
  echuan_product_list: 'listOrGetProducts',
  echuan_project_list: 'listOrGetProjects',
  echuan_project_get: 'listOrGetProjects',
  echuan_task_list: 'listOrGetTasks',
  echuan_task_get: 'listOrGetTasks',
  echuan_material_get: 'downloadMaterial',
} as const

/** 找出「客户端声明的 operation 在给定 yaml 里缺失/漂移」的第一处。 */
function operationIssue(yaml: string): string | undefined {
  const ops = parsePathOperations(yaml)
  for (const entry of TOOL_OPERATIONS) {
    const hit = ops.find(op => op.method === entry.method && op.path === entry.path)
    if (hit === undefined) return `${entry.tool} 缺少 ${entry.method} ${entry.path}`
    if (hit.operationId !== entry.operationId) {
      return `${entry.tool} operationId 漂移：${hit.operationId}`
    }
  }
  return undefined
}

describe('契约闸门：16 个工具逐个映射到 14 个 operation', () => {
  it('TOOL_NAMES 恰好 16 个，每个都映射到白名单里真实存在的 operation', () => {
    expect([...TOOL_NAMES]).toHaveLength(16)

    const known = new Set(EXPECTED_OPERATIONS.map(entry => entry.operationId))
    for (const tool of TOOL_NAMES) {
      const operationId = TOOL_TO_OPERATION[tool]
      expect(operationId, `${tool} 没有 operation 映射`).toBeDefined()
      expect(known.has(operationId), `${tool} 映射到未声明的 operation`).toBe(true)
      expect(
        yamlOperations.some(op => op.operationId === operationId),
        `${tool} 的 operation ${operationId} 不在 openapi.yaml 中`,
      ).toBe(true)
    }
  })

  it('list 与 get 共用同一个 operation；14 个 operation 全部被用到', () => {
    expect(TOOL_TO_OPERATION.echuan_project_list).toBe(TOOL_TO_OPERATION.echuan_project_get)
    expect(TOOL_TO_OPERATION.echuan_task_list).toBe(TOOL_TO_OPERATION.echuan_task_get)

    const used = new Set(Object.values(TOOL_TO_OPERATION))
    expect(used.size).toBe(14)
    expect([...used].sort()).toEqual(
      EXPECTED_OPERATIONS.map(entry => entry.operationId).sort(),
    )
  })

  it('删掉任一 operation 后闸门立即变红（含新增的 listOrGetProducts）', () => {
    // 自证：未变异的 yaml 没有漂移。
    expect(operationIssue(openapiYaml)).toBeUndefined()

    for (const entry of TOOL_OPERATIONS) {
      const marker = `      operationId: ${entry.operationId}\n`
      expect(openapiYaml, `${entry.operationId} 在 yaml 中应恰好出现一次`).toContain(marker)
      const mutated = openapiYaml.replace(marker, '')
      expect(mutated, `${entry.operationId} 未被删除`).not.toBe(openapiYaml)
      expect(operationIssue(mutated), `删掉 ${entry.operationId} 后闸门应报错`).toContain(
        entry.tool,
      )
    }
  })
})

// ---------------------------------------------------------------------------
// 6. 请求体必填字段（D1/D3：title 不得再被客户端做成可选）
// ---------------------------------------------------------------------------

describe('契约闸门：spec 写操作 requestBody required 含 title', () => {
  const SPEC_WRITE_CASES = [
    {
      tool: 'echuan_spec_create',
      operationId: 'createSpecDocument',
      method: 'POST',
      path: '/v1/specs',
      expectedRequired: ['project_id', 'title', 'body_md'],
    },
    {
      tool: 'echuan_spec_revise',
      operationId: 'reviseSpecDocument',
      method: 'PATCH',
      path: '/v1/specs',
      expectedRequired: ['title', 'body_md'],
    },
  ] as const

  it.each(SPEC_WRITE_CASES)(
    '$operationId：openapi requestBody 必填且 schema.required 含 title',
    ({ operationId, expectedRequired }) => {
      const contract = parseRequestBodyContract(operationId)
      expect(contract, `${operationId} 的 requestBody 契约解析失败`).toBeDefined()
      expect(contract?.requestBodyRequired).toBe(true)
      expect(contract?.schemaRequired).toContain('title')
      expect(contract?.schemaRequired).toContain('body_md')
      expect(contract?.schemaRequired).toEqual([...expectedRequired])
    },
  )

  it('kit 工具参数 required 与 openapi 同步（title 不可再被做成可选）', () => {
    const anyClient = createClient(config, { fetchImpl: async () => new Response('{}') })
    const tools = new Map(
      createAllEchuanTools({ client: anyClient, identity: { workerId: undefined } })
        .map(tool => [tool.name, tool]),
    )

    for (const { tool, operationId, expectedRequired } of SPEC_WRITE_CASES) {
      const definition = tools.get(tool)
      expect(definition, `${tool} 未注册`).toBeDefined()

      // dsh 编译后的 parameters 是 JSON Schema：required 为字段名数组。
      // 工具 required 还含路由参数（如 revise 的 spec_id / version），
      // 因此这里断言「契约 body 必填字段 ⊆ 工具 required」，title 必须在其中。
      const parameters = definition!.parameters as { required?: string[] }
      const toolRequired = parameters.required ?? []
      expect(toolRequired, `${tool} 参数 required 漂移`).toEqual(
        expect.arrayContaining([...expectedRequired]),
      )
      expect(toolRequired).toContain('title')

      // 与契约侧逐项对齐，防止单边修改。
      const contractRequired = parseRequestBodyContract(operationId)?.schemaRequired ?? []
      expect(toolRequired).toEqual(expect.arrayContaining(contractRequired))
    }
  })
})
