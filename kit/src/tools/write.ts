/**
 * 三个写工具：领卡 / 续租 / 交报告。
 *
 * 边界（硬约束）：
 * - 本文件只有这三个任务执行动作，**没有**验收/退回/取消/改状态一类的门禁工具；
 * - 工具层不代交报告、不重试、不判断任务成败、不落盘；
 * - worker_id 只有**一个内存来源**——心跳循环（{@link WorkerIdentity} 由 `index.ts`
 *   把 `createWorkerLoop` 的实例传进来），本模块不缓存、不读磁盘、不读 env。
 */
import { defineTool, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import type { ClaimTaskInput, EchuanClient } from '../client.js'
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

/** 领卡成功后的规范值（output.schema 的唯一真实来源）。 */
export interface ClaimedValue {
  kind: 'claimed'
  task_id: string
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
}

/** 204「当前无活」的规范值：正常结果，不是错误。 */
export interface NoWorkValue {
  kind: 'no_work'
}

export type ClaimValue = ClaimedValue | NoWorkValue

/** 续租成功的规范值。 */
export interface RenewedValue {
  kind: 'renewed'
  task_id: string
  lease_id: string
  lease_until: string
  ttl_seconds: number
}

/** 报告提交后的规范值。 */
export interface ReportedValue {
  kind: 'reported'
  task_id: string
  status_name?: string
}

/**
 * 共享身份：worker_id 的唯一内存来源。
 *
 * 生产实现由 `index.ts` 提供（读心跳循环的 `workerId` 只读属性）；
 * 未注册时返回 `undefined`，此时请求体里**完全不出现** `worker_id` 键。
 */
export interface WorkerIdentity {
  readonly workerId: string | undefined
  /**
   * 报告成功的回调（可选）：由 `index.ts` 接到心跳循环的 `notifyReported`，
   * 用于清除该任务的 inflight。只在平台 HTTP 成功后调用。
   */
  notifyReported?(taskId: string): void
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

const claimSchema = {
  oneOf: [
    {
      type: 'object',
      additionalProperties: false,
      properties: {
        kind: { type: 'string', const: 'claimed', required: true },
        task_id: { type: 'string', required: true },
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
    task_id: { type: 'string', required: true },
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
    task_id: { type: 'string', required: true },
    status_name: { type: 'string' },
  },
} as const

/**
 * `echuan_task_claim`：领一张卡。
 *
 * 自动带上心跳循环当前内存里的 `worker_id`；循环尚未注册（`undefined`）时
 * 请求体里完全不出现该键，交由平台按「注册新 worker」的语义处理。
 */
export function createClaimTool(options: EchuanToolOptions): ToolDefinition {
  const { client, identity } = options

  return defineTool({
    name: 'echuan_task_claim',
    description:
      '领一张驿传（echuan）任务卡。自动带上本地心跳循环当前的 worker_id（未注册时不带）。'
      + '成功返回任务编号、标题、正文与租约；平台返回 204 表示「当前无活」——'
      + '这是正常结果，不是错误。',
    parameters: {
      product: { type: 'string', required: true, description: '产品业务 id（必填），例如 APP' },
      task_id: {
        type: 'string',
        description: '只领指定的任务卡（可选；不传则由平台派单）',
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
        const input: ClaimTaskInput = { product: args.product }
        if (args.task_id !== undefined) input.task_id = args.task_id
        const workerId = identity.workerId
        if (workerId !== undefined) input.worker_id = workerId

        const result = await client.claimTask(input)
        if (result.kind === 'empty') return { kind: 'no_work' as const }

        const { task, lease, context } = result.data
        return {
          kind: 'claimed' as const,
          task_id: task.id,
          title: task.title,
          body_md: task.body_md,
          attempt: task.attempt,
          priority: task.priority,
          lease_id: lease.lease_id,
          lease_until: lease.until,
          ttl_seconds: lease.ttl_seconds,
          product_id: context.product.id,
          product_name: context.product.name,
          spec_version: context.spec_version,
          spec_digest: context.spec_digest,
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_task_renew`：手动续租（`POST /v1/tasks/heartbeat?id=`）。 */
export function createRenewTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_task_renew',
    description:
      '手动续租一张已领任务卡的租约（POST /v1/tasks/heartbeat?id=）。'
      + '成功返回新的 lease_id 与到期时间。租约已失效（lease_lost）时旧结果不可再提交，'
      + '需要重新领卡。',
    parameters: {
      task_id: { type: 'string', required: true, description: '任务编号（必填，走 query `?id=`）' },
      lease_id: { type: 'string', required: true, description: '当前有效的租约 id（必填）' },
    },
    output: {
      schema: renewedSchema,
      render: (_args, value) => renderRenewed(value),
    },
    async execute(args) {
      try {
        const result = await client.renewLease({ id: args.task_id, lease_id: args.lease_id })
        if (result.kind === 'empty') unexpectedEmpty('续租')

        const lease = result.data
        return {
          kind: 'renewed' as const,
          task_id: args.task_id,
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

/** 报告四段式的固定提示（本地校验用，也用于模型提示）。 */
export const REPORT_STRUCTURE_HINT =
  '必须按四段写：①改了什么（改动清单与关键决策）'
  + '②怎么自检（跑了什么命令/用例，结果如何）'
  + '③git 交付（分支 task/<id>、commit、远程分支与 ls-remote 校验结果、PR/compare 链接）'
  + '④遗留风险（未覆盖的边界、已知问题、需要人工确认的点）。'
  + '未 push 不得报完成。'

/**
 * `echuan_task_report`：交报告。
 *
 * 入口先做本地校验：`summary_md` 去空白后为空 → 直接拒绝，**不发任何请求**。
 */
export function createReportTool(options: EchuanToolOptions): ToolDefinition {
  const { client, identity } = options

  return defineTool({
    name: 'echuan_task_report',
    description:
      '提交任务卡的四段式报告并把任务转待验收（POST /v1/tasks/report?id=）。'
      + 'summary_md 必填且不能为空白；内容必须覆盖「改了什么 / 怎么自检 / git 交付 / 遗留风险」四项，'
      + '未 push 不得报完成。',
    parameters: {
      task_id: { type: 'string', required: true, description: '任务编号（必填，走 query `?id=`）' },
      lease_id: { type: 'string', required: true, description: '当前有效的租约 id（必填）' },
      summary_md: {
        type: 'string',
        required: true,
        description: `报告正文（必填，Markdown）。${REPORT_STRUCTURE_HINT}`,
      },
    },
    output: {
      schema: reportedSchema,
      render: (_args, value) => renderReported(value),
    },
    async execute(args) {
      try {
        if (args.summary_md.trim() === '') {
          throw new EchuanValidationError(
            `报告内容 summary_md 为空或全空白，已拒绝提交（未发出任何请求）。${REPORT_STRUCTURE_HINT}`,
            'invalid_argument',
            'summary_md',
          )
        }

        const result = await client.reportTask({
          id: args.task_id,
          lease_id: args.lease_id,
          summary_md: args.summary_md,
        })
        if (result.kind === 'empty') unexpectedEmpty('交报告')

        const detail = result.data
        const value: ReportedValue = { kind: 'reported', task_id: args.task_id }
        if (typeof detail.status_name === 'string') value.status_name = detail.status_name
        // inflight 只跟「平台确认收到报告」走：本地校验失败与请求失败都不会到这里。
        try {
          identity.notifyReported?.(args.task_id)
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
