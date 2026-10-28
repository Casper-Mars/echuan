/**
 * 四个只读工具：待办 / 项目全景 / 任务详情 / 资料正文。
 *
 * 边界（硬约束）：
 * - **只发 GET**：`getInbox` / `listProjects` / `listTasks` / `getMaterials`，
 *   不出现任何写方法；
 * - 缺 id 时**本地拒绝、零请求**，返回可读提示（不是抛异常、不是 404）；
 * - 平台 `not_found` 统一翻成「编号不存在，可能写错」+ 先查 `echuan_inbox`；
 * - 输出只有一段渲染好的中文文本，不把平台原始 JSON 甩给模型；
 * - 工具层不缓存、不落盘、不存状态。
 *
 * 输出规范值刻意收成 `{ text }`：只读工具没有 presentationMeta 之类的第二个消费方，
 * 规范值就是模型要读的那段文本，避免「视图 → schema → render」三处重复维护。
 */
import { defineTool, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import type { Project, TaskDetail } from '../client.js'
import { EchuanApiError } from '../errors.js'
import {
  formatInbox,
  formatMaterial,
  formatProject,
  formatTask,
  isRecord,
  missingIdText,
  renderTextValue as renderText,
  toToolError,
} from './render.js'
import type { EchuanToolOptions } from './write.js'

/** 只读工具的规范值 schema：一段文本。 */
const textOutputSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    text: { type: 'string', required: true, description: '渲染好的中文文本' },
  },
} as const

/** `limit` 的合法区间（平台契约为 1–200）。 */
const INBOX_LIMIT_MIN = 1
const INBOX_LIMIT_MAX = 200

/**
 * 把只读请求的错误翻成模型能直接照做的文案。
 *
 * `not_found` 单独处理：平台原文只说「没找到」，模型需要知道「编号可能写错了，
 * 先用 `echuan_inbox` 确认」；其余错误沿用 {@link toToolError} 的统一出口。
 */
function readError(err: unknown, subject: string): never {
  if (err instanceof EchuanApiError && (err.code === 'not_found' || err.status === 404)) {
    throw new Error(
      `[not_found] 平台没有找到这个${subject}：编号不存在，可能编号写错了。`
        + '下一步：先用 `echuan_inbox` 确认编号（也可从项目/任务详情里的编号字段复制），'
        + '再用正确编号重试；本次没有做任何修改。',
    )
  }
  return toToolError(err)
}

/** 平台返回的是不是项目详情（带 `project_id`），而不是列表。 */
function isProjectDetail(value: unknown): value is Project {
  return isRecord(value) && typeof value['project_id'] === 'string'
}

/** 平台返回的是不是任务详情（带 `task_id`）。 */
function isTaskDetail(value: unknown): value is TaskDetail {
  return isRecord(value) && typeof value['task_id'] === 'string'
}

/** 读取必填 id：缺参/空白返回 `undefined`（由调用方给本地提示，零请求）。 */
function readId(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  const trimmed = value.trim()
  return trimmed === '' ? undefined : trimmed
}

/**
 * `echuan_inbox`：待办列表（`GET /v1/inbox`）。
 *
 * 参数只有可选的 `limit`（1–200）；越界在本地拒绝，不发请求。
 */
export function createInboxTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_inbox',
    description:
      '列出驿传（echuan）当前待处理项（只读，GET /v1/inbox）：项目评审与任务待验收。'
      + '返回每条的类型、编号、状态、已停留时长、是否超时、标题与原因，以及超时阈值汇总。',
    parameters: {
      limit: {
        type: 'integer',
        description: `最多返回多少条（可选，${INBOX_LIMIT_MIN}–${INBOX_LIMIT_MAX}；不传用平台默认）`,
      },
    },
    output: {
      schema: textOutputSchema,
      render: (_args, value) => renderText(value),
    },
    async execute(args) {
      const limit = args.limit
      if (
        limit !== undefined
        && (!Number.isInteger(limit) || limit < INBOX_LIMIT_MIN || limit > INBOX_LIMIT_MAX)
      ) {
        return {
          text: `limit 必须是 ${INBOX_LIMIT_MIN}–${INBOX_LIMIT_MAX} 的整数，收到 ${String(limit)}。`
            + '本次已本地拒绝，未发出任何请求；不传 limit 则用平台默认值。',
        }
      }

      try {
        const result = await client.getInbox(limit === undefined ? undefined : { limit })
        if (result.kind === 'empty') {
          return {
            text: '平台对 `echuan_inbox` 返回 204（无内容）：当前没有待处理项。'
              + '下一步：稍后再看，不要把它当错误重试。',
          }
        }
        return { text: formatInbox(result.data) }
      } catch (err) {
        return readError(err, '待办条目')
      }
    },
  })
}

/**
 * `echuan_project_get`：项目全景（`GET /v1/projects?id=`）。
 *
 * 缺 id 直接本地返回提示，不发明请求。
 */
export function createProjectGetTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_project_get',
    description:
      '查看一个驿传（echuan）项目的全景（只读，GET /v1/projects?id=）：状态、需求摘要、'
      + '方案标题与版本、任务统计（总数与各状态计数）、原因说明。',
    parameters: {
      id: {
        type: 'string',
        description: '项目编号（必填，例如 APP-2）；先用 `echuan_inbox` 找到编号',
      },
    },
    output: {
      schema: textOutputSchema,
      render: (_args, value) => renderText(value),
    },
    async execute(args) {
      const id = readId(args.id)
      if (id === undefined) return { text: missingIdText('项目') }

      try {
        const result = await client.listProjects({ id })
        if (result.kind === 'empty') {
          return {
            text: `平台对项目 ${id} 返回 204（无内容），未给出详情。`
              + '下一步：先用 `echuan_inbox` 确认项目编号后再试。',
          }
        }
        if (!isProjectDetail(result.data)) {
          return {
            text: `平台没有返回项目 ${id} 的详情（拿到的是列表或字段不全）。`
              + '下一步：先用 `echuan_inbox` 确认项目编号，再带正确编号重试。',
          }
        }
        return { text: formatProject(result.data) }
      } catch (err) {
        return readError(err, '项目')
      }
    },
  })
}

/**
 * `echuan_task_get`：任务详情（`GET /v1/tasks?id=`）。
 *
 * 缺 id 直接本地返回提示，不发明请求。
 */
export function createTaskGetTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_task_get',
    description:
      '查看一张驿传（echuan）任务卡的详情（只读，GET /v1/tasks?id=）：所属项目、状态、优先级、'
      + '尝试次数与上限、正文、当前报告、原因说明、租约。',
    parameters: {
      id: {
        type: 'string',
        description: '任务编号（必填，例如 APP-1-T0007）；先用 `echuan_inbox` 找到编号',
      },
    },
    output: {
      schema: textOutputSchema,
      render: (_args, value) => renderText(value),
    },
    async execute(args) {
      const id = readId(args.id)
      if (id === undefined) return { text: missingIdText('任务') }

      try {
        const result = await client.listTasks({ id })
        if (result.kind === 'empty') {
          return {
            text: `平台对任务 ${id} 返回 204（无内容），未给出详情。`
              + '下一步：先用 `echuan_inbox` 确认任务编号后再试。',
          }
        }
        if (!isTaskDetail(result.data)) {
          return {
            text: `平台没有返回任务 ${id} 的详情（拿到的是列表或字段不全）。`
              + '下一步：先用 `echuan_inbox` 确认任务编号，再带正确编号重试。',
          }
        }
        return { text: formatTask(result.data) }
      } catch (err) {
        return readError(err, '任务')
      }
    },
  })
}

/**
 * `echuan_material_get`：资料正文（`GET /v1/materials?id=`）。
 *
 * 需求、方案、报告都以资料形式存放，这里直接给 markdown 正文。
 */
export function createMaterialGetTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_material_get',
    description:
      '读取一份驿传（echuan）资料的正文（只读，GET /v1/materials?id=）。'
      + '需求、方案、报告都是资料；编号可从项目/任务详情里的 material_id 取。',
    parameters: {
      id: {
        type: 'string',
        description: '资料编号 material_id（必填）；可从项目/任务详情里复制',
      },
    },
    output: {
      schema: textOutputSchema,
      render: (_args, value) => renderText(value),
    },
    async execute(args) {
      const id = readId(args.id)
      if (id === undefined) {
        return {
          text: missingIdText(
            '资料',
            '资料编号可从 `echuan_project_get` / `echuan_task_get` 返回的 material_id 复制。',
          ),
        }
      }

      try {
        const result = await client.getMaterials({ id })
        if (result.kind === 'empty') {
          return {
            text: `资料 ${id}：平台返回 204（无内容），正文为空。`
              + '下一步：先核对编号是否正确（可用 `echuan_inbox` 找到相关编号，或从详情里的 material_id 复制）。',
          }
        }
        return { text: formatMaterial(result.data) }
      } catch (err) {
        return readError(err, '资料')
      }
    },
  })
}

/** 四个只读工具名（顺序即向模型暴露的顺序）。 */
export const READ_TOOL_NAMES = [
  'echuan_inbox',
  'echuan_project_get',
  'echuan_task_get',
  'echuan_material_get',
] as const

/** 构造 4 个只读工具定义。 */
export function createEchuanReadTools(options: EchuanToolOptions): ToolDefinition[] {
  return [
    createInboxTool(options),
    createProjectGetTool(options),
    createTaskGetTool(options),
    createMaterialGetTool(options),
  ]
}
