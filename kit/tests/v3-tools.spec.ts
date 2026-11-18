import { describe, expect, it, vi } from 'vitest'
import { createAllEchuanTools } from '../src/tools/index.js'
import type { EchuanToolOptions, WorkerIdentity } from '../src/tools/write.js'
import type { V3Task } from '../src/v3-types.js'

const task: V3Task = {
  task_id: 't_1',
  project_id: 'APP-1',
  kind: 'dev',
  title: '实现接口',
  description_md: '描述',
  context_md: '上下文',
  priority: 0,
  status: 'ready',
  attempt: 0,
  max_attempts: 3,
  create_time: '2026-11-18T00:00:00.000Z',
  update_time: '2026-11-18T00:00:00.000Z',
}

function ok<T>(data: T) {
  return { kind: 'ok' as const, status: 200, data }
}

function buildOptions(client: Record<string, any>, identity: WorkerIdentity = { workerId: 'w_1' }) {
  const options = { client: client as any, identity } as EchuanToolOptions
  const tools = createAllEchuanTools(options)
  return { options, tools, byName: new Map(tools.map(tool => [tool.name, tool])) }
}

describe('V3 工具清单', () => {
  it('恰好 18 个：10 写 + 8 读，旧 work_* 全部消失', () => {
    const { tools } = buildOptions({})
    expect(tools).toHaveLength(18)
    expect(tools.filter(tool => tool.name.startsWith('echuan_task_'))).toHaveLength(8)
    expect(tools.some(tool => tool.name.startsWith('echuan_work_'))).toBe(false)
    expect(tools.some(tool => tool.name === 'echuan_spec_create')).toBe(false)
    expect(tools.some(tool => tool.name === 'echuan_plan_submit')).toBe(false)
  })
})

describe('V3 写工具', () => {
  it('task_claim 使用心跳 worker id，空队列返回 no_work', async () => {
    const claimV3Task = vi
      .fn()
      .mockResolvedValueOnce(ok(task))
      .mockResolvedValueOnce({ kind: 'empty', status: 204, message: 'no work' })
    const { byName } = buildOptions({ claimV3Task })

    const claimed = await byName.get('echuan_task_claim')!.execute({ kind: 'dev' } as any, {} as any)
    expect(claimV3Task).toHaveBeenCalledWith({ worker_id: 'w_1', kind: 'dev' })
    expect(claimed).toMatchObject({ kind: 'claimed', task: { task_id: 't_1' } })

    const empty = await byName.get('echuan_task_claim')!.execute({ kind: 'dev' } as any, {} as any)
    expect(empty).toMatchObject({ kind: 'no_work' })
  })

  it('task_claim 在 worker 未注册时本地拒绝', async () => {
    const { byName } = buildOptions({ claimV3Task: vi.fn() }, { workerId: undefined })
    await expect(
      byName.get('echuan_task_claim')!.execute({ kind: 'dev' } as any, {} as any),
    ).rejects.toThrow(/worker/)
  })

  it('task_report 传 material_id 时直接上报', async () => {
    const reportV3Task = vi.fn().mockResolvedValue(ok({ ...task, status: 'in_review' }))
    const createV3Material = vi.fn()
    const { byName } = buildOptions({ reportV3Task, createV3Material })

    const result = await byName.get('echuan_task_report')!.execute(
      { task_id: 't_1', lease_id: 'l_1', material_id: 'm_1' } as any,
      {} as any,
    )
    expect(createV3Material).not.toHaveBeenCalled()
    expect(reportV3Task).toHaveBeenCalledWith({ task_id: 't_1', lease_id: 'l_1', material_id: 'm_1' })
    expect(result).toMatchObject({ kind: 'reported' })
  })

  it('task_report 传 summary_md 时先建 material，再上报', async () => {
    const getV3Task = vi.fn().mockResolvedValue(ok(task))
    const createV3Material = vi.fn().mockResolvedValue(
      ok({ material_id: 'm_new', kind: 'report', version: 1, title: '实现接口' }),
    )
    const reportV3Task = vi.fn().mockResolvedValue(ok({ ...task, status: 'in_review' }))
    const { byName } = buildOptions({ getV3Task, createV3Material, reportV3Task })

    await byName.get('echuan_task_report')!.execute(
      { task_id: 't_1', lease_id: 'l_1', summary_md: '## 改了什么' } as any,
      {} as any,
    )
    expect(createV3Material).toHaveBeenCalledWith({
      kind: 'report',
      title: '实现接口',
      body_md: '## 改了什么',
    })
    expect(reportV3Task).toHaveBeenCalledWith({ task_id: 't_1', lease_id: 'l_1', material_id: 'm_new' })
  })

  it('task_attach 校验 role 并调用 attach', async () => {
    const attachV3 = vi.fn().mockResolvedValue(
      ok({ id: 1, material_id: 2, role: 'output', sort_no: 0, create_time: 't' }),
    )
    const { byName } = buildOptions({ attachV3 })

    await byName.get('echuan_task_attach')!.execute(
      { task_id: 't_1', material_id: 'm_1', role: 'output' } as any,
      {} as any,
    )
    expect(attachV3).toHaveBeenCalledWith({
      project_id: undefined,
      task_id: 't_1',
      material_id: 'm_1',
      role: 'output',
      sort_no: undefined,
    })

    await expect(
      byName.get('echuan_task_attach')!.execute(
        { task_id: 't_1', material_id: 'm_1', role: 'nope' } as any,
        {} as any,
      ),
    ).rejects.toThrow(/role/)
  })

  it('project_close 透传 force；project_reopen 调 reopen', async () => {
    const closeV3Project = vi.fn().mockResolvedValue({ kind: 'empty', status: 204, message: 'closed' })
    const reopenV3Project = vi.fn().mockResolvedValue({ kind: 'empty', status: 204, message: 'reopened' })
    const { byName } = buildOptions({ closeV3Project, reopenV3Project })

    await byName.get('echuan_project_close')!.execute({ id: 'APP-1', force: true } as any, {} as any)
    expect(closeV3Project).toHaveBeenCalledWith({ id: 'APP-1', force: true })

    await byName.get('echuan_project_reopen')!.execute({ id: 'APP-1' } as any, {} as any)
    expect(reopenV3Project).toHaveBeenCalledWith({ id: 'APP-1' })
  })
})
