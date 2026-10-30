/**
 * `echuan_worker_info`：worker 只读诊断（**零 HTTP、零副作用**）。
 *
 * 排障时模型最需要知道的是「心跳有没有开 / worker 有没有注册 / 手里压着哪几张卡 /
 * 项目会话在不在」，这些全是进程内内存态，不需要也不应该打平台：
 *
 * - **不发请求**：不拿 `client`，只读注入的 `workerInfo` 依赖；
 * - **不写盘、不改状态**：所有依赖都是只读投影（`workerId()` / `inflight()`）；
 * - **服务缺失只降级**：`sessionPersistence` / `agentPresets` / `workspaceRegistry`
 *   任一缺失都只是少展示一项（标注「服务缺失」），不抛错；
 * - 查看哪些项目：显式 `project_id` 优先，否则看 inflight 里压着的项目。
 *
 * 输出与只读工具一致：`textBlock` + `TextValue`（规范值就是模型要读的那段文本）。
 */
import { defineTool, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import type { Inflight } from '../worker.js'
import { maskWorkerId } from '../worker.js'
import { renderTextValue as renderText } from './render.js'
import type { EchuanToolOptions } from './write.js'

/** 只读工具的规范值 schema：一段文本（与 `read.ts` 保持一致）。 */
const textOutputSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    text: { type: 'string', required: true, description: '渲染好的中文文本' },
  },
} as const

/** 会话 header 的投影（`stat` 只用到这两个字段）。 */
export interface WorkerInfoSessionHeader {
  cwd?: string
  agentPreset?: string
}

/** 诊断工具的可注入依赖；全部惰性读取（`index.ts` 用 `ctx.get(...)` 包一层）。 */
export interface WorkerInfoDeps {
  /** `worker.poll.enable`：心跳是否开启。 */
  pollEnabled: boolean
  /** `worker.workspaceRoot`（已归一化；缺省走优先链）。 */
  workspaceRoot?: string | undefined
  /** 当前 worker id（未注册返回 `undefined`；输出前打码）。 */
  workerId: () => string | undefined
  /** 当前 inflight 快照。 */
  inflight: () => readonly Inflight[]
  /** 按会话 id 读 agent（只用 `status`）。 */
  agents: { get(id: string): { status?: string } | undefined } | undefined
  /** 会话持久化（缺失 → 无法判定 persisted / missing）。 */
  sessionPersistence?: {
    stat(id: string): Promise<{ header?: WorkerInfoSessionHeader } | undefined | null>
  } | undefined
  /** preset registry（缺失 → 默认 preset 不可用）。 */
  agentPresets?: { resolve(): Promise<{ id: string }> } | undefined
  /** 工作区列表（缺失 → 无法列出工作区）。 */
  workspaceRegistry?: { list(): readonly { path: string }[] } | undefined
}

/**
 * `echuan_worker_info`：查看本进程 worker 的运行态（只读，零 HTTP）。
 *
 * 参数只有可选的 `project_id`；不传则看 inflight 里的项目。
 */
export function createWorkerInfoTool(options: EchuanToolOptions): ToolDefinition {
  return defineTool({
    name: 'echuan_worker_info',
    description:
      '查看本进程驿传（echuan）worker 的运行态（只读，零 HTTP、零副作用）：心跳开关、'
      + 'worker_id（打码）、inflight 里的任务与租约、各项目会话状态（live / persisted / missing）、'
      + '默认 preset 与工作区根目录。排障「为什么没派单 / 会话在不在」时先看这里。',
    parameters: {
      project_id: {
        type: 'string',
        description: '要检查的项目编号（可选，例如 APP-1；不传则看 inflight 里的项目）',
      },
    },
    output: {
      schema: textOutputSchema,
      render: (_args, value) => renderText(value),
    },
    async execute(args) {
      const deps = options.workerInfo
      if (deps === undefined) {
        return {
          text: 'worker 诊断不可用：插件未注入 workerInfo 依赖（通常只出现在单测或裁剪过的装配里）。'
            + '本次没有发出任何请求，也没有改动任何状态。',
        }
      }

      const requested = typeof args.project_id === 'string' ? args.project_id.trim() : ''
      try {
        return { text: await renderWorkerInfo(deps, requested === '' ? undefined : requested) }
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        return {
          text: `worker 诊断失败（${message}）。这是只读工具，失败不影响心跳与派单；`
            + '可先看进程日志里的 `[dsh-echuan]` 关键字。',
        }
      }
    },
  })
}

/** 渲染诊断文本；单项服务失败只降级，不影响其余段落。 */
async function renderWorkerInfo(
  deps: WorkerInfoDeps,
  requestedProjectId: string | undefined,
): Promise<string> {
  const lines: string[] = ['驿传 worker 诊断（只读，零 HTTP）', '']

  // 1) 心跳开关与 worker id。
  const id = safeCall(() => deps.workerId())
  lines.push(`poll.enable：${deps.pollEnabled ? 'true' : 'false'}`)
  lines.push(
    id === undefined
      ? 'worker_id：未注册（本轮心跳尚未成功，或 poll.enable=false）'
      : `worker_id：${maskWorkerId(id)}`,
  )

  // 2) inflight。
  const inflight = safeCall(() => deps.inflight()) ?? []
  const nowMs = Date.now()
  lines.push('', `inflight：${inflight.length === 0 ? '（无）' : `${inflight.length} 条`}`)
  for (const item of inflight) {
    lines.push(`  - ${describeInflight(item, nowMs)}`)
  }

  // 3) 会话状态：显式 project_id 优先，否则看 inflight 里压着的项目。
  const projects = unique(
    requestedProjectId === undefined
      ? inflight.map(item => item.projectId)
      : [requestedProjectId, ...inflight.map(item => item.projectId)],
  )
  lines.push('', '会话状态：')
  if (projects.length === 0) {
    lines.push('  （无 inflight 项目；可带 project_id 参数指定要检查的项目）')
  } else {
    for (const projectId of projects) {
      lines.push(`  - ${projectId}：${await describeSession(deps, projectId)}`)
    }
  }

  // 4) 默认 preset。
  lines.push('', `默认 preset：${await describeDefaultPreset(deps.agentPresets)}`)

  // 5) 工作区根目录与工作区列表。
  lines.push(
    '',
    deps.workspaceRoot === undefined || deps.workspaceRoot === ''
      ? '工作区根目录：未配置（优先链：唯一工作区 / 最近有会话的工作区 / 进程目录）'
      : `工作区根目录：${deps.workspaceRoot}（worker.workspaceRoot）`,
  )
  lines.push('工作区列表：')
  const registry = deps.workspaceRegistry
  if (registry === undefined) {
    lines.push('  （workspaceRegistry 服务缺失，无法列出）')
  } else {
    const listed = safeCall(() => registry.list())
    if (listed === undefined) {
      lines.push('  （读取工作区列表失败，可稍后重试）')
    } else if (listed.length === 0) {
      lines.push('  （无）')
    } else {
      for (const workspace of listed) {
        if (typeof workspace?.path !== 'string' || workspace.path === '') continue
        lines.push(`  - ${workspace.path}`)
      }
    }
  }

  return lines.join('\n')
}

/** inflight 一行：任务 / 项目 / 租约到期 / 剩余秒数（过期标注）。 */
function describeInflight(item: Inflight, nowMs: number): string {
  const untilMs = Date.parse(item.leaseUntil)
  const head = `任务 ${item.taskId}｜项目 ${item.projectId}｜租约至 ${item.leaseUntil}`
  if (Number.isNaN(untilMs)) return `${head}｜剩余时间未知（租约时间无法解析）`
  const remainingMs = untilMs - nowMs
  if (remainingMs <= 0) {
    return `${head}｜已过期 ${formatSeconds(-remainingMs)}（下一轮心跳前清除）`
  }
  return `${head}｜剩余 ${formatSeconds(remainingMs)}`
}

/** 单个项目会话的三态：live / persisted / missing，服务缺失时标注降级原因。 */
async function describeSession(deps: WorkerInfoDeps, projectId: string): Promise<string> {
  const agents = deps.agents
  if (agents === undefined) return '无法判定（agents 服务缺失）'

  const agent = safeCall(() => agents.get(projectId))
  if (agent !== undefined && agent !== null) {
    const status = typeof agent.status === 'string' && agent.status !== ''
      ? agent.status
      : '未知'
    return `live（${status}）`
  }

  const persistence = deps.sessionPersistence
  if (persistence === undefined) return '无法判定（sessionPersistence 服务缺失）'

  let stat: { header?: WorkerInfoSessionHeader } | undefined | null
  try {
    stat = await persistence.stat(projectId)
  } catch {
    return '无法判定（sessionPersistence.stat 失败）'
  }
  if (stat === undefined || stat === null) return 'missing（无活跃会话，也未持久化）'

  const header = stat.header ?? {}
  const cwd = typeof header.cwd === 'string' && header.cwd !== '' ? header.cwd : '未记录'
  const preset = typeof header.agentPreset === 'string' && header.agentPreset !== ''
    ? header.agentPreset
    : '未记录'
  return `persisted（cwd=${cwd}, preset=${preset}）`
}

/** 默认 preset：服务缺失 / 解析失败都只标注「不可用」。 */
async function describeDefaultPreset(
  presets: WorkerInfoDeps['agentPresets'],
): Promise<string> {
  if (presets === undefined) return '不可用（agentPresets 服务缺失）'
  try {
    const preset = await presets.resolve()
    if (preset === undefined || preset === null || typeof preset.id !== 'string'
      || preset.id === '') {
      return '不可用（resolve 未返回有效 preset）'
    }
    return preset.id
  } catch (err) {
    return `不可用（解析失败：${err instanceof Error ? err.message : String(err)}）`
  }
}

/** 毫秒 → 「42s」/「3m20s」（诊断文本够用即可）。 */
function formatSeconds(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000))
  if (total < 60) return `${total}s`
  const minutes = Math.floor(total / 60)
  const seconds = total % 60
  if (minutes < 60) return seconds === 0 ? `${minutes}m` : `${minutes}m${seconds}s`
  const hours = Math.floor(minutes / 60)
  const restMinutes = minutes % 60
  return restMinutes === 0 ? `${hours}h` : `${hours}h${restMinutes}m`
}

/** 读依赖时吞掉异常（诊断工具不因单个服务抛错而整体失败）。 */
function safeCall<T>(read: () => T): T | undefined {
  try {
    return read()
  } catch {
    return undefined
  }
}

function unique(values: readonly string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    const trimmed = value.trim()
    if (trimmed === '' || seen.has(trimmed)) continue
    seen.add(trimmed)
    out.push(trimmed)
  }
  return out
}
