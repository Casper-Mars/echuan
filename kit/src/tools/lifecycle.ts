/**
 * 六个生命周期写工具：建项目 / 写需求 / 提交需求 / 建方案 / 修订方案 / 批量建卡。
 *
 * 边界（硬约束）：
 * - 只负责「起草 → 提交评审」这一段，**没有任何审批/驳回/取消动作**；
 * - 入参在本地先校验（缺字段、空白、空 `tasks`、非正 `version`），
 *   不合法时直接抛 {@link EchuanValidationError}，**不发任何请求**；
 * - 不自动重试、不落盘、不缓存；错误体（409 / 400 / 413）原样透出；
 * - 返回结构稳定，带下一步要用的 id（project_id / spec_id / 任务 id）。
 */
import { defineTool, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import type {
  CreateProjectInput,
  CreateSpecInput,
  PlanTaskInput,
  PlanResponse,
  Project,
  ProjectDetail,
  ReviseSpecInput,
  SpecDocument,
  SubmitPlanInput,
} from '../client.js'
import { EchuanValidationError } from '../errors.js'
import {
  asNumber,
  asString,
  projectStatusLabel,
  textBlock,
  toToolError,
  type TextContent,
} from './render.js'
import type { EchuanToolOptions } from './write.js'

/* ------------------------------------------------------------------ *
 * 规范值
 * ------------------------------------------------------------------ */

export interface ProjectCreatedValue {
  kind: 'project_created'
  project_id: string
  title: string
  status?: number
  status_name?: string
  version?: number
}

export interface RequirementWrittenValue {
  kind: 'requirement_written'
  project_id: string
  material_id?: string
  material_version?: number
  size?: number
}

export interface ProjectSubmittedValue {
  kind: 'project_submitted'
  project_id: string
  status?: number
  status_name?: string
  version?: number
}

export interface SpecCreatedValue {
  kind: 'spec_created'
  project_id: string
  spec_id: string
  title: string
  version: number
  material_id?: string
}

export interface SpecRevisedValue {
  kind: 'spec_revised'
  project_id: string
  spec_id: string
  title: string
  version: number
  material_id?: string
}

export interface PlanTaskValue {
  task_id: string
  seq_no?: number
  title: string
  priority?: number
  status_name?: string
  depends_on: string[]
}

export interface PlanSubmittedValue {
  kind: 'plan_submitted'
  project_id: string
  status?: number
  status_name?: string
  version?: number
  tasks: PlanTaskValue[]
}

/* ------------------------------------------------------------------ *
 * 输出 schema
 * ------------------------------------------------------------------ */

const nullableInteger = { type: 'integer' } as const
const nullableString = { type: 'string' } as const

const projectCreatedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'project_created', required: true },
    project_id: { type: 'string', required: true },
    title: { type: 'string', required: true },
    status: nullableInteger,
    status_name: nullableString,
    version: nullableInteger,
  },
} as const

const requirementWrittenSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'requirement_written', required: true },
    project_id: { type: 'string', required: true },
    material_id: nullableString,
    material_version: nullableInteger,
    size: nullableInteger,
  },
} as const

const projectSubmittedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'project_submitted', required: true },
    project_id: { type: 'string', required: true },
    status: nullableInteger,
    status_name: nullableString,
    version: nullableInteger,
  },
} as const

const specSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', required: true },
    project_id: { type: 'string', required: true },
    spec_id: { type: 'string', required: true },
    title: { type: 'string', required: true },
    version: { type: 'integer', required: true },
    material_id: nullableString,
  },
} as const

const planSubmittedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'plan_submitted', required: true },
    project_id: { type: 'string', required: true },
    status: nullableInteger,
    status_name: nullableString,
    version: nullableInteger,
    tasks: {
      type: 'array',
      required: true,
      items: {
        type: 'object',
        additionalProperties: false,
        properties: {
          task_id: { type: 'string', required: true },
          seq_no: nullableInteger,
          title: { type: 'string', required: true },
          priority: nullableInteger,
          status_name: nullableString,
          depends_on: { type: 'array', items: { type: 'string' }, required: true },
        },
      },
    },
  },
} as const

/* ------------------------------------------------------------------ *
 * 本地校验
 * ------------------------------------------------------------------ */

/** 必填字符串：缺失/非字符串/全空白 → 本地拒绝，零请求。 */
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

/** 必填正文：保留原始空白，只拒绝全空白。 */
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

/** 可选字符串：缺失 → undefined；给了就不能是空白。 */
function optionalText(value: unknown, field: string): string | undefined {
  if (value === undefined || value === null) return undefined
  return requireText(value, field)
}

/** 版本号必须是 >0 的整数（缺省不合法，避免漏带 If-Match）。 */
function requireVersion(value: unknown): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value <= 0) {
    throw new EchuanValidationError(
      `version 必须是大于 0 的整数，收到 ${String(value)}。`
        + '下一步：传入方案当前版本号（走 If-Match 头），本次没有发出任何请求。',
      'invalid_argument',
      'version',
    )
  }
  return value
}

/** 批量建卡的入参校验；`depends_on` 为 1-based 批内序号，原样透传。 */
function requireTasks(value: unknown): PlanTaskInput[] {
  if (!Array.isArray(value) || value.length === 0) {
    throw new EchuanValidationError(
      'tasks 不能为空。下一步：至少给一张卡（title + body_md），本次没有发出任何请求。',
      'invalid_argument',
      'tasks',
    )
  }

  return value.map((item, index) => {
    if (item === null || typeof item !== 'object' || Array.isArray(item)) {
      throw new EchuanValidationError(
        `tasks[${index}] 必须是对象。下一步：按 { title, body_md, priority?, depends_on? } 传参。`,
        'invalid_argument',
        'tasks',
      )
    }
    const record = item as Record<string, unknown>
    const task: PlanTaskInput = {
      title: requireText(record['title'], `tasks[${index}].title`),
      body_md: requireBody(record['body_md'], `tasks[${index}].body_md`),
    }
    if (record['priority'] !== undefined) {
      const priority = record['priority']
      if (typeof priority !== 'number' || !Number.isInteger(priority)) {
        throw new EchuanValidationError(
          `tasks[${index}].priority 必须是整数，收到 ${String(priority)}。`,
          'invalid_argument',
          'tasks',
        )
      }
      task.priority = priority
    }
    if (record['depends_on'] !== undefined) {
      const dependsOn = record['depends_on']
      if (!Array.isArray(dependsOn)) {
        throw new EchuanValidationError(
          `tasks[${index}].depends_on 必须是数组（1-based 批内序号）。`,
          'invalid_argument',
          'tasks',
        )
      }
      task.depends_on = dependsOn.map((dep, depIndex) => {
        if (typeof dep !== 'number' || !Number.isInteger(dep) || dep <= 0) {
          throw new EchuanValidationError(
            `tasks[${index}].depends_on[${depIndex}] 必须是大于 0 的整数（1-based 批内序号）。`,
            'invalid_argument',
            'tasks',
          )
        }
        return dep
      })
    }
    return task
  })
}

/* ------------------------------------------------------------------ *
 * 取值辅助
 * ------------------------------------------------------------------ */

function projectIdOf(detail: Project | ProjectDetail): string {
  return asString((detail as ProjectDetail).project_id)
    ?? asString(detail.id)
    ?? ''
}

/** 项目状态三件套：缺失字段不写入结果，保持 output.schema 的「可选」语义。 */
function projectFields(detail: Project): Pick<ProjectSubmittedValue, 'status' | 'status_name' | 'version'> {
  const status = asNumber(detail.status)
  const statusName = asString(detail.status_name)
  const version = asNumber(detail.version)
  return {
    ...(status === undefined ? {} : { status }),
    ...(statusName === undefined ? {} : { status_name: statusName }),
    ...(version === undefined ? {} : { version }),
  }
}

function specValue(
  kind: 'spec_created' | 'spec_revised',
  spec: SpecDocument,
): SpecCreatedValue | SpecRevisedValue {
  const materialId = asString(spec.material?.material_id)
  return {
    kind,
    project_id: asString(spec.project_id) ?? '',
    spec_id: asString(spec.spec_id) ?? '',
    title: asString(spec.title) ?? '',
    version: asNumber(spec.version) ?? 0,
    ...(materialId === undefined ? {} : { material_id: materialId }),
  }
}

function planValue(plan: PlanResponse): PlanSubmittedValue {
  const tasks = Array.isArray(plan.tasks) ? plan.tasks : []
  const status = asNumber(plan.status)
  const statusName = asString(plan.status_name)
  const version = asNumber(plan.project_version)
  return {
    kind: 'plan_submitted',
    project_id: asString(plan.project_id) ?? '',
    ...(status === undefined ? {} : { status }),
    ...(statusName === undefined ? {} : { status_name: statusName }),
    ...(version === undefined ? {} : { version }),
    tasks: tasks.map((task) => {
      const seqNo = asNumber(task.seq_no)
      const priority = asNumber(task.priority)
      const taskStatusName = asString(task.status_name)
      return {
        task_id: asString(task.id) ?? '',
        title: asString(task.title) ?? '',
        depends_on: Array.isArray(task.depends_on)
          ? task.depends_on.filter((id): id is string => typeof id === 'string')
          : [],
        ...(seqNo === undefined ? {} : { seq_no: seqNo }),
        ...(priority === undefined ? {} : { priority }),
        ...(taskStatusName === undefined ? {} : { status_name: taskStatusName }),
      }
    }),
  }
}

/* ------------------------------------------------------------------ *
 * 渲染
 * ------------------------------------------------------------------ */

function statusLine(status: number | undefined, statusName: string | undefined): string {
  return projectStatusLabel(status, statusName)
}

export function renderProjectCreated(value: ProjectCreatedValue): TextContent[] {
  return [textBlock([
    `✅ 项目已创建：${value.project_id}`,
    `- 标题：${value.title}`,
    `- 状态：${statusLine(value.status, value.status_name)}`,
    `- 版本：${value.version === undefined ? '未知' : `v${value.version}`}`,
    '',
    '下一步：用 `echuan_project_requirement_write` 写需求正文，再用 `echuan_project_submit` 提交评审。',
  ].join('\n'))]
}

export function renderRequirementWritten(value: RequirementWrittenValue): TextContent[] {
  const material = value.material_id === undefined ? 'material_id 未返回' : `material_id=${value.material_id}`
  const version = value.material_version === undefined ? '' : `，v${value.material_version}`
  const size = value.size === undefined ? '' : `，${value.size}B`
  return [textBlock([
    `✅ 需求已写入：项目 ${value.project_id}（${material}${version}${size}）`,
    '下一步：用 `echuan_project_submit` 提交评审；评审通过后再建方案。',
  ].join('\n'))]
}

export function renderProjectSubmitted(value: ProjectSubmittedValue): TextContent[] {
  return [textBlock([
    `✅ 项目已提交评审：${value.project_id}`,
    `- 状态：${statusLine(value.status, value.status_name)}`,
    `- 版本：${value.version === undefined ? '未知' : `v${value.version}`}`,
    '下一步：等待人工评审结论（本工具包不提供审批动作）；通过后用 `echuan_spec_create` 建方案。',
  ].join('\n'))]
}

export function renderSpecCreated(value: SpecCreatedValue): TextContent[] {
  return [textBlock([
    `✅ 方案已创建：${value.spec_id}（项目 ${value.project_id}）`,
    `- 标题：${value.title}`,
    `- 版本：v${value.version}${value.material_id === undefined ? '' : `，material_id=${value.material_id}`}`,
    '下一步：需要改稿时用 `echuan_spec_revise`（带 version）；定稿后用 `echuan_plan_submit` 批量建卡。',
  ].join('\n'))]
}

export function renderSpecRevised(value: SpecRevisedValue): TextContent[] {
  return [textBlock([
    `✅ 方案已修订：${value.spec_id}（项目 ${value.project_id}）`,
    `- 标题：${value.title}`,
    `- 新版本：v${value.version}${value.material_id === undefined ? '' : `，material_id=${value.material_id}`}`,
    '下一步：继续改稿时用新 version 再调 `echuan_spec_revise`；定稿后用 `echuan_plan_submit` 批量建卡。',
  ].join('\n'))]
}

export function renderPlanSubmitted(value: PlanSubmittedValue): TextContent[] {
  const lines = [
    `✅ 已批量建卡 ${value.tasks.length} 张：项目 ${value.project_id}`,
    `- 项目状态：${statusLine(value.status, value.status_name)}`,
    `- 项目版本：${value.version === undefined ? '未知' : `v${value.version}`}`,
  ]
  for (const task of value.tasks) {
    const seq = task.seq_no === undefined ? '' : `#${task.seq_no} `
    const priority = task.priority === undefined ? '未知' : String(task.priority)
    const depends = task.depends_on.length === 0 ? '无' : task.depends_on.join('、')
    const status = task.status_name === undefined ? '' : `｜${task.status_name}`
    lines.push(`- ${seq}${task.task_id}：${task.title}（优先级 ${priority}，依赖 ${depends}${status}）`)
  }
  lines.push('下一步：等待任务计划评审；通过后 worker 用 `echuan_task_claim` 领卡。')
  return [textBlock(lines.join('\n'))]
}

/* ------------------------------------------------------------------ *
 * 工具定义
 * ------------------------------------------------------------------ */

/** `echuan_project_create`：建项目（可带首版需求）。 */
export function createProjectCreateTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_project_create',
    description:
      '创建一个驿传（echuan）项目（POST /v1/projects），可同时写入首版需求正文。'
      + '返回 project_id、状态与版本，后续写需求 / 提交评审都用这个 project_id。',
    parameters: {
      product_id: { type: 'string', required: true, description: '产品业务 id（必填），例如 APP' },
      title: { type: 'string', required: true, description: '项目标题（必填）' },
      requirement_md: {
        type: 'string',
        description: '首版需求正文（可选，Markdown）；不传则项目停在需求草稿，稍后用 requirements 工具补写',
      },
    },
    output: {
      schema: projectCreatedSchema,
      render: (_args, value) => renderProjectCreated(value),
    },
    async execute(args) {
      try {
        const input: CreateProjectInput = {
          product_id: requireText(args.product_id, 'product_id'),
          title: requireText(args.title, 'title'),
        }
        if (args.requirement_md !== undefined) input.requirement_md = args.requirement_md

        const result = await client.createProject(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对创建项目返回 204（无内容），没有给出 project_id。'
              + '下一步：先查项目列表确认是否已创建，不要原样重试。',
            'unexpected_empty',
            'project',
          )
        }
        const detail = result.data
        const fields = projectFields(detail)
        return {
          kind: 'project_created' as const,
          project_id: projectIdOf(detail),
          title: asString(detail.title) ?? input.title,
          ...fields,
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_project_requirement_write`：追加需求版本。 */
export function createRequirementWriteTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_project_requirement_write',
    description:
      '给驿传（echuan）项目追加一版需求正文（POST /v1/projects/requirement?id=）。'
      + '正文按 Markdown 原样保存；返回 material_id / 版本 / 字节数。',
    parameters: {
      project_id: { type: 'string', required: true, description: '项目编号（必填，走 query `?id=`）' },
      body_md: { type: 'string', required: true, description: '需求正文（必填，Markdown）' },
    },
    output: {
      schema: requirementWrittenSchema,
      render: (_args, value) => renderRequirementWritten(value),
    },
    async execute(args) {
      try {
        const project_id = requireText(args.project_id, 'project_id')
        const body_md = requireBody(args.body_md, 'body_md')

        const result = await client.writeRequirement({ id: project_id, body_md })
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对写需求返回 204（无内容），没有给出物料信息。'
              + '下一步：用 `echuan_project_get` 确认需求版本是否已追加，不要原样重试。',
            'unexpected_empty',
            'requirement',
          )
        }
        const material = result.data.requirement
        const materialId = asString(material?.material_id)
        const materialVersion = asNumber(material?.version)
        const size = asNumber(material?.size)
        return {
          kind: 'requirement_written' as const,
          project_id: projectIdOf(result.data) || project_id,
          ...(materialId === undefined ? {} : { material_id: materialId }),
          ...(materialVersion === undefined ? {} : { material_version: materialVersion }),
          ...(size === undefined ? {} : { size }),
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_project_submit`：草稿提交评审（无 body）。 */
export function createProjectSubmitTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_project_submit',
    description:
      '把项目草稿提交评审（POST /v1/projects/submit?id=）。需求草稿缺需求文档时平台返回 409'
      + '（「请先上传需求文档」），错误原样透出。',
    parameters: {
      project_id: { type: 'string', required: true, description: '项目编号（必填，走 query `?id=`）' },
    },
    output: {
      schema: projectSubmittedSchema,
      render: (_args, value) => renderProjectSubmitted(value),
    },
    async execute(args) {
      try {
        const project_id = requireText(args.project_id, 'project_id')
        const result = await client.submitProject({ id: project_id })
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对提交评审返回 204（无内容），与接口契约不符。'
              + '下一步：用 `echuan_project_get` 确认项目状态，不要原样重试。',
            'unexpected_empty',
            'project',
          )
        }
        return {
          kind: 'project_submitted' as const,
          project_id: projectIdOf(result.data) || project_id,
          ...projectFields(result.data),
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_spec_create`：创建独立技术方案。 */
export function createSpecCreateTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_spec_create',
    description:
      '为驿传（echuan）项目创建一份独立技术方案（POST /v1/specs）。'
      + '返回 spec_id 与版本；后续修订用 `echuan_spec_revise`（必须带 version）。',
    parameters: {
      project_id: { type: 'string', required: true, description: '项目编号（必填）' },
      title: { type: 'string', description: '方案标题（可选；不传则平台沿用项目标题）' },
      body_md: { type: 'string', required: true, description: '方案正文（必填，Markdown）' },
    },
    output: {
      schema: specSchema,
      render: (_args, value) => renderSpecCreated(value as SpecCreatedValue),
    },
    async execute(args) {
      try {
        const input: CreateSpecInput = {
          project_id: requireText(args.project_id, 'project_id'),
          body_md: requireBody(args.body_md, 'body_md'),
        }
        const title = optionalText(args.title, 'title')
        if (title !== undefined) input.title = title

        const result = await client.createSpec(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对创建方案返回 204（无内容），没有给出 spec_id。'
              + '下一步：用 `echuan_project_get` 确认方案是否已创建，不要原样重试。',
            'unexpected_empty',
            'spec',
          )
        }
        return specValue('spec_created', result.data)
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_spec_revise`：修订独立方案（`If-Match` 走请求头）。 */
export function createSpecReviseTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_spec_revise',
    description:
      '修订一份驿传（echuan）独立技术方案（PATCH /v1/specs?id=）。'
      + '必须带当前 version（走 If-Match 头，不放进 body）；版本过期时平台返回 409 invalid_version。',
    parameters: {
      spec_id: { type: 'string', required: true, description: '方案编号（必填，走 query `?id=`）' },
      version: {
        type: 'integer',
        required: true,
        description: '方案当前版本号（必填，>0；走 If-Match 头）',
      },
      title: { type: 'string', description: '方案标题（可选；不传则平台沿用原标题）' },
      body_md: { type: 'string', required: true, description: '方案正文（必填，Markdown）' },
    },
    output: {
      schema: specSchema,
      render: (_args, value) => renderSpecRevised(value as SpecRevisedValue),
    },
    async execute(args) {
      try {
        const input: ReviseSpecInput = {
          id: requireText(args.spec_id, 'spec_id'),
          version: requireVersion(args.version),
          body_md: requireBody(args.body_md, 'body_md'),
        }
        const title = optionalText(args.title, 'title')
        if (title !== undefined) input.title = title

        const result = await client.reviseSpec(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对修订方案返回 204（无内容），没有给出新版本。'
              + '下一步：用 `echuan_project_get` 确认方案版本，不要原样重试。',
            'unexpected_empty',
            'spec',
          )
        }
        return specValue('spec_revised', result.data)
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}

/** `echuan_plan_submit`：批量建卡。 */
export function createPlanSubmitTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_plan_submit',
    description:
      '向驿传（echuan）项目批量添加任务卡（POST /v1/projects/plans?id=）。'
      + 'tasks 至少一张，每张含 title / body_md，可选 priority 与 depends_on（1-based 批内序号）。'
      + '返回项目状态与新建卡列表（含任务编号）。',
    parameters: {
      project_id: { type: 'string', required: true, description: '项目编号（必填，走 query `?id=`）' },
      tasks: {
        type: 'array',
        required: true,
        items: {
          type: 'object',
          additionalProperties: false,
          properties: {
            title: { type: 'string', required: true, description: '任务标题' },
            body_md: { type: 'string', required: true, description: '任务正文（Markdown）' },
            priority: { type: 'integer', description: '优先级（可选，默认由平台决定）' },
            depends_on: {
              type: 'array',
              items: { type: 'integer' },
              description: '依赖的批内序号（可选，1-based，例如 [1, 2]）',
            },
          },
        },
      },
    },
    output: {
      schema: planSubmittedSchema,
      render: (_args, value) => renderPlanSubmitted(value),
    },
    async execute(args) {
      try {
        const input: SubmitPlanInput = {
          id: requireText(args.project_id, 'project_id'),
          tasks: requireTasks(args.tasks),
        }

        const result = await client.submitPlan(input)
        if (result.kind === 'empty') {
          throw new EchuanValidationError(
            '平台对批量建卡返回 204（无内容），与接口契约不符。'
              + '下一步：用 `echuan_task_list` 确认卡片是否已创建，不要原样重试。',
            'unexpected_empty',
            'plan',
          )
        }
        return planValue(result.data)
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}
