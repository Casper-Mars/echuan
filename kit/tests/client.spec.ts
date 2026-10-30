/**
 * 平台 HTTP 客户端单测（任务-02）。
 *
 * 全部用假 fetch，不连真实平台。覆盖：
 * - 200 / 204 / 各类错误码的可读映射（保留 `code`）；
 * - 超时（含实际耗时）与网络失败；
 * - X-Actor、URL/query 编码、GET 不带 content-type；
 * - 恰好 14 个方法、与 openapi 一一对应；
 * - 不读 env / 不写盘 / 不缓存；
 * - D-01 metadata 预算前置拒绝。
 */
import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  NO_WORK_MESSAGE,
  TOOL_METHODS,
  TOOL_OPERATIONS,
  createClient,
  createRequestContext,
  requestRaw,
  type FetchLike,
} from '../src/client.js'
import { WORKER_METADATA_MAX_BYTES, resolveConfig } from '../src/config.js'
import {
  DEFAULT_RETRY_AFTER_MS,
  EchuanApiError,
  EchuanNetworkError,
  EchuanTimeoutError,
  EchuanValidationError,
  parseRetryAfter,
} from '../src/errors.js'

const here = path.dirname(fileURLToPath(import.meta.url))
const kitRoot = path.resolve(here, '..')

interface RecordedCall {
  url: string
  init: RequestInit
}

type Handler = (url: string, init: RequestInit) => Response | Promise<Response>

function createFetch(handler: Handler): { calls: RecordedCall[]; fetchImpl: FetchLike } {
  const calls: RecordedCall[] = []
  const fetchImpl: FetchLike = async (url, init = {}) => {
    calls.push({ url, init })
    return handler(url, init)
  }
  return { calls, fetchImpl }
}

function jsonResponse(
  body: unknown,
  status = 200,
  headers: Record<string, string> = {},
): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', ...headers },
  })
}

function errorResponse(
  status: number,
  code: string,
  message: string,
  extra: { headers?: Record<string, string>; requestId?: string; details?: unknown[] } = {},
): Response {
  const payload: Record<string, unknown> = {
    code,
    message,
    details: extra.details ?? [],
    request_id: extra.requestId ?? `req-${code}`,
  }
  return jsonResponse(payload, status, extra.headers ?? {})
}

function headerOf(init: RequestInit, name: string): string | undefined {
  const headers = init.headers
  if (headers === undefined) return undefined
  if (headers instanceof Headers) return headers.get(name) ?? undefined
  if (Array.isArray(headers)) {
    const hit = headers.find(pair => pair[0]?.toLowerCase() === name.toLowerCase())
    return hit?.[1]
  }
  const record = headers as Record<string, string>
  const key = Object.keys(record).find(k => k.toLowerCase() === name.toLowerCase())
  return key === undefined ? undefined : record[key]}

function bodyOf(init: RequestInit): unknown {
  if (init.body === undefined || init.body === null) return undefined
  return JSON.parse(String(init.body))
}

async function catchApiError(promise: Promise<unknown>): Promise<EchuanApiError> {
  try {
    await promise
  } catch (err) {
    expect(err).toBeInstanceOf(EchuanApiError)
    return err as EchuanApiError
  }
  throw new Error('期望请求失败，但成功返回了')
}

const config = resolveConfig({
  baseUrl: 'http://platform.test:8720/',
  actor: 'agent:echuan:test',
  requestTimeoutMs: 50,
})

const claimBody = {
  task: {
    id: 'APP-1-T0001',
    project_id: 'APP-1',
    title: '实现登录接口',
    body_md: '# 任务正文\n',
    attempt: 1,
    priority: 10,
  },
  lease: { lease_id: 'l_1', until: '2026-10-28T10:00:00Z', ttl_seconds: 90 },
  context: {
    spec_version: 1,
    spec_digest: 'sha256:abc',
    product: { id: 'APP', name: '示例', git_url: 'git@example.com:APP.git' },
    workdir: '/tmp/APP-1',
  },
}

describe('成功路径', () => {
  it('200：URL 正确、带 X-Actor 与 content-type，返回解析后的 data', async () => {
    const { calls, fetchImpl } = createFetch(() =>
      jsonResponse(claimBody, 200, { 'x-request-id': 'req-200' }),
    )
    const client = createClient(config, { fetchImpl })

    const result = await client.claimTask({ product: 'APP' })

    expect(result.kind).toBe('ok')
    expect(calls).toHaveLength(1)
    const call = calls[0]!
    expect(call.url).toBe('http://platform.test:8720/v1/tasks/claim')
    expect(call.init.method).toBe('POST')
    expect(headerOf(call.init, 'X-Actor')).toBe('agent:echuan:test')
    expect(headerOf(call.init, 'content-type')).toBe('application/json')
    expect(bodyOf(call.init)).toEqual({ product: 'APP' })

    if (result.kind === 'ok') {
      expect(result.status).toBe(200)
      expect(result.data.task.id).toBe('APP-1-T0001')
      expect(result.requestId).toBe('req-200')
    }
  })

  it('204：claimTask 返回明确的「无活」结果，不抛异常', async () => {
    const { fetchImpl } = createFetch(() => jsonResponse(null, 204, { 'x-request-id': 'req-204' }))
    const client = createClient(config, { fetchImpl })

    const result = await client.claimTask({ product: 'APP' })

    expect(result.kind).toBe('empty')
    if (result.kind !== 'empty') throw new Error('期望 204 返回 empty，但拿到了 200')
    expect(result.status).toBe(204)
    expect(result.message).toContain('无活')
    expect(result.message).toBe(NO_WORK_MESSAGE)
    expect(result.requestId).toBe('req-204')
  })

  it('204：任何 204 场景都走同一路径（getInbox 同样返回 empty）', async () => {
    const { fetchImpl } = createFetch(() => jsonResponse(null, 204))
    const client = createClient(config, { fetchImpl })

    const result = await client.getInbox()

    expect(result.kind).toBe('empty')
    if (result.kind !== 'empty') throw new Error('期望 204 返回 empty，但拿到了 200')
    expect(result.message).toBe(NO_WORK_MESSAGE)
  })
})

describe('错误映射：每种错误都给出可读中文与下一步，并保留 code', () => {
  const cases: Array<{
    status: number
    code: string
    message: string
    keyword: RegExp
    detailCheck?: boolean
  }> = [
    {
      status: 400,
      code: 'invalid_argument',
      message: 'id is empty',
      keyword: /参数/,
      detailCheck: true,
    },
    {
      status: 409,
      code: 'lease_lost',
      message: 'lease expired',
      keyword: /停止执行/,
    },
    {
      status: 409,
      code: 'worker_unknown',
      message: 'worker is unknown',
      keyword: /重新注册/,
    },
    {
      status: 409,
      code: 'worker_busy',
      message: 'worker is busy',
      keyword: /当前卡/,
    },
    {
      status: 413,
      code: 'too_large',
      message: 'body too large',
      keyword: /缩小/,
    },
    {
      status: 422,
      code: 'material_required',
      message: 'report content is empty',
      keyword: /物料|报告/,
    },
    {
      status: 429,
      code: 'rate_limited',
      message: 'slow down',
      keyword: /退避/,
    },
    {
      status: 500,
      code: 'internal',
      message: 'boom',
      keyword: /稍后|重试/,
    },
  ]

  for (const item of cases) {
    it(`${item.status} ${item.code}`, async () => {
      const { fetchImpl } = createFetch(() =>
        errorResponse(item.status, item.code, item.message, {
          requestId: `req-${item.code}`,
          details: item.detailCheck ? [{ field: 'id', reason: 'empty' }] : [],
          headers: item.status === 429 ? { 'retry-after': '1' } : {},
        }),
      )
      const client = createClient(config, { fetchImpl })

      const err = await catchApiError(client.claimTask({ product: 'APP' }))

      expect(err.code).toBe(item.code)
      expect(err.status).toBe(item.status)
      expect(err.platformMessage).toBe(item.message)
      expect(err.message).toContain(item.code)
      expect(err.message).toContain('下一步')
      expect(err.message).toMatch(item.keyword)
      expect(err.requestId).toBe(`req-${item.code}`)
      if (item.detailCheck) {
        expect(err.details).toEqual([{ field: 'id', reason: 'empty' }])
      } else {
        expect(err.details).toEqual([])
      }
    })
  }

  it('错误体缺失 code 时按状态兜底，不丢为 undefined', async () => {
    const { fetchImpl } = createFetch(() =>
      new Response('oops', { status: 500, headers: { 'content-type': 'text/plain' } }),
    )
    const client = createClient(config, { fetchImpl })

    const err = await catchApiError(client.getInbox())

    expect(err.code).toBe('internal')
    expect(err.message).toContain('平台返回 500 internal')
  })
})

describe('429：Retry-After 两种形态与保守默认值', () => {
  it('秒数形态 → retryAfterMs 毫秒', async () => {
    const { fetchImpl } = createFetch(() =>
      errorResponse(429, 'rate_limited', 'slow down', { headers: { 'retry-after': '3' } }),
    )
    const client = createClient(config, { fetchImpl })

    const err = await catchApiError(client.getInbox())

    expect(err.retryAfterMs).toBe(3_000)
    expect(err.message).toContain('3000ms')
    expect(err.message).toContain('不会自动重试')
  })

  it('HTTP-date 形态 → 按当前时间换算', async () => {
    const at = new Date(Date.now() + 3_000).toUTCString()
    const { fetchImpl } = createFetch(() =>
      errorResponse(429, 'rate_limited', 'slow down', { headers: { 'retry-after': at } }),
    )
    const client = createClient(config, { fetchImpl })

    const err = await catchApiError(client.getInbox())

    expect(err.retryAfterMs).toBeGreaterThan(1_000)
    expect(err.retryAfterMs).toBeLessThanOrEqual(3_000)
  })

  it('缺失 Retry-After → 保守默认值，并在 message 中说明', async () => {
    const { fetchImpl } = createFetch(() => errorResponse(429, 'rate_limited', 'slow down'))
    const client = createClient(config, { fetchImpl })

    const err = await catchApiError(client.getInbox())

    expect(err.retryAfterMs).toBe(DEFAULT_RETRY_AFTER_MS)
    expect(err.message).toContain('保守默认')
    expect(err.message).toContain(`${DEFAULT_RETRY_AFTER_MS}ms`)
  })

  it('parseRetryAfter 解析规则', () => {
    const now = Date.parse('2026-10-28T10:00:00Z')
    expect(parseRetryAfter('2', now)).toBe(2_000)
    expect(parseRetryAfter(new Date(now + 3_000).toUTCString(), now)).toBe(3_000)
    expect(parseRetryAfter(new Date(now - 3_000).toUTCString(), now)).toBe(0)
    expect(parseRetryAfter(null, now)).toBeUndefined()
    expect(parseRetryAfter('', now)).toBeUndefined()
    expect(parseRetryAfter('soon', now)).toBeUndefined()
  })
})

describe('超时与网络失败', () => {
  it('超时：消息含「超时」与实际耗时，不甩原始堆栈', async () => {
    const cfg = resolveConfig({
      baseUrl: 'http://platform.test:8720',
      requestTimeoutMs: 30,
    })
    const { fetchImpl } = createFetch((_url, init) =>
      new Promise<Response>((_resolve, reject) => {
        init.signal?.addEventListener('abort', () => {
          const err = new Error('The operation was aborted')
          err.name = 'AbortError'
          reject(err)
        })
      }),
    )
    const client = createClient(cfg, { fetchImpl })

    try {
      await client.getInbox()
      throw new Error('期望超时，但请求成功了')
    } catch (err) {
      expect(err).toBeInstanceOf(EchuanTimeoutError)
      const timeout = err as EchuanTimeoutError
      expect(timeout.code).toBe('timeout')
      expect(timeout.timeoutMs).toBe(30)
      expect(timeout.elapsedMs).toBeGreaterThanOrEqual(0)
      expect(timeout.message).toContain('超时')
      expect(timeout.message).toContain(`${timeout.elapsedMs}ms`)
      expect(timeout.message).toContain('下一步')
      expect(timeout.message).not.toContain('AbortError')
    }
  })

  it('网络失败：转成可读句子，不携带 TypeError/堆栈', async () => {
    const { fetchImpl } = createFetch(() =>
      Promise.reject(new TypeError('fetch failed: ECONNREFUSED 127.0.0.1:8720')),
    )
    const client = createClient(config, { fetchImpl })

    try {
      await client.getInbox()
      throw new Error('期望网络失败，但请求成功了')
    } catch (err) {
      expect(err).toBeInstanceOf(EchuanNetworkError)
      const network = err as EchuanNetworkError
      expect(network.code).toBe('network_error')
      expect(network.message).toContain('网络请求失败')
      expect(network.message).toContain('检查 baseUrl')
      expect(network.message).toContain(`${network.elapsedMs}ms`)
      expect(network.message).not.toContain('ECONNREFUSED')
      expect(network.message).not.toContain('TypeError')
    }
  })
})

describe('URL 与请求头', () => {
  it('query 正确编码；数组按重复 key 展开；GET 不带 content-type', async () => {
    const { calls, fetchImpl } = createFetch(() => jsonResponse({ items: [] }))
    const client = createClient(config, { fetchImpl })

    await client.getInbox({ limit: 5 })
    await client.listTasks({ status: [1, 2], cursor: 'a b&c', project_id: 'APP-1' })
    await client.getMaterials({ id: 'M 1' })

    expect(calls).toHaveLength(3)

    const inbox = new URL(calls[0]!.url)
    expect(inbox.origin + inbox.pathname).toBe('http://platform.test:8720/v1/inbox')
    expect(inbox.searchParams.get('limit')).toBe('5')

    const tasks = new URL(calls[1]!.url)
    expect(tasks.origin + tasks.pathname).toBe('http://platform.test:8720/v1/tasks')
    expect(tasks.searchParams.getAll('status')).toEqual(['1', '2'])
    expect(tasks.searchParams.get('cursor')).toBe('a b&c')
    expect(tasks.searchParams.get('project_id')).toBe('APP-1')
    expect(calls[1]!.url).toContain('%26')

    const materials = new URL(calls[2]!.url)
    expect(materials.origin + materials.pathname).toBe('http://platform.test:8720/v1/materials')
    expect(materials.searchParams.get('id')).toBe('M 1')

    for (const call of calls) {
      expect(headerOf(call.init, 'X-Actor')).toBe('agent:echuan:test')
      expect(headerOf(call.init, 'content-type')).toBeUndefined()
      expect(call.init.method).toBe('GET')
    }
  })

  it('语义参数走 JSON body，标识走 query（reportTask）', async () => {
    const { calls, fetchImpl } = createFetch(() => jsonResponse({ id: 'APP-1-T0001' }))
    const client = createClient(config, { fetchImpl })

    await client.reportTask({ id: 'APP-1-T0001', lease_id: 'l_1', summary_md: '# 完成' })

    const url = new URL(calls[0]!.url)
    expect(url.pathname).toBe('/v1/tasks/report')
    expect(url.searchParams.get('id')).toBe('APP-1-T0001')
    expect(bodyOf(calls[0]!.init)).toEqual({ lease_id: 'l_1', summary_md: '# 完成' })
    expect(headerOf(calls[0]!.init, 'content-type')).toBe('application/json')
  })
})

describe('方法集合：恰好 14 个，无审批类动作', () => {
  it('TOOL_METHODS 长度为 14，客户端对象键与之一致', () => {
    const client = createClient(config, { fetchImpl: createFetch(() => jsonResponse({})).fetchImpl })

    expect(TOOL_METHODS).toHaveLength(14)
    expect(Object.keys(client)).toEqual([...TOOL_METHODS])
    expect(Object.keys(client)).toHaveLength(14)
    for (const method of TOOL_METHODS) {
      expect(typeof client[method]).toBe('function')
    }
  })

  it('不包含 accept / decide / cancel / rework / approve / reject', () => {
    const names = TOOL_METHODS.map(name => name.toLowerCase())
    for (const forbidden of ['accept', 'decide', 'cancel', 'rework', 'approve', 'reject']) {
      expect(names.some(name => name.includes(forbidden))).toBe(false)
    }
  })
})

describe('与 openapi.yaml 一一对应', () => {
  interface YamlOperation {
    method: string
    path: string
    operationId: string
  }

  /** 极简 YAML 结构扫描：只提取 `paths` 下的 method / path / operationId。 */
  function parseOperations(yaml: string): YamlOperation[] {
    const ops: YamlOperation[] = []
    let inPaths = false
    let currentPath = ''
    let currentMethod = ''

    for (const line of yaml.split('\n')) {
      if (/^paths:\s*$/.test(line)) {
        inPaths = true
        continue
      }
      if (!inPaths) continue
      if (/^\S/.test(line)) {
        inPaths = false
        continue
      }

      const pathMatch = /^ {2}(\/\S*):\s*$/.exec(line)
      if (pathMatch !== null) {
        currentPath = pathMatch[1]!
        currentMethod = ''
        continue
      }

      const methodMatch = /^ {4}(get|post|put|patch|delete|head|options):\s*$/.exec(line)
      if (methodMatch !== null) {
        currentMethod = methodMatch[1]!.toUpperCase()
        continue
      }

      const opMatch = /^ {6}operationId:\s*(\S+)\s*$/.exec(line)
      if (opMatch !== null && currentPath !== '' && currentMethod !== '') {
        ops.push({ method: currentMethod, path: currentPath, operationId: opMatch[1]! })
      }
    }
    return ops
  }

  it('客户端用到的 14 个 operation 在 yaml 中恰好各命中一次', () => {
    const yaml = readFileSync(path.resolve(kitRoot, '..', 'api', 'openapi.yaml'), 'utf8')
    const ops = parseOperations(yaml)
    expect(ops.length).toBeGreaterThan(8)

    expect(TOOL_OPERATIONS).toHaveLength(14)
    const matched: YamlOperation[] = []
    for (const entry of TOOL_OPERATIONS) {
      const hits = ops.filter(op => op.path === entry.path && op.method === entry.method)
      expect(hits, `${entry.tool} 应在 yaml 中命中一次`).toHaveLength(1)
      expect(hits[0]!.operationId).toBe(entry.operationId)
      matched.push(hits[0]!)
    }

    expect(matched).toHaveLength(14)
    expect(new Set(matched.map(op => `${op.method} ${op.path}`)).size).toBe(14)
  })
})

describe('安全约束：无 env / 无磁盘 / 无缓存', () => {
  it('client.ts 不读 process.env、不写磁盘', () => {
    const source = readFileSync(path.resolve(kitRoot, 'src', 'client.ts'), 'utf8')
    expect(source).not.toContain('process.env')
    expect(source).not.toContain('node:fs')
    expect(source).not.toContain('writeFile')
  })

  it('src 下只有 client.ts 调用 fetch', () => {
    const srcDir = path.resolve(kitRoot, 'src')
    const files = listTsFiles(srcDir)
    const hits = files.filter(file =>
      readFileSync(file, 'utf8').split('\n').some(line => line.includes('fetch(')),
    )

    expect(hits.length).toBeGreaterThan(0)
    for (const file of hits) {
      expect(path.basename(file)).toBe('client.ts')
    }
  })

  it('两次相同请求会调用 fetch 两次（不缓存响应）', async () => {
    const { calls, fetchImpl } = createFetch(() =>
      jsonResponse({ items: [], count: 0, review_stale_hours: 72, generated_at: '2026-10-28T10:00:00Z' }),
    )
    const client = createClient(config, { fetchImpl })

    const first = await client.getInbox()
    const second = await client.getInbox()

    expect(calls).toHaveLength(2)
    expect(calls[0]!.url).toBe(calls[1]!.url)
    expect(first.kind).toBe('ok')
    expect(second.kind).toBe('ok')
    if (first.kind === 'ok' && second.kind === 'ok') {
      expect(first.data).not.toBe(second.data)
    }
  })
})

describe('D-01：worker.metadata 预算在本地前置拦截', () => {
  it('超过 8KiB 时直接拒绝，不发请求', async () => {
    const { calls, fetchImpl } = createFetch(() => jsonResponse({ ok: true }))
    const ctx = createRequestContext(config, { fetchImpl })
    const body = { metadata: { blob: 'x'.repeat(WORKER_METADATA_MAX_BYTES) } }

    let caught: unknown
    try {
      await requestRaw(ctx, { method: 'POST', path: '/v1/workers/heartbeat', body })
    } catch (err) {
      caught = err
    }

    expect(caught).toBeInstanceOf(EchuanValidationError)
    const err = caught as EchuanValidationError
    expect(err.code).toBe('too_large')
    expect(err.field).toBe('metadata')
    expect(err.message).toContain('metadata')
    expect(err.message).toContain('缩小')
    expect(calls).toHaveLength(0)
  })

  it('未超限时正常发送；循环引用 metadata 也前置拒绝', async () => {
    const { calls, fetchImpl } = createFetch(() => jsonResponse({ ok: true }))
    const ctx = createRequestContext(config, { fetchImpl })

    await requestRaw(ctx, {
      method: 'POST',
      path: '/v1/workers/heartbeat',
      body: { metadata: { host: 'mac-01' } },
    })
    expect(calls).toHaveLength(1)

    const cyclic: Record<string, unknown> = {}
    cyclic.self = cyclic

    let caught: unknown
    try {
      await requestRaw(ctx, {
        method: 'POST',
        path: '/v1/workers/heartbeat',
        body: { metadata: cyclic },
      })
    } catch (err) {
      caught = err
    }
    expect(caught).toBeInstanceOf(EchuanValidationError)
    expect((caught as EchuanValidationError).message).toContain('无法序列化为 JSON')
    expect(calls).toHaveLength(1)
  })
})

function listTsFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      out.push(...listTsFiles(full))
    } else if (entry.isFile() && entry.name.endsWith('.ts')) {
      out.push(full)
    }
  }
  return out
}
