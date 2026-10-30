/**
 * 工具 → 端点逐项连线（任务-08 B）。
 *
 * 已有测试证明了「工具有什么行为」（render / 错误文案 / 缺参），这里补的是
 * **线怎么接**：22 个工具各自必须产生**恰好 1 个**请求，method / path / query 键 /
 * body 键与契约表一致；只读工具一律 GET 且无 body；写工具里除资料包下载
 * （GET 二进制）外一律不发 GET（方案修订是 PATCH，且 `If-Match` 走请求头）。
 *
 * 全部用真 client + 假 fetch（不是假 client），因此连 URL 拼接、query 编码、
 * body 序列化都一起被验证。
 */
import { unlinkSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import type { ToolDefinition } from '@deepseek-ai/dsh-tools'
import { buildZip } from './helpers/zip.js'
import { createClient, type FetchLike } from '../src/client.js'
import { resolveConfig } from '../src/config.js'
import { createAllEchuanTools } from '../src/tools/index.js'
import type { EchuanToolOptions } from '../src/tools/index.js'

// ---------------------------------------------------------------------------
// 假平台
// ---------------------------------------------------------------------------

interface RecordedCall {
  url: URL
  method: string
  body: string | undefined
  headers: Record<string, string>
}

type Responder = (call: RecordedCall) => Response

function createPlatform(respond: Responder): { calls: RecordedCall[]; fetchImpl: FetchLike } {
  const calls: RecordedCall[] = []
  const fetchImpl: FetchLike = async (input, init = {}) => {
    const call: RecordedCall = {
      url: new URL(input),
      method: init.method ?? 'GET',
      body: init.body === undefined ? undefined : String(init.body),
      headers: (init.headers ?? {}) as Record<string, string>,
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
    workdir: '/var/lib/echuan/workspaces/APP-1',
  },
}

/** 按路径回包，让 22 个工具能在同一条链路上跑完。 */
const respondByPath: Responder = (call) => {
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
        return json({
          product_id: 'APPX',
          name: '示例产品',
          git_url: 'git@example.com:APPX.git',
        })
      }
      return json({ items: [] })
    case '/v1/projects':
      // POST = 新建；带 id = 详情；不带 id = 列表。
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
        return json({
          project_id: 'APP-2',
          title: '新项目名',
          status: 2,
          status_name: '方案草稿',
          version: 4,
        })
      }
      return call.url.searchParams.has('id')
        ? json({ project_id: 'APP-2', title: '示例项目' })
        : json({ items: [] })
    case '/v1/projects/requirement':
      return json({
        project_id: 'APP-2',
        requirement: {
          material_id: 'M-REQ-2',
          version: 2,
          title: '需求',
          size: 128,
        },
      })
    case '/v1/projects/submit':
      return json({
        project_id: 'APP-2',
        status: 1,
        status_name: '需求评审',
        version: 2,
      })
    case '/v1/projects/plans':
      return json({
        project_id: 'APP-2',
        status: 5,
        status_name: '任务评审',
        project_version: 6,
        tasks: [
          {
            id: 'APP-2-T0001',
            seq_no: 1,
            title: '卡 1',
            priority: 0,
            status: 0,
            status_name: '待办',
            depends_on: [],
          },
          {
            id: 'APP-2-T0002',
            seq_no: 2,
            title: '卡 2',
            priority: 5,
            status: 0,
            status_name: '待办',
            depends_on: ['APP-2-T0001'],
          },
        ],
      })
    case '/v1/specs':
      return json({
        spec_id: 'S-1',
        project_id: 'APP-2',
        title: '方案 A',
        version: call.method === 'PATCH' ? 4 : 1,
        material: { material_id: call.method === 'PATCH' ? 'M-SPEC-4' : 'M-SPEC-1' },
      })
    case '/v1/tasks':
      if (call.method === 'POST') {
        return json({
          task_id: 'APP-2-T0003',
          id: 'APP-2-T0003',
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
        return json({
          task_id: 'APP-2-T0003',
          title: '新标题',
          priority: 1,
          status: 0,
          status_name: '待办',
        })
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

const config = resolveConfig({ baseUrl: 'http://platform.test' })
const exec = { signal: new AbortController().signal } as unknown as Parameters<
  ToolDefinition['execute']
>[1]

function toolsFor(fetchImpl: FetchLike, workerId: string | undefined = undefined): Map<string, ToolDefinition> {
  const client = createClient(config, { fetchImpl })
  const options: EchuanToolOptions = { client, identity: { workerId } }
  return new Map(createAllEchuanTools(options).map(tool => [tool.name, tool]))
}

async function call(
  tool: ToolDefinition | undefined,
  args: Record<string, unknown>,
): Promise<unknown> {
  if (tool === undefined) throw new Error('工具不存在')
  return tool.execute(args, exec)
}

function bodyKeys(call: RecordedCall): string[] {
  if (call.body === undefined) return []
  return Object.keys(JSON.parse(call.body) as Record<string, unknown>).sort()
}

// ---------------------------------------------------------------------------
// 1. 逐项连线：22 个工具，每个恰好 1 个请求
// ---------------------------------------------------------------------------

interface WiringCase {
  /** 用例标签，同时作为工具名断言的目标。 */
  name: string
  args: Record<string, unknown>
  method: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  path: string
  /** 期望的 query（键与值都要精确匹配，不允许出现多余键）。 */
  query: Record<string, string>
  /** 非 GET 期望的 body 键（排序后比较）；GET 不检查。 */
  bodyKeys?: string[]
  /** 期望的额外请求头（例如 `If-Match`）。 */
  headers?: Record<string, string>
  /** 唯一的 tmpdir 写盘工具（资料包）：测试后删掉落盘文件。 */
  cleanupSavedFile?: boolean
}

const WIRING_CASES: WiringCase[] = [
  {
    name: 'echuan_task_claim',
    args: { product: 'APP' },
    method: 'POST',
    path: '/v1/tasks/claim',
    query: {},
    bodyKeys: ['product'],
  },
  {
    name: 'echuan_task_renew',
    args: { task_id: 'APP-1-T0001', lease_id: 'l_9f8b' },
    method: 'POST',
    path: '/v1/tasks/heartbeat',
    query: { id: 'APP-1-T0001' },
    bodyKeys: ['lease_id'],
  },
  {
    name: 'echuan_task_report',
    args: {
      task_id: 'APP-1-T0001',
      lease_id: 'l_9f8b',
      summary_md: '①改了什么 …\n②怎么自检 …\n③遗留风险 …',
    },
    method: 'POST',
    path: '/v1/tasks/report',
    query: { id: 'APP-1-T0001' },
    bodyKeys: ['lease_id', 'summary_md'],
  },
  {
    name: 'echuan_project_create',
    args: { product_id: 'APP', title: '新项目' },
    method: 'POST',
    path: '/v1/projects',
    query: {},
    bodyKeys: ['product_id', 'title'],
  },
  {
    name: 'echuan_project_requirement_write',
    args: { project_id: 'APP-2', body_md: '# 需求正文' },
    method: 'POST',
    path: '/v1/projects/requirement',
    query: { id: 'APP-2' },
    bodyKeys: ['body_md'],
  },
  {
    name: 'echuan_project_submit',
    args: { project_id: 'APP-2' },
    method: 'POST',
    path: '/v1/projects/submit',
    query: { id: 'APP-2' },
    bodyKeys: [],
  },
  {
    name: 'echuan_spec_create',
    args: { project_id: 'APP-2', title: '方案 A', body_md: '# 方案正文' },
    method: 'POST',
    path: '/v1/specs',
    query: {},
    bodyKeys: ['body_md', 'project_id', 'title'],
  },
  {
    name: 'echuan_spec_revise',
    args: { spec_id: 'S-1', version: 3, title: '方案 A', body_md: '# 方案正文 v2' },
    method: 'PATCH',
    path: '/v1/specs',
    query: { id: 'S-1' },
    bodyKeys: ['body_md', 'title'],
    headers: { 'If-Match': '3' },
  },
  {
    name: 'echuan_plan_submit',
    args: {
      project_id: 'APP-2',
      tasks: [
        { title: '卡 1', body_md: '正文 1' },
        { title: '卡 2', body_md: '正文 2', priority: 5, depends_on: [1] },
      ],
    },
    method: 'POST',
    path: '/v1/projects/plans',
    query: { id: 'APP-2' },
    bodyKeys: ['tasks'],
  },
  {
    name: 'echuan_task_create',
    args: {
      project_id: 'APP-2',
      title: '卡 3',
      body_md: '正文 3',
      priority: 7,
      depends_on: ['APP-2-T0001'],
    },
    method: 'POST',
    path: '/v1/tasks',
    query: { project_id: 'APP-2' },
    bodyKeys: ['body_md', 'depends_on', 'priority', 'title'],
  },
  {
    name: 'echuan_task_update',
    args: { task_id: 'APP-2-T0003', title: '新标题', priority: 1 },
    method: 'PATCH',
    path: '/v1/tasks',
    query: { id: 'APP-2-T0003' },
    bodyKeys: ['priority', 'title'],
  },
  {
    name: 'echuan_task_delete',
    args: { task_id: 'APP-2-T0003' },
    method: 'DELETE',
    path: '/v1/tasks',
    query: { id: 'APP-2-T0003' },
    bodyKeys: [],
  },
  {
    name: 'echuan_project_update',
    args: { project_id: 'APP-2', title: '新项目名' },
    method: 'PATCH',
    path: '/v1/projects',
    query: { id: 'APP-2' },
    bodyKeys: ['title'],
  },
  {
    name: 'echuan_product_create',
    args: {
      product_id: 'APPX',
      name: '示例产品',
      git_url: 'git@example.com:APPX.git',
    },
    method: 'POST',
    path: '/v1/products',
    query: {},
    bodyKeys: ['git_url', 'name', 'product_id'],
  },
  {
    name: 'echuan_project_package',
    args: { project_id: 'APP-2' },
    method: 'GET',
    path: '/v1/projects/package',
    query: { id: 'APP-2' },
    cleanupSavedFile: true,
  },
  {
    name: 'echuan_inbox',
    args: {},
    method: 'GET',
    path: '/v1/inbox',
    query: {},
  },
  {
    name: 'echuan_product_list',
    args: {},
    method: 'GET',
    path: '/v1/products',
    query: {},
  },
  {
    name: 'echuan_project_list',
    args: { product_id: 'APP', status: 1, limit: 10, cursor: 'c1' },
    method: 'GET',
    path: '/v1/projects',
    query: { product_id: 'APP', status: '1', limit: '10', cursor: 'c1' },
  },
  {
    name: 'echuan_task_list',
    args: { project_id: 'APP-1', status: [1], limit: 5 },
    method: 'GET',
    path: '/v1/tasks',
    query: { project_id: 'APP-1', status: '1', limit: '5' },
  },
  {
    name: 'echuan_project_get',
    args: { id: 'APP-2' },
    method: 'GET',
    path: '/v1/projects',
    query: { id: 'APP-2' },
  },
  {
    name: 'echuan_task_get',
    args: { id: 'APP-1-T0007' },
    method: 'GET',
    path: '/v1/tasks',
    query: { id: 'APP-1-T0007' },
  },
  {
    name: 'echuan_material_get',
    args: { id: 'M-1' },
    method: 'GET',
    path: '/v1/materials',
    query: { id: 'M-1' },
  },
]

describe('工具接线：逐个工具恰好 1 个请求，method/path/query/body 精确匹配', () => {
  for (const wiring of WIRING_CASES) {
    it(`${wiring.name} → ${wiring.method} ${wiring.path}`, async () => {
      const platform = createPlatform(respondByPath)

      const value = await call(toolsFor(platform.fetchImpl).get(wiring.name), wiring.args)
      if (wiring.cleanupSavedFile === true) {
        unlinkSync((value as { saved_path: string }).saved_path)
      }

      expect(platform.calls, `${wiring.name} 应当只发 1 个请求`).toHaveLength(1)
      const request = platform.calls[0]!

      expect(request.method).toBe(wiring.method)
      expect(request.url.pathname).toBe(wiring.path)
      expect(request.url.origin).toBe('http://platform.test')

      // query 键与值都要精确：多一个键、少一个键都算接线错误。
      const expectedKeys = Object.keys(wiring.query).sort()
      expect([...request.url.searchParams.keys()].sort()).toEqual(expectedKeys)
      for (const [key, value] of Object.entries(wiring.query)) {
        expect(request.url.searchParams.get(key)).toBe(value)
      }

      if (wiring.method === 'GET') {
        // 只读请求不带 body，也不带 content-type。
        expect(request.body).toBeUndefined()
        expect(request.headers['content-type']).toBeUndefined()
      } else {
        if ((wiring.bodyKeys ?? []).length === 0) {
          // 无 body 的写请求（提交评审）不带 content-type。
          expect(request.body).toBeUndefined()
        } else {
          expect(bodyKeys(request)).toEqual([...(wiring.bodyKeys ?? [])].sort())
          expect(request.headers['content-type']).toBe('application/json')
        }
        // 写工具也必须有身份头，否则平台无法审计是谁改的。
        expect(request.headers['X-Actor']).toBe(config.actor)
      }
      for (const [name, value] of Object.entries(wiring.headers ?? {})) {
        expect(request.headers[name]).toBe(value)
      }
    })
  }
})

// ---------------------------------------------------------------------------
// 2. 可选参数的接线（body 键 ⊆ 允许集合）
// ---------------------------------------------------------------------------

describe('工具接线：可选参数', () => {
  it('echuan_task_claim 带 task_id 时 body 键 ⊆ {product, task_id, worker_id}', async () => {
    const platform = createPlatform(respondByPath)

    await call(toolsFor(platform.fetchImpl).get('echuan_task_claim'), {
      product: 'APP',
      task_id: 'APP-1-T0001',
    })

    expect(platform.calls).toHaveLength(1)
    const allowed = new Set(['product', 'task_id', 'worker_id'])
    for (const key of bodyKeys(platform.calls[0]!)) expect(allowed.has(key)).toBe(true)
    expect(bodyKeys(platform.calls[0]!)).toEqual(['product', 'task_id'])
    expect(JSON.parse(platform.calls[0]!.body as string)).toMatchObject({
      product: 'APP',
      task_id: 'APP-1-T0001',
    })
  })

  it('echuan_task_claim 在心跳循环已注册时把 worker_id 放进 body', async () => {
    const platform = createPlatform(respondByPath)

    await call(toolsFor(platform.fetchImpl, 'w_TEST01ABCDEFGHIJKLMN').get('echuan_task_claim'), {
      product: 'APP',
    })

    expect(platform.calls).toHaveLength(1)
    expect(bodyKeys(platform.calls[0]!)).toEqual(['product', 'worker_id'])
  })

  it('echuan_inbox 带 limit 时 query 只有 limit 一个键', async () => {
    const platform = createPlatform(respondByPath)

    await call(toolsFor(platform.fetchImpl).get('echuan_inbox'), { limit: 5 })

    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.url.pathname).toBe('/v1/inbox')
    expect([...platform.calls[0]!.url.searchParams.keys()]).toEqual(['limit'])
    expect(platform.calls[0]!.url.searchParams.get('limit')).toBe('5')
  })
})

// ---------------------------------------------------------------------------
// 3. 只读 / 写的方向性约束
// ---------------------------------------------------------------------------

const READ_TOOLS = [
  'echuan_inbox',
  'echuan_product_list',
  'echuan_project_list',
  'echuan_task_list',
  'echuan_project_get',
  'echuan_task_get',
  'echuan_material_get',
]
const WRITE_TOOLS = [
  'echuan_task_claim',
  'echuan_task_renew',
  'echuan_task_report',
  'echuan_project_create',
  'echuan_project_requirement_write',
  'echuan_project_submit',
  'echuan_spec_create',
  'echuan_spec_revise',
  'echuan_plan_submit',
  'echuan_task_create',
  'echuan_task_update',
  'echuan_task_delete',
  'echuan_project_update',
  'echuan_product_create',
  'echuan_project_package',
]

/** 把 22 个工具都调用一遍，返回全部请求（用于方向性断言）。 */
async function runWholeChain(): Promise<RecordedCall[]> {
  const platform = createPlatform(respondByPath)
  const tools = toolsFor(platform.fetchImpl, 'w_TEST01ABCDEFGHIJKLMN')

  for (const wiring of WIRING_CASES) {
    await call(tools.get(wiring.name), wiring.args)
  }
  return platform.calls
}

describe('工具接线：方向性约束', () => {
  it('7 个只读工具全部 GET 且没有 body', async () => {
    const platform = createPlatform(respondByPath)
    const tools = toolsFor(platform.fetchImpl)

    for (const name of READ_TOOLS) {
      const wiring = WIRING_CASES.find(item => item.name === name)
      expect(wiring, `${name} 缺少接线用例`).toBeDefined()
      await call(tools.get(name), wiring!.args)
    }

    expect(platform.calls).toHaveLength(7)
    for (const request of platform.calls) {
      expect(request.method).toBe('GET')
      expect(request.body).toBeUndefined()
      expect(request.headers['content-type']).toBeUndefined()
    }
  })

  it('15 个写工具里除资料包下载外都不是 GET（PATCH 3 个、DELETE 1 个）', async () => {
    const platform = createPlatform(respondByPath)
    const tools = toolsFor(platform.fetchImpl)

    for (const name of WRITE_TOOLS) {
      const wiring = WIRING_CASES.find(item => item.name === name)
      expect(wiring, `${name} 缺少接线用例`).toBeDefined()
      const value = await call(tools.get(name), wiring!.args)
      if (wiring!.cleanupSavedFile === true) {
        unlinkSync((value as { saved_path: string }).saved_path)
      }
    }

    expect(platform.calls).toHaveLength(15)
    const downloadCalls = platform.calls.filter(request => request.url.pathname === '/v1/projects/package')
    expect(downloadCalls).toHaveLength(1)
    expect(downloadCalls[0]!.method).toBe('GET')
    for (const request of platform.calls) {
      if (request.url.pathname === '/v1/projects/package') continue
      expect(request.method).not.toBe('GET')
    }
    expect(platform.calls.filter(request => request.method === 'PATCH')).toHaveLength(3)
    expect(platform.calls.filter(request => request.method === 'DELETE')).toHaveLength(1)
  })

  it('22 个工具跑完一共 22 个请求，共用 20 个 HTTP operation', async () => {
    const calls = await runWholeChain()

    expect(calls).toHaveLength(22)
    const endpoints = calls.map(request => `${request.method} ${request.url.pathname}`)
    expect(new Set(endpoints).size).toBe(20)
    expect([...endpoints].sort()).toEqual([
      'DELETE /v1/tasks',
      'GET /v1/inbox',
      'GET /v1/materials',
      'GET /v1/products',
      'GET /v1/projects',
      'GET /v1/projects',
      'GET /v1/projects/package',
      'GET /v1/tasks',
      'GET /v1/tasks',
      'PATCH /v1/projects',
      'PATCH /v1/specs',
      'PATCH /v1/tasks',
      'POST /v1/products',
      'POST /v1/projects',
      'POST /v1/projects/plans',
      'POST /v1/projects/requirement',
      'POST /v1/projects/submit',
      'POST /v1/specs',
      'POST /v1/tasks',
      'POST /v1/tasks/claim',
      'POST /v1/tasks/heartbeat',
      'POST /v1/tasks/report',
    ])
  })
})

// ---------------------------------------------------------------------------
// 5. spec 写工具 title 必填（D1）
// ---------------------------------------------------------------------------

describe('工具接线：spec 写工具缺 title 本地拒绝且零请求', () => {
  const MISSING_TITLE_CASES = [
    {
      name: 'echuan_spec_create',
      args: { project_id: 'APP-2', body_md: '# 方案正文' },
    },
    {
      name: 'echuan_spec_revise',
      args: { spec_id: 'S-1', version: 3, body_md: '# 方案正文 v2' },
    },
  ] as const

  for (const { name, args } of MISSING_TITLE_CASES) {
    it(`${name} 缺 title：本地拒绝且 0 请求`, async () => {
      const platform = createPlatform(respondByPath)

      let failure: unknown
      try {
        await call(toolsFor(platform.fetchImpl).get(name), args)
      } catch (err) {
        failure = err
      }

      expect(failure).toBeInstanceOf(Error)
      const message = (failure as Error).message
      expect(message).toMatch(/invalid argument|\[invalid_argument\]/)
      expect(message).toContain('title')
      expect(platform.calls).toHaveLength(0)
    })
  }
})
