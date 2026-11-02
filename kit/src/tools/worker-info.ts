/**
 * `echuan_worker_info`：worker 只读诊断（**零 HTTP、零副作用**）。
 *
 * 排障时模型最需要知道的是「心跳有没有开 / 声明了哪些 kinds / worker 有没有注册 /
 * 手里压着哪几张卡 / 派单会投给哪个 agent」，这些全是进程内内存态，不需要也不应该
 * 打平台：
 *
 * - **不发请求**：不拿 `client`，只读注入的 `workerInfo` 依赖；
 * - **不写盘、不改状态**：所有依赖都是只读投影（`workerId()` / `inflight()` /
 *   `agents.roots()`）；
 * - **服务缺失只降级**：`agents` 缺失只标注「无法判定」，不抛错。
 *
 * 一个 worker 只有一个 agent：这里展示的派单目标固定是 `agents.roots()[0]`，
 * 与心跳循环的投递目标完全一致。
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

/** 诊断工具用到的 agent 投影（只读 `id` / `status`）。 */
export interface WorkerInfoAgent {
  id?: string
  status?: string
}

/** 诊断工具的可注入依赖；全部惰性读取（`index.ts` 用 `ctx.agents` 包一层）。 */
export interface WorkerInfoDeps {
  /** `worker.poll.enable`：心跳是否开启。 */
  pollEnabled: boolean
  /** 配置声明的 kinds（与心跳 body 一致）。 */
  kinds: readonly string[]
  /** 当前 worker id（未注册返回 `undefined`；输出前打码）。 */
  workerId: () => string | undefined
  /** 当前 inflight 快照。 */
  inflight: () => readonly Inflight[]
  /** 本进程 agent 注册表（取第一项作为派单目标；缺失 → 无法判定）。 */
  agents?: { roots(): readonly WorkerInfoAgent[] } | undefined
}

/**
 * `echuan_worker_info`：查看本进程 worker 的运行态（只读，零 HTTP）。
 */
export function createWorkerInfoTool(options: EchuanToolOptions): ToolDefinition {
  return defineTool({
    name: 'echuan_worker_info',
    description:
      '查看本进程驿传（echuan）worker 的运行态（只读，零 HTTP、零副作用）：心跳开关、'
      + '声明的 kinds、worker_id（打码）、派单目标 agent、inflight 里的工作项与租约。'
      + '排障「为什么没派单 / 派单投给了谁」时先看这里。',
    parameters: {},
    output: {
      schema: textOutputSchema,
      render: (_args, value) => renderText(value),
    },
    async execute() {
      const deps = options.workerInfo
      if (deps === undefined) {
        return {
          text: 'worker 诊断不可用：插件未注入 workerInfo 依赖（通常只出现在单测或裁剪过的装配里）。'
            + '本次没有发出任何请求，也没有改动任何状态。',
        }
      }

      try {
        return { text: renderWorkerInfo(deps) }
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

/** 渲染诊断文本；单个依赖读取失败只降级，不影响其余段落。 */
function renderWorkerInfo(deps: WorkerInfoDeps): string {
  const lines: string[] = ['驿传 worker 诊断（只读，零 HTTP）', '']

  // 1) 心跳开关与 kinds。
  lines.push(`poll.enable：${deps.pollEnabled ? 'true' : 'false'}`)
  lines.push(
    deps.kinds.length === 0
      ? 'worker.kinds：（未声明；心跳循环不会启动）'
      : `worker.kinds：${deps.kinds.join('、')}`,
  )

  // 2) worker id。
  const id = safeCall(() => deps.workerId())
  lines.push(
    id === undefined
      ? 'worker_id：未注册（本轮心跳尚未成功，或 poll.enable=false）'
      : `worker_id：${maskWorkerId(id)}`,
  )

  // 3) 派单目标：一个 worker 一个 agent，固定看 roots()[0]。
  lines.push(`派单目标：${describeTarget(deps.agents)}`)

  // 4) inflight。
  const inflight = safeCall(() => deps.inflight()) ?? []
  const nowMs = Date.now()
  lines.push('', `inflight：${inflight.length === 0 ? '（无）' : `${inflight.length} 条`}`)
  for (const item of inflight) {
    lines.push(`  - ${describeInflight(item, nowMs)}`)
  }

  return lines.join('\n')
}

/** 派单目标一行：取第一个 root agent；服务缺失 / 为空都只标注，不抛错。 */
function describeTarget(agents: WorkerInfoDeps['agents']): string {
  if (agents === undefined) return '无法判定（agents 服务缺失）'
  const roots = safeCall(() => agents.roots())
  if (roots === undefined) return '无法判定（读取 agents.roots() 失败）'
  const agent = roots.find(item => item !== undefined && item !== null)
  if (agent === undefined) {
    return '本进程暂无 agent（下一轮心跳会重试投递）'
  }
  const status = typeof agent.status === 'string' && agent.status !== ''
    ? agent.status
    : '状态未知'
  const agentId = typeof agent.id === 'string' && agent.id !== '' ? agent.id : '编号未知'
  return `agent ${agentId}（${status}）`
}

/** inflight 一行：工作项 / kind / 租约到期 / 剩余秒数（过期标注）。 */
function describeInflight(item: Inflight, nowMs: number): string {
  const untilMs = Date.parse(item.leaseUntil)
  const head = `工作项 ${item.workId}｜kind=${item.kind}｜租约至 ${item.leaseUntil}`
  if (Number.isNaN(untilMs)) return `${head}｜剩余时间未知（租约时间无法解析）`
  const remainingMs = untilMs - nowMs
  if (remainingMs <= 0) {
    return `${head}｜已过期 ${formatSeconds(-remainingMs)}（下一轮心跳前清除）`
  }
  return `${head}｜剩余 ${formatSeconds(remainingMs)}`
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
