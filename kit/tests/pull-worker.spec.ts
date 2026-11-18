import { describe, expect, it, vi } from 'vitest'
import {
  createPullWorkerLoop,
  renderPullDispatch,
  type PullClaimEnvelope,
} from '../src/pull-worker.js'
import { resolveConfig } from '../src/config.js'

const envelope: PullClaimEnvelope = {
  task: {
    task_id: 't_1',
    project_id: 'APP-1',
    kind: 'dev',
    title: '实现接口',
    description_md: '实现 GET /x',
    context_md: '复用现有 handler',
    priority: 0,
    status: 'in_progress',
    attempt: 1,
    max_attempts: 3,
    lease_id: 'l_1',
    lease_until: '2099-01-01T00:00:00.000Z',
    create_time: '2026-11-18T00:00:00.000Z',
    update_time: '2026-11-18T00:00:00.000Z',
  },
  project: { project_id: 'APP-1', name: '登录改造', context_md: 'issue #12' },
  product: { product_id: 'APP', name: '驿传', git_url: 'git@example.com:app.git' },
  inputs: [{ material_id: 'm_req', title: '需求', kind: 'requirement', role: 'input' }],
  upstream: [{ task_id: 't_req', material_id: 'm_req', title: '需求' }],
  has_downstream_dev: false,
}

function config() {
  return resolveConfig({
    baseUrl: 'http://127.0.0.1:8721',
    worker: { poll: { enable: true }, presetByKind: { dev: 'dev-leader' } },
  })
}

describe('renderPullDispatch', () => {
  it('包含任务、项目、产品、租约、上游与输入', () => {
    const text = renderPullDispatch(envelope)
    expect(text).toContain('任务编号：t_1')
    expect(text).toContain('项目：APP-1（登录改造）')
    expect(text).toContain('仓库：git@example.com:app.git')
    expect(text).toContain('lease_id=l_1')
    expect(text).toContain('issue #12')
    expect(text).toContain('material_id=m_req')
    expect(text).toContain('最后一条 dev')
  })
})

describe('createPullWorkerLoop', () => {
  it('心跳 → 按 kind 领卡 → 投递 → inflight；报告后清空再领', async () => {
    const heartbeatV3 = vi.fn().mockResolvedValue({ kind: 'empty' })
    const claimV3Task = vi
      .fn()
      .mockResolvedValueOnce({ kind: 'ok', data: envelope })
      .mockResolvedValue({ kind: 'empty' })
    const deliver = vi.fn()
    const followup = vi.fn()
    const agent = { followup } as any
    const ensureSession = vi.fn().mockResolvedValue(agent)

    const loop = createPullWorkerLoop({
      config: config(),
      client: { heartbeatV3, claimV3Task } as any,
      ensureSession,
      deliver,
      logger: { info: () => {}, warn: () => {} },
    })

    await loop.pollOnce()
    expect(heartbeatV3).toHaveBeenLastCalledWith(
      expect.objectContaining({ state: 'idle', kinds: ['dev'] }),
    )
    expect(claimV3Task).toHaveBeenCalledWith({ worker_id: loop.workerId, kind: 'dev' })
    expect(ensureSession).toHaveBeenCalledWith('APP-1', 'dev-leader')
    expect(deliver).toHaveBeenCalledWith(agent, expect.stringContaining('任务编号：t_1'))
    expect(loop.inflight).toHaveLength(1)

    // Busy: no second claim while inflight.
    await loop.pollOnce()
    expect(heartbeatV3).toHaveBeenLastCalledWith(expect.objectContaining({ state: 'busy' }))
    expect(claimV3Task).toHaveBeenCalledTimes(1)

    loop.notifyReported('t_1')
    expect(loop.inflight).toHaveLength(0)
    await loop.pollOnce()
    expect(claimV3Task).toHaveBeenCalledTimes(2)
  })

  it('所有 kind 都 204 时不投递', async () => {
    const deliver = vi.fn()
    const loop = createPullWorkerLoop({
      config: config(),
      client: {
        heartbeatV3: vi.fn().mockResolvedValue({ kind: 'empty' }),
        claimV3Task: vi.fn().mockResolvedValue({ kind: 'empty' }),
      } as any,
      ensureSession: vi.fn(),
      deliver,
      logger: { info: () => {}, warn: () => {} },
    })
    await loop.pollOnce()
    expect(deliver).not.toHaveBeenCalled()
    expect(loop.inflight).toHaveLength(0)
  })

  it('会话未就绪时不投递，也不写 inflight', async () => {
    const deliver = vi.fn()
    const loop = createPullWorkerLoop({
      config: config(),
      client: {
        heartbeatV3: vi.fn().mockResolvedValue({ kind: 'empty' }),
        claimV3Task: vi.fn().mockResolvedValue({ kind: 'ok', data: envelope }),
      } as any,
      ensureSession: vi.fn().mockResolvedValue(undefined),
      deliver,
      logger: { info: () => {}, warn: () => {} },
    })
    await loop.pollOnce()
    expect(deliver).not.toHaveBeenCalled()
    expect(loop.inflight).toHaveLength(0)
  })
})
