/**
 * 三个工作项写工具：领卡 / 续租 / 交报告（v2 work-item 契约）。
 *
 * 边界（硬约束）：
 * - 本文件只有这三个执行动作，**没有**验收/退回/取消/改状态一类的门禁工具；
 * - 工具层不代交报告、不重试、不判断工作项成败、不落盘；
 * - worker_id 只有**一个内存来源**——心跳循环（{@link WorkerIdentity} 由 `index.ts`
 *   把 `createWorkerLoop` 的实例传进来），本模块不缓存、不读磁盘、不读 env。
 */
import { defineTool, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import type {
  EchuanClient,
  ReportPlanInput,
  ReportPlanItemInput,
  WorkItemInput,
} from '../client.js'
import { EchuanValidationError } from '../errors.js'
import type { WorkerInfoDeps } from './worker-info.js'
import {
  renderClaimed,
  renderNoWork,
  renderRenewed,
  renderReported,
  toToolError,
  unexpectedEmpty,
} from './render.js'

/** 派单附带的输入物料（工具输出的投影）。 */
export interface ClaimInputValue {
  material_id: string
  kind: number
  version: number
}

/** 领卡成功后的规范值（output.schema 的唯一真实来源）。 */
export interface ClaimedValue {
  kind: 'claimed'
  work_id: string
  work_kind: string
  title: string
  body_md: string
  attempt: number
  priority: number
  lease_id: string
  lease_until: string
  ttl_seconds: number
  product_id: string
  product_name: string
  spec_version: number
  spec_digest: string
  inputs: ClaimInputValue[]
}

/** 204「当前无活」的规范值：正常结果，不是错误。 */
export interface NoWorkValue {
  kind: 'no_work'
}

export type ClaimValue = ClaimedValue | NoWorkValue

/** 续租成功的规范值。 */
export interface RenewedValue {
  kind: 'renewed'
  work_id: string
  lease_id: string
  lease_until: string
  ttl_seconds: number
}

/** 报告提交后的规范值。 */
export interface ReportedValue {
  kind: 'reported'
  work_id: string
  state?: string
}

/**
 * 共享身份：worker_id 的唯一内存来源。
 *
 * 生产实现由 `index.ts` 提供（读心跳循环的 `workerId` 只读属性）；
 * 未注册时返回 `undefined`，此时 `echuan_work_claim` 会在本地拒绝（平台契约要求
 * `worker_id` 必填）。
 */
export interface WorkerIdentity {
  readonly workerId: string | undefined
  /**
   * 报告成功的回调（可选）：由 `index.ts` 接到心跳循环的 `notifyReported`，
   * 用于清除该工作项的 inflight。只在平台 HTTP 成功后调用。
   */
  notifyReported?(workId: string): void
}

export interface EchuanToolOptions {
  /** 平台 HTTP 客户端（TOOL_METHODS 里的 22 个方法）。 */
  client: EchuanClient
  /** 共享身份（心跳循环）。 */
  identity: WorkerIdentity
  /**
   * `echuan_worker_info` 的只读依赖（可选）。缺省时该工具返回「诊断不可用」，
   * 不影响其余工具；依赖本身也是每次调用惰性读取。
   */
  workerInfo?: WorkerInfoDeps
  /**
   * 不注册的工具名（可选）。只影响本进程注册哪些工具，不改变工具定义与 HTTP 契约；
   * 缺省等价于 `[]`（全量 25 个）。名称的归一化与校验见 `../tool-names.js`。
   */
  exclude?: readonly string[]
}

const inputsSchema = {
  type: 'array',
  items: {
    type: 'object',
    additionalProperties: false,
    properties: {
      material_id: { type: 'string', required: true },
      kind: { type: 'integer', required: true },
      version: { type: 'integer', required: true },
    },
  },
} as const

const claimSchema = {
  oneOf: [
    {
      type: 'object',
      additionalProperties: false,
      properties: {
        kind: { type: 'string', const: 'claimed', required: true },
        work_id: { type: 'string', required: true },
        work_kind: { type: 'string', required: true },
        title: { type: 'string', required: true },
        body_md: { type: 'string', required: true },
        attempt: { type: 'integer', required: true },
        priority: { type: 'integer', required: true },
        lease_id: { type: 'string', required: true },
        lease_until: { type: 'string', required: true },
        ttl_seconds: { type: 'integer', required: true },
        product_id: { type: 'string', required: true },
        product_name: { type: 'string', required: true },
        spec_version: { type: 'integer', required: true },
        spec_digest: { type: 'string', required: true },
        inputs: { ...inputsSchema, required: true },
      },
    },
    {
      type: 'object',
      additionalProperties: false,
      properties: {
        kind: { type: 'string', const: 'no_work', required: true },
      },
    },
  ],
} as const

const renewedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'renewed', required: true },
    work_id: { type: 'string', required: true },
    lease_id: { type: 'string', required: true },
    lease_until: { type: 'string', required: true },
    ttl_seconds: { type: 'integer', required: true },
  },
} as const

const reportedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'reported', required: true },
    work_id: { type: 'string', required: true },
    state: { type: 'string' },
  },
} as const

/** 把平台 `inputs` 投影成工具输出用的浅结构。 */
function projectInputs(inputs: readonly WorkItemInput[] | undefined): ClaimInputValue[] {
  if (!Array.isArray(inputs)) return []
  return inputs.map(input => ({
    material_id: input.material_id,
    kind: input.kind,
    version: input.version,
  }))
}

/**
 * `echuan_work_claim`：领一张卡。
 *
 * 自动带上心跳循环当前内存里的 `worker_id`（平台契约必填）。循环尚未注册时本地拒绝、
 * 零请求：平台无法把卡派给一个不存在的 worker。
 */
export function createClaimTool(options: EchuanToolOptions): ToolDefinition {
  const { client, identity } = options

  return defineTool({
    name: 'echuan_work_claim',
    description:
      '领一张驿传（echuan）work item（POST /v1/work-items/claim）。自动带上本地心跳循环当前的 '
      + 'worker_id（平台必填）；成功返回 work_id、kind、标题、正文、inputs 与租约；'
      + '平台返回 204 表示「当前无活」——这是正常结果，不是错误。',
    parameters: {
      work_id: {
        type: 'string',
        description: '只领指定的 work item（可选；不传则由平台按已声明的 kinds 派单）',
      },
    },
    output: {
      schema: claimSchema,
      render: (_args, value) => (
        value.kind === 'claimed' ? renderClaimed(value) : renderNoWork()
      ),
    },
    async execute(args) {
      try {
        const workerId = identity.workerId
        if (workerId === undefined || workerId === '') {
          throw new EchuanValidationError(
            'worker_id 尚未就绪：本地心跳循环还没注册成功（或 worker.poll.enable=false），'
              + '平台无法把 work item 派给未知 worker。下一步：启用 worker.poll.enable 并声明 '
              + 'worker.kinds，等首轮心跳成功后再领卡；本次没有发出任何请求。',
            'invalid_argument',
            'worker_id',
          )
        }

        const result = await client.claimWorkItem(
          args.work_id === undefined
            ? { worker_id: workerId }
            : { worker_id: workerId, work_id: args.work_id },
        )
        if (result.kind === 'empty') return { kind: 'no_work' as const }

        const { dispatch } = result.data
        const { work_item: item, lease, context } = dispatch
        return {
          kind: 'claimed' as const,
          work_id: dispatch.work_id,
          work_kind: dispatch.kind,
          title: item.title,
          body_md: item.body_md,
          attempt: item.attempt,
          priority: item.priority,
          lease_id: lease.lease_id,
          lease_until: lease.until,
          ttl_seconds: lease.ttl_seconds,
          product_id: context.product.id,
          product_name: context.product.name,
          spec_version: context.spec_version,
          spec_digest: context.spec_digest,
          inputs: projectInputs(dispatch.inputs),
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_work_renew`：手动续租（`POST /v1/work-items/lease`）。 */
export function createRenewTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_work_renew',
    description:
      '手动续租一张已领 work item 的租约（POST /v1/work-items/lease）。'
      + '成功返回新的 lease_id 与到期时间。租约已失效（lease_lost）时旧结果不可再提交，'
      + '需要重新领卡。',
    parameters: {
      work_id: { type: 'string', required: true, description: '工作项编号（必填）' },
      lease_id: { type: 'string', required: true, description: '当前有效的租约 id（必填）' },
    },
    output: {
      schema: renewedSchema,
      render: (_args, value) => renderRenewed(value),
    },
    async execute(args) {
      try {
        const result = await client.renewWorkItemLease({
          work_id: args.work_id,
          lease_id: args.lease_id,
        })
        if (result.kind === 'empty') unexpectedEmpty('续租')

        const lease = result.data
        return {
          kind: 'renewed' as const,
          work_id: args.work_id,
          lease_id: lease.lease_id,
          lease_until: lease.until,
          ttl_seconds: lease.ttl_seconds,
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** 可选字符串：undefined 或全空白视为「未提供」。 */
function optionalText(value: unknown, field: string): string | undefined {
  if (value === undefined) return undefined
  if (typeof value !== 'string') {
    throw new EchuanValidationError(
      `${field} 必须是字符串。下一步：检查调用参数后重试。`,
      'invalid_argument',
      field,
    )
  }
  const trimmed = value.trim()
  return trimmed === '' ? undefined : trimmed
}

/** 必填字符串：保留原文，只拒绝全空白。 */
function optionalBody(value: unknown, field: string): string | undefined {
  if (value === undefined) return undefined
  if (typeof value !== 'string') {
    throw new EchuanValidationError(
      `${field} 必须是字符串。下一步：检查调用参数后重试。`,
      'invalid_argument',
      field,
    )
  }
  return value.trim() === '' ? undefined : value
}

function requiredText(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new EchuanValidationError(
      `${field} 不能为空。下一步：补齐必填字段后重试，不要发空报告。`,
      'invalid_argument',
      field,
    )
  }
  return value
}

function requiredInteger(value: unknown, field: string, minimum?: number): number {
  if (typeof value !== 'number' || !Number.isInteger(value)
    || (minimum !== undefined && value < minimum)) {
    throw new EchuanValidationError(
      `${field} 必须是整数${minimum === undefined ? '' : `（>= ${minimum}）`}，收到 ${String(value)}。`
        + '下一步：按 plan.items 结构传入整数后重试。',
      'invalid_argument',
      field,
    )
  }
  return value
}

/**
 * 校验 breakdown report 的 plan 结构（只做 shape，不判断 seq 连续性 / 依赖环等
 * 业务规则；这些由平台校验并原样透出）。
 */
function requireReportPlan(value: unknown): ReportPlanInput | undefined {
  if (value === undefined || value === null) return undefined
  if (typeof value !== 'object' || Array.isArray(value)) {
    throw new EchuanValidationError(
      'plan 必须是对象，形如 { items: [...] }。下一步：按 echuan_work_report 的参数说明传入。',
      'invalid_argument',
      'plan',
    )
  }
  const items = (value as Record<string, unknown>)['items']
  if (!Array.isArray(items) || items.length === 0) {
    throw new EchuanValidationError(
      'plan.items 至少包含 1 张待拆分的开发卡。下一步：补齐 items（seq_no/title/body_md）后重试。',
      'invalid_argument',
      'plan.items',
    )
  }

  return {
    items: items.map((raw, index): ReportPlanItemInput => {
      const field = `plan.items[${index}]`
      if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) {
        throw new EchuanValidationError(
          `${field} 必须是对象。下一步：按 { seq_no, title, body_md, priority, depends_on } 传入。`,
          'invalid_argument',
          field,
        )
      }
      const record = raw as Record<string, unknown>
      const item: ReportPlanItemInput = {
        seq_no: requiredInteger(record['seq_no'], `${field}.seq_no`, 1),
        title: requiredText(record['title'], `${field}.title`),
        body_md: requiredText(record['body_md'], `${field}.body_md`),
      }
      if (record['priority'] !== undefined) {
        item.priority = requiredInteger(record['priority'], `${field}.priority`)
      }
      if (record['depends_on'] !== undefined) {
        const dependsOn = record['depends_on']
        if (!Array.isArray(dependsOn)) {
          throw new EchuanValidationError(
            `${field}.depends_on 必须是数组（1-based 计划内序号）。`,
            'invalid_argument',
            `${field}.depends_on`,
          )
        }
        item.depends_on = dependsOn.map((dep, depIndex) => requiredInteger(
          dep,
          `${field}.depends_on[${depIndex}]`,
          1,
        ))
      }
      const projectID = optionalText(record['project_id'], `${field}.project_id`)
      if (projectID !== undefined) item.project_id = projectID
      return item
    }),
  }
}

/**
 * `echuan_work_report`：交报告。
 *
 * 非 breakdown 卡必须带 `material_id`；breakdown 卡必须带 `plan`（`material_id`
 * 可省略）。工具只做「至少一个」与 plan shape 的本地校验，不判断 seq 连续性 /
 * 依赖环等业务规则；平台的 404（物料不存在）/409（租约失效/重复提交）/400
 * （plan 业务校验）原样透出。
 */
export function createReportTool(options: EchuanToolOptions): ToolDefinition {
  const { client, identity } = options

  return defineTool({
    name: 'echuan_work_report',
    description:
      '交付一张租约中的 work item（POST /v1/work-items/report），平台把状态转为 submitted。'
      + 'work_id / lease_id 必填；material_id、summary_md 与 plan 至少提供一个：'
      + '非 breakdown 卡用 material_id（已有报告物料）或 summary_md（报告正文，平台落成 kind=3 物料）'
      + '二选一；breakdown 卡必须传 plan（material_id 可省略，summary_md 不允许），'
      + '平台会把计划落成 kind=4 物料并回填 result_material_id。'
      + 'plan.items 每项含 seq_no / title / body_md，可选 priority / depends_on'
      + '（1-based 计划内序号）/ project_id。物料不存在 404，租约失效或重复提交 409，'
      + 'plan 业务校验失败 400，均原样透出且不重试。',
    parameters: {
      work_id: { type: 'string', required: true, description: '工作项编号（必填）' },
      material_id: {
        type: 'string',
        description: '已有交付物料的 material_id；非 breakdown 与 summary_md 二选一，breakdown 可省略',
      },
      summary_md: {
        type: 'string',
        description: '报告正文（Markdown，四段式）。非 breakdown 未提供 material_id 时必填；'
          + '平台落成 kind=3 物料并回填 result_material_id；与 material_id 互斥，breakdown 不要传',
      },
      lease_id: { type: 'string', required: true, description: '当前有效的租约 id（必填）' },
      plan: {
        type: 'object',
        additionalProperties: false,
        description: 'breakdown 卡的拆分计划（非 breakdown 不要传）；items 至少 1 条',
        properties: {
          items: {
            type: 'array',
            required: true,
            items: {
              type: 'object',
              additionalProperties: false,
              properties: {
                seq_no: { type: 'integer', required: true, description: '计划内 1..N 序号' },
                title: { type: 'string', required: true, description: '开发卡标题' },
                body_md: { type: 'string', required: true, description: '开发卡正文（Markdown）' },
                priority: { type: 'integer', description: '优先级（可选，0..99，缺省 0）' },
                depends_on: {
                  type: 'array',
                  items: { type: 'integer' },
                  description: '依赖的计划内序号（可选，1-based，例如 [1]）',
                },
                project_id: {
                  type: 'string',
                  description: '目标项目编号（可选；传了必须等于当前项目）',
                },
              },
            },
          },
        },
      },
    },
    output: {
      schema: reportedSchema,
      render: (_args, value) => renderReported(value),
    },
    async execute(args) {
      try {
        const work_id = requiredText(args.work_id, 'work_id')
        const lease_id = requiredText(args.lease_id, 'lease_id')
        const material_id = optionalText(args.material_id, 'material_id')
        const summary_md = optionalBody(args.summary_md, 'summary_md')
        const plan = requireReportPlan(args.plan)
        if (material_id !== undefined && summary_md !== undefined) {
          throw new EchuanValidationError(
            'material_id 与 summary_md 互斥：已有物料传 material_id，报告正文传 summary_md。'
              + '下一步：二选一后重试，本次没有发出请求。',
            'invalid_argument',
            'summary_md',
          )
        }
        if (plan !== undefined && summary_md !== undefined) {
          throw new EchuanValidationError(
            'breakdown 卡只交 plan，不要传 summary_md。下一步：去掉 summary_md 后重试，本次没有发出请求。',
            'invalid_argument',
            'summary_md',
          )
        }
        if (material_id === undefined && summary_md === undefined && plan === undefined) {
          throw new EchuanValidationError(
            'material_id、summary_md 与 plan 至少提供一个：非 breakdown 卡用 material_id'
              + '（已有物料）或 summary_md（平台落报告物料）交报告，breakdown 卡用 plan 提拆分计划。'
              + '下一步：补齐参数后重试，本次没有发出请求。',
            'invalid_argument',
            'material_id',
          )
        }

        const input = { work_id, lease_id } as {
          work_id: string
          lease_id: string
          material_id?: string
          summary_md?: string
          plan?: ReportPlanInput
        }
        if (material_id !== undefined) input.material_id = material_id
        if (summary_md !== undefined) input.summary_md = summary_md
        if (plan !== undefined) input.plan = plan

        const result = await client.reportWorkItem(input)
        if (result.kind === 'empty') unexpectedEmpty('交报告')

        const detail = result.data
        const value: ReportedValue = { kind: 'reported', work_id }
        if (typeof detail.state === 'string') value.state = detail.state
        // inflight 只跟「平台确认收到报告」走：本地校验失败与请求失败都不会到这里。
        try {
          identity.notifyReported?.(work_id)
        } catch {
          // 回调失败不影响已成功的报告。
        }
        return value
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}
