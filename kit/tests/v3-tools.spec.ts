import { describe, expect, it, vi } from 'vitest'
import { createAllEchuanTools } from '../src/tools/index.js'
import type { EchuanToolOptions, WorkerIdentity } from '../src/tools/write.js'
import { renderTextValue } from '../src/tools/render.js'
import type { V3Project, V3Task } from '../src/v3-types.js'

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

/** 递归检查是否出现值为 `undefined` 的键（dsh 会把这种结果判成非无损 JSON）。 */
function hasUndefinedKey(value: unknown): boolean {
  if (Array.isArray(value)) return value.some(hasUndefinedKey)
  if (value !== null && typeof value === 'object') {
    return Object.entries(value).some(([, item]) => item === undefined || hasUndefinedKey(item))
  }
  return false
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

  it('task_claim 在 worker.poll 开启时本地拒绝，零请求', async () => {
    const claimV3Task = vi.fn()
    const { byName } = buildOptions({ claimV3Task }, { workerId: 'w_1', pollEnabled: true })
    await expect(
      byName.get('echuan_task_claim')!.execute({ kind: 'dev' } as any, {} as any),
    ).rejects.toThrow(/poll/)
    expect(claimV3Task).not.toHaveBeenCalled()
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

  it('task_report 的返回值是无损 JSON：不含 undefined 键（dsh 会做 JSON 快照）', async () => {
    // 回归：没带简报时曾返回 `brief_error: undefined`，dsh 侧快照判非无损，
    // 整次调用被报成 invalid output（value is not lossless JSON），而交付其实已经成功。
    const reportV3Task = vi.fn().mockResolvedValue(ok({ ...task, status: 'in_review' }))
    const { byName } = buildOptions({ reportV3Task, createV3Material: vi.fn(), getV3Task: vi.fn() })

    const result = await byName.get('echuan_task_report')!.execute(
      { task_id: 't_1', lease_id: 'l_1', material_id: 'm_1' } as any,
      {} as any,
    )

    expect(hasUndefinedKey(result)).toBe(false)
    expect(JSON.parse(JSON.stringify(result))).toStrictEqual(result)
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

  it('task_report 带 brief_md 时创建 context 资料并挂到项目', async () => {
    const getV3Task = vi.fn().mockResolvedValue(ok(task))
    const createV3Material = vi.fn()
      .mockResolvedValueOnce(ok({ material_id: 'm_out', kind: 'report', version: 1, title: '实现接口' }))
      .mockResolvedValueOnce(ok({ material_id: 'm_brief', kind: 'context', version: 1, title: '实现接口 · 决策简报' }))
    const reportV3Task = vi.fn().mockResolvedValue(ok({ ...task, status: 'in_review' }))
    const attachV3 = vi.fn().mockResolvedValue(
      ok({ id: 1, material_id: 2, role: 'input', sort_no: 0, create_time: 't' }),
    )
    const { byName } = buildOptions({ getV3Task, createV3Material, reportV3Task, attachV3 })

    const result = await byName.get('echuan_task_report')!.execute(
      { task_id: 't_1', lease_id: 'l_1', summary_md: '# 交付', brief_md: '## 结论\n做。' } as any,
      {} as any,
    )

    expect(createV3Material).toHaveBeenNthCalledWith(2, {
      kind: 'context',
      title: '实现接口 · 决策简报',
      body_md: '## 结论\n做。',
      source_material_id: 'm_out',
    })
    expect(attachV3).toHaveBeenCalledWith({
      task_id: 't_1',
      material_id: 'm_brief',
      role: 'output',
    })
    expect(result).toMatchObject({ kind: 'reported', brief_material_id: 'm_brief' })
    expect(String((result as any).text)).toContain('m_brief')
  })

  it('简报提交失败不回滚交付，只在回显里报错', async () => {
    const getV3Task = vi.fn().mockResolvedValue(ok(task))
    const createV3Material = vi.fn()
      .mockResolvedValueOnce(ok({ material_id: 'm_out', kind: 'report', version: 1, title: '实现接口' }))
      .mockRejectedValueOnce(new Error('registry down'))
    const reportV3Task = vi.fn().mockResolvedValue(ok({ ...task, status: 'in_review' }))
    const attachV3 = vi.fn()
    const { byName } = buildOptions({ getV3Task, createV3Material, reportV3Task, attachV3 })

    const result = await byName.get('echuan_task_report')!.execute(
      { task_id: 't_1', lease_id: 'l_1', summary_md: '# 交付', brief_md: '## 结论\n做。' } as any,
      {} as any,
    )

    expect(reportV3Task).toHaveBeenCalled()
    expect(attachV3).not.toHaveBeenCalled()
    expect(result).toMatchObject({ kind: 'reported', brief_error: 'registry down' })
    expect(String((result as any).text)).toContain('registry down')
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

/* ------------------------------------------------------------------ *
 * V3 工具面渲染：成功路径必须有可读文本（issue #65）
 *
 * 这一组断言是此前缺失的防线：`tests/v3-tools.spec.ts` 里 `grep -c render` 为 0，
 * 所以「execute 返回了契约 JSON、render 却渲染成空串」可以全绿地漏过去。
 * 每个用例都走 execute → output.render 的真实路径，断言最终喂给模型的那段文本。
 * ------------------------------------------------------------------ */

const project: V3Project = {
  project_id: 'ECHUAN-2',
  product_id: 'ECHUAN',
  seq_no: 2,
  name: '修复 kit V3 工具面渲染',
  context_md: '项目上下文：issue #65 根因证据与验收口径。',
  status: 'open',
  create_time: '2026-11-18T00:00:00.000Z',
  update_time: '2026-11-18T01:00:00.000Z',
}

const product = {
  product_id: 'ECHUAN',
  id: 'ECHUAN',
  name: '驿传',
  git_url: 'https://github.com/Casper-Mars/echuan',
  license: 'MIT',
  project_count: 2,
}

/** 走真实路径：execute 拿规范值，再用该工具的 `output.render` 渲染成模型可见文本。 */
async function runAndRender(
  byName: Map<string, any>,
  name: string,
  args: Record<string, unknown> = {},
): Promise<{ value: unknown; text: string }> {
  const tool = byName.get(name)
  expect(tool, `工具 ${name} 不存在`).toBeDefined()
  expect(typeof tool!.output?.render, `工具 ${name} 没有 render`).toBe('function')
  const value = await tool!.execute(args as any, {} as any)
  const blocks = tool!.output.render(args, value as any) as Array<{ type: string; text?: string }>
  const text = blocks
    .filter(block => block.type === 'text')
    .map(block => block.text ?? '')
    .join('\n')
  return { value, text }
}

describe('V3 读工具：成功即非空中文文本', () => {
  it('product_list 渲染条目数与每条 product_id / name / git_url', async () => {
    const listProducts = vi.fn().mockResolvedValue(ok({ items: [product] }))
    const { byName } = buildOptions({ listProducts })

    const { value, text } = await runAndRender(byName, 'echuan_product_list')

    expect(value).toMatchObject({ text: expect.any(String) })
    expect(text).not.toBe('')
    expect(text).toContain('产品共 1 个')
    expect(text).toContain('ECHUAN')
    expect(text).toContain('驿传')
    expect(text).toContain('https://github.com/Casper-Mars/echuan')
  })

  it('product_list 空集给明确中文提示，不是空串', async () => {
    const listProducts = vi.fn().mockResolvedValue(ok({ items: [] }))
    const { byName } = buildOptions({ listProducts })

    const { text } = await runAndRender(byName, 'echuan_product_list')
    expect(text).toContain('当前没有产品')
  })

  it('product_list 带 id（平台回单个详情）也能渲染', async () => {
    const listProducts = vi.fn().mockResolvedValue(ok(product))
    const { byName } = buildOptions({ listProducts })

    const { text } = await runAndRender(byName, 'echuan_product_list', { id: 'ECHUAN' })
    expect(text).toContain('产品共 1 个')
    expect(text).toContain('ECHUAN')
  })

  it('读工具成功输出只给数据：不注入「下一步」这类工作流建议', async () => {
    const listProducts = vi.fn().mockResolvedValue(ok({ items: [product] }))
    const listV3Projects = vi.fn().mockResolvedValue(ok({ items: [project] }))
    const listV3Tasks = vi.fn().mockResolvedValue(ok({ items: [task] }))
    const { byName } = buildOptions({ listProducts, listV3Projects, listV3Tasks })

    for (const name of ['echuan_product_list', 'echuan_project_list', 'echuan_task_list']) {
      const { text } = await runAndRender(byName, name)
      expect(text).not.toContain('下一步')
    }
  })

  it('project_list 渲染项目编号 / 名称 / 状态，空集有提示', async () => {
    const listV3Projects = vi
      .fn()
      .mockResolvedValueOnce(ok({ items: [project] }))
      .mockResolvedValueOnce(ok({ items: [] }))
    const { byName } = buildOptions({ listV3Projects })

    const { text } = await runAndRender(byName, 'echuan_project_list')
    expect(text).toContain('项目共 1 个')
    expect(text).toContain('ECHUAN-2')
    expect(text).toContain('修复 kit V3 工具面渲染')
    expect(text).toContain('进行中（open）')

    const { text: emptyText } = await runAndRender(byName, 'echuan_project_list')
    expect(emptyText).toContain('当前没有项目')
  })

  it('project_list schema 暴露 product_id，描述写明倒序与「更早一页」语义', () => {
    const { byName } = buildOptions({})
    const tool = byName.get('echuan_project_list')!

    const parameters = tool.parameters as { properties?: Record<string, { type?: string }> }
    expect(Object.keys(parameters.properties ?? {})).toEqual(
      expect.arrayContaining(['product_id', 'status', 'after_id', 'limit']),
    )
    expect(parameters.properties?.product_id?.type).toBe('string')

    expect(tool.description).toContain('倒序')
    expect(tool.description).toContain('更早一页')
    expect(tool.description).toContain('product_id')
  })

  it('project_list 把 product_id 透传给 listV3Projects（不传时不带该参数）', async () => {
    const listV3Projects = vi.fn().mockResolvedValue(ok({ items: [project] }))
    const { byName } = buildOptions({ listV3Projects })

    await runAndRender(byName, 'echuan_project_list', { product_id: 'echuan' })
    expect(listV3Projects.mock.calls[0]![0]).toMatchObject({ product_id: 'echuan' })

    await runAndRender(byName, 'echuan_project_list')
    expect(listV3Projects.mock.calls[1]![0]).not.toHaveProperty('product_id')
  })

  it('task_list 渲染任务编号 / 状态，空集有提示', async () => {
    const listV3Tasks = vi
      .fn()
      .mockResolvedValueOnce(ok({ items: [task, { ...task, task_id: 't_2', status: 'in_progress', lease_id: 'l_9' }] }))
      .mockResolvedValueOnce(ok({ items: [] }))
    const { byName } = buildOptions({ listV3Tasks })

    const { text } = await runAndRender(byName, 'echuan_task_list', { project_id: 'ECHUAN-2' })
    expect(text).toContain('任务共 2 个')
    expect(text).toContain('t_1')
    expect(text).toContain('待领取（ready）')
    expect(text).toContain('进行中（in_progress）')
    expect(text).toContain('lease_id=l_9')

    const { text: emptyText } = await runAndRender(byName, 'echuan_task_list', { project_id: 'ECHUAN-2' })
    expect(emptyText).toContain('当前没有任务')
  })

  it('inbox 渲染待办条目与状态，空集有提示', async () => {
    const getV3Inbox = vi
      .fn()
      .mockResolvedValueOnce(ok({ items: [{ ...task, status: 'in_review' }] }))
      .mockResolvedValueOnce(ok({ items: [] }))
    const { byName } = buildOptions({ getV3Inbox })

    const { text } = await runAndRender(byName, 'echuan_inbox')
    expect(text).toContain('待办共 1 条')
    expect(text).toContain('t_1')
    expect(text).toContain('待验收（in_review）')

    const { text: emptyText } = await runAndRender(byName, 'echuan_inbox')
    expect(emptyText).toContain('当前没有待处理项')
  })

  it('task_get 渲染正文与租约', async () => {
    const getV3Task = vi.fn().mockResolvedValue(ok({ ...task, lease_id: 'l_1', lease_until: '2026-11-18T02:00:00.000Z' }))
    const { byName } = buildOptions({ getV3Task })

    const { text } = await runAndRender(byName, 'echuan_task_get', { id: 't_1' })
    expect(text).toContain('任务 t_1：实现接口')
    expect(text).toContain('任务正文')
    expect(text).toContain('描述')
    expect(text).toContain('lease_id=l_1')
  })

  it('project_get 渲染项目上下文', async () => {
    const getV3Project = vi.fn().mockResolvedValue(ok(project))
    const { byName } = buildOptions({ getV3Project })

    const { text } = await runAndRender(byName, 'echuan_project_get', { id: 'ECHUAN-2' })
    expect(text).toContain('项目 ECHUAN-2：修复 kit V3 工具面渲染')
    expect(text).toContain('项目上下文')
    expect(text).toContain('issue #65 根因证据与验收口径')
  })

  it('material_get 渲染资料元信息与正文', async () => {
    const getMaterials = vi.fn().mockResolvedValue(ok({
      content: '# 交付说明\n\n已修复渲染。',
      contentType: 'text/markdown; charset=utf-8',
      etag: '"abc"',
      materialId: 'm_1',
      materialVersion: 3,
    }))
    const { byName } = buildOptions({ getMaterials })

    const { text } = await runAndRender(byName, 'echuan_material_get', { id: 'm_1' })
    expect(text).toContain('资料 m_1')
    expect(text).toContain('已修复渲染')
  })
})

describe('V3 写工具：成功即带关键编号的确认文本', () => {
  it('claim 成功文本含 task_id 与 lease_id（issue #65 危害最高的一条）', async () => {
    const claimV3Task = vi.fn().mockResolvedValue(ok({
      ...task,
      status: 'in_progress',
      lease_id: 'l_01ABC',
      lease_until: '2026-11-18T02:00:00.000Z',
    }))
    const { byName } = buildOptions({ claimV3Task })

    const { value, text } = await runAndRender(byName, 'echuan_task_claim', { kind: 'dev' })
    expect(value).toMatchObject({ kind: 'claimed', task: { task_id: 't_1' } })
    expect(text).not.toBe('')
    expect(text).toContain('task_id：t_1')
    expect(text).toContain('lease_id：l_01ABC')
  })

  it('claim 按平台真实 envelope 回包（{ task, inputs }）取 task_id / lease_id', async () => {
    // 形状取自 internal/api/v3_handlers.go:486 v3ClaimResponse
    const envelope = {
      task: { ...task, status: 'in_progress', lease_id: 'l_env', lease_until: '2026-11-18T02:00:00.000Z' },
      project: { project_id: 'ECHUAN-2', name: '修复 #65', context_md: 'ctx' },
      product: { product_id: 'ECHUAN', name: '驿传', git_url: 'https://github.com/Casper-Mars/echuan' },
      inputs: [{ material_id: 'm_plan', title: '方案', kind: 'design', role: 'input' }],
      upstream: [],
      has_downstream_dev: false,
    }
    const claimV3Task = vi.fn().mockResolvedValue(ok(envelope))
    const { byName } = buildOptions({ claimV3Task })

    const { value, text } = await runAndRender(byName, 'echuan_task_claim', { kind: 'dev' })
    expect(value).toMatchObject({ kind: 'claimed', task: { task_id: 't_1', lease_id: 'l_env' } })
    expect(text).toContain('task_id：t_1')
    expect(text).toContain('lease_id：l_env')
    expect(text).toContain('material_id=m_plan')
  })

  it('claim 的 no_work 分支仍然可读', async () => {
    const claimV3Task = vi.fn().mockResolvedValue({ kind: 'empty', status: 204, message: 'no work' })
    const { byName } = buildOptions({ claimV3Task })

    const { text } = await runAndRender(byName, 'echuan_task_claim', { kind: 'dev' })
    expect(text).toContain('当前无活')
  })

  it('task_report 成功文本含 material_id 与任务编号', async () => {
    const reportV3Task = vi.fn().mockResolvedValue(ok({ ...task, status: 'in_review' }))
    const { byName } = buildOptions({ reportV3Task, createV3Material: vi.fn() })

    const { text } = await runAndRender(byName, 'echuan_task_report', {
      task_id: 't_1',
      lease_id: 'l_1',
      material_id: 'm_1',
    })
    expect(text).not.toBe('')
    expect(text).toContain('task_id：t_1')
    expect(text).toContain('material_id：m_1')
    expect(text).toContain('待验收（in_review）')
  })

  it('task_create 成功文本回显新任务编号', async () => {
    const createV3Task = vi.fn().mockResolvedValue(ok({ ...task, task_id: 't_new', project_id: 'ECHUAN-2' }))
    const { byName } = buildOptions({ createV3Task })

    const { text } = await runAndRender(byName, 'echuan_task_create', {
      project_id: 'ECHUAN-2',
      kind: 'dev',
      title: '实现接口',
    })
    expect(text).toContain('t_new')
    expect(text).toContain('ECHUAN-2')
  })

  it('product_create 成功文本回显 product_id 与仓库地址', async () => {
    const createProduct = vi.fn().mockResolvedValue(ok(product))
    const { byName } = buildOptions({ createProduct })

    const { text } = await runAndRender(byName, 'echuan_product_create', {
      product_id: 'ECHUAN',
      name: '驿传',
      git_url: 'https://github.com/Casper-Mars/echuan',
    })
    expect(text).toContain('product_id：ECHUAN')
    expect(text).toContain('https://github.com/Casper-Mars/echuan')
  })

  it('project_create / project_close / project_reopen 都有确认文本', async () => {
    const createV3Project = vi.fn().mockResolvedValue(ok(project))
    const closeV3Project = vi.fn().mockResolvedValue({ kind: 'empty', status: 204, message: 'closed' })
    const reopenV3Project = vi.fn().mockResolvedValue({ kind: 'empty', status: 204, message: 'reopened' })
    const { byName } = buildOptions({ createV3Project, closeV3Project, reopenV3Project })

    const created = await runAndRender(byName, 'echuan_project_create', { product_id: 'ECHUAN', name: '新项目' })
    expect(created.text).toContain('project_id：ECHUAN-2')

    const closed = await runAndRender(byName, 'echuan_project_close', { id: 'ECHUAN-2', force: false })
    expect(closed.text).toContain('项目已关闭：ECHUAN-2')

    const reopened = await runAndRender(byName, 'echuan_project_reopen', { id: 'ECHUAN-2' })
    expect(reopened.text).toContain('项目已重开：ECHUAN-2')
  })

  it('task_update / task_attach / task_renew 都有确认文本', async () => {
    const updateV3Task = vi.fn().mockResolvedValue(ok({ ...task, title: '改后的标题' }))
    const attachV3 = vi.fn().mockResolvedValue(
      ok({ id: 7, material_id: 'm_1', role: 'output', sort_no: 0, create_time: 't' }),
    )
    const renewV3Task = vi.fn().mockResolvedValue(ok({ lease_id: 'l_new', lease_until: '2026-11-18T03:00:00.000Z' }))
    const { byName } = buildOptions({ updateV3Task, attachV3, renewV3Task })

    const updated = await runAndRender(byName, 'echuan_task_update', { id: 't_1', title: '改后的标题' })
    expect(updated.text).toContain('task_id：t_1')
    expect(updated.text).toContain('改后的标题')

    const attached = await runAndRender(byName, 'echuan_task_attach', {
      task_id: 't_1',
      material_id: 'm_1',
      role: 'output',
    })
    expect(attached.text).toContain('material_id：m_1')
    expect(attached.text).toContain('任务 t_1')

    const renewed = await runAndRender(byName, 'echuan_task_renew', { task_id: 't_1', lease_id: 'l_1' })
    expect(renewed.text).toContain('新 lease_id：l_new')
    expect(renewed.text).toContain('t_1')
  })
})

describe('V3 渲染兜底：任何情况都不返回空串', () => {
  it('renderTextValue 对非 { text } 值退化为缩进 JSON', () => {
    const cases: unknown[] = [
      undefined,
      null,
      '',
      {},
      [],
      { items: [] },
      { kind: 'claimed', task: { task_id: 't_1' } },
      0,
      false,
    ]
    for (const value of cases) {
      const blocks = renderTextValue(value)
      const text = blocks.map(block => block.text).join('')
      expect(text, `兜底渲染对 ${JSON.stringify(value)} 返回了空串`).not.toBe('')
    }
  })

  it('兜底文本明确说出「这是原始 JSON」而不是空着', () => {
    const text = renderTextValue({ items: [{ task_id: 't_1' }] })
      .map(block => block.text)
      .join('')
    expect(text).toContain('JSON')
    expect(text).toContain('t_1')
  })

  it('18 个工具的 render 对空/异常规范值都不返回空串', () => {
    const { tools } = buildOptions({})
    const values: unknown[] = [undefined, null, {}, { items: [] }, '']
    for (const tool of tools) {
      for (const value of values) {
        const blocks = tool.output!.render({}, value as any) as Array<{ type: string; text?: string }>
        const text = blocks
          .filter(block => block.type === 'text')
          .map(block => block.text ?? '')
          .join('\n')
        expect(text, `工具 ${tool.name} 对 ${JSON.stringify(value)} 渲染成空串`).not.toBe('')
      }
    }
  })
})


describe('环节 name：词汇由平台裁决（0.7.0，ADR-0003 D7″）', () => {
  it('task_claim 接受平台流程里新增的环节 name，不再有本地白名单', async () => {
    const safetyTask: V3Task = {
      ...task,
      kind: '安全评审',
      kind_id: 5,
      kind_label: '安全评审',
      material_kind: 'report',
    }
    const claimV3Task = vi.fn().mockResolvedValue(ok(safetyTask))
    const { byName } = buildOptions({ claimV3Task })

    const claimed = await byName.get('echuan_task_claim')!.execute({ kind: '  安全评审  ' } as any, {} as any)

    // trim 后原样交给平台：平台按配置把 name 映射成 id，kit 不做词表比对。
    expect(claimV3Task).toHaveBeenCalledWith({ worker_id: 'w_1', kind: '安全评审' })
    expect(claimed).toMatchObject({ kind: 'claimed', task: { task_id: 't_1', kind: '安全评审' } })
  })

  it('task_claim 对不认识的 name 不本地拦截：请求照发，平台 400 的可用清单透传', async () => {
    const claimV3Task = vi.fn().mockRejectedValue(
      new Error('[invalid_argument] kind "deploy" 不是流程配置里的环节（可用环节: requirement(需求), dev(开发)）'),
    )
    const { byName } = buildOptions({ claimV3Task })

    await expect(
      byName.get('echuan_task_claim')!.execute({ kind: 'deploy' } as any, {} as any),
    ).rejects.toThrow(/可用环节/)
    expect(claimV3Task).toHaveBeenCalledWith({ worker_id: 'w_1', kind: 'deploy' })
  })

  it('task_claim 只拦形态：空 name 与超 32 字符在本地拒绝，零请求', async () => {
    const claimV3Task = vi.fn()
    const { byName } = buildOptions({ claimV3Task })

    await expect(
      byName.get('echuan_task_claim')!.execute({ kind: '   ' } as any, {} as any),
    ).rejects.toThrow(/kind 不能为空/)
    await expect(
      byName.get('echuan_task_claim')!.execute({ kind: 'a'.repeat(33) } as any, {} as any),
    ).rejects.toThrow(/32/)
    expect(claimV3Task).not.toHaveBeenCalled()
  })

  it('32 个字符的 name 放行，按码点计数（中文也算 1 个）', async () => {
    const name = '安'.repeat(32)
    const claimV3Task = vi.fn().mockResolvedValue(ok({ ...task, kind: name }))
    const { byName } = buildOptions({ claimV3Task })

    await byName.get('echuan_task_claim')!.execute({ kind: name } as any, {} as any)
    expect(claimV3Task).toHaveBeenCalledWith({ worker_id: 'w_1', kind: name })
  })

  it('task_create / task_list 的 kind 走同一套放宽规则', async () => {
    const createV3Task = vi.fn().mockResolvedValue(ok({ ...task, kind: 'safety_review' }))
    const listV3Tasks = vi.fn().mockResolvedValue(ok({ items: [] }))
    const { byName } = buildOptions({ createV3Task, listV3Tasks })

    await byName.get('echuan_task_create')!.execute(
      { project_id: 'APP-1', kind: 'safety_review', title: '安全评审' } as any,
      {} as any,
    )
    expect(createV3Task).toHaveBeenCalledWith(expect.objectContaining({ kind: 'safety_review' }))

    await byName.get('echuan_task_list')!.execute({ kind: 'safety_review' } as any, {} as any)
    expect(listV3Tasks).toHaveBeenCalledWith(expect.objectContaining({ kind: 'safety_review' }))
  })
})

describe('上报素材类别取平台 material_kind，不按 kind 本地猜（0.7.0，ADR-0003 D7″）', () => {
  /** 跑一次 task_report(summary_md)，返回它建 material 时用的 kind。 */
  async function materialKindOf(taskDTO: V3Task): Promise<string> {
    const getV3Task = vi.fn().mockResolvedValue(ok(taskDTO))
    const createV3Material = vi.fn().mockResolvedValue(
      ok({ material_id: 'm_new', kind: 'x', version: 1, title: '交付' }),
    )
    const reportV3Task = vi.fn().mockResolvedValue(ok({ ...taskDTO, status: 'in_review' }))
    const { byName } = buildOptions({ getV3Task, createV3Material, reportV3Task })

    await byName.get('echuan_task_report')!.execute(
      { task_id: 't_1', lease_id: 'l_1', summary_md: '交付正文' } as any,
      {} as any,
    )
    expect(reportV3Task).toHaveBeenCalled()
    return createV3Material.mock.calls[0]![0].kind as string
  }

  it('环节声明 material: plan → 上报 plan（不再按 kind 猜）', async () => {
    expect(await materialKindOf({ ...task, kind: 'review', material_kind: 'plan' })).toBe('plan')
  })

  it('material: report 的环节与改造前一致（0.6.10 的 dev 也落 report）', async () => {
    expect(await materialKindOf({ ...task, kind: 'dev', material_kind: 'report' })).toBe('report')
  })

  it('内置三个资料环节与改造前的本地映射逐一相同', async () => {
    expect(await materialKindOf({ ...task, kind: 'requirement', material_kind: 'requirement' }))
      .toBe('requirement')
    expect(await materialKindOf({ ...task, kind: 'design', material_kind: 'design' })).toBe('design')
    expect(await materialKindOf({ ...task, kind: 'breakdown', material_kind: 'plan' })).toBe('plan')
  })

  it('旧平台不带 material_kind（回滚期）→ 回落 report', async () => {
    expect(await materialKindOf({ ...task, kind: 'dev' })).toBe('report')
  })

  it('历史卡的环节已从配置删掉（material_kind 空串）→ 回落 report', async () => {
    expect(await materialKindOf({
      ...task,
      kind: 'kind(9)',
      kind_id: 9,
      kind_label: 'kind(9)',
      material_kind: '',
    })).toBe('report')
  })
})
