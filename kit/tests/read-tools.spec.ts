/**
 * 四个只读工具单测（任务-05：待办 / 项目全景 / 任务详情 / 资料正文）。
 *
 * 全部用假平台（注入 fetch），不连真实平台。覆盖：
 * 1. 7 个工具名（3 写 + 4 读），只读 4 个名字精确匹配；
 * 2. inbox 的汇总、停留时长人话、超时标记、reason 为 null 显示「无」；
 * 3. project 的需求摘要（截断标注）、方案标题/版本、任务统计数字、reason；
 * 4. task 的 attempt_count/max_attempts、报告、reason、正文；
 * 5. material 的正文原文、元信息、空正文提示；
 * 6. 三个按 id 的工具缺 id → 本地拒绝、零请求、提示含 echuan_inbox；
 * 7. 平台 404 → 文案含「不存在」与「编号」；
 * 8. 4 个工具只发 GET（记录所有 method）；
 * 9. 输出全部过 output.schema，且不把原始 JSON 甩给模型。
 */
import { describe, expect, it } from 'vitest'
import { validateJsonSchemaValue, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import { createClient, type FetchLike } from '../src/client.js'
import { resolveConfig } from '../src/config.js'
import {
  READ_TOOL_NAMES,
  TOOL_NAMES,
  WRITE_TOOL_NAMES,
  createAllEchuanTools,
} from '../src/tools/index.js'
import type { EchuanToolOptions } from '../src/tools/index.js'

// ---------------------------------------------------------------------------
// 假平台
// ---------------------------------------------------------------------------

type Handler = (url: string, init: RequestInit, call: number) => Response | Promise<Response>

interface RecordedCall {
  url: string
  body: string | undefined
  init: RequestInit
}

function createPlatform(handler: Handler): { calls: RecordedCall[]; fetchImpl: FetchLike } {
  const calls: RecordedCall[] = []
  const fetchImpl: FetchLike = async (url, init = {}) => {
    calls.push({ url, body: init.body === undefined ? undefined : String(init.body), init })
    return handler(url, init, calls.length - 1)
  }
  return { calls, fetchImpl }
}

function jsonResponse(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

function textResponse(body: string, headers: Record<string, string>): Response {
  return new Response(body, { status: 200, headers })
}

function errorResponse(status: number, code: string): Response {
  return new Response(
    JSON.stringify({ code, message: `platform says ${code}`, details: [], request_id: `req-${code}` }),
    { status, headers: { 'content-type': 'application/json' } },
  )
}

const exec = { signal: new AbortController().signal } as unknown as Parameters<ToolDefinition['execute']>[1]

interface ToolRun {
  value: unknown
  text: string
}

/** 执行工具并把 render 结果的文本拼起来（模拟模型看到的内容）。 */
async function runTool(tool: ToolDefinition, args: Record<string, unknown>): Promise<ToolRun> {
  const value = await tool.execute(args, exec)
  const blocks = tool.output.render(args, value as never) as Array<{ type: string; text?: string }>
  return {
    value,
    text: blocks.map(block => (block.type === 'text' ? block.text ?? '' : '')).join('\n'),
  }
}

/** 规范值必须真的能过注册表用的那份 output.schema。 */
function expectValidOutput(tool: ToolDefinition, value: unknown): void {
  expect(validateJsonSchemaValue(tool.output.schema, value)).toEqual([])
}

async function failureOf(promise: Promise<unknown>): Promise<Error> {
  let caught: unknown
  let failed = false
  try {
    await promise
  } catch (err) {
    caught = err
    failed = true
  }
  if (!failed) throw new Error('预期抛错，但调用成功了')
  return caught as Error
}

function toolsFor(fetchImpl: FetchLike): Map<string, ToolDefinition> {
  const options: EchuanToolOptions = {
    client: createClient(resolveConfig({ baseUrl: 'http://platform.test' }), { fetchImpl }),
    identity: { workerId: undefined },
  }
  return new Map(createAllEchuanTools(options).map(tool => [tool.name, tool]))
}

// ---------------------------------------------------------------------------
// 平台返回样例（字段名与 api/openapi.yaml 一致）
// ---------------------------------------------------------------------------

const INBOX_PAYLOAD = {
  items: [
    {
      kind: 'project',
      id: 'APP-2',
      state: 1,
      state_name: 'spec_review',
      title: '登录模块',
      update_time: '2026-10-28T09:44:00.000Z',
      age_seconds: 11_520,
      stale: true,
      reason: '方案待评审',
    },
    {
      kind: 'task',
      id: 'APP-1-T0007',
      state: 3,
      state_name: 'pending_acceptance',
      title: '登录接口',
      update_time: '2026-10-28T20:55:15.000Z',
      age_seconds: 45,
      stale: false,
      reason: null,
    },
  ],
  count: 2,
  review_stale_hours: 24,
  generated_at: '2026-10-28T20:56:00.000Z',
}

const REQUIREMENT_MD = '需求正文。'.repeat(400) // 2000 字，超过需求摘要上限

const PROJECT_PAYLOAD = {
  id: 'APP-2',
  project_id: 'APP-2',
  product_id: 'APP',
  seq_no: 2,
  title: '登录模块',
  status: 1,
  status_name: 'spec_review',
  reason: '等待人工确认方案',
  last_actor: 'agent:echuan',
  version: 7,
  task_stats: { total: 12, done: 6, by_status: { '0': 3, '2': 2, '3': 1, '4': 6 } },
  create_time: '2026-10-27T10:00:00.000Z',
  update_time: '2026-10-28T09:44:00.000Z',
  requirement: {
    material_id: 'm_req0001',
    kind: 1,
    version: 2,
    title: '登录需求',
    sha256: 'sha256:aaa',
    size: 3200,
    uploaded_by: 'human:alice',
    create_time: '2026-10-27T10:01:00.000Z',
  },
  requirement_md: REQUIREMENT_MD,
  spec: {
    material_id: 'm_spec001',
    kind: 2,
    version: 3,
    title: '登录技术方案',
    sha256: 'sha256:bbb',
    size: 12_800,
    uploaded_by: 'human:alice',
    create_time: '2026-10-28T09:40:00.000Z',
  },
  spec_excerpt: '方案摘要：分层实现，先做会话再做权限。',
}

const TASK_PAYLOAD = {
  id: 'APP-1-T0007',
  task_id: 'APP-1-T0007',
  project_id: 'APP-1',
  seq_no: 7,
  title: '实现登录接口',
  body_md: '# 任务正文\n请实现登录接口并补单测。',
  priority: 10,
  status: 3,
  status_name: 'pending_acceptance',
  worker_id: 'w_TEST01ABCDEFGHIJKLMN',
  attempt_count: 2,
  max_attempts: 3,
  reason: '第一次报告缺单测',
  last_actor: 'agent:echuan',
  version: 9,
  depends_on: ['APP-1-T0005'],
  create_time: '2026-10-27T11:00:00.000Z',
  update_time: '2026-10-28T20:30:00.000Z',
  report: {
    material_id: 'm_rep0001',
    kind: 3,
    version: 1,
    title: '登录接口实现报告',
    sha256: 'sha256:ccc',
    size: 900,
    uploaded_by: 'agent:echuan',
    create_time: '2026-10-28T20:30:00.000Z',
  },
  lease: { lease_id: 'l_9f8b7b09ff971dd1', until: '2026-10-28T21:10:00.000Z' },
}

const MATERIAL_MD = '# 登录需求\n\n## 背景\n支持手机号+密码登录。\n'

// ---------------------------------------------------------------------------
// 1. 工具集合：7 个（3 写 + 4 读）
// ---------------------------------------------------------------------------

describe('工具集合：7 个（3 写 + 4 读）', () => {
  it('READ_TOOL_NAMES 精确是 4 个只读工具', () => {
    expect([...READ_TOOL_NAMES]).toEqual([
      'echuan_inbox',
      'echuan_project_get',
      'echuan_task_get',
      'echuan_material_get',
    ])
  })

  it('注册的 7 个名字 = 3 写 + 4 读，且写工具名未变', () => {
    expect([...TOOL_NAMES]).toEqual([
      ...WRITE_TOOL_NAMES,
      ...READ_TOOL_NAMES,
    ])
    expect(TOOL_NAMES).toHaveLength(7)

    const platform = createPlatform(() => jsonResponse({}))
    const names = [...toolsFor(platform.fetchImpl).keys()]
    expect(names).toEqual([...TOOL_NAMES])
    expect(names.filter(name => name === 'echuan_inbox')).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// 2. inbox
// ---------------------------------------------------------------------------

describe('echuan_inbox：待办', () => {
  it('render 含编号、state_name、停留时长人话、超时标记、reason（null → 无）', async () => {
    const platform = createPlatform(() => jsonResponse(INBOX_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl).get('echuan_inbox')

    const { value, text } = await runTool(tool!, { limit: 5 })
    expectValidOutput(tool!, value)

    // 汇总行
    expect(text).toContain('待办共 2 条')
    expect(text).toContain('超时阈值 24 小时')
    expect(text).toContain('2026-10-28T20:56:00.000Z')

    // 项目条目
    expect(text).toContain('[项目] APP-2')
    expect(text).toContain('spec_review')
    expect(text).toContain('已停留 3 小时 12 分')
    expect(text).toContain('⚠ 已超时')
    expect(text).toContain('原因：方案待评审')

    // 任务条目
    expect(text).toContain('[任务] APP-1-T0007')
    expect(text).toContain('pending_acceptance')
    expect(text).toContain('已停留 45 秒')
    expect(text).toContain('正常')
    expect(text).toContain('原因：无')

    // 不把原始 JSON 甩给模型
    expect(text).not.toContain('{"')
    expect(text).not.toContain('age_seconds')
  })

  it('GET /v1/inbox?limit=5；不传 limit 时不带 query', async () => {
    const platform = createPlatform(() => jsonResponse(INBOX_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl).get('echuan_inbox')

    await runTool(tool!, { limit: 5 })
    expect(platform.calls[0]!.init.method).toBe('GET')
    expect(platform.calls[0]!.url).toBe('http://platform.test/v1/inbox?limit=5')
    expect(platform.calls[0]!.body).toBeUndefined()

    await runTool(tool!, {})
    expect(platform.calls[1]!.url).toBe('http://platform.test/v1/inbox')
  })

  it('空列表 →「当前没有待处理项」+ 下一步', async () => {
    const platform = createPlatform(() => jsonResponse({
      items: [],
      count: 0,
      review_stale_hours: 24,
      generated_at: '2026-10-28T20:56:00.000Z',
    }))
    const tool = toolsFor(platform.fetchImpl).get('echuan_inbox')

    const { text } = await runTool(tool!, {})
    expect(text).toContain('当前没有待处理项')
    expect(text).toContain('下一步')
  })

  it('limit 越界 → 本地拒绝、零请求', async () => {
    const platform = createPlatform(() => jsonResponse(INBOX_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl).get('echuan_inbox')

    for (const limit of [0, 201]) {
      const { text } = await runTool(tool!, { limit })
      expect(text).toContain('1–200')
      expect(text).toContain('未发出任何请求')
    }
    expect(platform.calls).toHaveLength(0)

    // 非整数由参数 schema 在 execute 之前拦住，同样零请求。
    const error = await failureOf(runTool(tool!, { limit: 2.5 }))
    expect(error.message).toContain('limit')
    expect(platform.calls).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// 3. project_get
// ---------------------------------------------------------------------------

describe('echuan_project_get：项目全景', () => {
  it('render 含需求摘要（截断标注）、方案标题/版本、任务统计、reason', async () => {
    const platform = createPlatform(() => jsonResponse(PROJECT_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl).get('echuan_project_get')

    const { value, text } = await runTool(tool!, { id: 'APP-2' })
    expectValidOutput(tool!, value)

    expect(text).toContain('项目 APP-2：登录模块')
    expect(text).toContain('spec_review')
    expect(text).toContain('需求摘要')

    // 需求摘要被截断并标注完整长度
    expect(text).toContain('已截断')
    expect(text).toContain(`完整内容共 ${REQUIREMENT_MD.length} 字`)

    // 方案标题与版本
    expect(text).toContain('登录技术方案')
    expect(text).toContain('v3')
    expect(text).toContain('m_spec001')
    expect(text).toContain('方案摘要：分层实现')

    // 任务统计数字
    expect(text).toContain('共 12 个')
    expect(text).toContain('已完成 6 个')
    expect(text).toContain('待办 3')
    expect(text).toContain('进行中 2')

    // reason
    expect(text).toContain('等待人工确认方案')

    expect(text).not.toContain('{"')
    expect(text).not.toContain('requirement_md')
  })

  it('GET /v1/projects?id=APP-2', async () => {
    const platform = createPlatform(() => jsonResponse(PROJECT_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl).get('echuan_project_get')

    await runTool(tool!, { id: 'APP-2' })

    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.init.method).toBe('GET')
    expect(platform.calls[0]!.url).toBe('http://platform.test/v1/projects?id=APP-2')
  })

  it('需求为空时明说「需求正文为空」', async () => {
    const platform = createPlatform(() => jsonResponse({
      ...PROJECT_PAYLOAD,
      requirement_md: '',
      requirement: null,
      spec: null,
      spec_excerpt: null,
    }))
    const tool = toolsFor(platform.fetchImpl).get('echuan_project_get')

    const { text } = await runTool(tool!, { id: 'APP-2' })
    expect(text).toContain('需求正文为空')
    expect(text).toContain('暂无（平台未给出方案）')
  })
})

// ---------------------------------------------------------------------------
// 4. task_get
// ---------------------------------------------------------------------------

describe('echuan_task_get：任务详情', () => {
  it('render 含 attempt_count/max_attempts、报告、reason、正文', async () => {
    const platform = createPlatform(() => jsonResponse(TASK_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl).get('echuan_task_get')

    const { value, text } = await runTool(tool!, { id: 'APP-1-T0007' })
    expectValidOutput(tool!, value)

    expect(text).toContain('任务 APP-1-T0007：实现登录接口')
    expect(text).toContain('所属项目：APP-1')
    expect(text).toContain('pending_acceptance')
    expect(text).toContain('尝试次数：2/3')
    expect(text).toContain('登录接口实现报告')
    expect(text).toContain('m_rep0001')
    expect(text).toContain('第一次报告缺单测')
    expect(text).toContain('请实现登录接口并补单测。')
    expect(text).toContain('l_9f8b7b09ff971dd1')
    expect(text).toContain('APP-1-T0005')

    expect(text).not.toContain('{"')
    expect(text).not.toContain('attempt_count')
  })

  it('GET /v1/tasks?id=APP-1-T0007', async () => {
    const platform = createPlatform(() => jsonResponse(TASK_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl).get('echuan_task_get')

    await runTool(tool!, { id: 'APP-1-T0007' })

    expect(platform.calls[0]!.init.method).toBe('GET')
    expect(platform.calls[0]!.url).toBe('http://platform.test/v1/tasks?id=APP-1-T0007')
  })

  it('超长正文截断并标注总字数', async () => {
    const body = '正文'.repeat(4000) // 8000 字
    const platform = createPlatform(() => jsonResponse({ ...TASK_PAYLOAD, body_md: body }))
    const tool = toolsFor(platform.fetchImpl).get('echuan_task_get')

    const { text } = await runTool(tool!, { id: 'APP-1-T0007' })
    expect(text).toContain('已截断')
    expect(text).toContain(`完整内容共 ${body.length} 字`)
  })

  it('没有报告/租约时明确说「无」', async () => {
    const platform = createPlatform(() => jsonResponse({
      ...TASK_PAYLOAD,
      report: null,
      lease: null,
      reason: null,
    }))
    const tool = toolsFor(platform.fetchImpl).get('echuan_task_get')

    const { text } = await runTool(tool!, { id: 'APP-1-T0007' })
    expect(text).toContain('无（尚未提交报告）')
    expect(text).toContain('原因说明：无')
    expect(text).not.toContain('租约：')
  })
})

// ---------------------------------------------------------------------------
// 5. material_get
// ---------------------------------------------------------------------------

describe('echuan_material_get：资料正文', () => {
  it('render 直接给 markdown 正文，并带 content-type / etag 元信息', async () => {
    const platform = createPlatform(() => textResponse(MATERIAL_MD, {
      'content-type': 'text/markdown; charset=utf-8',
      etag: '"abc123"',
      'x-material-id': 'm_req0001',
      'x-material-version': '2',
    }))
    const tool = toolsFor(platform.fetchImpl).get('echuan_material_get')

    const { value, text } = await runTool(tool!, { id: 'm_req0001' })
    expectValidOutput(tool!, value)

    expect(text).toContain('content-type text/markdown; charset=utf-8')
    expect(text).toContain('etag "abc123"')
    expect(text).toContain('m_req0001')
    expect(text).toContain('版本 2')
    // 正文原文
    expect(text).toContain('# 登录需求')
    expect(text).toContain('支持手机号+密码登录。')

    expect(platform.calls[0]!.init.method).toBe('GET')
    expect(platform.calls[0]!.url).toBe('http://platform.test/v1/materials?id=m_req0001')
  })

  it('空正文 →「资料正文为空」+ 下一步', async () => {
    const platform = createPlatform(() => textResponse('', {
      'content-type': 'text/markdown',
      'x-material-id': 'm_empty01',
    }))
    const tool = toolsFor(platform.fetchImpl).get('echuan_material_get')

    const { text } = await runTool(tool!, { id: 'm_empty01' })
    expect(text).toContain('资料正文为空')
    expect(text).toContain('下一步')
  })

  it('204（无内容）也按空正文处理，不抛异常', async () => {
    const platform = createPlatform(() => new Response(null, { status: 204 }))
    const tool = toolsFor(platform.fetchImpl).get('echuan_material_get')

    const { text } = await runTool(tool!, { id: 'm_empty01' })
    expect(text).toContain('204')
    expect(text).toContain('正文为空')
  })
})

// ---------------------------------------------------------------------------
// 6. 缺 id：本地拒绝、零请求
// ---------------------------------------------------------------------------

describe('缺 id：本地拒绝、零请求、提示先查 echuan_inbox', () => {
  for (const name of ['echuan_project_get', 'echuan_task_get', 'echuan_material_get'] as const) {
    it(`${name}：不传 id / 空白 id 都不发请求`, async () => {
      const platform = createPlatform(() => jsonResponse(PROJECT_PAYLOAD))
      const tool = toolsFor(platform.fetchImpl).get(name)

      for (const args of [{}, { id: '' }, { id: '   ' }]) {
        const { text } = await runTool(tool!, args)
        expect(text).toContain('缺少 id')
        expect(text).toContain('echuan_inbox')
        expect(text).toContain('未发出任何请求')
        // 不是 404、不是抛异常：就是一段可读提示。
        expect(text).not.toContain('not_found')
      }

      expect(platform.calls).toHaveLength(0)
    })
  }
})

// ---------------------------------------------------------------------------
// 7. 平台 404
// ---------------------------------------------------------------------------

describe('平台 404：文案含「不存在」与「编号」', () => {
  for (const name of ['echuan_project_get', 'echuan_task_get', 'echuan_material_get'] as const) {
    it(`${name}：not_found → 编号不存在，先用 echuan_inbox 确认`, async () => {
      const platform = createPlatform(() => errorResponse(404, 'not_found'))
      const tool = toolsFor(platform.fetchImpl).get(name)

      const error = await failureOf(runTool(tool!, { id: 'NOPE-1' }))

      expect(error.message).toContain('[not_found]')
      expect(error.message).toContain('不存在')
      expect(error.message).toContain('编号')
      expect(error.message).toContain('echuan_inbox')
      expect(platform.calls).toHaveLength(1)
    })
  }

  it('inbox 的 404 也走同一套文案（不发写请求）', async () => {
    const platform = createPlatform(() => errorResponse(404, 'not_found'))
    const tool = toolsFor(platform.fetchImpl).get('echuan_inbox')

    const error = await failureOf(runTool(tool!, {}))
    expect(error.message).toContain('[not_found]')
    expect(platform.calls).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// 8. 只读：4 个工具只发 GET
// ---------------------------------------------------------------------------

describe('只读边界：4 个工具只发 GET', () => {
  it('记录所有 method，断言全部是 GET', async () => {
    const platform = createPlatform((url) => {
      if (url.includes('/v1/inbox')) return jsonResponse(INBOX_PAYLOAD)
      if (url.includes('/v1/projects')) return jsonResponse(PROJECT_PAYLOAD)
      if (url.includes('/v1/tasks')) return jsonResponse(TASK_PAYLOAD)
      return textResponse(MATERIAL_MD, { 'content-type': 'text/markdown' })
    })
    const tools = toolsFor(platform.fetchImpl)

    await runTool(tools.get('echuan_inbox')!, {})
    await runTool(tools.get('echuan_project_get')!, { id: 'APP-2' })
    await runTool(tools.get('echuan_task_get')!, { id: 'APP-1-T0007' })
    await runTool(tools.get('echuan_material_get')!, { id: 'm_req0001' })

    expect(platform.calls).toHaveLength(4)
    for (const call of platform.calls) {
      expect(call.init.method).toBe('GET')
      expect(call.body).toBeUndefined()
    }
    expect(platform.calls.map(call => new URL(call.url).pathname)).toEqual([
      '/v1/inbox',
      '/v1/projects',
      '/v1/tasks',
      '/v1/materials',
    ])
  })
})
