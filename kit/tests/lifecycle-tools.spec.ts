/**
 * 生命周期写工具单测（任务-07）：建项目 → 写需求 → 提交 → 建方案 → 提交 → 批量建卡。
 *
 * 全部用真 client + 假 fetch，覆盖：
 * 1. 完整链路：每步返回的 id 能直接串下一步，项目最终到「任务评审」(5)；
 * 2. 缺需求就提交：平台 409「请先上传需求文档」原样透出（不吞、不改写）；
 * 3. `spec_revise` 缺 version：本地拒绝且零请求；
 * 4. `spec_revise` 错版本：后端 409 invalid_version 透出，且 `If-Match` 走请求头、不进 body；
 * 5. `depends_on` 透传为 1-based 批内序号数组；
 * 6. 缺字段 / 空 tasks：本地拒绝、零请求。
 */
import { describe, expect, it } from 'vitest'
import type { ToolDefinition } from '@deepseek-ai/dsh-tools'
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
  body: Record<string, unknown> | undefined
  headers: Record<string, string>
}

type Responder = (call: RecordedCall) => Response

function createPlatform(respond: Responder): { calls: RecordedCall[]; fetchImpl: FetchLike } {
  const calls: RecordedCall[] = []
  const fetchImpl: FetchLike = async (input, init = {}) => {
    const call: RecordedCall = {
      url: new URL(input),
      method: init.method ?? 'GET',
      body: init.body === undefined ? undefined : (JSON.parse(String(init.body)) as Record<string, unknown>),
      headers: (init.headers ?? {}) as Record<string, string>,
    }
    calls.push(call)
    return respond(call)
  }
  return { calls, fetchImpl }
}

function json(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

function apiError(status: number, code: string, message: string): Response {
  return json({ code, message, details: [], request_id: `req-${code}` }, status)
}

const config = resolveConfig({ baseUrl: 'http://platform.test' })
const exec = { signal: new AbortController().signal } as unknown as Parameters<
  ToolDefinition['execute']
>[1]

function toolsFor(fetchImpl: FetchLike): Map<string, ToolDefinition> {
  const options: EchuanToolOptions = {
    client: createClient(config, { fetchImpl }),
    identity: { workerId: undefined },
  }
  return new Map(createAllEchuanTools(options).map(tool => [tool.name, tool]))
}

async function call(
  tools: Map<string, ToolDefinition>,
  name: string,
  args: Record<string, unknown>,
): Promise<unknown> {
  const tool = tools.get(name)
  if (tool === undefined) throw new Error(`工具 ${name} 不存在`)
  return tool.execute(args, exec)
}

async function failureOf(promise: Promise<unknown>): Promise<Error> {
  try {
    await promise
  } catch (err) {
    return err as Error
  }
  throw new Error('预期抛错，但调用成功了')
}

// ---------------------------------------------------------------------------
// 1. 完整链路
// ---------------------------------------------------------------------------

describe('生命周期链路：项目 0 → 1 → 2 → 3 → 5，id 逐步串联', () => {
  it('建项目 → 写需求 → 提交 → 建方案 → 提交 → 批量建卡', async () => {
    let requirementWritten = false
    let specCreated = false

    const platform = createPlatform((request) => {
      switch (`${request.method} ${request.url.pathname}`) {
        case 'POST /v1/projects':
          return json({
            project_id: 'APP-9',
            product_id: 'APP',
            title: '新项目',
            status: 0,
            status_name: '需求草稿',
            version: 1,
          }, 201)
        case 'POST /v1/projects/requirement':
          requirementWritten = true
          return json({
            project_id: 'APP-9',
            requirement: { material_id: 'M-REQ-1', version: 1, title: '需求', size: 64 },
          })
        case 'POST /v1/projects/submit':
          if (request.url.searchParams.get('id') !== 'APP-9') {
            return apiError(404, 'not_found', '项目不存在')
          }
          if (!requirementWritten) {
            return apiError(409, 'invalid_state', '请先上传需求文档')
          }
          return json({
            project_id: 'APP-9',
            status: specCreated ? 3 : 1,
            status_name: specCreated ? '方案评审' : '需求评审',
            version: specCreated ? 4 : 2,
          })
        case 'POST /v1/specs':
          specCreated = true
          return json({
            spec_id: 'APP-9-S1',
            project_id: 'APP-9',
            title: '方案 A',
            version: 1,
            material: { material_id: 'M-SPEC-1' },
          }, 201)
        case 'POST /v1/projects/plans':
          return json({
            project_id: 'APP-9',
            status: 5,
            status_name: '任务评审',
            project_version: 6,
            tasks: [
              { id: 'APP-9-T0001', seq_no: 1, title: '卡 1', priority: 0, status: 0, status_name: '待办', depends_on: [] },
              { id: 'APP-9-T0002', seq_no: 2, title: '卡 2', priority: 5, status: 0, status_name: '待办', depends_on: ['APP-9-T0001'] },
            ],
          }, 201)
        default:
          return json({})
      }
    })

    const tools = toolsFor(platform.fetchImpl)

    const created = await call(tools, 'echuan_project_create', {
      product_id: 'APP',
      title: '新项目',
    }) as { project_id: string; status: number }
    expect(created.project_id).toBe('APP-9')
    expect(created.status).toBe(0)

    const written = await call(tools, 'echuan_project_requirement_write', {
      project_id: created.project_id,
      body_md: '# 需求正文',
    }) as { material_id: string }
    expect(written.material_id).toBe('M-REQ-1')

    const firstSubmit = await call(tools, 'echuan_project_submit', {
      project_id: created.project_id,
    }) as { status: number }
    expect(firstSubmit.status).toBe(1)

    const spec = await call(tools, 'echuan_spec_create', {
      project_id: created.project_id,
      title: '方案 A',
      body_md: '# 方案正文',
    }) as { spec_id: string; version: number }
    expect(spec.spec_id).toBe('APP-9-S1')
    expect(spec.version).toBe(1)

    const secondSubmit = await call(tools, 'echuan_project_submit', {
      project_id: created.project_id,
    }) as { status: number }
    expect(secondSubmit.status).toBe(3)

    const plan = await call(tools, 'echuan_plan_submit', {
      project_id: created.project_id,
      tasks: [
        { title: '卡 1', body_md: '正文 1' },
        { title: '卡 2', body_md: '正文 2', priority: 5, depends_on: [1] },
      ],
    }) as { status: number; tasks: Array<{ task_id: string }> }
    expect(plan.status).toBe(5)
    expect(plan.tasks.map(task => task.task_id)).toEqual(['APP-9-T0001', 'APP-9-T0002'])

    // 每个请求都落在预期端点上，且没有多余请求。
    expect(platform.calls.map(request => `${request.method} ${request.url.pathname}`)).toEqual([
      'POST /v1/projects',
      'POST /v1/projects/requirement',
      'POST /v1/projects/submit',
      'POST /v1/specs',
      'POST /v1/projects/submit',
      'POST /v1/projects/plans',
    ])
  })
})

// ---------------------------------------------------------------------------
// 2. 缺需求提交：409 原样透出
// ---------------------------------------------------------------------------

describe('echuan_project_submit：缺需求 409 透出', () => {
  it('平台 message「请先上传需求文档」原样出现在错误里', async () => {
    const platform = createPlatform(() => apiError(409, 'invalid_state', '请先上传需求文档'))
    const tools = toolsFor(platform.fetchImpl)

    const err = await failureOf(call(tools, 'echuan_project_submit', { project_id: 'APP-2' }))

    expect(err.message).toContain('[invalid_state]')
    expect(err.message).toContain('请先上传需求文档')
    expect(err.message).toContain('409')
    expect(platform.calls).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// 3. spec_revise 版本校验
// ---------------------------------------------------------------------------

describe('echuan_spec_revise：version 本地校验与 409 透出', () => {
  it('缺 version：本地拒绝且零请求', async () => {
    const platform = createPlatform(() => json({}))
    const tools = toolsFor(platform.fetchImpl)

    const err = await failureOf(call(tools, 'echuan_spec_revise', {
      spec_id: 'S-1',
      body_md: '# 方案',
    }))

    // dsh 在 execute 之前就按 parameters schema 拒绝了缺参（本地、零请求）。
    expect(err.message).toMatch(/invalid argument|\[invalid_argument\]/)
    expect(err.message).toContain('version')
    expect(platform.calls).toHaveLength(0)
  })

  it('version 非正数：本地拒绝且零请求', async () => {
    const platform = createPlatform(() => json({}))
    const tools = toolsFor(platform.fetchImpl)

    const err = await failureOf(call(tools, 'echuan_spec_revise', {
      spec_id: 'S-1',
      version: 0,
      body_md: '# 方案',
    }))

    expect(err.message).toContain('version')
    expect(platform.calls).toHaveLength(0)
  })

  it('错版本：If-Match 走请求头、body 不带 version，409 invalid_version 透出', async () => {
    const platform = createPlatform(() => apiError(409, 'invalid_version', '版本已过期，请刷新后重试'))
    const tools = toolsFor(platform.fetchImpl)

    const err = await failureOf(call(tools, 'echuan_spec_revise', {
      spec_id: 'S-1',
      version: 2,
      title: '方案 A',
      body_md: '# 方案 v2',
    }))

    expect(err.message).toContain('[invalid_version]')
    expect(err.message).toContain('版本已过期')
    expect(platform.calls).toHaveLength(1)

    const request = platform.calls[0]!
    expect(request.method).toBe('PATCH')
    expect(request.url.pathname).toBe('/v1/specs')
    expect(request.url.searchParams.get('id')).toBe('S-1')
    expect(request.headers['If-Match']).toBe('2')
    expect(request.body).toEqual({ title: '方案 A', body_md: '# 方案 v2' })
    expect(request.body).not.toHaveProperty('version')
  })
})

// ---------------------------------------------------------------------------
// 4. depends_on 透传
// ---------------------------------------------------------------------------

describe('echuan_plan_submit：depends_on 透传', () => {
  it('1-based 批内序号原样进入 body，不做 id 转换', async () => {
    const platform = createPlatform(() => json({
      project_id: 'APP-2',
      status: 5,
      status_name: '任务评审',
      project_version: 2,
      tasks: [],
    }, 201))
    const tools = toolsFor(platform.fetchImpl)

    await call(tools, 'echuan_plan_submit', {
      project_id: 'APP-2',
      tasks: [
        { title: '卡 1', body_md: '正文 1', depends_on: [2, 3] },
        { title: '卡 2', body_md: '正文 2', priority: -1 },
      ],
    })

    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.url.searchParams.get('id')).toBe('APP-2')
    expect(platform.calls[0]!.body).toEqual({
      tasks: [
        { title: '卡 1', body_md: '正文 1', depends_on: [2, 3] },
        { title: '卡 2', body_md: '正文 2', priority: -1 },
      ],
    })
  })

  it('空 tasks / 空白 title / 非法 depends_on：本地拒绝且零请求', async () => {
    const cases: Array<Record<string, unknown>> = [
      { project_id: 'APP-2', tasks: [] },
      { project_id: 'APP-2', tasks: [{ title: '  ', body_md: '正文' }] },
      { project_id: 'APP-2', tasks: [{ title: '卡', body_md: '' }] },
      { project_id: 'APP-2', tasks: [{ title: '卡', body_md: '正文', depends_on: [0] }] },
    ]

    for (const args of cases) {
      const platform = createPlatform(() => json({}))
      const tools = toolsFor(platform.fetchImpl)
      const err = await failureOf(call(tools, 'echuan_plan_submit', args))
      expect(err.message).toContain('[invalid_argument]')
      expect(platform.calls).toHaveLength(0)
    }
  })
})

// ---------------------------------------------------------------------------
// 5. 其它入参校验
// ---------------------------------------------------------------------------

describe('生命周期写工具：必填入参本地校验', () => {
  it('建项目缺 product_id / title：零请求', async () => {
    const platform = createPlatform(() => json({}))
    const tools = toolsFor(platform.fetchImpl)

    await failureOf(call(tools, 'echuan_project_create', { title: '只有标题' }))
    await failureOf(call(tools, 'echuan_project_create', { product_id: 'APP', title: '  ' }))

    expect(platform.calls).toHaveLength(0)
  })

  it('写需求空白正文：零请求', async () => {
    const platform = createPlatform(() => json({}))
    const tools = toolsFor(platform.fetchImpl)

    await failureOf(call(tools, 'echuan_project_requirement_write', {
      project_id: 'APP-2',
      body_md: '   ',
    }))

    expect(platform.calls).toHaveLength(0)
  })
})
