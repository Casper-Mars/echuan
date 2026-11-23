/**
 * V3 工具面：18 个工具（10 写 + 8 读）。
 *
 * 与 v2 的差异：
 * - `work_*` 全部改为 `task_*`；交付物统一走任务附件；
 * - 删除需求写入 / 提交评审 / 方案创建 / 方案修订 / 拆分计划 / 资料包；
 * - 新增 `task_attach`、`project_close`、`project_reopen`；
 * - 项目只是容器，建项不再携带需求正文。
 *
 * 边界不变：工具层没有审批 / 驳回 / 取消动作，也不缓存业务状态。
 */
import { defineTool, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import type { EchuanToolOptions, WorkerIdentity } from './write.js'
import { EchuanValidationError } from '../errors.js'
import { createWorkerInfoTool } from './worker-info.js'
import {
  formatMaterial,
  formatV3Attached,
  formatV3Claimed,
  formatV3Inbox,
  formatV3Project,
  formatV3ProjectClosed,
  formatV3ProjectCreated,
  formatV3ProjectList,
  formatV3ProjectReopened,
  formatV3Products,
  formatV3ProductCreated,
  formatV3Reported,
  formatV3Renewed,
  formatV3Task,
  formatV3TaskCreated,
  formatV3TaskList,
  formatV3TaskUpdated,
  isRecord,
  NO_WORK_TEXT,
  renderTextValue,
  toToolError,
} from './render.js'
import type { V3ClaimInput, V3Project, V3Task, V3TaskKind } from '../v3-types.js'

const jsonObjectSchema = { type: 'object', additionalProperties: true } as const
/** 只读工具的规范值 schema：一段渲染好的中文文本。 */
const textOutputSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    text: { type: 'string', required: true, description: '渲染好的中文文本' },
  },
} as const

/**
 * 所有工具共用的 render：优先取规范值里的 `text`（V3 读工具与写工具都会带），
 * 拿不到时退化为缩进 JSON —— 任何情况都不返回空串（issue #65）。
 */
const renderJSON = (_args: unknown, value: unknown) => renderTextValue(value)

function requireText(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new EchuanValidationError(`${field} 不能为空`, 'invalid_argument', field)
  }
  return value.trim()
}

function requireKind(value: unknown): V3TaskKind {
  const kind = requireText(value, 'kind')
  if (kind !== 'requirement' && kind !== 'design' && kind !== 'breakdown' && kind !== 'dev') {
    throw new EchuanValidationError(
      `kind 必须是 requirement / design / breakdown / dev，收到 ${kind}`,
      'invalid_argument',
      'kind',
    )
  }
  return kind
}

function requireWorker(identity: WorkerIdentity): string {
  const workerID = identity.workerId
  if (workerID === undefined || workerID.trim() === '') {
    throw new EchuanValidationError(
      'worker 尚未注册（心跳循环未就绪），不能领取任务',
      'worker_unknown',
      'worker_id',
    )
  }
  return workerID
}

/**
 * 领卡回包是 envelope（`{ task, project, product, inputs, upstream, … }`），不是扁平任务；
 * 这里统一取出任务与输入物料，兼容平台将来直接回扁平任务。
 */
function unwrapClaim(data: unknown): { task: V3Task; inputs: V3ClaimInput[] } {
  if (isRecord(data) && isRecord(data['task'])) {
    const inputs = Array.isArray(data['inputs'])
      ? (data['inputs'] as V3ClaimInput[])
      : []
    return { task: data['task'] as unknown as V3Task, inputs }
  }
  return { task: data as V3Task, inputs: [] }
}

function writeTool(
  name: string,
  description: string,
  parameters: Record<string, unknown>,
  run: (args: Record<string, any>) => Promise<unknown>,
): ToolDefinition {
  return defineTool({
    name,
    description,
    parameters: parameters as any,
    output: { schema: jsonObjectSchema, render: renderJSON },
    async execute(args) {
      try {
        return (await run(args as Record<string, any>)) as any
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/**
 * 只读工具工厂：规范值刻意收成 `{ text }`（与 V2 `read.ts` 对齐）。
 *
 * 只读工具没有第二个消费方，规范值就是模型要读的那段文本；渲染只有一条路径
 * （`renderTextValue`），因此不会出现「拿到数据但渲染成空串」的情况。
 */
function readTool(
  name: string,
  description: string,
  parameters: Record<string, unknown>,
  run: (args: Record<string, any>) => Promise<string>,
): ToolDefinition {
  return defineTool({
    name,
    description,
    parameters: parameters as any,
    output: { schema: textOutputSchema, render: renderJSON },
    async execute(args) {
      try {
        return { text: await run(args as Record<string, any>) } as any
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

export function createV3WriteTools(options: EchuanToolOptions): ToolDefinition[] {
  const { client, identity } = options

  return [
    writeTool(
      'echuan_task_claim',
      '领取一张 ready 任务（POST /v1/tasks/claim）。无任务时返回 no_work，不要当错误重试。',
      { kind: { type: 'string', required: true, description: 'requirement / design / breakdown / dev' } },
      async args => {
        // worker.poll 开启时领卡是心跳循环的职责：再手动 claim 会让同一张卡
        // 出现两个持有者（本会话 + 派单投递到的会话），两边重复干活。
        if (identity.pollEnabled === true) {
          throw new EchuanValidationError(
            'worker.poll 已开启：领卡由 worker 心跳自动完成，不能手动 claim（会让同一张卡出现两个持有者）。收到派单消息即开工。',
            'invalid_argument',
            'kind',
          )
        }
        const result = await client.claimV3Task({ worker_id: requireWorker(identity), kind: requireKind(args.kind) })
        if (result.kind === 'empty') return { kind: 'no_work', text: NO_WORK_TEXT }
        const claimed = unwrapClaim(result.data)
        identity.notifyReported?.(claimed.task.task_id)
        return { kind: 'claimed', task: claimed.task, text: formatV3Claimed(claimed.task, claimed.inputs) }
      },
    ),
    writeTool(
      'echuan_task_renew',
      '续租一张在跑的任务（POST /v1/tasks/lease）。',
      {
        task_id: { type: 'string', required: true, description: '任务编号' },
        lease_id: { type: 'string', required: true, description: '当前租约' },
      },
      async args => {
        const input = {
          task_id: requireText(args.task_id, 'task_id'),
          lease_id: requireText(args.lease_id, 'lease_id'),
        }
        const result = await client.renewV3Task(input)
        const data = result.kind === 'empty' ? undefined : result.data
        return {
          kind: 'renewed',
          ...(data === undefined ? {} : { result: data }),
          text: formatV3Renewed(input, data),
        }
      },
    ),
    writeTool(
      'echuan_task_report',
      '交付任务（POST /v1/tasks/report）：写 output 附件并把任务转为 in_review。'
        + 'brief_md 可选：一页决策简报，作为任务的第二个产物（kind=context）随交付一起提交。',
      {
        task_id: { type: 'string', required: true },
        lease_id: { type: 'string', required: true },
        material_id: { type: 'string', description: '已上传的交付物编号；与 summary_md 二选一' },
        summary_md: { type: 'string', description: '交付正文；缺 material_id 时由工具创建 material' },
        material_title: { type: 'string' },
        brief_md: {
          type: 'string',
          description: '可选：一页决策简报（人类层，≤600 字）。作为任务的 context 附件提交，不替代交付正文',
        },
        brief_title: { type: 'string', description: '可选：简报标题；缺省为「<任务标题> · 决策简报」' },
      },
      async args => {
        const taskID = requireText(args.task_id, 'task_id')
        const leaseID = requireText(args.lease_id, 'lease_id')
        const briefMD = typeof args.brief_md === 'string' ? args.brief_md.trim() : ''
        let task: V3Task | undefined
        let taskResult: Awaited<ReturnType<typeof client.getV3Task>> | undefined
        // 只在需要推断资料类型或需要 project_id（提交简报）时才查任务。
        if ((typeof args.material_id !== 'string' || args.material_id.trim() === '') || briefMD !== '') {
          taskResult = await client.getV3Task({ id: taskID })
          if (taskResult.kind === 'empty') {
            throw new EchuanValidationError('任务不存在，无法推断资料类型', 'not_found', 'task_id')
          }
          task = taskResult.data
        }
        let materialID = typeof args.material_id === 'string' && args.material_id.trim() !== '' ? args.material_id.trim() : ''
        if (materialID === '') {
          const summary = requireText(args.summary_md, 'summary_md')
          const taskKind = task?.kind
          const materialKind = taskKind === 'requirement' ? 'requirement'
            : taskKind === 'design' ? 'design'
              : taskKind === 'breakdown' ? 'plan'
                : 'report'
          const created = await client.createV3Material({
            kind: materialKind,
            title: typeof args.material_title === 'string' && args.material_title.trim() !== ''
              ? args.material_title
              : (task?.title ?? taskID),
            body_md: summary,
          })
          if (created.kind === 'empty') {
            throw new EchuanValidationError('平台对建资料返回 204', 'unexpected_empty', 'summary_md')
          }
          materialID = created.data.material_id
        }
        const result = await client.reportV3Task({ task_id: taskID, lease_id: leaseID, material_id: materialID })
        if (result.kind === 'empty') {
          throw new EchuanValidationError('平台对交报告返回 204，没有回包可核对', 'unexpected_empty', 'material_id')
        }

        // 简报是第二个产物：交付已经被平台接收，简报失败不回滚交付，只回显错误。
        let briefMaterialID: string | undefined
        let briefError: string | undefined
        if (briefMD !== '') {
          try {
            if (task === undefined) throw new EchuanValidationError('缺少任务上下文，无法定位项目', 'not_found', 'task_id')
            const createdBrief = await client.createV3Material({
              kind: 'context',
              title: typeof args.brief_title === 'string' && args.brief_title.trim() !== ''
                ? args.brief_title
                : `${task.title} · 决策简报`,
              body_md: briefMD,
              // 简报指向它解释的那一版交付物：重提版本时一一配对，不会查出两份。
              source_material_id: materialID,
            })
            if (createdBrief.kind === 'empty') {
              throw new EchuanValidationError('平台对建简报资料返回 204', 'unexpected_empty', 'brief_md')
            }
            await client.attachV3({
              task_id: taskID,
              material_id: createdBrief.data.material_id,
              role: 'output',
            })
            briefMaterialID = createdBrief.data.material_id
          } catch (error) {
            briefError = error instanceof Error ? error.message : String(error)
          }
        }

        const lines = [formatV3Reported({ task_id: taskID, material_id: materialID, status: result.data.status })]
        if (briefMaterialID !== undefined) {
          lines.push(`- 决策简报：${briefMaterialID}（项目输入附件，kind=context）`)
        } else if (briefError !== undefined) {
          lines.push(`- ⚠️ 决策简报未提交：${briefError}`)
        }
        return {
          kind: 'reported',
          task: result.data,
          brief_material_id: briefMaterialID,
          brief_error: briefError,
          text: lines.join('\n'),
        }
      },
    ),
    writeTool(
      'echuan_task_create',
      '创建任务（POST /v1/tasks）。项目只是容器；依赖未满足时平台会落 pending。',
      {
        project_id: { type: 'string', required: true },
        kind: { type: 'string', required: true },
        title: { type: 'string', required: true },
        task_id: { type: 'string', description: '可选；缺省由平台生成 t_<ULID>' },
        parent_task_id: { type: 'string' },
        description_md: { type: 'string' },
        context_md: { type: 'string' },
        priority: { type: 'integer' },
        max_attempts: { type: 'integer' },
        depends_on: { type: 'array', items: { type: 'string' } },
      },
      async args => {
        const result = await client.createV3Task(({
          project_id: requireText(args.project_id, 'project_id'),
          kind: requireKind(args.kind),
          title: requireText(args.title, 'title'),
          task_id: typeof args.task_id === 'string' ? args.task_id : undefined,
          parent_task_id: typeof args.parent_task_id === 'string' ? args.parent_task_id : undefined,
          description_md: typeof args.description_md === 'string' ? args.description_md : undefined,
          context_md: typeof args.context_md === 'string' ? args.context_md : undefined,
          priority: typeof args.priority === 'number' ? args.priority : undefined,
          max_attempts: typeof args.max_attempts === 'number' ? args.max_attempts : undefined,
          depends_on: Array.isArray(args.depends_on) ? args.depends_on.map((v: unknown) => String(v)) : undefined,
        }) as any)
        if (result.kind === 'empty') {
          throw new EchuanValidationError('平台对建任务返回 204，没有回包可核对', 'unexpected_empty', 'project_id')
        }
        return { kind: 'task_created', task: result.data, text: formatV3TaskCreated(result.data) }
      },
    ),
    writeTool(
      'echuan_task_update',
      '修改未领取任务（PATCH /v1/tasks?id=）：title / description_md / context_md / priority。',
      {
        id: { type: 'string', required: true },
        title: { type: 'string', required: true },
        description_md: { type: 'string' },
        context_md: { type: 'string' },
        priority: { type: 'integer' },
      },
      async args => {
        const result = await client.updateV3Task({
          id: requireText(args.id, 'id'),
          body: {
            title: requireText(args.title, 'title'),
            description_md: typeof args.description_md === 'string' ? args.description_md : undefined,
            context_md: typeof args.context_md === 'string' ? args.context_md : undefined,
            priority: typeof args.priority === 'number' ? args.priority : undefined,
          } as any,
        })
        if (result.kind === 'empty') throw new EchuanValidationError('平台返回 204', 'unexpected_empty', 'id')
        return { kind: 'task_updated', task: result.data, text: formatV3TaskUpdated(result.data) }
      },
    ),
    writeTool(
      'echuan_task_attach',
      '给项目或任务挂附件（POST /v1/tasks/attach）。project_id 与 task_id 必须二选一。',
      {
        project_id: { type: 'string' },
        task_id: { type: 'string' },
        material_id: { type: 'string', required: true },
        role: { type: 'string', required: true, description: 'input / output' },
        sort_no: { type: 'integer' },
      },
      async args => {
        const role = requireText(args.role, 'role')
        if (role !== 'input' && role !== 'output') {
          throw new EchuanValidationError(`role 必须是 input / output，收到 ${role}`, 'invalid_argument', 'role')
        }
        const result = await client.attachV3({
          project_id: typeof args.project_id === 'string' ? args.project_id : undefined,
          task_id: typeof args.task_id === 'string' ? args.task_id : undefined,
          material_id: requireText(args.material_id, 'material_id'),
          role,
          sort_no: typeof args.sort_no === 'number' ? args.sort_no : undefined,
        } as any)
        if (result.kind === 'empty') throw new EchuanValidationError('平台返回 204', 'unexpected_empty', 'material_id')
        return {
          kind: 'attached',
          attachment: result.data,
          text: formatV3Attached(
            {
              project_id: typeof args.project_id === 'string' ? args.project_id : undefined,
              task_id: typeof args.task_id === 'string' ? args.task_id : undefined,
              material_id: requireText(args.material_id, 'material_id'),
              role,
            },
            result.data,
          ),
        }
      },
    ),
    writeTool(
      'echuan_project_create',
      '创建项目容器（POST /v1/projects）：product_id / name / context_md；不会创建任何任务。',
      {
        product_id: { type: 'string', required: true },
        name: { type: 'string', required: true },
        context_md: { type: 'string' },
      },
      async args => {
        const result = await client.createV3Project({
          product_id: requireText(args.product_id, 'product_id'),
          name: requireText(args.name, 'name'),
          context_md: typeof args.context_md === 'string' ? args.context_md : undefined,
        } as any)
        if (result.kind === 'empty') throw new EchuanValidationError('平台返回 204', 'unexpected_empty', 'project_id')
        return { kind: 'project_created', project: result.data, text: formatV3ProjectCreated(result.data) }
      },
    ),
    writeTool(
      'echuan_project_close',
      '关闭项目（POST /v1/projects/close?id=）。有未完成任务时需 force=true，会取消它们。',
      {
        id: { type: 'string', required: true },
        force: { type: 'boolean' },
      },
      async args => {
        const id = requireText(args.id, 'id')
        const force = args.force === true
        await client.closeV3Project({ id, force })
        return { kind: 'project_closed', text: formatV3ProjectClosed(id, force) }
      },
    ),
    writeTool(
      'echuan_project_reopen',
      '重开项目（POST /v1/projects/reopen?id=）。已取消的任务不会复活。',
      { id: { type: 'string', required: true } },
      async args => {
        const id = requireText(args.id, 'id')
        await client.reopenV3Project({ id })
        return { kind: 'project_reopened', text: formatV3ProjectReopened(id) }
      },
    ),
    writeTool(
      'echuan_product_create',
      '创建产品（POST /v1/products）。',
      {
        product_id: { type: 'string', required: true },
        name: { type: 'string', required: true },
        git_url: { type: 'string', required: true },
        description: { type: 'string' },
        license: { type: 'string' },
      },
      async args => {
        const result = await client.createProduct({
          product_id: requireText(args.product_id, 'product_id'),
          name: requireText(args.name, 'name'),
          git_url: requireText(args.git_url, 'git_url'),
          description: typeof args.description === 'string' ? args.description : undefined,
          license: typeof args.license === 'string' ? args.license : undefined,
        } as any)
        if (result.kind === 'empty') throw new EchuanValidationError('平台返回 204', 'unexpected_empty', 'product_id')
        return { kind: 'product_created', product: result.data, text: formatV3ProductCreated(result.data) }
      },
    ),
  ]
}

export function createV3ReadTools(options: EchuanToolOptions): ToolDefinition[] {
  const { client } = options

  return [
    readTool('echuan_inbox', '待领取 + 待审核任务（GET /v1/inbox）。', {}, async () => {
      const result = await client.getV3Inbox()
      return formatV3Inbox(result.kind === 'empty' ? [] : (result.data.items ?? []))
    }),
    readTool(
      'echuan_product_list',
      '产品列表或详情（GET /v1/products，可带 id）。',
      { id: { type: 'string' }, after_id: { type: 'integer' }, limit: { type: 'integer' } },
      async args => {
        const result = await client.listProducts({
          ...(typeof args.id === 'string' ? { id: args.id } : {}),
          ...(typeof args.after_id === 'number' ? { after_id: args.after_id } : {}),
          ...(typeof args.limit === 'number' ? { limit: args.limit } : {}),
        } as any)
        return formatV3Products(result.kind === 'empty' ? undefined : result.data)
      },
    ),
    readTool(
      'echuan_project_list',
      '项目列表（GET /v1/projects），可按 open/closed 过滤。',
      {
        status: { type: 'string' },
        after_id: { type: 'integer' },
        limit: { type: 'integer' },
      },
      async args => {
        const status = args.status === 'open' || args.status === 'closed' ? args.status : undefined
        const result = await client.listV3Projects({
          status,
          after_id: typeof args.after_id === 'number' ? args.after_id : undefined,
          limit: typeof args.limit === 'number' ? args.limit : undefined,
        } as any)
        return formatV3ProjectList(result.kind === 'empty' ? [] : (result.data.items ?? []))
      },
    ),
    readTool(
      'echuan_project_get',
      '项目详情（GET /v1/projects?id=）。',
      { id: { type: 'string', required: true } },
      async args => {
        const result = await client.getV3Project({ id: requireText(args.id, 'id') })
        if (result.kind === 'empty') throw new EchuanValidationError('项目不存在', 'not_found', 'id')
        return formatV3Project(result.data)
      },
    ),
    readTool(
      'echuan_task_list',
      '任务列表（GET /v1/tasks），可按 project_id / kind / status 过滤。',
      {
        project_id: { type: 'string' },
        kind: { type: 'string' },
        status: { type: 'string' },
        limit: { type: 'integer' },
      },
      async args => {
        const kind = typeof args.kind === 'string' && args.kind !== '' ? requireKind(args.kind) : undefined
        const result = await client.listV3Tasks({
          project_id: typeof args.project_id === 'string' ? args.project_id : undefined,
          kind,
          status: typeof args.status === 'string' && args.status !== '' ? (args.status as any) : undefined,
          limit: typeof args.limit === 'number' ? args.limit : undefined,
        } as any)
        return formatV3TaskList(result.kind === 'empty' ? [] : (result.data.items ?? []))
      },
    ),
    readTool(
      'echuan_task_get',
      '任务详情（GET /v1/tasks?id=）。',
      { id: { type: 'string', required: true } },
      async args => {
        const result = await client.getV3Task({ id: requireText(args.id, 'id') })
        if (result.kind === 'empty') throw new EchuanValidationError('任务不存在', 'not_found', 'id')
        return formatV3Task(result.data)
      },
    ),
    readTool(
      'echuan_material_get',
      '下载资料正文（GET /v1/materials?id=）。',
      { id: { type: 'string', required: true } },
      async args => {
        const result = await client.getMaterials({ id: requireText(args.id, 'id') })
        if (result.kind === 'empty') throw new EchuanValidationError('资料不存在', 'not_found', 'id')
        return formatMaterial(result.data)
      },
    ),
    createWorkerInfoTool(options),
  ]
}
