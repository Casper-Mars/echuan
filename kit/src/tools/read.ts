/**
 * 七个只读工具：待办 / 产品列表 / 项目列表 / 任务列表 / 项目全景 / 任务详情 / 资料正文。
 *
 * 边界（硬约束）：
 * - **只发 GET**：`getInbox` / `listProducts` / `listProjects` / `listTasks` /
 *   `getMaterials`，不出现任何写方法；
 * - 缺 id 时**本地拒绝、零请求**，返回可读提示（不是抛异常、不是 404）；
 * - 平台 `not_found` 统一翻成「编号不存在，可能写错」+ 先查 `echuan_inbox`；
 * - 输出只有一段渲染好的中文文本，不把平台原始 JSON 甩给模型；
 * - 工具层不缓存、不落盘、不存状态。
 *
 * 输出规范值刻意收成 `{ text }`：只读工具没有 presentationMeta 之类的第二个消费方，
 * 规范值就是模型要读的那段文本，避免「视图 → schema → render」三处重复维护。
 */
import {
  defineTool,
  type ParameterSchemaSpec,
  type ToolDefinition,
} from '@deepseek-ai/dsh-tools'
import type {
  ApiResult,
  Product,
  ProductsQuery,
  Project,
  ProjectsQuery,
  QueryParams,
  TaskDetail,
  TasksQuery,
} from '../client.js'
import { EchuanApiError } from '../errors.js'
import {
  formatInbox,
  formatMaterial,
  formatProductList,
  formatProject,
  formatProjectList,
  formatTask,
  formatTaskList,
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
const LIST_LIMIT_MIN = 1
const LIST_LIMIT_MAX = 200

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
 * 列表工具配置：三个列表工具共用分页校验、空列表与 `next_cursor` 提示。
 *
 * `subject` 是中文资源名（产品 / 项目 / 任务），只用于拼装用户可见文案；
 * `request` 必须只发 GET；`format` 只投影非空列表（表头 + 每行字段）。
 */
interface ListToolConfig<TItem> {
  name: string
  description: string
  parameters: ParameterSchemaSpec
  /** 列表资源中文名，用于空列表、「拿到详情」与 404 文案。 */
  subject: string
  /** 空列表时的下一步建议（不含「当前没有…」前缀）。 */
  emptyHint: string
  /** 非空且没有 next_cursor 时的下一步建议。 */
  nextHint: string
  /** 发起只读列表请求；`query` 为空表示不带任何参数。 */
  request(query: QueryParams | undefined): Promise<ApiResult<unknown>>
  /** 渲染非空列表：表头 + 每条一行（或两行）。 */
  format(items: TItem[]): string
}

/**
 * 列表工具工厂：分页 / limit 校验 / 空列表 / `next_cursor` 提示只在这里写一份。
 *
 * 行为契约：
 * 1. `limit` 非整数、<1 或 >200 → 本地拒绝，**零 HTTP 请求**；
 * 2. 204 或 `items: []` → 「当前没有…」+ 下一步，不抛错；
 * 3. 回包带 `next_cursor` → 文本末尾追加 `cursor=…` 提示；
 * 4. 回包没有 `items` 数组（拿到详情）→ 明确说「列表接口应返回 items」，不硬渲染；
 * 5. 只发 GET，不缓存、不落盘、不甩原始 JSON。
 */
function createListTool<TItem>(config: ListToolConfig<TItem>): ToolDefinition {
  return defineTool({
    name: config.name,
    description: config.description,
    parameters: config.parameters,
    output: {
      schema: textOutputSchema,
      render: (_args, value) => renderText(value),
    },
    async execute(args) {
      const values = args as unknown as Record<string, unknown>

      const limit = values.limit
      if (
        limit !== undefined
        && (
          typeof limit !== 'number'
          || !Number.isInteger(limit)
          || limit < LIST_LIMIT_MIN
          || limit > LIST_LIMIT_MAX
        )
      ) {
        return {
          text: `limit 必须是 ${LIST_LIMIT_MIN}–${LIST_LIMIT_MAX} 的整数，收到 ${String(limit)}。`
            + '本次已本地拒绝，未发出任何请求；不传 limit 则用平台默认值。',
        }
      }

      const query = listQuery(values)
      try {
        const result = await config.request(query)
        if (result.kind === 'empty') {
          return {
            text: `平台对 \`${config.name}\` 返回 204（无内容）：当前没有${config.subject}。`
              + '下一步：稍后再试，不要把它当错误重试。',
          }
        }

        const payload = result.data
        const items = isRecord(payload) && Array.isArray(payload['items'])
          ? payload['items']
          : undefined
        if (items === undefined) {
          return {
            text: `平台没有返回${config.subject}列表（拿到的是详情或字段不全）。`
              + '这次拿到的是详情，列表接口应返回 items。'
              + '下一步：重试一次；若仍是详情，把该现象反馈给平台维护者，不要按详情字段硬读。',
          }
        }

        if (items.length === 0) {
          return { text: `当前没有${config.subject}。下一步：${config.emptyHint}` }
        }

        const body = config.format(items as TItem[])
        const cursor = isRecord(payload) ? asCursor(payload['next_cursor']) : undefined
        const tail = cursor === undefined
          ? `下一步：${config.nextHint}`
          : `还有更多，可传 cursor=${cursor} 继续（再次调用 \`${config.name}\` 并带上 cursor）。`
        return { text: `${body}\n${tail}` }
      } catch (err) {
        return readError(err, config.subject)
      }
    },
  })
}

/** 收集工具参数里真正要发给平台的 query：丢掉空值，数组原样（重复 key）。 */
function listQuery(values: Record<string, unknown>): QueryParams | undefined {
  const query: Record<string, string | number | readonly number[]> = {}
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === null || value === '') continue
    if (typeof value === 'string' || typeof value === 'number') {
      query[key] = value
      continue
    }
    if (Array.isArray(value)) {
      const items = value.filter(
        (item): item is number => typeof item === 'number' && Number.isFinite(item),
      )
      if (items.length > 0) query[key] = items
    }
  }
  return Object.keys(query).length === 0 ? undefined : query as QueryParams
}

/** `next_cursor` 只接受非空字符串。 */
function asCursor(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  return value.trim() === '' ? undefined : value
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
        description: `最多返回多少条（可选，${LIST_LIMIT_MIN}–${LIST_LIMIT_MAX}；不传用平台默认）`,
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
        && (!Number.isInteger(limit) || limit < LIST_LIMIT_MIN || limit > LIST_LIMIT_MAX)
      ) {
        return {
          text: `limit 必须是 ${LIST_LIMIT_MIN}–${LIST_LIMIT_MAX} 的整数，收到 ${String(limit)}。`
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
 * `echuan_product_list`：产品列表（`GET /v1/products`）。
 *
 * 只读列表；Agent 拿不到仓库地址时必须先看这里，而不是猜路径。
 */
export function createProductListTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return createListTool<Product>({
    name: 'echuan_product_list',
    description:
      '列出驿传（echuan）产品（只读，GET /v1/products）：产品编号、名称、git 仓库地址、'
      + '描述、许可、项目数与更新时间。不知道代码仓库地址时先看这里，不要猜路径。',
    parameters: {
      limit: {
        // 用 number 接收，非整数由 createListTool 本地拒绝并给出可读文案。
        type: 'number',
        description: `最多返回多少条（可选，${LIST_LIMIT_MIN}–${LIST_LIMIT_MAX} 的整数；不传用平台默认）`,
      },
      cursor: {
        type: 'string',
        description: '分页游标（可选；上一页返回 next_cursor 时原样传入）',
      },
    },
    subject: '产品',
    emptyHint: '确认产品是否已在平台创建；稍后再试，不要连续重试。',
    nextHint: '带 product_id 用 `echuan_project_list` 看该产品的项目。',
    request: query => client.listProducts(query as ProductsQuery | undefined),
    format: formatProductList,
  })
}

/**
 * `echuan_project_list`：项目列表（`GET /v1/projects`）。
 *
 * `status` 在契约里是**单个整数**（与任务列表的数组语义不同）。
 */
export function createProjectListTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return createListTool<Project>({
    name: 'echuan_project_list',
    description:
      '列出驿传（echuan）项目（只读，GET /v1/projects）：项目编号、标题、状态、'
      + '任务统计、原因说明与更新时间；可按 product_id 与 status 筛选。',
    parameters: {
      product_id: {
        type: 'string',
        description: '产品编号（可选，例如 APP）；先用 `echuan_product_list` 找到编号',
      },
      status: {
        type: 'integer',
        description: '项目状态数字（可选，单个整数，例如 1=方案评审、4=开发中）',
      },
      limit: {
        // 用 number 接收，非整数由 createListTool 本地拒绝并给出可读文案。
        type: 'number',
        description: `最多返回多少条（可选，${LIST_LIMIT_MIN}–${LIST_LIMIT_MAX} 的整数；不传用平台默认）`,
      },
      cursor: {
        type: 'string',
        description: '分页游标（可选；上一页返回 next_cursor 时原样传入）',
      },
    },
    subject: '项目',
    emptyHint: '核对 product_id / status 筛选是否正确；不确定时去掉筛选再查一次。',
    nextHint: '带 project_id 用 `echuan_task_list` 看该项目的任务。',
    request: query => client.listProjects(query as ProjectsQuery | undefined),
    format: formatProjectList,
  })
}

/**
 * `echuan_task_list`：任务列表（`GET /v1/tasks`）。
 *
 * `status` 按契约 `style: form / explode: true` 以**重复 key** 传给平台。
 */
export function createTaskListTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return createListTool<TaskDetail>({
    name: 'echuan_task_list',
    description:
      '列出驿传（echuan）任务（只读，GET /v1/tasks）：序号、任务编号、标题、状态、'
      + '优先级、尝试次数与依赖；可按 project_id 与 status 筛选（status 可传多个）。',
    parameters: {
      project_id: {
        type: 'string',
        description: '项目编号（可选，例如 APP-1）；先用 `echuan_project_list` 找到编号',
      },
      status: {
        type: 'array',
        items: { type: 'integer' },
        description: '任务状态数字（可选，可传多个；按重复 key 传给平台，例如 [1, 2]）',
      },
      limit: {
        // 用 number 接收，非整数由 createListTool 本地拒绝并给出可读文案。
        type: 'number',
        description: `最多返回多少条（可选，${LIST_LIMIT_MIN}–${LIST_LIMIT_MAX} 的整数；不传用平台默认）`,
      },
      cursor: {
        type: 'string',
        description: '分页游标（可选；上一页返回 next_cursor 时原样传入）',
      },
    },
    subject: '任务',
    emptyHint: '核对 project_id / status 筛选是否正确；不确定时去掉筛选再查一次。',
    nextHint: '带任务编号用 `echuan_task_get` 看任务详情。',
    request: query => client.listTasks(query as TasksQuery | undefined),
    format: formatTaskList,
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
      '查看一个驿传（echuan）项目的全景（只读，GET /v1/projects?id=）：状态、'
      + '需求与方案的元信息（标题、版本、material_id）、任务统计（总数与各状态计数）、'
      + '原因说明；正文用 `echuan_material_get` 按 material_id 取。',
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

/** 七个只读工具名（顺序即向模型暴露的顺序）。 */
export const READ_TOOL_NAMES = [
  'echuan_inbox',
  'echuan_product_list',
  'echuan_project_list',
  'echuan_task_list',
  'echuan_project_get',
  'echuan_task_get',
  'echuan_material_get',
] as const

/** 构造 7 个只读工具定义。 */
export function createEchuanReadTools(options: EchuanToolOptions): ToolDefinition[] {
  return [
    createInboxTool(options),
    createProductListTool(options),
    createProjectListTool(options),
    createTaskListTool(options),
    createProjectGetTool(options),
    createTaskGetTool(options),
    createMaterialGetTool(options),
  ]
}
