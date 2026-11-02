/**
 * 五个辅助写工具：work item 增 / 改 / 删、项目改标题、产品创建。
 *
 * 边界（硬约束）：
 * - **没有**审批 / 决策 / 取消动作：只做「建卡、改卡、删卡、改标题、建产品」；
 * - 入参在本地先校验，不合法直接 {@link EchuanValidationError}，**零请求**；
 * - 不自动重试；平台 400 / 404 / 409 原样透出（含 `details` 的字段原因）；
 * - 不落盘、不缓存。
 */
import { defineTool, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import type {
  CreateProductInput,
  CreateWorkItemInput,
  UpdateProjectInput,
  UpdateWorkItemInput,
  WorkItemDetail,
  WorkItemKind,
} from '../client.js'
import { EchuanValidationError } from '../errors.js'
import {
  asNumber,
  asString,
  textBlock,
  toToolError,
  workItemStateLabel,
  type TextContent,
} from './render.js'
import type { EchuanToolOptions } from './write.js'

/* ------------------------------------------------------------------ *
 * 规范值
 * ------------------------------------------------------------------ */

export interface WorkItemCreatedValue {
  kind: 'work_item_created'
  work_id: string
  work_kind: string
  seq_no?: number
  title: string
  state?: string
  priority?: number
  depends_on: string[]
}

export interface WorkItemUpdatedValue {
  kind: 'work_item_updated'
  work_id: string
  title?: string
  work_kind?: string
  state?: string
  priority?: number
}

export interface WorkItemDeletedValue {
  kind: 'work_item_deleted'
  work_id: string
}

export interface ProjectUpdatedValue {
  kind: 'project_updated'
  project_id: string
  title: string
  status?: number
  status_name?: string
  version?: number
}

export interface ProductCreatedValue {
  kind: 'product_created'
  product_id: string
  name: string
  git_url: string
}

/* ------------------------------------------------------------------ *
 * 输出 schema
 * ------------------------------------------------------------------ */

const optionalInteger = { type: 'integer' } as const
const optionalString = { type: 'string' } as const

const workItemCreatedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'work_item_created', required: true },
    work_id: { type: 'string', required: true },
    work_kind: { type: 'string', required: true },
    seq_no: optionalInteger,
    title: { type: 'string', required: true },
    state: optionalString,
    priority: optionalInteger,
    depends_on: { type: 'array', items: { type: 'string' }, required: true },
  },
} as const

const workItemUpdatedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'work_item_updated', required: true },
    work_id: { type: 'string', required: true },
    title: optionalString,
    work_kind: optionalString,
    state: optionalString,
    priority: optionalInteger,
  },
} as const

const workItemDeletedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'work_item_deleted', required: true },
    work_id: { type: 'string', required: true },
  },
} as const

const projectUpdatedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'project_updated', required: true },
    project_id: { type: 'string', required: true },
    title: { type: 'string', required: true },
    status: optionalInteger,
    status_name: optionalString,
    version: optionalInteger,
  },
} as const

const productCreatedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'product_created', required: true },
    product_id: { type: 'string', required: true },
    name: { type: 'string', required: true },
    git_url: { type: 'string', required: true },
  },
} as const

/* ------------------------------------------------------------------ *
 * 本地校验
 * ------------------------------------------------------------------ */

function requireText(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new EchuanValidationError(
      `${field} 不能为空。下一步：补齐参数后重试，本次没有发出任何请求。`,
      'invalid_argument',
      field,
    )
  }
  return value
}

function requireBody(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new EchuanValidationError(
      `${field} 不能为空或全空白。下一步：传入 Markdown 正文后重试，本次没有发出任何请求。`,
      'invalid_argument',
      field,
    )
  }
  return value
}

function requireKind(value: unknown): WorkItemKind {
  if (value !== 'requirement' && value !== 'design' && value !== 'breakdown' && value !== 'dev') {
    throw new EchuanValidationError(
      `kind 必须是 requirement / design / breakdown / dev 之一，收到 ${String(value)}。`
        + '本次没有发出任何请求。',
      'invalid_argument',
      'kind',
    )
  }
  return value
}

function requirePriority(value: unknown): number {
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    throw new EchuanValidationError(
      `priority 必须是整数，收到 ${String(value)}。本次没有发出任何请求。`,
      'invalid_argument',
      'priority',
    )
  }
  return value
}

function requireDependsOn(value: unknown): string[] {
  if (!Array.isArray(value)) {
    throw new EchuanValidationError(
      'depends_on 必须是 work_id 数组（不是批内序号）。本次没有发出任何请求。',
      'invalid_argument',
      'depends_on',
    )
  }
  return value.map((item, index) => requireText(item, `depends_on[${index}]`))
}

/** 详情里的依赖字段，用于返回；平台可能缺省。 */
function dependsOnOf(detail: WorkItemDetail): string[] {
  const raw = detail.depends_on
  return Array.isArray(raw) ? raw.filter((id): id is string => typeof id === 'string') : []
}

function workIdOf(detail: WorkItemDetail): string {
  return asString(detail.work_id) ?? asString(detail.id) ?? ''
}

/* ------------------------------------------------------------------ *
 * 渲染
 * ------------------------------------------------------------------ */

export function renderWorkItemCreated(value: WorkItemCreatedValue): TextContent[] {
  const seq = value.seq_no === undefined ? '' : `（#${value.seq_no}）`
  const priority = value.priority === undefined ? '未知' : String(value.priority)
  const depends = value.depends_on.length === 0 ? '无' : value.depends_on.join('、')
  return [textBlock([
    `✅ 工作项已创建：${value.work_id}${seq}`,
    `- 类别：${value.work_kind}`,
    `- 标题：${value.title}`,
    `- 优先级：${priority}`,
    `- 依赖：${depends}`,
    `- 状态：${workItemStateLabel(value.state)}`,
    '下一步：需要调整时用 `echuan_work_update`；要撤掉用 `echuan_work_delete`。',
  ].join('\n'))]
}

export function renderWorkItemUpdated(value: WorkItemUpdatedValue): TextContent[] {
  const state = value.state === undefined ? '' : `，状态 ${workItemStateLabel(value.state)}`
  return [textBlock([
    `✅ 工作项已修改：${value.work_id}${state}`,
    ...(value.title === undefined ? [] : [`- 标题：${value.title}`]),
    ...(value.work_kind === undefined ? [] : [`- 类别：${value.work_kind}`]),
    ...(value.priority === undefined ? [] : [`- 优先级：${value.priority}`]),
    '下一步：若该卡刚被驳回，驳回提示会随本次写入清空，可确认后重新领卡。',
  ].join('\n'))]
}

export function renderWorkItemDeleted(value: WorkItemDeletedValue): TextContent[] {
  return [textBlock([
    `✅ 工作项已删除：${value.work_id}`,
    '下一步：需要补卡时用 `echuan_work_create`（或重新提交计划批次）。',
  ].join('\n'))]
}

export function renderProjectUpdated(value: ProjectUpdatedValue): TextContent[] {
  return [textBlock([
    `✅ 项目标题已修改：${value.project_id}`,
    `- 新标题：${value.title}`,
    ...(value.status_name === undefined ? [] : [`- 状态（未变）：${value.status_name}`]),
    '下一步：状态与需求/方案指针不受影响，可继续原有流程。',
  ].join('\n'))]
}

export function renderProductCreated(value: ProductCreatedValue): TextContent[] {
  return [textBlock([
    `✅ 产品已创建：${value.product_id}`,
    `- 名称：${value.name}`,
    `- 仓库：${value.git_url}`,
    '下一步：用 `echuan_project_create` 带该 product_id 建项目。',
  ].join('\n'))]
}

/* ------------------------------------------------------------------ *
 * 工具定义
 * ------------------------------------------------------------------ */

/** `echuan_work_create`：在规划中的项目里新增一张卡。 */
export function createWorkItemCreateTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_work_create',
    description:
      '在驿传（echuan）项目中新增一个 work item（POST /v1/work-items）。'
      + '输入 project_id、kind、标题与正文，可选优先级与依赖 work_id；'
      + '返回 work_id / kind / state / seq_no。',
    parameters: {
      project_id: { type: 'string', required: true, description: '项目编号（必填）' },
      kind: {
        type: 'string',
        required: true,
        description: '工作项类别（必填）：requirement / design / breakdown / dev',
      },
      title: { type: 'string', required: true, description: '工作项标题（必填）' },
      body_md: { type: 'string', required: true, description: '工作项正文（必填，Markdown）' },
      priority: { type: 'integer', description: '优先级（可选，整数，平台范围 -1000..1000）' },
      depends_on: {
        type: 'array',
        items: { type: 'string' },
        description: '依赖的 work_id 数组（可选，不是批内序号）',
      },
    },
    output: {
      schema: workItemCreatedSchema,
      render: (_args, value) => renderWorkItemCreated(value),
    },
    async execute(args) {
      try {
        const input: CreateWorkItemInput = {
          project_id: requireText(args.project_id, 'project_id'),
          kind: requireKind(args.kind),
          title: requireText(args.title, 'title'),
          body_md: requireBody(args.body_md, 'body_md'),
        }
        if (args.priority !== undefined) input.priority = requirePriority(args.priority)
        if (args.depends_on !== undefined) input.depends_on = requireDependsOn(args.depends_on)

        const result = await client.createWorkItem(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对建卡返回 204（无内容），没有给出 work_id。'
              + '下一步：先用 `echuan_work_list` 确认是否已创建，不要原样重试。',
            'unexpected_empty',
            'work_item',
          )
        }
        const detail = result.data
        const seqNo = asNumber(detail.seq_no)
        const state = asString(detail.state)
        const priority = asNumber(detail.priority)
        const kind = asString(detail.kind) ?? input.kind
        return {
          kind: 'work_item_created' as const,
          work_id: workIdOf(detail),
          work_kind: kind,
          title: asString(detail.title) ?? input.title,
          depends_on: dependsOnOf(detail),
          ...(seqNo === undefined ? {} : { seq_no: seqNo }),
          ...(state === undefined ? {} : { state }),
          ...(priority === undefined ? {} : { priority }),
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_work_update`：改标题 / 正文 / 优先级（至少一项，空白不算有效修改）。 */
export function createWorkItemUpdateTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_work_update',
    description:
      '修改驿传 work item 的标题、正文或优先级（PATCH /v1/work-items?id=）。'
      + '三个字段至少传一个且不能是空白；平台按状态校验，不可改时 400/409 原样透出。',
    parameters: {
      work_id: { type: 'string', required: true, description: '工作项编号（必填，走 query `?id=`）' },
      title: { type: 'string', description: '新标题（可选，非空白）' },
      body_md: { type: 'string', description: '新正文（可选，非空白，Markdown）' },
      priority: { type: 'integer', description: '新优先级（可选，整数）' },
    },
    output: {
      schema: workItemUpdatedSchema,
      render: (_args, value) => renderWorkItemUpdated(value),
    },
    async execute(args) {
      try {
        const input: UpdateWorkItemInput = { id: requireText(args.work_id, 'work_id') }
        if (args.title !== undefined) input.title = requireText(args.title, 'title')
        if (args.body_md !== undefined) input.body_md = requireBody(args.body_md, 'body_md')
        if (args.priority !== undefined) input.priority = requirePriority(args.priority)
        if (input.title === undefined && input.body_md === undefined && input.priority === undefined) {
          throw new EchuanValidationError(
            'title / body_md / priority 至少需要提供一个（空 patch 不会发请求）。'
              + '下一步：补上要改的字段后重试。',
            'invalid_argument',
            'body',
          )
        }

        const result = await client.updateWorkItem(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对改卡返回 204（无内容），没有回包可核对。'
              + '下一步：用 `echuan_work_get` 确认修改结果，不要原样重试。',
            'unexpected_empty',
            'work_item',
          )
        }
        const detail = result.data
        const title = asString(detail.title)
        const kind = asString(detail.kind)
        const state = asString(detail.state)
        const priority = asNumber(detail.priority)
        return {
          kind: 'work_item_updated' as const,
          work_id: args.work_id,
          ...(title === undefined ? {} : { title }),
          ...(kind === undefined ? {} : { work_kind: kind }),
          ...(state === undefined ? {} : { state }),
          ...(priority === undefined ? {} : { priority }),
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_work_delete`：删一张未被领取的卡。 */
export function createWorkItemDeleteTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_work_delete',
    description:
      '删除一个从未被领取的 blocked work item（DELETE /v1/work-items?id=）。'
      + '成功为 204；卡已被领取或不可删时平台 409 原样透出，不要重试。',
    parameters: {
      work_id: { type: 'string', required: true, description: '工作项编号（必填，走 query `?id=`）' },
    },
    output: {
      schema: workItemDeletedSchema,
      render: (_args, value) => renderWorkItemDeleted(value),
    },
    async execute(args) {
      try {
        const workId = requireText(args.work_id, 'work_id')
        const result = await client.deleteWorkItem({ id: workId })
        if (result.kind !== 'empty') {
          throw new EchuanValidationError(
            `平台对删卡返回 ${result.status}（预期 204）。下一步：用 \`echuan_work_get\` 确认卡是否还在，不要原样重试。`,
            'invalid_response',
            'work_item',
          )
        }
        return { kind: 'work_item_deleted' as const, work_id: workId }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_project_update`：只改项目标题（不暴露 status / product_id）。 */
export function createProjectUpdateTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_project_update',
    description:
      '修改驿传项目标题（PATCH /v1/projects?id=），只暴露 title 一个字段。'
      + '状态与需求/方案指针不受影响；平台错误原样透出。',
    parameters: {
      project_id: { type: 'string', required: true, description: '项目编号（必填，走 query `?id=`）' },
      title: { type: 'string', required: true, description: '新标题（必填，非空白）' },
    },
    output: {
      schema: projectUpdatedSchema,
      render: (_args, value) => renderProjectUpdated(value),
    },
    async execute(args) {
      try {
        const input: UpdateProjectInput = {
          id: requireText(args.project_id, 'project_id'),
          title: requireText(args.title, 'title'),
        }
        const result = await client.updateProject(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对改标题返回 204（无内容）。下一步：用 `echuan_project_get` 确认标题，不要原样重试。',
            'unexpected_empty',
            'project',
          )
        }
        const detail = result.data
        const status = asNumber(detail.status)
        const statusName = asString(detail.status_name)
        const version = asNumber(detail.version)
        return {
          kind: 'project_updated' as const,
          project_id: asString(detail.project_id) ?? asString(detail.id) ?? input.id,
          title: asString(detail.title) ?? input.title,
          ...(status === undefined ? {} : { status }),
          ...(statusName === undefined ? {} : { status_name: statusName }),
          ...(version === undefined ? {} : { version }),
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_product_create`：建产品。 */
export function createProductCreateTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_product_create',
    description:
      '创建一个驿传（echuan）产品（POST /v1/products）。product_id / name / git_url 必填，'
      + 'description / license 可选；创建后即可用该 product_id 建项目。',
    parameters: {
      product_id: { type: 'string', required: true, description: '产品业务 id（必填），例如 APP' },
      name: { type: 'string', required: true, description: '产品名称（必填）' },
      git_url: { type: 'string', required: true, description: '代码仓库地址（必填）' },
      description: { type: 'string', description: '产品描述（可选）' },
      license: { type: 'string', description: '许可证（可选）' },
    },
    output: {
      schema: productCreatedSchema,
      render: (_args, value) => renderProductCreated(value),
    },
    async execute(args) {
      try {
        const input: CreateProductInput = {
          product_id: requireText(args.product_id, 'product_id'),
          name: requireText(args.name, 'name'),
          git_url: requireText(args.git_url, 'git_url'),
        }
        if (args.description !== undefined) input.description = args.description
        if (args.license !== undefined) input.license = args.license

        const result = await client.createProduct(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对建产品返回 204（无内容），没有回包可核对。'
              + '下一步：先用 `echuan_product_list` 确认是否已创建，不要原样重试。',
            'unexpected_empty',
            'product',
          )
        }
        const detail = result.data
        return {
          kind: 'product_created' as const,
          product_id: asString(detail.product_id) ?? asString(detail.id) ?? input.product_id,
          name: asString(detail.name) ?? input.name,
          git_url: asString(detail.git_url) ?? input.git_url,
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}
