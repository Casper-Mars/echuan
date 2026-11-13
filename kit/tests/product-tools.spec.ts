/**
 * 产品改 / 删两个工具的单测（方案 v0.4 §3 AC1–AC6、§6、§7.1–§7.3）。
 *
 * 全部假平台 + 真 client（不是假 client）：URL 拼接、query 编码、body 序列化一起被验证。
 *
 * 断言口径（对应决策）：
 * - **D2**：参数校验全交平台——工具层不写任何守卫、参数 schema 不声明 `required`，
 *   错误一律由「假平台 400」产生；断言里同时检查「工具确实发了请求」（不是本地拦）；
 * - **D3**：一次 PATCH 全量提交四个可改字段，缺参补 `""`，不存在只发部分字段的路径；
 * - **D4**：输出字段值取平台回包，不回声入参（假平台故意回与入参不同的值）。
 *
 * 「零写入」的可观测定义：假平台只在 2xx 回包时记一次写入，错误路径留空数组。
 */
import { describe, expect, it } from 'vitest'
import type { ToolDefinition } from '@deepseek-ai/dsh-tools'
import { createClient, type FetchLike } from '../src/client.js'
import { resolveConfig } from '../src/config.js'
import { createAllEchuanTools } from '../src/tools/index.js'

interface RecordedCall {
  url: URL
  method: string
  body: string | undefined
}

function json(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

/** 平台错误体（`api/openapi.yaml#/components/schemas/Error` 的形状）。 */
function jsonError(
  status: number,
  code: string,
  message: string,
  details: Array<{ field?: string; reason?: string }> = [],
): Response {
  return json({ code, message, details, request_id: `req-${code}` }, status)
}

/** 平台 200 回包：字段值与调用方提交值**故意不同**，用于证明 D4「不回声入参」。 */
const PRODUCT_PAYLOAD = {
  id: 'APP',
  product_id: 'APP',
  name: '平台回包名称',
  git_url: 'git@example.com:platform.git',
  description: '平台回包描述',
  license: 'Apache-2.0',
  project_count: 2,
  update_time: '2026-11-13T08:00:00.000Z',
}

interface ProductPlatform {
  calls: RecordedCall[]
  /** 只有 2xx 才记的「写入」轨迹；错误路径必须为空（= 零写入）。 */
  writes: string[]
  fetchImpl: FetchLike
}

/** 假产品平台：默认 PATCH → 200 产品回包，DELETE → 204。 */
function createProductPlatform(responders: {
  patch?: (body: Record<string, unknown>, call: RecordedCall) => Response
  delete?: (call: RecordedCall) => Response
} = {}): ProductPlatform {
  const calls: RecordedCall[] = []
  const writes: string[] = []

  const fetchImpl: FetchLike = async (input, init = {}) => {
    const call: RecordedCall = {
      url: new URL(input),
      method: init.method ?? 'GET',
      body: init.body === undefined ? undefined : String(init.body),
    }
    calls.push(call)

    const body = call.body === undefined
      ? {}
      : (JSON.parse(call.body) as Record<string, unknown>)

    let response: Response
    if (call.method === 'PATCH') {
      response = responders.patch === undefined ? json(PRODUCT_PAYLOAD) : responders.patch(body, call)
    } else if (call.method === 'DELETE') {
      response = responders.delete === undefined
        ? new Response(null, { status: 204 })
        : responders.delete(call)
    } else {
      response = json({})
    }

    if (response.status < 300) writes.push(`${call.method} ${call.url.pathname}`)
    return response
  }

  return { calls, writes, fetchImpl }
}

const config = resolveConfig({ baseUrl: 'http://platform.test' })
const exec = { signal: new AbortController().signal } as unknown as Parameters<
  ToolDefinition['execute']
>[1]

function toolsFor(fetchImpl: FetchLike): Map<string, ToolDefinition> {
  const options = { client: createClient(config, { fetchImpl }), identity: { workerId: undefined } }
  return new Map(createAllEchuanTools(options).map(tool => [tool.name, tool]))
}

async function run(
  tool: ToolDefinition | undefined,
  args: Record<string, unknown>,
): Promise<unknown> {
  if (tool === undefined) throw new Error('工具未注册')
  return tool.execute(args, exec)
}

async function failureOf(promise: Promise<unknown>): Promise<Error> {
  try {
    await promise
  } catch (err) {
    return err as Error
  }
  throw new Error('预期失败，但调用成功了')
}

function bodyOf(call: RecordedCall): Record<string, unknown> {
  if (call.body === undefined) throw new Error('这次请求没有 body')
  return JSON.parse(call.body) as Record<string, unknown>
}

/** 取工具的模型可见 JSON Schema（`required` 断言用）。 */
function schemaOf(tool: ToolDefinition): {
  properties?: Record<string, unknown>
  required?: unknown
} {
  return tool.parameters as { properties?: Record<string, unknown>; required?: unknown }
}

function renderedText(tool: ToolDefinition, value: unknown): string {
  const render = tool.output.render as (args: unknown, value: unknown) => Array<{ text: string }>
  return render({}, value)[0]!.text
}

// ---------------------------------------------------------------------------
// 1. update：一次 PATCH 全量提交四字段（决策 D3）
// ---------------------------------------------------------------------------

describe('echuan_product_update：一次 PATCH 全量提交四字段（D3）', () => {
  it('只传 description：body 仍含四键，name / git_url / license 补空串', async () => {
    const platform = createProductPlatform()

    const value = await run(toolsFor(platform.fetchImpl).get('echuan_product_update'), {
      id: 'APP',
      description: '新描述',
    }) as { kind: string }

    // 恰好一次 PATCH，标识只走 query。
    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.method).toBe('PATCH')
    expect(platform.calls[0]!.url.pathname).toBe('/v1/products')
    expect(platform.calls[0]!.url.searchParams.get('id')).toBe('APP')

    // 精确到值：四键恒在，缺参处是空串（不是 undefined、不是缺键）。
    expect(bodyOf(platform.calls[0]!)).toEqual({
      name: '',
      git_url: '',
      description: '新描述',
      license: '',
    })
    expect(value.kind).toBe('product_updated')
  })

  it('传齐四字段：四键原样进 body，输出取平台回包（不回声入参）', async () => {
    const platform = createProductPlatform()

    const tool = toolsFor(platform.fetchImpl).get('echuan_product_update')
    const value = await run(tool, {
      id: 'APP',
      name: '提交的名称',
      git_url: 'git@example.com:submitted.git',
      description: '提交的描述',
      license: 'MIT',
    }) as Record<string, unknown>

    expect(bodyOf(platform.calls[0]!)).toEqual({
      name: '提交的名称',
      git_url: 'git@example.com:submitted.git',
      description: '提交的描述',
      license: 'MIT',
    })

    // D4：所有字段值都来自假平台回包，没有一个等于提交值。
    expect(value).toEqual({
      kind: 'product_updated',
      product_id: 'APP',
      name: '平台回包名称',
      git_url: 'git@example.com:platform.git',
      description: '平台回包描述',
      license: 'Apache-2.0',
      project_count: 2,
      update_time: '2026-11-13T08:00:00.000Z',
    })

    // 渲染同样只陈述回包值。
    const text = renderedText(tool!, value)
    expect(text).toContain('平台回包名称')
    expect(text).toContain('项目数：2')
    expect(text).not.toContain('提交的名称')
  })

  it('description / license 传空串即清空：空串原样进 body，平台 200', async () => {
    const platform = createProductPlatform()

    await run(toolsFor(platform.fetchImpl).get('echuan_product_update'), {
      id: 'APP',
      name: '保留的名称',
      git_url: 'git@example.com:keep.git',
      description: '',
      license: '',
    })

    expect(bodyOf(platform.calls[0]!)).toEqual({
      name: '保留的名称',
      git_url: 'git@example.com:keep.git',
      description: '',
      license: '',
    })
  })

  it('四字段全缺（只传 id）：body 仍是四键空串，不存在「空 patch」快捷路径', async () => {
    const platform = createProductPlatform()

    await run(toolsFor(platform.fetchImpl).get('echuan_product_update'), { id: 'APP' })

    expect(platform.calls).toHaveLength(1)
    expect(Object.keys(bodyOf(platform.calls[0]!)).sort())
      .toEqual(['description', 'git_url', 'license', 'name'])
    expect(bodyOf(platform.calls[0]!)).toEqual({
      name: '',
      git_url: '',
      description: '',
      license: '',
    })
  })
})

// ---------------------------------------------------------------------------
// 2. update：参数校验全交平台，工具不本地拦（决策 D2）
// ---------------------------------------------------------------------------

describe('echuan_product_update：参数校验全交平台，工具不本地拦（D2）', () => {
  it('误传 product_id：原样并入 body → 平台 400 product_id is immutable，零写入', async () => {
    const platform = createProductPlatform({
      patch: () => jsonError(400, 'invalid_argument', 'invalid request body', [
        { field: 'product_id', reason: 'is immutable' },
      ]),
    })

    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_product_update'), {
      id: 'APP',
      name: '名称',
      git_url: 'git@example.com:x.git',
      description: '描述',
      license: 'MIT',
      // 未声明键：工具不拦、不改写，原样交给平台判定。
      product_id: 'APP2',
    }))

    // 工具确实发了请求（不是本地拒绝），且 product_id 原样进了 body。
    expect(platform.calls).toHaveLength(1)
    expect(bodyOf(platform.calls[0]!)).toEqual({
      name: '名称',
      git_url: 'git@example.com:x.git',
      description: '描述',
      license: 'MIT',
      product_id: 'APP2',
    })

    expect(err.message).toContain('invalid_argument')
    expect(err.message).toContain('400')
    expect(err.message).toContain('product_id: is immutable')
    // 零写入：假平台只在 2xx 记写入，这里必须是空。
    expect(platform.writes).toEqual([])
  })

  it('name 传空串：工具照发请求 → 平台 400 field=name，零写入', async () => {
    const platform = createProductPlatform({
      patch: () => jsonError(400, 'invalid_argument', 'invalid request body', [
        { field: 'name', reason: 'is required' },
      ]),
    })

    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_product_update'), {
      id: 'APP',
      name: '',
      git_url: 'git@example.com:x.git',
    }))

    expect(platform.calls).toHaveLength(1)
    expect(bodyOf(platform.calls[0]!).name).toBe('')
    expect(err.message).toContain('invalid_argument')
    expect(err.message).toContain('name: is required')
    expect(platform.writes).toEqual([])
  })

  it('契约外的未声明键（如 status）不阻断调用，也不会混进 body（客户端只按契约拼）', async () => {
    const platform = createProductPlatform()

    await run(toolsFor(platform.fetchImpl).get('echuan_product_update'), {
      id: 'APP',
      name: '名称',
      git_url: 'git@example.com:x.git',
      status: 7,
    })

    // 工具不本地报错（D2），请求照发；客户端 updateProduct 只拼契约字段，
    // 因此 status 这种契约外键既不会进 body，也不会让调用失败。
    expect(platform.calls).toHaveLength(1)
    expect(bodyOf(platform.calls[0]!)).toEqual({
      name: '名称',
      git_url: 'git@example.com:x.git',
      description: '',
      license: '',
    })
  })

  it('缺 id：不本地拦，query 仍发 ?id=（空值），由平台 400 id is required', async () => {
    const platform = createProductPlatform({
      patch: () => jsonError(400, 'invalid_argument', 'invalid request', [
        { field: 'id', reason: 'is required' },
      ]),
    })

    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_product_update'), {
      name: '名称',
      git_url: 'git@example.com:x.git',
    }))

    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.url.searchParams.get('id')).toBe('')
    expect(err.message).toContain('id: is required')
    expect(platform.writes).toEqual([])
  })

  it('平台 404 not_found 原样透出（提示先读回现状）', async () => {
    const platform = createProductPlatform({
      patch: () => jsonError(404, 'not_found', 'product "NOPE" not found'),
    })

    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_product_update'), {
      id: 'NOPE',
      name: '名称',
      git_url: 'git@example.com:x.git',
    }))

    expect(err.message).toContain('not_found')
    expect(err.message).toContain('404')
    expect(platform.writes).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// 3. delete：204 / 404 / 409 三条路径
// ---------------------------------------------------------------------------

describe('echuan_product_delete：204 / 404 / 409 三条路径', () => {
  it('204 → 输出 kind=product_deleted，DELETE 带 ?id=', async () => {
    const platform = createProductPlatform()
    const tool = toolsFor(platform.fetchImpl).get('echuan_product_delete')

    const value = await run(tool, { id: 'APPX' }) as { kind: string; product_id: string }

    expect(value).toEqual({ kind: 'product_deleted', product_id: 'APPX' })
    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.method).toBe('DELETE')
    expect(platform.calls[0]!.url.pathname).toBe('/v1/products')
    expect(platform.calls[0]!.url.searchParams.get('id')).toBe('APPX')
    expect(platform.writes).toEqual(['DELETE /v1/products'])

    const text = renderedText(tool!, value)
    expect(text).toContain('产品已删除：APPX')
    expect(text).toContain('不可恢复')
  })

  it('409（仍有项目）→ 原样透出 code=conflict，不强删、不兜底', async () => {
    const platform = createProductPlatform({
      delete: () => jsonError(409, 'conflict', 'product "APP" still has 2 project(s)'),
    })

    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_product_delete'), {
      id: 'APP',
    }))

    expect(err.message).toContain('conflict')
    expect(err.message).toContain('409')
    expect(err.message).toContain('still has 2 project(s)')
    // 不强删 / 不兜底：只有一次 DELETE，没有重试，也没有任何写入。
    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.method).toBe('DELETE')
    expect(platform.writes).toEqual([])
  })

  it('404（不存在/已删除）→ 透出 code=not_found，不当成功', async () => {
    const platform = createProductPlatform({
      delete: () => jsonError(404, 'not_found', 'product "GONE" not found'),
    })

    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_product_delete'), {
      id: 'GONE',
    }))

    expect(err.message).toContain('not_found')
    expect(err.message).toContain('404')
    expect(platform.calls).toHaveLength(1)
  })

  it('2xx 但非 204（200）→ 本地 invalid_response，不当成功', async () => {
    const platform = createProductPlatform({
      delete: () => json({ ok: true }),
    })

    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_product_delete'), {
      id: 'APPX',
    }))

    expect(err.message).toContain('invalid_response')
    expect(err.message).toContain('200')
    expect(err.message).toContain('预期 204')
  })

  it('缺 id：不本地拦，DELETE ?id= 交平台 400 id is required', async () => {
    const platform = createProductPlatform({
      delete: () => jsonError(400, 'invalid_argument', 'invalid request', [
        { field: 'id', reason: 'is required' },
      ]),
    })

    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_product_delete'), {}))

    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.url.searchParams.get('id')).toBe('')
    expect(err.message).toContain('id: is required')
    expect(platform.writes).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// 4. 参数 schema：一律不声明 required（决策 D2）
// ---------------------------------------------------------------------------

describe('工具参数 schema：一律不声明 required（D2）', () => {
  const tools = () => toolsFor(createProductPlatform().fetchImpl)

  it('两个新工具都不声明任何必填参数', () => {
    const update = tools().get('echuan_product_update')!
    const remove = tools().get('echuan_product_delete')!

    // 参数清单固定（多一个少一个都会被发现）。
    expect(Object.keys(schemaOf(update).properties ?? {}).sort())
      .toEqual(['description', 'git_url', 'id', 'license', 'name'])
    expect(Object.keys(schemaOf(remove).properties ?? {})).toEqual(['id'])

    // 根级 required 不含任何字段；缺省与空数组都算「没声明」。
    expect(schemaOf(update).required ?? []).toEqual([])
    expect(schemaOf(remove).required ?? []).toEqual([])
  })

  it('自证：同文件里 echuan_product_create 确实声明了 required（上面不是空转）', () => {
    const create = tools().get('echuan_product_create')!
    expect(schemaOf(create).required).toEqual(
      expect.arrayContaining(['product_id', 'name', 'git_url']),
    )
  })

  it('标识参数是 id（走 query ?id=），product_id 不在参数清单里', () => {
    const update = tools().get('echuan_product_update')!
    const properties = schemaOf(update).properties ?? {}
    expect(properties).toHaveProperty('id')
    expect(properties).not.toHaveProperty('product_id')
  })
})

// ---------------------------------------------------------------------------
// 5. 工具描述：方案 §7.3 的要点逐条落地（AC6 判定依据）
// ---------------------------------------------------------------------------

describe('echuan_product_update 描述：§7.3 要点逐条落地', () => {
  const description = (): string => toolsFor(createProductPlatform().fetchImpl)
    .get('echuan_product_update')!.description

  it('写明接口与「一次提交四个字段」', () => {
    const text = description()
    expect(text).toContain('PATCH /v1/products?id=')
    expect(text).toMatch(/一次提交\s*name\s*\/\s*git_url\s*\/\s*description\s*\/\s*license/)
    expect(text).toContain('整体修改')
  })

  it('写明 product_id 不可修改、误传被平台 400 product_id is immutable 拒绝', () => {
    const text = description()
    expect(text).toContain('product_id')
    expect(text).toMatch(/不可(修改|改)/)
    expect(text).toContain('product_id is immutable')
    expect(text).toContain('工具不提供该入参')
    expect(text).toContain('400')
  })

  it('写明空参数即空：description / license 清空、name / git_url 由平台 400', () => {
    const text = description()
    expect(text).toContain('description / license 传空即清空')
    expect(text).toContain('name / git_url 为空会被平台 400')
  })

  it('写明要保留原值须先 echuan_product_list 读回现值再一起传', () => {
    const text = description()
    expect(text).toContain('echuan_product_list')
    expect(text).toContain('读回现值再一起传')
  })

  it('写明改 git_url 影响已有项目、参数合法性由平台判定', () => {
    const text = description()
    expect(text).toContain('改 git_url 会影响已有项目')
    expect(text).toContain('项目归属 product_id 不变')
    expect(text).toContain('参数合法性由平台判定，本工具不做本地校验')
  })
})

describe('echuan_product_delete 描述：§7.3 要点逐条落地', () => {
  const description = (): string => toolsFor(createProductPlatform().fetchImpl)
    .get('echuan_product_delete')!.description

  it('写明接口 DELETE /v1/products?id= 与「没有项目才可删」', () => {
    const text = description()
    expect(text).toContain('DELETE /v1/products?id=')
    expect(text).toContain('没有项目')
  })

  it('写明删除不可恢复、同 id（product_id）重建会复用编号', () => {
    const text = description()
    expect(text).toContain('删除不可恢复')
    expect(text).toContain('同 id（product_id）重建会复用编号')
  })

  it('写明 409 不强删、参数由平台校验', () => {
    const text = description()
    expect(text).toContain('平台 409')
    expect(text).toContain('不会强删')
    expect(text).toContain('参数由平台校验，本工具不做本地校验')
  })
})

describe('两段描述：AC6 原文断言的三个要点都在', () => {
  it('含「product_id」+「不可（修改/改）」+「删除不可恢复」', () => {
    const tools = toolsFor(createProductPlatform().fetchImpl)
    const update = tools.get('echuan_product_update')!.description
    const remove = tools.get('echuan_product_delete')!.description

    expect(update).toContain('product_id')
    expect(update).toMatch(/不可(修改|改)/)
    expect(remove).toContain('product_id')
    expect(remove).toContain('删除不可恢复')
  })
})
