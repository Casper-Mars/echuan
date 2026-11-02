/**
 * Worker 心跳循环单测（v2 派单协议）。
 *
 * 全部使用假平台（注入 fetch）+ 假时钟（注入 setTimeout 链）：
 * 不连真实平台、不做真实 sleep。
 *
 * 覆盖：
 * - 首轮心跳延迟 5s、首次注册字段、kinds 上报、间隔重排；
 * - dispatch 直接投给 `agents.roots()[0]`（不数数量、不建项目会话、无 agent 时跳过）；
 * - state 与 inflight（workId / kind / leaseUntil）、current_work_item_id；
 * - 409 worker_unknown 退避、网络失败不崩、poll 关闭 / kinds 为空 / metadata 超限；
 * - `work_id + lease_id` 去重与有界 LRU。
 */
import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Agent } from '@deepseek-ai/dsh-agent'
import { describe, expect, it, vi } from 'vitest'
import {
  TOOL_METHODS,
  createWorkerClient,
  type FetchLike,
  type WorkerHeartbeatPayload,
} from '../src/client.js'
import { WORKER_METADATA_MAX_BYTES, metadataByteLength, resolveConfig } from '../src/config.js'
import type { Logger } from '../src/runtime.js'
import {
  DEFAULT_HEARTBEAT_INTERVAL_MS,
  FIRST_HEARTBEAT_DELAY_MS,
  MAX_HEARTBEAT_INTERVAL_MS,
  MAX_REMEMBERED_DELIVERIES,
  MIN_HEARTBEAT_INTERVAL_MS,
  createWorkerLoop,
  maskWorkerId,
  renderDispatchMessage,
  type WorkerLoop,
  type WorkerLoopOptions,
} from '../src/worker.js'

const here = path.dirname(fileURLToPath(import.meta.url))

// ---------------------------------------------------------------------------
// 假平台与假时钟
// ---------------------------------------------------------------------------

type Handler = (body: Record<string, unknown>, call: number) => Response | Promise<Response>

interface RecordedCall {
  url: string
  body: Record<string, unknown>
  init: RequestInit
}

function createPlatform(handler: Handler): { calls: RecordedCall[]; fetchImpl: FetchLike } {
  const calls: RecordedCall[] = []
  const fetchImpl: FetchLike = async (url, init = {}) => {
    const body: Record<string, unknown> = init.body === undefined
      ? {}
      : (JSON.parse(String(init.body)) as Record<string, unknown>)
    calls.push({ url, body, init })
    return handler(body, calls.length - 1)
  }
  return { calls, fetchImpl }
}

function okResponse(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

function errorResponse(status: number, code: string): Response {
  return new Response(
    JSON.stringify({ code, message: `platform says ${code}`, details: [], request_id: `req-${code}` }),
    { status, headers: { 'content-type': 'application/json' } },
  )
}

function heartbeatPayload(
  workerId: string,
  commands: unknown[] = [],
  heartbeatIntervalMs?: number,
): WorkerHeartbeatPayload {
  return {
    worker: {
      worker_id: workerId,
      ...(heartbeatIntervalMs === undefined ? {} : { heartbeat_interval_ms: heartbeatIntervalMs }),
    },
    commands: commands as WorkerHeartbeatPayload['commands'],
  }
}

interface ScheduledTask {
  at: number
  fn: () => void
}

/** 假时钟：只接管心跳循环的 setTimeout 链，可手动推进。 */
function createScheduler(): {
  api: NonNullable<WorkerLoopOptions['timer']>
  pending: ScheduledTask[]
  count: number
  now: number
  advance(ms: number): Promise<void>
} {
  let now = 0
  let seq = 0
  const tasks = new Map<number, ScheduledTask>()

  return {
    api: {
      setTimeout(handler, ms) {
        const id = ++seq
        tasks.set(id, { at: now + ms, fn: handler })
        return id
      },
      clearTimeout(handle) {
        tasks.delete(Number(handle))
      },
    },
    get pending() {
      return [...tasks.values()].sort((left, right) => left.at - right.at)
    },
    get count() {
      return tasks.size
    },
    get now() {
      return now
    },
    async advance(ms) {
      now += ms
      // 只触发推进前已到期的任务；回调里新排的任务留到下一次 advance，
      // 这样 409 的「立即重排」也能被逐步断言。
      const due = [...tasks.entries()]
        .filter(([, scheduled]) => scheduled.at <= now)
        .sort((left, right) => left[1].at - right[1].at)
      for (const [id, scheduled] of due) {
        if (!tasks.has(id)) continue
        tasks.delete(id)
        scheduled.fn()
      }
      await drain()
    },
  }
}

/** 排空微任务队列，等一轮异步心跳结束。 */
async function drain(times = 3): Promise<void> {
  for (let index = 0; index < times; index += 1) {
    await new Promise<void>(resolve => {
      setImmediate(resolve)
    })
  }
}

/** 启动循环并推进过首轮心跳延迟。 */
async function startAndBeat(
  loop: WorkerLoop,
  scheduler: ReturnType<typeof createScheduler>,
): Promise<void> {
  loop.start()
  await drain()
  await scheduler.advance(FIRST_HEARTBEAT_DELAY_MS)
}

function buildAgent(status: 'idle' | 'running' = 'idle', id = 'agent-root'): {
  agent: Agent
  followup: ReturnType<typeof vi.fn>
  setStatus(next: 'idle' | 'running'): void
} {
  const followup = vi.fn()
  const agent = { id, status, followup } as unknown as Agent
  return {
    agent,
    followup,
    setStatus(next) {
      (agent as unknown as { status: string }).status = next
    },
  }
}

interface LoopHarness {
  config: ReturnType<typeof resolveConfig>
  platform: ReturnType<typeof createPlatform>
  scheduler: ReturnType<typeof createScheduler>
  lines: string[]
  loop: WorkerLoop
}

function setup(
  handler: Handler,
  options: {
    enable?: boolean
    kinds?: readonly ('requirement' | 'design' | 'breakdown' | 'dev')[]
    metadata?: Record<string, unknown>
    /** 本进程的 root agents；派单固定投给第一个。 */
    agents?: readonly Agent[]
    now?: () => number
    deliver?: WorkerLoopOptions['deliver']
  } = {},
): LoopHarness {
  const metadata = options.metadata ?? { host: 'build-01', os: 'linux' }
  const config = resolveConfig({
    worker: {
      poll: { enable: options.enable ?? true },
      kinds: [...(options.kinds ?? ['dev'])],
      metadata,
    },
  })
  const platform = createPlatform(handler)
  const client = createWorkerClient(config, { fetchImpl: platform.fetchImpl })
  const scheduler = createScheduler()
  const lines: string[] = []
  const logger: Logger = { info: message => lines.push(message), warn: message => lines.push(message) }
  const roots = [...(options.agents ?? [])]
  const loop = createWorkerLoop({
    config,
    client,
    agents: { roots: () => roots },
    logger,
    timer: scheduler.api,
    ...(options.now === undefined ? {} : { now: options.now }),
    ...(options.deliver === undefined ? {} : { deliver: options.deliver }),
  })
  return { config, platform, scheduler, lines, loop }
}

// ---------------------------------------------------------------------------
// 测试数据
// ---------------------------------------------------------------------------

const WORKER_ID = 'w_TEST01ABCDEFGHIJKLMN'
const WORK_ID = 'w_01M5Z4ABCDEF'
const LEASE_UNTIL = '2026-10-27T16:49:25.157Z'

const DISPATCH = {
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
    until: LEASE_UNTIL,
    ttl_seconds: 90,
  },
  context: {
    spec_version: 1,
    spec_digest: 'sha256:bb23da360a3b21e827138bad027ef3f30a29699e84014bc520e4f40131ea2d76',
    product: { id: 'APP', name: '驿传示例产品', git_url: 'git@example.com:APP.git' },
  },
}

function dispatchCommand(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return { kind: 'dispatch', dispatch: { ...DISPATCH, ...overrides } }
}

// ---------------------------------------------------------------------------
// 1. 注册与心跳字段
// ---------------------------------------------------------------------------

describe('首次注册与后续心跳', () => {
  it('常量 FIRST_HEARTBEAT_DELAY_MS 钉死为 5000ms', () => {
    expect(FIRST_HEARTBEAT_DELAY_MS).toBe(5_000)
  })

  it('字面时序契约：4999ms 零请求、第 5000ms 恰好发出 1 次', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)))
    harness.loop.start()
    await drain()
    await harness.scheduler.advance(4_999)
    expect(harness.platform.calls).toHaveLength(0)

    await harness.scheduler.advance(1)
    expect(harness.platform.calls).toHaveLength(1)
  })

  it('首次请求体没有 worker_id；kinds 与配置一致；第二次带同一 id', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)), {
      kinds: ['dev', 'design'],
    })
    await startAndBeat(harness.loop, harness.scheduler)
    await harness.scheduler.advance(15_000)

    expect(harness.platform.calls).toHaveLength(2)
    const first = harness.platform.calls[0]!.body
    expect('worker_id' in first).toBe(false)
    expect(first['state']).toBe('idle')
    expect(first['kinds']).toEqual(['dev', 'design'])
    expect(first['metadata']).toEqual({ host: 'build-01', os: 'linux' })

    expect(harness.platform.calls[1]!.body['worker_id']).toBe(WORKER_ID)
    expect(harness.platform.calls[1]!.body['kinds']).toEqual(['dev', 'design'])
  })

  it('metadata 原样上报（深等于配置）', async () => {
    const metadata = { host: 'mac-01', nested: { a: [1, 2, 3] } }
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)), { metadata })
    await startAndBeat(harness.loop, harness.scheduler)

    expect(harness.platform.calls[0]!.body['metadata']).toEqual(metadata)
  })

  it('日志里的 worker id 打码，不出现明文', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)))
    await startAndBeat(harness.loop, harness.scheduler)

    const log = harness.lines.join('\n')
    expect(log).toContain(maskWorkerId(WORKER_ID))
    expect(log).not.toContain(WORKER_ID)
    expect(maskWorkerId(WORKER_ID)).toBe('w_TE****')
  })
})

// ---------------------------------------------------------------------------
// 2. 心跳间隔
// ---------------------------------------------------------------------------

describe('心跳间隔', () => {
  it('回包 heartbeat_interval_ms=5000 → 下一轮在 5000ms 后', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)))
    await startAndBeat(harness.loop, harness.scheduler)
    expect(harness.scheduler.pending.map(item => item.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + 5_000,
    ])
  })

  it('回包缺失 heartbeat_interval_ms → 用 15000ms', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)))
    await startAndBeat(harness.loop, harness.scheduler)
    expect(harness.scheduler.pending.map(item => item.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + DEFAULT_HEARTBEAT_INTERVAL_MS,
    ])
  })

  it('回 1ms → 夹紧到 500ms；回超大值 → 夹紧到 24h', async () => {
    const low = setup(() => okResponse(heartbeatPayload(WORKER_ID, [], 1)))
    await startAndBeat(low.loop, low.scheduler)
    expect(low.scheduler.pending.map(item => item.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + MIN_HEARTBEAT_INTERVAL_MS,
    ])

    const high = setup(() => okResponse(heartbeatPayload(WORKER_ID, [], 999_999_999)))
    await startAndBeat(high.loop, high.scheduler)
    expect(high.scheduler.pending.map(item => item.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + MAX_HEARTBEAT_INTERVAL_MS,
    ])
  })
})

// ---------------------------------------------------------------------------
// 3. dispatch 投递
// ---------------------------------------------------------------------------

describe('dispatch 投递', () => {
  it('派单文案含 work_id / kind / 标题 / 租约 / inputs / 正文，不含操作指示', async () => {
    const agent = buildAgent('idle')
    const delivered: string[] = []
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: [agent.agent], deliver: (_agent, text) => delivered.push(text) },
    )
    await startAndBeat(harness.loop, harness.scheduler)

    expect(delivered).toHaveLength(1)
    const text = delivered[0]!
    expect(text).toContain(`工作项编号：${WORK_ID}`)
    expect(text).toContain('类别：dev（开发）')
    expect(text).toContain('标题：实现登录接口')
    expect(text).toContain(`租约到期：${LEASE_UNTIL}`)
    expect(text).toContain('material_id=M_spec_1')
    expect(text).toContain('请实现登录接口。')
    expect(text).not.toContain('工作目录')
    expect(text).not.toContain('echuan_work_report')
    expect(text).not.toContain('下一步')
  })

  it('未注入 deliver 时走 agent.followup，只投 1 次', async () => {
    const agent = buildAgent('idle')
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: [agent.agent] },
    )
    await startAndBeat(harness.loop, harness.scheduler)

    expect(agent.followup).toHaveBeenCalledTimes(1)
  })

  it('多个 root：仍然投给 roots()[0]，且只投一次（不因 >1 丢弃）', async () => {
    const first = buildAgent('idle', 'agent-first')
    const second = buildAgent('running', 'agent-second')
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: [first.agent, second.agent] },
    )
    await startAndBeat(harness.loop, harness.scheduler)

    expect(first.followup).toHaveBeenCalledTimes(1)
    expect(second.followup).not.toHaveBeenCalled()
  })

  it('renderDispatchMessage 首行严格是【驿传派单】，仓库一行等于 git_url', () => {
    const text = renderDispatchMessage(DISPATCH as never)
    const lines = text.split('\n')
    expect(lines[0]).toBe('【驿传派单】')
    expect(lines).toContain(`仓库：${DISPATCH.context.product.git_url}`)
  })

  it('无可用 agent：warn 一条、本轮跳过、无 inflight；下一轮有 agent 就投', async () => {
    const roots: Agent[] = []
    const delivered: string[] = []
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: roots, deliver: (_agent, text) => delivered.push(text) },
    )
    // 手工构造一个共享数组：循环每次读 roots()，因此可动态加入。
    const loop = createWorkerLoop({
      config: harness.config,
      client: createWorkerClient(harness.config, { fetchImpl: harness.platform.fetchImpl }),
      agents: { roots: () => roots },
      logger: { info: message => harness.lines.push(message), warn: message => harness.lines.push(message) },
      timer: harness.scheduler.api,
      deliver: (_agent, text) => delivered.push(text),
    })
    const scheduler = harness.scheduler
    loop.start()
    await drain()
    await scheduler.advance(FIRST_HEARTBEAT_DELAY_MS)

    expect(delivered).toHaveLength(0)
    expect(loop.inflight).toHaveLength(0)
    expect(harness.lines.some(line => line.includes('没有可用 agent'))).toBe(true)

    // 下一轮前补上 agent：同一张派单这次必须投出去。
    const agent = buildAgent('idle')
    roots.push(agent.agent)
    await scheduler.advance(5_000)
    expect(delivered).toHaveLength(1)
    loop.dispose('test')
  })

  it('deliver 抛错：warn + 不写 inflight + 不记去重（下一轮重投）', async () => {
    const agent = buildAgent('idle')
    let attempts = 0
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      {
        agents: [agent.agent],
        deliver: () => {
          attempts += 1
          throw new Error('followup boom')
        },
      },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    expect(attempts).toBe(1)
    expect(harness.loop.inflight).toHaveLength(0)
    expect(harness.lines.some(line => line.includes('投递失败'))).toBe(true)

    await harness.scheduler.advance(5_000)
    expect(attempts).toBe(2)
  })

  it('未知 command kind 原样忽略', async () => {
    const agent = buildAgent('idle')
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [{ kind: 'noop' }], 5_000)),
      { agents: [agent.agent] },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    expect(agent.followup).not.toHaveBeenCalled()
  })
})

// ---------------------------------------------------------------------------
// 4. state 只看「手里有没有卡」
// ---------------------------------------------------------------------------

describe('state 只看「手里有没有卡」', () => {
  it('无 inflight → idle（即使存在 running agent）', async () => {
    const agent = buildAgent('running')
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)), { agents: [agent.agent] })
    await startAndBeat(harness.loop, harness.scheduler)
    expect(harness.platform.calls[0]!.body['state']).toBe('idle')
  })

  it('投递后 inflight → 下一轮 busy；agent 状态抖动不影响', async () => {
    const agent = buildAgent('idle')
    const until = Date.parse(LEASE_UNTIL)
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: [agent.agent], now: () => until - 60_000 },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    expect(harness.platform.calls[0]!.body['state']).toBe('idle')

    agent.setStatus('running')
    await harness.scheduler.advance(5_000)
    expect(harness.platform.calls[1]!.body['state']).toBe('busy')

    agent.setStatus('idle')
    await harness.scheduler.advance(5_000)
    expect(harness.platform.calls[2]!.body['state']).toBe('busy')
  })

  it('回包 current_work_item_id 非空（本地无 inflight）→ 下一轮 busy；置空后回到 idle', async () => {
    const harness = setup((body, call) => {
      const commands = call === 0 ? [] : []
      return okResponse({
        worker: {
          worker_id: WORKER_ID,
          heartbeat_interval_ms: 5_000,
          ...(call === 0 ? { current_work_item_id: WORK_ID } : {}),
        },
        commands,
      } as WorkerHeartbeatPayload)
    })
    await startAndBeat(harness.loop, harness.scheduler)
    await harness.scheduler.advance(5_000)
    expect(harness.platform.calls[1]!.body['state']).toBe('busy')
    await harness.scheduler.advance(5_000)
    expect(harness.platform.calls[2]!.body['state']).toBe('idle')
  })

  it('notifyReported 之后回到 idle', async () => {
    const agent = buildAgent('running')
    const until = Date.parse(LEASE_UNTIL)
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: [agent.agent], now: () => until - 60_000 },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    harness.loop.notifyReported(WORK_ID)

    await harness.scheduler.advance(5_000)
    expect(harness.platform.calls[1]!.body['state']).toBe('idle')
  })
})

// ---------------------------------------------------------------------------
// 5. inflight 生命周期
// ---------------------------------------------------------------------------

describe('inflight 生命周期', () => {
  it('投递成功写入 workId / kind / leaseUntil', async () => {
    const agent = buildAgent('idle')
    const until = Date.parse(LEASE_UNTIL)
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: [agent.agent], now: () => until - 60_000 },
    )
    await startAndBeat(harness.loop, harness.scheduler)

    expect(harness.loop.inflight).toEqual([
      { workId: WORK_ID, kind: 'dev', leaseUntil: LEASE_UNTIL },
    ])
  })

  it('notifyReported 清除对应工作项；未知 workId 静默忽略', async () => {
    const agent = buildAgent('idle')
    const until = Date.parse(LEASE_UNTIL)
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: [agent.agent], now: () => until - 60_000 },
    )
    await startAndBeat(harness.loop, harness.scheduler)

    harness.loop.notifyReported('w_unknown')
    expect(harness.loop.inflight).toHaveLength(1)
    harness.loop.notifyReported(WORK_ID)
    expect(harness.loop.inflight).toHaveLength(0)
  })

  it('租约过期：下一轮开始前清除并记一条日志', async () => {
    const agent = buildAgent('idle')
    const until = Date.parse(LEASE_UNTIL)
    let now = until - 60_000
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: [agent.agent], now: () => now },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    expect(harness.loop.inflight).toHaveLength(1)

    now = until + 1_000
    await harness.scheduler.advance(5_000)
    expect(harness.loop.inflight).toHaveLength(0)
    expect(harness.lines.some(line => line.includes('租约已过期'))).toBe(true)
  })

  it('同一 work_id 新 lease：覆盖旧 leaseUntil，不重复计数', async () => {
    const agent = buildAgent('idle')
    const until = Date.parse(LEASE_UNTIL)
    const second = { ...DISPATCH.lease, lease_id: 'l_second', until: '2026-10-27T17:10:00.000Z' }
    const harness = setup(
      (body, call) => okResponse(heartbeatPayload(
        WORKER_ID,
        [dispatchCommand(call === 0 ? {} : { lease: second })],
        5_000,
      )),
      { agents: [agent.agent], now: () => until - 60_000 },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    await harness.scheduler.advance(5_000)

    expect(harness.loop.inflight).toEqual([
      { workId: WORK_ID, kind: 'dev', leaseUntil: second.until },
    ])
  })
})

// ---------------------------------------------------------------------------
// 6. 409 worker_unknown
// ---------------------------------------------------------------------------

describe('409 worker_unknown', () => {
  it('清空 id → 立即重排 → 下一轮无 worker_id → 再下一轮带新 id', async () => {
    const harness = setup((body, call) => (
      call === 0
        ? okResponse(heartbeatPayload(WORKER_ID, [], 5_000))
        : call === 1
          ? errorResponse(409, 'worker_unknown')
          : okResponse(heartbeatPayload(WORKER_ID, [], 5_000))
    ))
    await startAndBeat(harness.loop, harness.scheduler)
    await harness.scheduler.advance(5_000)
    expect(harness.platform.calls[1]!.body['worker_id']).toBe(WORKER_ID)

    // 409 后立即重排（0ms）。
    await harness.scheduler.advance(0)
    expect(harness.platform.calls).toHaveLength(3)
    expect('worker_id' in harness.platform.calls[2]!.body).toBe(false)
  })

  it('持续 409 按 1s→2s→5s→15s 退避，不忙等', async () => {
    const harness = setup(() => errorResponse(409, 'worker_unknown'))
    await startAndBeat(harness.loop, harness.scheduler)
    await harness.scheduler.advance(0)
    expect(harness.platform.calls).toHaveLength(2)

    await harness.scheduler.advance(999)
    expect(harness.platform.calls).toHaveLength(2)
    await harness.scheduler.advance(1)
    expect(harness.platform.calls).toHaveLength(3)

    await harness.scheduler.advance(1_999)
    expect(harness.platform.calls).toHaveLength(3)
    await harness.scheduler.advance(1)
    expect(harness.platform.calls).toHaveLength(4)
  })
})

// ---------------------------------------------------------------------------
// 7. 单轮失败不崩溃
// ---------------------------------------------------------------------------

describe('单轮失败不崩溃', () => {
  it('网络失败：warn、保留 id、按当前间隔继续', async () => {
    const harness = setup((body, call) => {
      if (call === 0) return okResponse(heartbeatPayload(WORKER_ID, [], 5_000))
      throw new Error('boom')
    })
    await startAndBeat(harness.loop, harness.scheduler)
    harness.loop.start()
    await harness.scheduler.advance(5_000)

    expect(harness.platform.calls).toHaveLength(2)
    expect(harness.lines.some(line => line.includes('心跳失败'))).toBe(true)
    expect(harness.loop.workerId).toBe(WORKER_ID)
  })
})

// ---------------------------------------------------------------------------
// 8. 开关与卸载
// ---------------------------------------------------------------------------

describe('开关与卸载', () => {
  it('poll.enable=false：零请求、零定时器', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)), { enable: false })
    harness.loop.start()
    await drain()
    expect(harness.platform.calls).toHaveLength(0)
    expect(harness.scheduler.count).toBe(0)
    expect(harness.lines.some(line => line.includes('worker.poll.enable=false'))).toBe(true)
  })

  it('kinds 为空：warn 一条、零请求、零定时器（不每轮 400）', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)), { kinds: [] })
    harness.loop.start()
    await drain()
    expect(harness.platform.calls).toHaveLength(0)
    expect(harness.scheduler.count).toBe(0)
    expect(harness.lines.some(line => line.includes('worker.kinds 为空'))).toBe(true)
  })

  it('metadata 超 8KiB：零请求、零定时器，warn 含实际字节数与上限', async () => {
    const metadata = { blob: 'x'.repeat(WORKER_METADATA_MAX_BYTES) }
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)), { metadata })
    harness.loop.start()
    await drain()

    expect(harness.platform.calls).toHaveLength(0)
    expect(harness.scheduler.count).toBe(0)
    expect(harness.loop.metadataOverLimit).toBe(true)
    const warn = harness.lines.find(line => line.includes('metadata'))!
    expect(warn).toContain(String(metadataByteLength(metadata)))
    expect(warn).toContain(String(WORKER_METADATA_MAX_BYTES))
  })

  it('metadata 恰好等于上限（8192B）时仍正常启动（边界不误伤）', async () => {
    const overhead = metadataByteLength({ blob: '' })
    const metadata = { blob: 'x'.repeat(WORKER_METADATA_MAX_BYTES - overhead) }
    expect(metadataByteLength(metadata)).toBe(WORKER_METADATA_MAX_BYTES)

    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)), { metadata })
    harness.loop.start()
    await drain()
    expect(harness.loop.metadataOverLimit).toBe(false)
    expect(harness.scheduler.count).toBe(1)
  })

  it('运行中 dispose：定时器立即清除，推进假时钟不再有请求与日志', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)))
    await startAndBeat(harness.loop, harness.scheduler)
    const before = harness.platform.calls.length
    const linesBefore = harness.lines.length

    harness.loop.dispose('test')
    expect(harness.scheduler.count).toBe(0)
    const linesAfterDispose = harness.lines.length
    expect(linesAfterDispose).toBeGreaterThanOrEqual(linesBefore)
    await harness.scheduler.advance(60_000)
    expect(harness.platform.calls).toHaveLength(before)
    expect(harness.lines).toHaveLength(linesAfterDispose)
  })

  it('首轮延迟内 dispose：连首轮心跳也不发（零请求）', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)))
    harness.loop.start()
    await drain()
    harness.loop.dispose('test')
    await harness.scheduler.advance(FIRST_HEARTBEAT_DELAY_MS)
    expect(harness.platform.calls).toHaveLength(0)
  })

  it('dispose 幂等，重复调用不再记日志；dispose 后 start() 不再建定时器', async () => {
    const harness = setup(() => okResponse(heartbeatPayload(WORKER_ID)))
    harness.loop.dispose('first')
    const lines = harness.lines.length
    harness.loop.dispose('second')
    expect(harness.lines).toHaveLength(lines)
    harness.loop.start()
    await drain()
    expect(harness.scheduler.count).toBe(0)
  })
})

// ---------------------------------------------------------------------------
// 9. 客户端与源码约束
// ---------------------------------------------------------------------------

describe('客户端与源码约束', () => {
  it('worker 心跳走独立客户端，TOOL_METHODS 恰好 20 个', () => {
    expect(TOOL_METHODS).toHaveLength(20)
  })

  it('kit/src 不含任何磁盘写入调用', () => {
    const srcDir = path.resolve(here, '..', 'src')
    const files: string[] = []
    const walk = (dir: string): void => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name)
        if (entry.isDirectory()) walk(full)
        else if (entry.isFile() && entry.name.endsWith('.ts')) files.push(full)
      }
    }
    walk(srcDir)
    for (const file of files) {
      // 资料包下载是唯一允许落盘的出口（tmpdir），其余源码一律不写盘。
      if (path.basename(file) === 'package.ts') continue
      const source = readFileSync(file, 'utf8')
      expect(source, `${file} 不应写盘`).not.toMatch(/writeFile|appendFile|createWriteStream/)
    }
  })

  it("kit/src 不再出现 V3 的 `kind: 'plugin'` 来源写法", () => {
    const workerSource = readFileSync(path.resolve(here, '..', 'src', 'worker.ts'), 'utf8')
    expect(workerSource).toContain("'plugin:echuan'")
    expect(workerSource).not.toMatch(/kind:\s*'plugin'/)
  })
})

// ---------------------------------------------------------------------------
// 10. S-01 去重
// ---------------------------------------------------------------------------

describe('S-01 重复派单去重', () => {
  it('同一 (work_id, lease_id) 连续多轮下发 → followup 只 1 次', async () => {
    const agent = buildAgent('idle')
    const until = Date.parse(LEASE_UNTIL)
    const harness = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [dispatchCommand()], 5_000)),
      { agents: [agent.agent], now: () => until - 60_000 },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    await harness.scheduler.advance(5_000)
    await harness.scheduler.advance(5_000)
    expect(agent.followup).toHaveBeenCalledTimes(1)
  })

  it('同一轮里重复下发同一条 → 只投 1 次', async () => {
    const agent = buildAgent('idle')
    const harness = setup(
      () => okResponse(heartbeatPayload(
        WORKER_ID,
        [dispatchCommand(), dispatchCommand()],
        5_000,
      )),
      { agents: [agent.agent] },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    expect(agent.followup).toHaveBeenCalledTimes(1)
  })

  it('同 work_id 不同 lease_id → 两次都投递', async () => {
    const agent = buildAgent('idle')
    const until = Date.parse(LEASE_UNTIL)
    const second = { ...DISPATCH.lease, lease_id: 'l_second' }
    const harness = setup(
      (body, call) => okResponse(heartbeatPayload(
        WORKER_ID,
        [dispatchCommand(call === 0 ? {} : { lease: second })],
        5_000,
      )),
      { agents: [agent.agent], now: () => until - 60_000 },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    await harness.scheduler.advance(5_000)
    expect(agent.followup).toHaveBeenCalledTimes(2)
  })

  it('去重表是有界 LRU（容量 200）：超出后最早的键被淘汰可重投', async () => {
    const agent = buildAgent('idle')
    let call = 0
    const until = Date.parse(LEASE_UNTIL)
    const harness = setup(
      () => {
        // 先下发 201 条不同 work_id，再回到第 1 条。
        const index = call < 201 ? call : 0
        call += 1
        const dispatch = {
          ...DISPATCH,
          work_id: `w_bulk_${String(index).padStart(4, '0')}`,
        }
        return okResponse(heartbeatPayload(WORKER_ID, [{ kind: 'dispatch', dispatch }], 5_000))
      },
      { agents: [agent.agent], now: () => until - 60_000 },
    )
    await startAndBeat(harness.loop, harness.scheduler)
    for (let index = 0; index < 200; index += 1) {
      await harness.scheduler.advance(5_000)
    }
    // 201 次投递（0..200）。
    expect(agent.followup).toHaveBeenCalledTimes(201)
    // 再回到第 1 条：已超出容量 200，最早键被淘汰 → 重新投递。
    await harness.scheduler.advance(5_000)
    expect(agent.followup).toHaveBeenCalledTimes(202)
    expect(MAX_REMEMBERED_DELIVERIES).toBe(200)
  })
})
