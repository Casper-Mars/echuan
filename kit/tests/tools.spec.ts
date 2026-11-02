/**
 * 三个写工具单测（任务-04：领卡 / 续租 / 交报告）。
 *
 * 全部用假平台（注入 fetch），不连真实平台。覆盖：
 * 1. 领卡成功的 render（任务编号 / 标题 / 租约到期，不含工作目录与操作指示）；
 * 2. 204 → 「当前无活」，正常返回不抛错；
 * 3. 请求体 worker_id 与心跳循环内存 id 一致；未注册时该键完全不出现；
 * 4. report 缺失/空白 material_id → 本地拒绝、零请求；
 * 5. lease_lost / worker_busy / worker_unknown / material_required 的下一步文案；
 * 6. 工具名集合恰好 15 个，且没有门禁动作；
 * 7. TOOL_METHODS 恰好 20 个（工具层不污染客户端方法集）。
 */
import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it, vi } from 'vitest'
import { validateJsonSchemaValue, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import {
  TOOL_METHODS,
  createClient,
  createWorkerClient,
  type FetchLike,
} from '../src/client.js'
import { resolveConfig } from '../src/config.js'
import { createWorkerLoop } from '../src/worker.js'
import {
  NO_WORK_TEXT,
  WRITE_TOOL_NAMES,
  createEchuanTools,
} from '../src/tools/index.js'
import type { WorkerIdentity } from '../src/tools/index.js'

const here = path.dirname(fileURLToPath(import.meta.url))
const kitRoot = path.resolve(here, '..')
const toolsDir = path.join(kitRoot, 'src', 'tools')

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

function emptyResponse(): Response {
  return new Response(null, { status: 204 })
}

function errorResponse(status: number, code: string): Response {
  return new Response(
    JSON.stringify({ code, message: `platform says ${code}`, details: [], request_id: `req-${code}` }),
    { status, headers: { 'content-type': 'application/json' } },
  )
}

function parsedBody(call: RecordedCall | undefined): Record<string, unknown> {
  if (call?.body === undefined) return {}
  return JSON.parse(call.body) as Record<string, unknown>
}

// ---------------------------------------------------------------------------
// 测试数据与执行辅助
// ---------------------------------------------------------------------------

const WORKER_ID = 'w_TEST01ABCDEFGHIJKLMN'

const WORK_ID = 'w_01M5Z4ABCDEF'

const CLAIM_PAYLOAD = {
  dispatch: {
    protocol: 'v2',
    work_id: WORK_ID,
    kind: 'dev',
    inputs: [{ material_id: 'M_spec_1', kind: 2, version: 1 }],
    work_item: {
      project_id: 'APP-1',
      title: '实现登录接口',
      body_md: '# 工作项正文\n请实现登录接口。',
      attempt: 1,
      priority: 10,
    },
    lease: {
      lease_id: 'l_9f8b7b09ff971dd1',
      until: '2026-10-27T16:49:25.157Z',
      ttl_seconds: 90,
    },
    context: {
      spec_version: 1,
      spec_digest: 'sha256:bb23da360a3b21e827138bad027ef3f30a29699e84014bc520e4f40131ea2d76',
      product: { id: 'APP', name: '驿传示例产品', git_url: 'git@example.com:APP.git' },
    },
  },
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

/** 规范值必须真的能过注册表用的那份 output.schema（不是只过 render）。 */
function expectValidOutput(tool: ToolDefinition, value: unknown): void {
  expect(validateJsonSchemaValue(tool.output.schema, value)).toEqual([])
}

/** 取「应当抛错」那次的 Error；没抛错则直接让用例失败。 */
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

function toolsFor(fetchImpl: FetchLike, workerId: string | undefined): Map<string, ToolDefinition> {
  const client = createClient(resolveConfig({ baseUrl: 'http://platform.test' }), { fetchImpl })
  const identity: WorkerIdentity = { workerId }
  const tools = createEchuanTools({ client, identity })
  return new Map(tools.map(tool => [tool.name, tool]))
}

// ---------------------------------------------------------------------------
// 1. 领卡成功
// ---------------------------------------------------------------------------

describe('echuan_work_claim：领到卡', () => {
  it('render 含工作项编号、kind、标题、租约、inputs，不含工作目录与操作指示', async () => {
    const platform = createPlatform(() => jsonResponse(CLAIM_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_claim')

    const { value, text } = await runTool(tool!, {})
    expectValidOutput(tool!, value)

    expect(text).toContain(`工作项编号：${WORK_ID}`)
    expect(text).toContain('类别：dev（开发）')
    expect(text).toContain('标题：实现登录接口')
    expect(text).toContain('2026-10-27T16:49:25.157Z')
    expect(text).toContain('material_id=M_spec_1')
    expect(text).not.toContain('工作目录')
    expect(text).not.toContain('echuan_work_report')
    expect(text).not.toContain('下一步')
    // 正文也要给到模型，否则没法干活。
    expect(text).toContain('请实现登录接口。')
  })

  it('走 POST /v1/work-items/claim，body 带 worker_id，可选 work_id 原样透传', async () => {
    const platform = createPlatform(() => jsonResponse(CLAIM_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_claim')

    await runTool(tool!, { work_id: WORK_ID })

    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.url).toBe('http://platform.test/v1/work-items/claim')
    expect(platform.calls[0]!.init.method).toBe('POST')
    expect(parsedBody(platform.calls[0])).toEqual({ worker_id: WORKER_ID, work_id: WORK_ID })
  })
})

// ---------------------------------------------------------------------------
// 2. 204 = 当前无活
// ---------------------------------------------------------------------------

describe('echuan_work_claim：204 当前无活', () => {
  it('按正常结果返回，不抛异常，render 明确说「当前无活」', async () => {
    const platform = createPlatform(() => emptyResponse())
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_claim')

    const { value, text } = await runTool(tool!, {})

    expect(value).toEqual({ kind: 'no_work' })
    expectValidOutput(tool!, value)
    expect(text).toContain('当前无活')
    expect(text).toBe(NO_WORK_TEXT)
  })
})

// ---------------------------------------------------------------------------
// 3. worker_id 只来自心跳循环内存
// ---------------------------------------------------------------------------

describe('echuan_work_claim：worker_id 的唯一来源是心跳循环', () => {
  it('body 的 worker_id 与 loop.workerId 完全一致', async () => {
    // 真循环 + 假平台：心跳把 id 写进循环内存。
    const loopPlatform = createPlatform(() => jsonResponse({
      worker: { worker_id: WORKER_ID, heartbeat_interval_ms: 15_000 },
      commands: [],
    }))
    const config = resolveConfig({ worker: { poll: { enable: true }, kinds: ['dev'] } })
    // 首轮心跳延迟 5s：抓住挂起的定时器回调，手动触发它完成注册。
    const pendingTimers: Array<() => void> = []
    const loop = createWorkerLoop({
      config,
      client: createWorkerClient(config, { fetchImpl: loopPlatform.fetchImpl }),
      agents: { roots: () => [] },
      logger: { info: () => {}, warn: () => {} },
      timer: {
        setTimeout: (handler: () => void) => {
          pendingTimers.push(handler)
          return pendingTimers.length
        },
        clearTimeout: () => {},
      },
    })
    loop.start()
    pendingTimers.shift()?.()
    await vi.waitFor(() => expect(loop.workerId).toBe(WORKER_ID))
    loop.dispose('test')

    // 工具拿到的 identity 只读同一个内存值。
    const claimPlatform = createPlatform(() => jsonResponse(CLAIM_PAYLOAD))
    const client = createClient(resolveConfig({ baseUrl: 'http://platform.test' }), {
      fetchImpl: claimPlatform.fetchImpl,
    })
    const tool = createEchuanTools({
      client,
      identity: { get workerId() { return loop.workerId } },
    }).find(item => item.name === 'echuan_work_claim')

    await runTool(tool!, {})

    expect(parsedBody(claimPlatform.calls[0])).toEqual({ worker_id: WORKER_ID })
    expect(parsedBody(claimPlatform.calls[0])['worker_id']).toBe(loop.workerId)
  })

  it('未注册（workerId=undefined）时本地拒绝、零请求', async () => {
    const platform = createPlatform(() => jsonResponse(CLAIM_PAYLOAD))
    const tool = toolsFor(platform.fetchImpl, undefined).get('echuan_work_claim')

    const error = await failureOf(runTool(tool!, {}))

    expect(error.message).toContain('worker_id')
    expect(error.message).toContain('worker.poll.enable')
    expect(platform.calls).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// 4. 交报告前的本地校验
// ---------------------------------------------------------------------------

describe('echuan_work_report：material_id 本地校验', () => {
  for (const [label, material] of [
    ['缺失', undefined],
    ['空串', ''],
    ['全空白', '   \n\t  '],
  ] as const) {
    it(`${label} → 本地拒绝、零请求`, async () => {
      const platform = createPlatform(() => jsonResponse({ work_id: WORK_ID }))
      const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_report')

      const args: Record<string, unknown> = { work_id: WORK_ID, lease_id: 'l_1' }
      if (material !== undefined) args.material_id = material
      const error = await failureOf(runTool(tool!, args))

      expect(error.message).toContain('material_id')
      expect(platform.calls).toHaveLength(0)
    })
  }

  it('正常提交：POST /v1/work-items/report（无 query），render 说「工作项已提交待验收」', async () => {
    const platform = createPlatform(() => jsonResponse({ work_id: WORK_ID, state: 'submitted' }))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_report')

    const { value, text } = await runTool(tool!, {
      work_id: WORK_ID,
      material_id: 'M_report_1',
      lease_id: 'l_9f8b7b09ff971dd1',
    })
    expectValidOutput(tool!, value)

    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.url).toBe('http://platform.test/v1/work-items/report')
    expect(parsedBody(platform.calls[0])).toEqual({
      work_id: WORK_ID,
      material_id: 'M_report_1',
      lease_id: 'l_9f8b7b09ff971dd1',
    })
    expect(text).toContain('工作项已提交待验收')
    expect(text).toContain(WORK_ID)
  })
})

// ---------------------------------------------------------------------------
// 4a. breakdown：plan 与 material_id 二选一
// ---------------------------------------------------------------------------

describe('echuan_work_report：breakdown plan 与 material_id 二选一', () => {
  const reportResponse = { work_id: WORK_ID, state: 'submitted' }

  it('只传 plan：POST body 携带 plan，不出现 material_id', async () => {
    const platform = createPlatform(() => jsonResponse(reportResponse))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_report')

    const plan = {
      items: [
        {
          seq_no: 1,
          project_id: 'APP-1',
          title: '实现登录',
          body_md: '# 实现登录\n',
          priority: 10,
          depends_on: [],
        },
        {
          seq_no: 2,
          title: '登录测试',
          body_md: '# 登录测试\n',
          priority: 5,
          depends_on: [1],
        },
      ],
    }
    const { value, text } = await runTool(tool!, {
      work_id: WORK_ID,
      lease_id: 'l_9f8b7b09ff971dd1',
      plan,
    })
    expectValidOutput(tool!, value)

    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.url).toBe('http://platform.test/v1/work-items/report')
    const body = parsedBody(platform.calls[0])
    expect(body).toEqual({
      work_id: WORK_ID,
      lease_id: 'l_9f8b7b09ff971dd1',
      plan,
    })
    expect(body).not.toHaveProperty('material_id')
    expect(text).toContain('工作项已提交待验收')
    expect(text).toContain(WORK_ID)
  })

  it('只传 material_id：body 不出现 plan（非 breakdown 旧路径不变）', async () => {
    const platform = createPlatform(() => jsonResponse(reportResponse))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_report')

    await runTool(tool!, {
      work_id: WORK_ID,
      material_id: 'M_report_1',
      lease_id: 'l_9f8b',
    })

    expect(platform.calls).toHaveLength(1)
    const body = parsedBody(platform.calls[0])
    expect(body).toEqual({ work_id: WORK_ID, material_id: 'M_report_1', lease_id: 'l_9f8b' })
    expect(body).not.toHaveProperty('plan')
  })

  it('material_id 与 plan 都不传 → 本地拒绝、零请求', async () => {
    const platform = createPlatform(() => jsonResponse(reportResponse))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_report')

    const error = await failureOf(runTool(tool!, {
      work_id: WORK_ID,
      lease_id: 'l_9f8b',
    }))

    expect(error.message).toContain('material_id')
    expect(error.message).toContain('plan')
    expect(platform.calls).toHaveLength(0)
  })

  for (const [label, plan, field] of [
    ['plan 缺 items', {}, 'plan.items'],
    ['items 为空', { items: [] }, 'plan.items'],
    ['seq_no 非正整数', { items: [{ seq_no: 0, title: '卡', body_md: '# 卡' }] }, 'plan.items[0].seq_no'],
    ['title 空白', { items: [{ seq_no: 1, title: '   ', body_md: '# 卡' }] }, 'plan.items[0].title'],
    [
      'depends_on 含 0',
      { items: [{ seq_no: 1, title: '卡', body_md: '# 卡', depends_on: [0] }] },
      'plan.items[0].depends_on[0]',
    ],
  ] as const) {
    it(`${label} → 本地拒绝、零请求`, async () => {
      const platform = createPlatform(() => jsonResponse(reportResponse))
      const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_report')

      const error = await failureOf(runTool(tool!, {
        work_id: WORK_ID,
        lease_id: 'l_9f8b',
        plan,
      }))

      expect(error.message).toContain(field)
      expect(platform.calls).toHaveLength(0)
    })
  }
})

// ---------------------------------------------------------------------------
// 4b. 报告成功 → notifyReported
// ---------------------------------------------------------------------------

describe('echuan_work_report：成功后回调 notifyReported', () => {
  it('HTTP 成功 → notifyReported(work_id) 恰好 1 次', async () => {
    const platform = createPlatform(() => jsonResponse({ work_id: 'APP-1-T0001', status_name: '待验收' }))
    const client = createClient(resolveConfig({ baseUrl: 'http://platform.test' }), {
      fetchImpl: platform.fetchImpl,
    })
    const seen: string[] = []
    const tool = createEchuanTools({
      client,
      identity: { workerId: WORKER_ID, notifyReported: workId => seen.push(workId) },
    }).find(item => item.name === 'echuan_work_report')

    await runTool(tool!, {
      work_id: WORK_ID,
      material_id: 'M_report_1',
      lease_id: 'l_9f8b',
    })

    expect(platform.calls).toHaveLength(1)
    expect(seen).toEqual([WORK_ID])
  })

  it('本地校验失败（空 material_id）→ 0 请求、notifyReported 0 次', async () => {
    const platform = createPlatform(() => jsonResponse({}))
    const client = createClient(resolveConfig({ baseUrl: 'http://platform.test' }), {
      fetchImpl: platform.fetchImpl,
    })
    const seen: string[] = []
    const tool = createEchuanTools({
      client,
      identity: { workerId: WORKER_ID, notifyReported: workId => seen.push(workId) },
    }).find(item => item.name === 'echuan_work_report')

    await expect(runTool(tool!, {
      work_id: WORK_ID,
      material_id: '   ',
      lease_id: 'l_9f8b',
    })).rejects.toThrow(/material_id/)

    expect(platform.calls).toHaveLength(0)
    expect(seen).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// 5. 错误 → 明确的「下一步」
// ---------------------------------------------------------------------------

describe('错误映射：每种错误都给出该怎么做', () => {
  it('worker_busy → 先完成当前卡并用 echuan_work_report 汇报', async () => {
    const platform = createPlatform(() => errorResponse(409, 'worker_busy'))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_claim')

    const error = await failureOf(runTool(tool!, {}))

    expect(error.message).toContain('[worker_busy]')
    expect(error.message).toContain('已持卡')
    expect(error.message).toContain('echuan_work_report')
  })

  it('worker_unknown → 等下一次心跳自动重注册后再试', async () => {
    const platform = createPlatform(() => errorResponse(409, 'worker_unknown'))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_claim')

    const error = await failureOf(runTool(tool!, {}))

    expect(error.message).toContain('[worker_unknown]')
    expect(error.message).toContain('心跳')
    expect(error.message).toContain('重新注册')
  })

  it('lease_lost → 停止该卡、旧结果不可再提交、重新 echuan_work_claim', async () => {
    const platform = createPlatform(() => errorResponse(409, 'lease_lost'))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_renew')

    const error = await failureOf(runTool(tool!, {
      work_id: 'APP-1-T0001',
      lease_id: 'l_stale',
    }))

    expect(error.message).toContain('[lease_lost]')
    expect(error.message).toContain('停止执行')
    expect(error.message).toContain('旧结果不可再提交')
    expect(error.message).toContain('echuan_work_claim')
  })

  it('material_required（422）→ 提示补全报告内容', async () => {
    const platform = createPlatform(() => errorResponse(422, 'material_required'))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_report')

    const error = await failureOf(runTool(tool!, {
      work_id: WORK_ID,
      material_id: 'M_missing',
      lease_id: 'l_1',
    }))

    expect(error.message).toContain('[material_required]')
    expect(error.message).toContain('material_id')
    expect(error.message).toContain('echuan_work_report')
  })
})

// ---------------------------------------------------------------------------
// 6. 续租成功
// ---------------------------------------------------------------------------

describe('echuan_work_renew：续租成功', () => {
  it('render 新的 lease_id 与到期时间；走 POST /v1/work-items/lease?id=', async () => {
    const platform = createPlatform(() => jsonResponse({
      lease_id: 'l_newlease0000001',
      until: '2026-10-27T16:51:00.000Z',
      ttl_seconds: 90,
    }))
    const tool = toolsFor(platform.fetchImpl, WORKER_ID).get('echuan_work_renew')

    const { value, text } = await runTool(tool!, {
      work_id: 'APP-1-T0001',
      lease_id: 'l_9f8b7b09ff971dd1',
    })
    expectValidOutput(tool!, value)

    expect(platform.calls[0]!.url).toBe('http://platform.test/v1/work-items/lease')
    expect(parsedBody(platform.calls[0])).toEqual({
      work_id: 'APP-1-T0001',
      lease_id: 'l_9f8b7b09ff971dd1',
    })
    expect(text).toContain('l_newlease0000001')
    expect(text).toContain('2026-10-27T16:51:00.000Z')
  })
})

// ---------------------------------------------------------------------------
// 7. 工具集合与边界
// ---------------------------------------------------------------------------

describe('工具集合：恰好 15 个写工具，无审批类动作', () => {
  const tools = () => {
    const client = createClient(resolveConfig({ baseUrl: 'http://platform.test' }), {
      fetchImpl: createPlatform(() => jsonResponse({})).fetchImpl,
    })
    return createEchuanTools({ client, identity: { workerId: undefined } })
  }

  it('名字集合与 WRITE_TOOL_NAMES 完全一致', () => {
    expect(tools().map(tool => tool.name)).toEqual([...WRITE_TOOL_NAMES])
    expect(WRITE_TOOL_NAMES).toHaveLength(15)
  })

  it('工具定义与 src/tools 源码里都没有 accept / decide / cancel / rework', () => {
    const forbidden = ['accept', 'decide', 'cancel', 'rework']
    const defined = tools()
      .map(tool => `${tool.name} ${tool.description}`)
      .join('\n')
      .toLowerCase()
    for (const word of forbidden) expect(defined).not.toContain(word)

    // 等价于 `grep -rn "accept|decide|cancel|rework" src/tools` 必须无命中。
    for (const file of readdirSync(toolsDir)) {
      // `accepted` / `cancelled` 是契约里的工作项状态枚举，不是门禁动作；先剔除再扫。
      const source = readFileSync(path.join(toolsDir, file), 'utf8')
        .toLowerCase()
        .replaceAll('accepted', '')
        .replaceAll('cancelled', '')
      for (const word of forbidden) {
        expect(source, `${file} 不应出现 ${word}`).not.toContain(word)
      }
    }
  })

  it('TOOL_METHODS 恰好 20 个（工具层没有污染客户端方法集）', () => {
    expect(TOOL_METHODS).toHaveLength(20)
  })
})
