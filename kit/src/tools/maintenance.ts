/**
 * 五个辅助写工具：单卡增 / 改 / 删、项目改标题、产品创建。
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
  CreateTaskInput,
  TaskDetail,
  UpdateProjectInput,
  UpdateTaskInput,
} from '../client.js'
import { EchuanValidationError } from '../errors.js'
import {
  asNumber,
  asString,
  textBlock,
  toToolError,
  type TextContent,
} from './render.js'
import type { EchuanToolOptions } from './write.js'

/* ------------------------------------------------------------------ *
 * 规范值
 * ------------------------------------------------------------------ */

export interface TaskCreatedValue {
  kind: 'task_created'
  task_id: string
  seq_no?: number
  title: string
  status?: number
  status_name?: string
  priority?: number
  depends_on: string[]
}

export interface TaskUpdatedValue {
  kind: 'task_updated'
  task_id: string
  title?: string
  status?: number
  status_name?: string
  priority?: number
}

export interface TaskDeletedValue {
  kind: 'task_deleted'
  task_id: string
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

const taskCreatedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'task_created', required: true },
    task_id: { type: 'string', required: true },
    seq_no: optionalInteger,
    title: { type: 'string', required: true },
    status: optionalInteger,
    status_name: optionalString,
    priority: optionalInteger,
    depends_on: { type: 'array', items: { type: 'string' }, required: true },
  },
} as const

const taskUpdatedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'task_updated', required: true },
    task_id: { type: 'string', required: true },
    title: optionalString,
    status: optionalInteger,
    status_name: optionalString,
    priority: optionalInteger,
  },
} as const

const taskDeletedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'task_deleted', required: true },
    task_id: { type: 'string', required: true },
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
      'depends_on 必须是任务 id 数组（不是批内序号）。本次没有发出任何请求。',
      'invalid_argument',
      'depends_on',
    )
  }
  return value.map((item, index) => requireText(item, `depends_on[${index}]`))
}

/** 任务 id 列表里的字段，用于返回；平台可能缺省。 */
function dependsOnOf(detail: TaskDetail): string[] {
  const raw = detail.depends_on
  return Array.isArray(raw) ? raw.filter((id): id is string => typeof id === 'string') : []
}

function taskIdOf(detail: TaskDetail): string {
  return asString(detail.task_id) ?? asString(detail.id) ?? ''
}

/* ------------------------------------------------------------------ *
 * 渲染
 * ------------------------------------------------------------------ */

export function renderTaskCreated(value: TaskCreatedValue): TextContent[] {
  const seq = value.seq_no === undefined ? '' : `（#${value.seq_no}）`
  const priority = value.priority === undefined ? '未知' : String(value.priority)
  const depends = value.depends_on.length === 0 ? '无' : value.depends_on.join('、')
  return [textBlock([
    `✅ 任务已创建：${value.task_id}${seq}`,
    `- 标题：${value.title}`,
    `- 优先级：${priority}`,
    `- 依赖：${depends}`,
    '下一步：需要调整时用 `echuan_task_update`；要撤掉用 `echuan_task_delete`。',
  ].join('\n'))]
}

export function renderTaskUpdated(value: TaskUpdatedValue): TextContent[] {
  const status = value.status_name === undefined ? '' : `，状态 ${value.status_name}`
  return [textBlock([
    `✅ 任务已修改：${value.task_id}${status}`,
    ...(value.title === undefined ? [] : [`- 标题：${value.title}`]),
    ...(value.priority === undefined ? [] : [`- 优先级：${value.priority}`]),
    '下一步：若该卡刚被驳回，驳回提示会随本次写入清空，可确认后重新领卡。',
  ].join('\n'))]
}

export function renderTaskDeleted(value: TaskDeletedValue): TextContent[] {
  return [textBlock([
    `✅ 任务已删除：${value.task_id}`,
    '下一步：需要补卡时用 `echuan_task_create`（或重新提交任务计划）。',
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

/** `echuan_task_create`：在规划中的项目里新增一张卡。 */
export function createTaskCreateTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_task_create',
    description:
      '在驿传（echuan）项目中新增一张任务卡（POST /v1/tasks?project_id=）。'
      + '输入标题、正文，可选优先级与依赖任务 id；返回 task_id / seq_no / 状态。',
    parameters: {
      project_id: { type: 'string', required: true, description: '项目编号（必填，走 query `?project_id=`）' },
      title: { type: 'string', required: true, description: '任务标题（必填）' },
      body_md: { type: 'string', required: true, description: '任务正文（必填，Markdown）' },
      priority: { type: 'integer', description: '优先级（可选，整数，平台范围 -1000..1000）' },
      depends_on: {
        type: 'array',
        items: { type: 'string' },
        description: '依赖的任务 id 数组（可选，不是批内序号）',
      },
    },
    output: {
      schema: taskCreatedSchema,
      render: (_args, value) => renderTaskCreated(value),
    },
    async execute(args) {
      try {
        const input: CreateTaskInput = {
          project_id: requireText(args.project_id, 'project_id'),
          title: requireText(args.title, 'title'),
          body_md: requireBody(args.body_md, 'body_md'),
        }
        if (args.priority !== undefined) input.priority = requirePriority(args.priority)
        if (args.depends_on !== undefined) input.depends_on = requireDependsOn(args.depends_on)

        const result = await client.createTask(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对建卡返回 204（无内容），没有给出 task_id。'
              + '下一步：先用 `echuan_task_list` 确认是否已创建，不要原样重试。',
            'unexpected_empty',
            'task',
          )
        }
        const detail = result.data
        const seqNo = asNumber(detail.seq_no)
        const status = asNumber(detail.status)
        const statusName = asString(detail.status_name)
        const priority = asNumber(detail.priority)
        return {
          kind: 'task_created' as const,
          task_id: taskIdOf(detail),
          title: asString(detail.title) ?? input.title,
          depends_on: dependsOnOf(detail),
          ...(seqNo === undefined ? {} : { seq_no: seqNo }),
          ...(status === undefined ? {} : { status }),
          ...(statusName === undefined ? {} : { status_name: statusName }),
          ...(priority === undefined ? {} : { priority }),
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_task_update`：改标题 / 正文 / 优先级（至少一项，空白不算有效修改）。 */
export function createTaskUpdateTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_task_update',
    description:
      '修改驿传任务卡的标题、正文或优先级（PATCH /v1/tasks?id=）。'
      + '三个字段至少传一个且不能是空白；平台按任务状态校验，不可改时 400/409 原样透出。',
    parameters: {
      task_id: { type: 'string', required: true, description: '任务编号（必填，走 query `?id=`）' },
      title: { type: 'string', description: '新标题（可选，非空白）' },
      body_md: { type: 'string', description: '新正文（可选，非空白，Markdown）' },
      priority: { type: 'integer', description: '新优先级（可选，整数）' },
    },
    output: {
      schema: taskUpdatedSchema,
      render: (_args, value) => renderTaskUpdated(value),
    },
    async execute(args) {
      try {
        const input: UpdateTaskInput = { id: requireText(args.task_id, 'task_id') }
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

        const result = await client.updateTask(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对改卡返回 204（无内容），没有回包可核对。'
              + '下一步：用 `echuan_task_get` 确认修改结果，不要原样重试。',
            'unexpected_empty',
            'task',
          )
        }
        const detail = result.data
        const title = asString(detail.title)
        const status = asNumber(detail.status)
        const statusName = asString(detail.status_name)
        const priority = asNumber(detail.priority)
        return {
          kind: 'task_updated' as const,
          task_id: args.task_id,
          ...(title === undefined ? {} : { title }),
          ...(status === undefined ? {} : { status }),
          ...(statusName === undefined ? {} : { status_name: statusName }),
          ...(priority === undefined ? {} : { priority }),
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_task_delete`：删一张未领取的待办卡。 */
export function createTaskDeleteTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_task_delete',
    description:
      '删除一张未领取的待办任务卡（DELETE /v1/tasks?id=）。'
      + '成功为 204；卡已被领取或不可删时平台 409 原样透出，不要重试。',
    parameters: {
      task_id: { type: 'string', required: true, description: '任务编号（必填，走 query `?id=`）' },
    },
    output: {
      schema: taskDeletedSchema,
      render: (_args, value) => renderTaskDeleted(value),
    },
    async execute(args) {
      try {
        const taskId = requireText(args.task_id, 'task_id')
        const result = await client.deleteTask({ id: taskId })
        if (result.kind !== 'empty') {
          throw new EchuanValidationError(
            `平台对删卡返回 ${result.status}（预期 204）。下一步：用 \`echuan_task_get\` 确认卡是否还在，不要原样重试。`,
            'invalid_response',
            'task',
          )
        }
        return { kind: 'task_deleted' as const, task_id: taskId }
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
