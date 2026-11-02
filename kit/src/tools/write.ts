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
import type { EchuanClient, WorkItemInput } from '../client.js'
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
  /** 平台 HTTP 客户端（TOOL_METHODS 里的 20 个方法）。 */
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
   * 缺省等价于 `[]`（全量 23 个）。名称的归一化与校验见 `../tool-names.js`。
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

/**
 * `echuan_work_report`：交报告。
 *
 * 平台契约要求 `material_id`（报告正文已作为物料存在）：工具不代传正文、不做本地
 * 内容校验，只把三个编号原样交给平台；Platform 的 404（物料不存在）/409（租约失效）
 * 原样透出。
 */
export function createReportTool(options: EchuanToolOptions): ToolDefinition {
  const { client, identity } = options

  return defineTool({
    name: 'echuan_work_report',
    description:
      '用一份已有物料交付一张租约中的 work item（POST /v1/work-items/report），'
      + '平台把状态转为 submitted。work_id / material_id / lease_id 均必填；'
      + 'material_id 不存在时平台 404，租约失效时 409，均不重试。',
    parameters: {
      work_id: { type: 'string', required: true, description: '工作项编号（必填）' },
      material_id: { type: 'string', required: true, description: '交付物料的 material_id（必填）' },
      lease_id: { type: 'string', required: true, description: '当前有效的租约 id（必填）' },
    },
    output: {
      schema: reportedSchema,
      render: (_args, value) => renderReported(value),
    },
    async execute(args) {
      try {
        const result = await client.reportWorkItem({
          work_id: args.work_id,
          material_id: args.material_id,
          lease_id: args.lease_id,
        })
        if (result.kind === 'empty') unexpectedEmpty('交报告')

        const detail = result.data
        const value: ReportedValue = { kind: 'reported', work_id: args.work_id }
        if (typeof detail.state === 'string') value.state = detail.state
        // inflight 只跟「平台确认收到报告」走：本地校验失败与请求失败都不会到这里。
        try {
          identity.notifyReported?.(args.work_id)
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
