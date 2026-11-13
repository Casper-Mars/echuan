/**
 * 工具 → 端点逐项连线（任务-08 B）。
 *
 * 已有测试证明了「工具有什么行为」（render / 错误文案 / 缺参），这里补的是
 * **线怎么接**：25 个工具里 24 个必须各产生**恰好一条**请求（`echuan_worker_info`
 * 零 HTTP），method / path / query 键 /
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
  dispatch: {
    protocol: 'v2',
    work_id: 'APP-1-T0001',
    kind: 'dev',
    inputs: [{ material_id: 'M-SPEC-1', kind: 2, version: 1 }],
    work_item: {
      project_id: 'APP-1',
      title: '实现登录接口',
      body_md: '# 工作项正文',
      attempt: 1,
      priority: 10,
    },
    lease: { lease_id: 'l_9f8b', until: '2026-10-27T16:49:25.157Z', ttl_seconds: 90 },
    context: {
      spec_version: 1,
      spec_digest: 'sha256:bb23',
      product: { id: 'APP', name: '驿传示例产品', git_url: 'git@example.com:APP.git' },
    },
  },
}

/** 按路径回包，让 24 个会发请求的工具能在同一条链路上跑完。 */
const respondByPath: Responder = (call) => {
  switch (call.url.pathname) {
    case '/v1/work-items/claim':
      return json(CLAIM_PAYLOAD)
    case '/v1/work-items/lease':
      return json({ lease_id: 'l_next', until: '2026-10-27T16:51:00.000Z', ttl_seconds: 90 })
    case '/v1/work-items/report':
      return json({ work_id: 'APP-1-T0001', state: 'submitted' })
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
      if (call.method === 'PATCH') {
        return json({
          product_id: 'APP',
          name: 'APP 产品',
          git_url: 'git@example.com:APP.git',
          description: '平台回包描述',
          license: 'MIT',
          project_count: 2,
          update_time: '2026-11-13T08:00:00.000Z',
        })
      }
      if (call.method === 'DELETE') {
        return new Response(null, { status: 204 })
      }
      return json({ items: [] })
    case '/v1/projects':
      // POST = 新建；带 id = 详情；不带 id = 列表。
      if (call.method === 'POST') {
        return json({
          project_id: 'APP-3',
          product_id: 'APP',
          title: '新项目',
          // 建项即入需求评审：1 = requirement_review（不是 0 需求草稿）。
          status: 1,
          status_name: '需求评审',
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
        status_name: '开发任务评审',
        project_version: 6,
        work_items: [
          {
            work_id: 'APP-2-T0001',
            kind: 'dev',
            seq_no: 1,
            title: '卡 1',
            priority: 0,
            state: 'queued',
            depends_on: [],
          },
          {
            work_id: 'APP-2-T0002',
            kind: 'dev',
            seq_no: 2,
            title: '卡 2',
            priority: 5,
            state: 'blocked',
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
    case '/v1/work-items':
      if (call.method === 'POST') {
        return json({
          work_id: 'APP-2-T0003',
          id: 'APP-2-T0003',
          project_id: 'APP-2',
          kind: 'dev',
          seq_no: 3,
          title: '卡 3',
          body_md: '正文 3',
          priority: 7,
          state: 'queued',
          depends_on: ['APP-2-T0001'],
        })
      }
      if (call.method === 'PATCH') {
        return json({
          work_id: 'APP-2-T0003',
          kind: 'dev',
          title: '新标题',
          priority: 1,
          state: 'queued',
        })
      }
      if (call.method === 'DELETE') {
        return new Response(null, { status: 204 })
      }
      return call.url.searchParams.has('id') || call.url.searchParams.has('work_id')
        ? json({
          work_id: 'APP-1-T0007',
          project_id: 'APP-1',
          kind: 'dev',
          title: '示例工作项',
          body_md: '# 正文',
          state: 'queued',
          attempt: 1,
          max_attempts: 3,
          depends_on: [],
        })
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

function toolsFor(fetchImpl: FetchLike, workerId: string | undefined = 'w_TEST01ABCDEFGHIJKLMN'): Map<string, ToolDefinition> {
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
// 1. 逐项连线：24 个会发请求的工具，每个恰好一条请求
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
    name: 'echuan_work_claim',
    args: {},
    method: 'POST',
    path: '/v1/work-items/claim',
    query: {},
    bodyKeys: ['worker_id'],
  },
  {
    name: 'echuan_work_renew',
    args: { work_id: 'APP-1-T0001', lease_id: 'l_9f8b' },
    method: 'POST',
    path: '/v1/work-items/lease',
    query: {},
    bodyKeys: ['lease_id', 'work_id'],
  },
  {
    name: 'echuan_work_report',
    args: {
      work_id: 'APP-1-T0001',
      lease_id: 'l_9f8b',
      material_id: 'M-REPORT-1',
    },
    method: 'POST',
    path: '/v1/work-items/report',
    query: {},
    bodyKeys: ['lease_id', 'material_id', 'work_id'],
  },
  {
    name: 'echuan_project_create',
    // requirement_md 已是必填（契约 ProjectCreateRequest.required 含该字段）。
    args: { product_id: 'APP', title: '新项目', requirement_md: '# 需求正文' },
    method: 'POST',
    path: '/v1/projects',
    query: {},
    bodyKeys: ['product_id', 'requirement_md', 'title'],
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
      work_items: [
        { title: '卡 1', body_md: '正文 1' },
        { title: '卡 2', body_md: '正文 2', priority: 5, depends_on: [1] },
      ],
    },
    method: 'POST',
    path: '/v1/projects/plans',
    query: { id: 'APP-2' },
    bodyKeys: ['work_items'],
  },
  {
    name: 'echuan_work_create',
    args: {
      project_id: 'APP-2',
      kind: 'dev',
      title: '卡 3',
      body_md: '正文 3',
      priority: 7,
      depends_on: ['APP-2-T0001'],
    },
    method: 'POST',
    path: '/v1/work-items',
    query: {},
    bodyKeys: ['body_md', 'depends_on', 'kind', 'priority', 'project_id', 'title'],
  },
  {
    name: 'echuan_work_update',
    args: { work_id: 'APP-2-T0003', title: '新标题', priority: 1 },
    method: 'PATCH',
    path: '/v1/work-items',
    query: { id: 'APP-2-T0003' },
    bodyKeys: ['priority', 'title'],
  },
  {
    name: 'echuan_work_delete',
    args: { work_id: 'APP-2-T0003' },
    method: 'DELETE',
    path: '/v1/work-items',
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
    name: 'echuan_product_update',
    // 标识走 id；四个可改字段只给 description，验证「缺参补空串、body 恒含四键」。
    args: { id: 'APP', description: '新描述' },
    method: 'PATCH',
    path: '/v1/products',
    query: { id: 'APP' },
    bodyKeys: ['description', 'git_url', 'license', 'name'],
  },
  {
    name: 'echuan_product_delete',
    args: { id: 'APPX' },
    method: 'DELETE',
    path: '/v1/products',
    query: { id: 'APPX' },
    bodyKeys: [],
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
    name: 'echuan_work_list',
    args: { project_id: 'APP-1', kind: ['dev'], state: ['queued'], limit: 5 },
    method: 'GET',
    path: '/v1/work-items',
    query: { project_id: 'APP-1', kind: 'dev', state: 'queued', limit: '5' },
  },
  {
    name: 'echuan_project_get',
    args: { id: 'APP-2' },
    method: 'GET',
    path: '/v1/projects',
    query: { id: 'APP-2' },
  },
  {
    name: 'echuan_work_get',
    args: { work_id: 'APP-1-T0007' },
    method: 'GET',
    path: '/v1/work-items',
    query: { work_id: 'APP-1-T0007' },
  },
  {
    name: 'echuan_material_get',
    args: { id: 'M-1' },
    method: 'GET',
    path: '/v1/materials',
    query: { id: 'M-1' },
  },
]

describe('工具接线：逐个工具恰好一条请求，method/path/query/body 精确匹配', () => {
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
  it('echuan_work_claim 带 work_id 时 body 键 ⊆ {work_id, worker_id}', async () => {
    const platform = createPlatform(respondByPath)

    await call(toolsFor(platform.fetchImpl).get('echuan_work_claim'), {
      work_id: 'APP-1-T0001',
    })

    expect(platform.calls).toHaveLength(1)
    const allowed = new Set(['work_id', 'worker_id'])
    for (const key of bodyKeys(platform.calls[0]!)) expect(allowed.has(key)).toBe(true)
    expect(bodyKeys(platform.calls[0]!)).toEqual(['work_id', 'worker_id'])
    expect(JSON.parse(platform.calls[0]!.body as string)).toMatchObject({
      work_id: 'APP-1-T0001',
      worker_id: 'w_TEST01ABCDEFGHIJKLMN',
    })
  })

  it('echuan_work_claim 在心跳循环已注册时把 worker_id 放进 body', async () => {
    const platform = createPlatform(respondByPath)

    await call(toolsFor(platform.fetchImpl, 'w_TEST01ABCDEFGHIJKLMN').get('echuan_work_claim'), {})

    expect(platform.calls).toHaveLength(1)
    expect(bodyKeys(platform.calls[0]!)).toEqual(['worker_id'])
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
  'echuan_work_list',
  'echuan_project_get',
  'echuan_work_get',
  'echuan_material_get',
]
const WRITE_TOOLS = [
  'echuan_work_claim',
  'echuan_work_renew',
  'echuan_work_report',
  'echuan_project_create',
  'echuan_project_requirement_write',
  'echuan_project_submit',
  'echuan_spec_create',
  'echuan_spec_revise',
  'echuan_plan_submit',
  'echuan_work_create',
  'echuan_work_update',
  'echuan_work_delete',
  'echuan_project_update',
  'echuan_product_create',
  'echuan_product_update',
  'echuan_product_delete',
  'echuan_project_package',
]

/**
 * 把 25 个工具都调用一遍，返回全部请求（用于方向性断言）。
 *
 * `echuan_worker_info` 零 HTTP，调用它不会产生请求。
 */
async function runWholeChain(): Promise<RecordedCall[]> {
  const platform = createPlatform(respondByPath)
  const tools = toolsFor(platform.fetchImpl, 'w_TEST01ABCDEFGHIJKLMN')

  for (const wiring of WIRING_CASES) {
    await call(tools.get(wiring.name), wiring.args)
  }
  await call(tools.get('echuan_worker_info'), {})
  return platform.calls
}

describe('工具接线：方向性约束', () => {
  it('7 个会发请求的只读工具全部 GET 且没有 body', async () => {
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

  it('17 个写工具里除资料包下载外都不是 GET（PATCH 4 个、DELETE 2 个）', async () => {
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

    expect(platform.calls).toHaveLength(17)
    const downloadCalls = platform.calls.filter(request => request.url.pathname === '/v1/projects/package')
    expect(downloadCalls).toHaveLength(1)
    expect(downloadCalls[0]!.method).toBe('GET')
    for (const request of platform.calls) {
      if (request.url.pathname === '/v1/projects/package') continue
      expect(request.method).not.toBe('GET')
    }
    expect(platform.calls.filter(request => request.method === 'PATCH')).toHaveLength(4)
    expect(platform.calls.filter(request => request.method === 'DELETE')).toHaveLength(2)
  })

  /**
   * 25 个工具跑完一共 24 个请求（`echuan_worker_info` 零 HTTP），method+path 去重后
   * 正好覆盖客户端声明的 22 条 operation：`updateProduct` / `deleteProduct` 已在卡 2
   * 装配，因此不再有「待装配」的端点。
   */
  it('25 个工具跑完一共 24 个请求（worker_info 零 HTTP），共用 22 个 operation', async () => {
    const calls = await runWholeChain()

    expect(calls).toHaveLength(24)
    const endpoints = calls.map(request => `${request.method} ${request.url.pathname}`)
    expect(new Set(endpoints).size).toBe(22)
    expect([...endpoints].sort()).toEqual([
      'DELETE /v1/products',
      'DELETE /v1/work-items',
      'GET /v1/inbox',
      'GET /v1/materials',
      'GET /v1/products',
      'GET /v1/projects',
      'GET /v1/projects',
      'GET /v1/projects/package',
      'GET /v1/work-items',
      'GET /v1/work-items',
      'PATCH /v1/products',
      'PATCH /v1/projects',
      'PATCH /v1/specs',
      'PATCH /v1/work-items',
      'POST /v1/products',
      'POST /v1/projects',
      'POST /v1/projects/plans',
      'POST /v1/projects/requirement',
      'POST /v1/projects/submit',
      'POST /v1/specs',
      'POST /v1/work-items',
      'POST /v1/work-items/claim',
      'POST /v1/work-items/lease',
      'POST /v1/work-items/report',
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
