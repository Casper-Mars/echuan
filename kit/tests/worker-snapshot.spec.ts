/**
 * worker 注册表快照契约（卡 1 `4b8cd28` 之后）。
 *
 * 两道门：
 * - 类型级：`WorkerSnapshot` 的键集必须与卡 1 详情响应**逐个对齐**（多一个 / 少一个都编译失败），
 *   见下方两个 `never` 断言；
 * - 运行级：真实 curl 抓下来的详情响应体键集与之相等，且心跳仍是 204 空体
 *   （不返回 worker 对象、不下发指令），请求体必须带 `worker_id`。
 *
 * 期望值出处：`GET /v1/workers?id=w_lease` 在 d006569 上的实测响应（交付证据里贴了 curl 原文）。
 */
import { describe, expect, it } from 'vitest'
import {
  HEARTBEAT_ACCEPTED_MESSAGE,
  createWorkerClient,
  type FetchLike,
  type WorkerSnapshot,
} from '../src/client.js'
import { resolveConfig } from '../src/config.js'

/** 卡 1 详情响应体（原样抄自实测，不做任何裁剪/补字段）。 */
const detailResponse = {
  current_lease_id: 'l_01M86G6NSJ1VFGB7G38KFQDR8J',
  current_task_id: 12,
  current_work_item_id: 't_01M86G6AASGFJ3RTVD6NCGQMYX',
  first_seen: '2026-11-24T10:04:12.460Z',
  heartbeat_interval_ms: 15000,
  kinds: ['dev'],
  last_seen: '2026-11-24T10:05:09.689Z',
  lease_ttl_seconds: 90,
  metadata: { host: 'race-box' },
  presence: 'active',
  state: 'busy',
  worker_id: 'w_lease',
} as const

/** 类型级对齐：详情响应里有、`WorkerSnapshot` 里没有的键（应为 never）。 */
type MissingInSnapshot = Exclude<keyof typeof detailResponse, keyof WorkerSnapshot>
/** 类型级对齐：`WorkerSnapshot` 里有、详情响应里没有的键（应为 never）。 */
type ExtraInSnapshot = Exclude<keyof WorkerSnapshot, keyof typeof detailResponse>

const noMissingField: MissingInSnapshot extends never ? true : never = true
const noExtraField: ExtraInSnapshot extends never ? true : never = true

describe('WorkerSnapshot 与 GET /v1/workers?id= 详情响应字段逐个对齐', () => {
  it('类型级：两个方向都没有多余键（编译期断言）', () => {
    expect(noMissingField).toBe(true)
    expect(noExtraField).toBe(true)
  })

  it('运行级：实测详情响应恰好 12 个字段，且都在类型里', () => {
    const keys = Object.keys(detailResponse).sort()
    expect(keys).toEqual([
      'current_lease_id',
      'current_task_id',
      'current_work_item_id',
      'first_seen',
      'heartbeat_interval_ms',
      'kinds',
      'last_seen',
      'lease_ttl_seconds',
      'metadata',
      'presence',
      'state',
      'worker_id',
    ])
  })

  it('详情里的字段口径：presence 只有 active / offline，指针字段可显式 null', () => {
    const idle: WorkerSnapshot = {
      ...detailResponse,
      current_lease_id: null,
      current_task_id: null,
      current_work_item_id: null,
      presence: 'offline',
      state: 'idle',
    }
    expect(idle.presence).toBe('offline')
    expect(idle.current_work_item_id).toBeNull()
    expect(detailResponse.presence).toBe('active')
    expect(detailResponse.heartbeat_interval_ms).toBe(15000)
    expect(detailResponse.lease_ttl_seconds).toBe(90)
  })
})

describe('心跳：204 空体 + worker_id 必填', () => {
  const config = resolveConfig({ baseUrl: 'http://127.0.0.1:8720' })

  it('204 无回包 → accepted 结果，不猜 worker 对象', async () => {
    const calls: Array<{ url: string; body: string }> = []
    const fetchImpl: FetchLike = async (input, init) => {
      calls.push({ url: String(input), body: String(init?.body ?? '') })
      return new Response(null, { status: 204, headers: { 'x-request-id': 'req-hb' } })
    }
    const client = createWorkerClient(config, { fetchImpl })

    const result = await client.heartbeat({
      worker_id: 'w_lease',
      state: 'busy',
      kinds: ['dev'],
      metadata: { host: 'race-box' },
    })

    expect(result.kind).toBe('accepted')
    expect(result.status).toBe(204)
    expect(result.requestId).toBe('req-hb')
    expect(result.message).toBe(HEARTBEAT_ACCEPTED_MESSAGE)
    expect(result.message).toContain('HTTP 204')
    expect(calls).toHaveLength(1)
    expect(calls[0]!.url).toBe('http://127.0.0.1:8720/v1/workers/heartbeat')
    expect(JSON.parse(calls[0]!.body)).toEqual({
      worker_id: 'w_lease',
      state: 'busy',
      kinds: ['dev'],
      metadata: { host: 'race-box' },
    })
  })

  it('worker_id 为空在本地拒绝（服务端同口径：400 worker_id is required）', async () => {
    const fetchImpl: FetchLike = async () => {
      throw new Error('不该发请求')
    }
    const client = createWorkerClient(config, { fetchImpl })

    await expect(
      client.heartbeat({ worker_id: '  ', state: 'idle', kinds: ['dev'], metadata: {} }),
    ).rejects.toThrow(/worker_id/)
  })
})
