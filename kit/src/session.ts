/**
 * 会话确保层（技术方案 v0.4 §4 / §4.0 / §4.1）。
 *
 * 投递前保证「项目会话」存在：项目号即会话号，三条路径按固定顺序判定：
 *
 *   1. `agents.get(id)` 命中（live）→ 直接复用，不调 stat / resume / create；
 *   2. `sessionPersistence.stat(id)` 命中（已持久化）→ `resume` 恢复，cwd / preset
 *      一律取会话 header，**不 mkdir、不覆盖 meta**；`agentOptions` 与 create 同源，
 *      否则恢复出的 agent 没有模型，回合发不出请求；
 *   3. 都不存在 → `create` 新建：按 §4.1 优先链选出**工作区根目录**，直接以它作为
 *      会话 cwd（项目号只用于 sessionId，**不拼路径、不建子目录**），建会话并挂
 *      registry 默认 preset。根目录必须已存在：插件不替用户创建目录。
 *
 * 设计要点：
 * - **纯逻辑 + 依赖注入**：本模块不读 `ctx`，所有服务走结构化接口；`createSessionEnsurer`
 *   才用 `ctx.get('…')` 适配，供 worker 投递（K3）使用。
 * - **绝不抛错**：任一必需步骤失败 → `undefined` + warn，调用方降级为「只投在线会话」。
 * - **resume 不被 preset 拖累**：header 记录的 preset 解析失败（已删除 / 改名）只 warn
 *   一条（带 preset id），不挂 preset 继续 resume；create 失败仍然放弃（空壳会话无意义）。
 * - 可选服务（workspaceRegistry / sessionTitle / permissionPresets / agentDefaultModel）
 *   缺失只跳过对应步骤，不阻断 create。
 *
 * 日志前缀统一 `[dsh-echuan/session]`，不打印 token / 密钥。
 */
import { statSync } from 'node:fs'

/** 日志前缀（验收要求）。 */
const TAG = '[dsh-echuan/session]'

/** 新建会话后设置的权限 preset。 */
export const DEFAULT_PERMISSION_PRESET = 'workspace-write'

/** ensure 结局：live 复用 / resume 恢复 / create 新建。 */
export type EnsureOutcome = 'reuse' | 'resumed' | 'created'

/** agent 的最小结构替身（`id` 必填，`session` 用于标题/权限设置）。 */
export interface EnsureAgent {
  readonly id: string
  readonly session?: unknown
}

/** `agentOptions` 的精简投影（provider / model / reasoningEffort，原样透传）。 */
export interface EnsureAgentOptions {
  provider?: string
  model?: string
  reasoningEffort?: string
}

/** `AgentHandle` 的最小结构替身（归属契约：调用方负责 dispose）。 */
export interface EnsureAgentHandle {
  agent: EnsureAgent
  dispose(): Promise<void>
}

/** 新建会话选项（对齐 harness `ctx.agents.create`）。 */
export interface EnsureCreateOptions {
  sessionId: string
  meta?: { cwd?: string; agentPreset?: string }
  agentOptions?: EnsureAgentOptions
  setup?: (agentCtx: unknown) => void | Promise<void>
}

/** 恢复会话选项（对齐 harness `ctx.agents.resume`）。 */
export interface EnsureResumeOptions {
  resumeSessionId: string
  agentOptions?: EnsureAgentOptions
  setup?: (agentCtx: unknown) => void | Promise<void>
}

/** `ctx.agents` 的最小结构替身。 */
export interface EnsureAgentsLike {
  get(id: string): EnsureAgent | undefined | null
  create(options: EnsureCreateOptions): Promise<EnsureAgentHandle>
  resume(options: EnsureResumeOptions): Promise<EnsureAgentHandle>
}

/** 会话 header 的投影（resume 用 `cwd` / `agentPreset`，优先链用 `createdAt`）。 */
export interface EnsureSessionHeader {
  cwd?: string
  agentPreset?: string
  createdAt?: string | number
}

/** `ctx.sessionPersistence` 的最小结构替身（ensure 只需 `stat`）。 */
export interface EnsureSessionPersistenceLike {
  stat(id: string): Promise<{ header?: EnsureSessionHeader } | undefined | null>
}

/** `ctx.agentPresets` 的最小结构替身。 */
export interface EnsureAgentPreset {
  readonly id: string
}

export interface EnsureAgentPresetsLike {
  resolve(id?: string): EnsureAgentPreset | Promise<EnsureAgentPreset>
  mount(agentCtx: unknown, id: string): void | Promise<void>
}

/** `ctx.workspaceRegistry` 的最小结构替身。 */
export interface EnsureWorkspaceLike {
  readonly path: string
  /** 新会话在数组首位（`sessionIds[0]` 最新）。 */
  readonly sessionIds?: readonly string[]
  attachSession?(sessionId: string): void | Promise<void>
}

export interface EnsureWorkspaceRegistryLike {
  list(): EnsureWorkspaceLike[] | Promise<EnsureWorkspaceLike[]>
  /**
   * 把目录登记为工作区（真实签名 `create(path, title?)`）。
   *
   * 只用于「cwd 对应的目录还没被登记」的兜底（正常情况根目录早已在工作区列表里）；
   * 缺失则跳过 attach。
   */
  create?(path: string): Promise<EnsureWorkspaceLike> | EnsureWorkspaceLike
}

/** `ctx.sessionTitle` 的最小结构替身。 */
export interface EnsureSessionTitleLike {
  rename(session: unknown, title: string): void
}

/** `ctx.permissionPresets` 的最小结构替身。 */
export interface EnsurePermissionPresetsLike {
  set(session: unknown, preset: string): void
}

/** `ctx.agentDefaultModel` 的最小结构替身。 */
export interface EnsureAgentDefaultModelLike {
  currentSelection(): EnsureAgentOptions | Promise<EnsureAgentOptions>
}

/** ensure 依赖的可注入面；只有 `agents` 必需。 */
export interface EnsureSessionDeps {
  /** 活跃 agent 注册表（`ctx.agents`）。 */
  agents: EnsureAgentsLike
  /** 会话持久化（缺失 → 无法判定 live 之外的状态，降级 undefined）。 */
  sessionPersistence?: EnsureSessionPersistenceLike
  /** preset registry（缺失 → 无法挂载 preset，降级 undefined）。 */
  agentPresets?: EnsureAgentPresetsLike
  /** 工作区列表（缺失 → 优先链回落 `process.cwd()`）。 */
  workspaceRegistry?: EnsureWorkspaceRegistryLike
  /** 会话标题（缺失跳过）。 */
  sessionTitle?: EnsureSessionTitleLike
  /** 权限 preset（缺失跳过）。 */
  permissionPresets?: EnsurePermissionPresetsLike
  /** 默认模型选择（缺失不传 `agentOptions`）。 */
  agentDefaultModel?: EnsureAgentDefaultModelLike
  /** `worker.workspaceRoot`（已归一化的绝对路径；空/缺省走优先链）。 */
  workspaceRoot?: string
  /** info 日志（成功路径）。 */
  info?: (message: string) => void
  /** warn 日志（所有降级路径都会调用；默认 `console.warn`）。 */
  warn?: (message: string) => void
  /**
   * 目录探测：路径存在且是目录返回 `true`（单测注入，生产用 `node:fs` 的
   * `statSync`）。会话 cwd 就是工作区根目录，必须已存在，插件不会创建它。
   */
  isDirectory?: (path: string) => boolean
  /** 进程目录（单测注入，生产用 `process.cwd()`）。 */
  cwd?: () => string
}

/** ensure 结果：结局 + agent；`handle` 仅 resumed / created 返回，由调用方持有。 */
export interface EnsureSessionResult {
  readonly outcome: EnsureOutcome
  readonly agent: EnsureAgent
  readonly handle?: EnsureAgentHandle
}

/** 适配器用的配置投影（`ResolvedConfig` / `ConfigInput` 都满足）。 */
export interface EnsureSessionConfigLike {
  worker?: { workspaceRoot?: string } | null
}

/** 适配器日志出口（沿用插件 `apply` 的 info / warn）。 */
export interface EnsureSessionLogger {
  info?(message: string): void
  warn?(message: string): void
}

/**
 * 确保目标会话就绪（纯逻辑，可注入依赖）。
 *
 * @param projectId 项目编号，同时作为会话 id 与标题。
 * @param deps 可注入依赖（只有 `agents` 必需）。
 * @returns 命中/新建成功返回结果；任一必需步骤失败返回 `undefined`，**绝不抛错**。
 */
export async function ensureSession(
  projectId: string,
  deps: EnsureSessionDeps,
): Promise<EnsureSessionResult | undefined> {
  const warn = deps.warn ?? ((message: string): void => console.warn(message))
  const info = deps.info ?? ((): void => {})
  const id = typeof projectId === 'string' ? projectId.trim() : ''
  if (id === '') {
    warn(`${TAG} 警告：projectId 为空，放弃本次 ensure`)
    return undefined
  }

  // 1) live 命中 → 直接复用（不调 stat / resume / create）。
  let live: EnsureAgent | undefined | null
  try {
    live = deps.agents.get(id)
  } catch (err) {
    warn(`${TAG} 警告：agents.get 失败，放弃本次 ensure（project=${id}）：${messageOf(err)}`)
    return undefined
  }
  if (live !== undefined && live !== null) {
    info(`${TAG} 会话已 live，直接复用：${id}`)
    return { outcome: 'reuse', agent: live }
  }

  // 2) 已持久化 → resume 恢复。cwd / preset 以 header 为准，不 mkdir、不覆盖 meta。
  if (deps.sessionPersistence === undefined) {
    warn(
      `${TAG} 警告：sessionPersistence 服务缺失，无法判定或恢复会话，`
        + `放弃本次 ensure（project=${id}）`,
    )
    return undefined
  }
  let stat: { header?: EnsureSessionHeader } | undefined | null
  try {
    stat = await deps.sessionPersistence.stat(id)
  } catch (err) {
    warn(`${TAG} 警告：sessionPersistence.stat 失败，放弃本次 ensure（project=${id}）：${messageOf(err)}`)
    return undefined
  }
  if (stat !== undefined && stat !== null) {
    const header = stat.header ?? {}
    // resume 以 header 为准：header 未记录 preset 就不挂，绝不套 registry 默认 preset 覆盖。
    const requestedPreset = typeof header.agentPreset === 'string' && header.agentPreset.trim() !== ''
      ? header.agentPreset
      : undefined
    let setup: ((agentCtx: unknown) => void | Promise<void>) | undefined
    if (requestedPreset === undefined) {
      info(`${TAG} 会话 header 未记录 agentPreset，resume 不挂载 preset（project=${id}）`)
    } else {
      // resume 解析失败只 warn，不挂 preset 继续恢复：header 是历史事实，
      // 为了一个找不到的 preset 放弃投递，等于把已存在的会话永久拉黑。
      const preset = await resolvePreset(deps.agentPresets, requestedPreset, id, warn, 'resume')
      if (preset !== undefined) {
        setup = async (agentCtx: unknown): Promise<void> => {
          await deps.agentPresets?.mount(agentCtx, preset.id)
        }
      }
    }
    // resume 与 create 必须用同一份 agentOptions：宿主 `dsh-agent-loop` 用
    // `options.agentOptions ?? {}` 建 agent，缺模型时回合只能空转（无 request/header）。
    const agentOptions = await resolveAgentOptions(deps.agentDefaultModel, id, warn, 'resume')
    try {
      const handle = await deps.agents.resume({
        resumeSessionId: id,
        ...(agentOptions === undefined ? {} : { agentOptions }),
        ...(setup === undefined ? {} : { setup }),
      })
      info(`${TAG} 会话已持久化，resume 恢复：${id}（cwd=${header.cwd ?? '未记录'}）`)
      return { outcome: 'resumed', agent: handle.agent, handle }
    } catch (err) {
      warn(`${TAG} 警告：resume 失败，放弃本次 ensure（project=${id}）：${messageOf(err)}`)
      return undefined
    }
  }

  // 3) 都不存在 → create 新建。
  const preset = await resolvePreset(deps.agentPresets, undefined, id, warn)
  if (preset === undefined) return undefined

  // cwd 就是工作区根目录本身：项目号只用于 sessionId（项目号即会话号），
  // 不拼 `<root>/<projectId>`，也不替会话建子目录。
  const cwd = await resolveWorkspaceRoot(deps, id, info, warn)
  const isDirectory = deps.isDirectory ?? defaultIsDirectory
  let usable = false
  try {
    usable = isDirectory(cwd)
  } catch (err) {
    warn(
      `${TAG} 警告：无法确认工作目录 ${cwd} 是否存在或是否为目录，`
        + `放弃本次 ensure（project=${id}）：${messageOf(err)}`,
    )
    return undefined
  }
  if (!usable) {
    // 根目录是「当前工作目录 / 已配置的工作区」，理应存在；插件不自动创建，
    // 免得在用户没预期的地方落地一个空目录树。
    warn(
      `${TAG} 警告：工作目录 ${cwd} 不存在或不是目录，放弃本次 ensure（project=${id}）；`
        + '请检查 worker.workspaceRoot 或启动进程的工作目录（插件不会创建目录）',
    )
    return undefined
  }

  const agentOptions = await resolveAgentOptions(deps.agentDefaultModel, id, warn)
  let handle: EnsureAgentHandle
  try {
    handle = await deps.agents.create({
      sessionId: id,
      meta: { cwd, agentPreset: preset.id },
      ...(agentOptions === undefined ? {} : { agentOptions }),
      setup: async (agentCtx: unknown): Promise<void> => {
        await deps.agentPresets?.mount(agentCtx, preset.id)
      },
    })
  } catch (err) {
    warn(`${TAG} 警告：create 失败，放弃本次 ensure（project=${id}, cwd=${cwd}）：${messageOf(err)}`)
    return undefined
  }

  info(`${TAG} 会话已新建：${id}（cwd=${cwd}, preset=${preset.id}）`)

  // 建会话后的收尾：每一步都尽力而为，失败只 warn，绝不回滚已建好的会话。
  await attachWorkspace(deps, id, cwd, info, warn)
  const session = handle.agent.session
  if (session === undefined || session === null) {
    warn(`${TAG} 警告：新建会话的 agent 没有 session，跳过标题与权限设置（project=${id}）`)
  } else {
    tryRename(deps.sessionTitle, session, id, warn)
    trySetPermission(deps.permissionPresets, session, warn, id)
  }

  return { outcome: 'created', agent: handle.agent, handle }
}

/**
 * 适配器：从 Cordis 上下文取可选服务，返回可直接交给 worker 投递使用的 ensure 函数。
 *
 * 服务每次调用时惰性读取（`ctx.get('…')`），避免插件加载早期服务尚未就绪。
 * 只在 `agents` 缺失时直接降级；其余服务缺失由 {@link ensureSession} 各自跳过。
 *
 * @param ctx Cordis 上下文（或任何带 `agents` + `get()` 的结构替身）。
 * @param config 配置（`ResolvedConfig` / `ConfigInput` 均可，只读 `worker.workspaceRoot`）。
 * @param logger 日志出口；缺省 warn 走 `console.warn`。
 */
export function createSessionEnsurer(
  ctx: unknown,
  config?: EnsureSessionConfigLike | null,
  logger?: EnsureSessionLogger,
): (projectId: string) => Promise<EnsureSessionResult | undefined> {
  const lookup = ctx as { agents?: EnsureAgentsLike; get?: (name: string) => unknown }
  const warn = logger?.warn ?? ((message: string): void => console.warn(message))
  const info = logger?.info
  const rawRoot = config?.worker?.workspaceRoot
  const workspaceRoot = typeof rawRoot === 'string' && rawRoot.trim() !== ''
    ? rawRoot.trim()
    : undefined

  const service = <T>(name: string): T | undefined => {
    if (typeof lookup.get !== 'function') return undefined
    try {
      return (lookup.get(name) ?? undefined) as T | undefined
    } catch {
      return undefined
    }
  }

  return async (projectId: string): Promise<EnsureSessionResult | undefined> => {
    const agents = lookup.agents
    if (agents === undefined || agents === null) {
      warn(`${TAG} 警告：agents 服务不可用，放弃本次 ensure（project=${projectId}）`)
      return undefined
    }
    const persistence = service<EnsureSessionPersistenceLike>('sessionPersistence')
    const presets = service<EnsureAgentPresetsLike>('agentPresets')
    const workspaces = service<EnsureWorkspaceRegistryLike>('workspaceRegistry')
    const titles = service<EnsureSessionTitleLike>('sessionTitle')
    const permissions = service<EnsurePermissionPresetsLike>('permissionPresets')
    const models = service<EnsureAgentDefaultModelLike>('agentDefaultModel')
    return ensureSession(projectId, {
      agents,
      ...(persistence === undefined ? {} : { sessionPersistence: persistence }),
      ...(presets === undefined ? {} : { agentPresets: presets }),
      ...(workspaces === undefined ? {} : { workspaceRegistry: workspaces }),
      ...(titles === undefined ? {} : { sessionTitle: titles }),
      ...(permissions === undefined ? {} : { permissionPresets: permissions }),
      ...(models === undefined ? {} : { agentDefaultModel: models }),
      ...(workspaceRoot === undefined ? {} : { workspaceRoot }),
      ...(info === undefined ? {} : { info }),
      warn,
    })
  }
}

// ---------------------------------------------------------------------------
// 内部实现
// ---------------------------------------------------------------------------

/** 默认目录探测：存在且是目录才算可用（不跟随创建、不写盘）。 */
function defaultIsDirectory(path: string): boolean {
  try {
    return statSync(path).isDirectory()
  } catch {
    return false
  }
}

type PresetResolutionMode = 'create' | 'resume'

/**
 * 解析要挂载的 preset：显式 id（resume 的 `header.agentPreset`）优先，缺失取 registry 默认。
 *
 * `mode` 决定解析失败后的语义：
 * - `create`：返回 `undefined`，调用方放弃（没有 preset 的新会话是空壳）；
 * - `resume`：返回 `undefined`，调用方**不挂 preset 继续 resume**（warn 带 preset id）。
 *
 * @returns 解析失败 / 服务缺失返回 `undefined`。
 */
async function resolvePreset(
  presets: EnsureAgentPresetsLike | undefined,
  requested: string | undefined,
  projectId: string,
  warn: (message: string) => void,
  mode: PresetResolutionMode = 'create',
): Promise<EnsureAgentPreset | undefined> {
  const requestedId = requested !== undefined && requested.trim() !== '' ? requested.trim() : undefined
  const label = requestedId ?? '默认 preset'
  if (presets === undefined) {
    warn(mode === 'resume'
      ? `${TAG} 警告：agentPresets 服务缺失，resume 不挂 preset 继续恢复（preset=${label}, project=${projectId}）`
      : `${TAG} 警告：agentPresets 服务缺失，无法挂载 preset，放弃本次 ensure（project=${projectId}）`)
    return undefined
  }
  try {
    const preset = requestedId === undefined
      ? await presets.resolve()
      : await presets.resolve(requestedId)
    if (preset === undefined || preset === null
      || typeof preset.id !== 'string' || preset.id === '') {
      warn(mode === 'resume'
        ? `${TAG} 警告：preset ${label} 未解析出有效结果，resume 不挂 preset 继续恢复（project=${projectId}）`
        : `${TAG} 警告：agentPresets.resolve 未返回有效 preset，放弃本次 ensure（project=${projectId}）`)
      return undefined
    }
    return preset
  } catch (err) {
    warn(mode === 'resume'
      ? `${TAG} 警告：preset ${label} 解析失败，resume 不挂 preset 继续恢复（project=${projectId}）：${messageOf(err)}`
      : `${TAG} 警告：agentPresets.resolve 失败，放弃本次 ensure（project=${projectId}）：${messageOf(err)}`)
    return undefined
  }
}

/**
 * §4.1 优先链：配置根目录 → 唯一工作区 → 最近有新会话的工作区 → `process.cwd()`。
 *
 * 返回值**就是会话 cwd**（调用方不再拼接项目号）。只有「多工作区选中」与
 * 「零工作区回落」需要 warn 并带上路径；唯一工作区是正常情况，走 info（info
 * 缺省为静默）。任何异常都降级到 `process.cwd()`，绝不抛错。
 */
async function resolveWorkspaceRoot(
  deps: EnsureSessionDeps,
  projectId: string,
  info: (message: string) => void,
  warn: (message: string) => void,
): Promise<string> {
  const fallback = (): string => (deps.cwd ?? process.cwd)()

  if (deps.workspaceRoot !== undefined && deps.workspaceRoot !== '') {
    return deps.workspaceRoot
  }

  const registry = deps.workspaceRegistry
  if (registry === undefined) {
    const cwd = fallback()
    warn(
      `${TAG} 警告：未配置 worker.workspaceRoot 且 workspaceRegistry 缺失，`
        + `新建会话回落到进程目录 ${cwd}（project=${projectId}）`,
    )
    return cwd
  }

  let listed: EnsureWorkspaceLike[] | undefined
  try {
    listed = (await registry.list()) ?? []
  } catch (err) {
    const cwd = fallback()
    warn(
      `${TAG} 警告：读取工作区列表失败，新建会话回落到进程目录 ${cwd}`
        + `（project=${projectId}）：${messageOf(err)}`,
    )
    return cwd
  }

  const workspaces = listed.filter(
    (workspace): workspace is EnsureWorkspaceLike =>
      workspace !== undefined && workspace !== null
      && typeof workspace.path === 'string' && workspace.path !== '',
  )

  if (workspaces.length === 0) {
    const cwd = fallback()
    warn(
      `${TAG} 警告：未配置 worker.workspaceRoot 且没有工作区，`
        + `新建会话回落到进程目录 ${cwd}（project=${projectId}）`,
    )
    return cwd
  }

  if (workspaces.length === 1) {
    const only = workspaces[0]!.path
    info(`${TAG} 未配置 worker.workspaceRoot，使用唯一工作区 ${only}（project=${projectId}）`)
    return only
  }

  const latest = await pickLatestWorkspace(deps.sessionPersistence, workspaces)
  if (latest !== undefined) {
    warn(
      `${TAG} 警告：未配置 worker.workspaceRoot，在 ${workspaces.length} 个工作区中`
        + `选用最近有会话的 ${latest}（project=${projectId}）`,
    )
    return latest
  }

  const cwd = fallback()
  warn(
    `${TAG} 警告：未配置 worker.workspaceRoot，${workspaces.length} 个工作区`
      + `都没有可用的会话记录，新建会话回落到进程目录 ${cwd}（project=${projectId}）`,
  )
  return cwd
}

/**
 * 多工作区时取 `sessionIds[0]` 的 `header.createdAt` 最新者。
 *
 * 空 `sessionIds` / stat 失败 / 无 createdAt 的工作区一律跳过；全部不可用返回 `undefined`。
 */
async function pickLatestWorkspace(
  persistence: EnsureSessionPersistenceLike | undefined,
  workspaces: readonly EnsureWorkspaceLike[],
): Promise<string | undefined> {
  if (persistence === undefined) return undefined

  let bestPath: string | undefined
  let bestTime = Number.NEGATIVE_INFINITY
  for (const workspace of workspaces) {
    const newestSession = workspace.sessionIds?.[0]
    if (newestSession === undefined || newestSession === '') continue
    let stat: { header?: EnsureSessionHeader } | undefined | null
    try {
      stat = await persistence.stat(newestSession)
    } catch {
      continue
    }
    const createdAt = createdAtOf(stat?.header)
    if (createdAt === undefined) continue
    if (createdAt > bestTime) {
      bestTime = createdAt
      bestPath = workspace.path
    }
  }
  return bestPath
}

/** 把 `header.createdAt`（ISO 字符串或毫秒时间戳）归一化成可比较的数字。 */
function createdAtOf(header: EnsureSessionHeader | undefined): number | undefined {
  const value = header?.createdAt
  if (typeof value === 'number') return Number.isFinite(value) ? value : undefined
  if (typeof value === 'string') {
    const parsed = Date.parse(value)
    return Number.isNaN(parsed) ? undefined : parsed
  }
  return undefined
}

/** `agentDefaultModel.currentSelection()` → `agentOptions`；缺失/失败返回 `undefined`。 */
async function resolveAgentOptions(
  service: EnsureAgentDefaultModelLike | undefined,
  projectId: string,
  warn: (message: string) => void,
  mode: PresetResolutionMode = 'create',
): Promise<EnsureAgentOptions | undefined> {
  if (service === undefined) return undefined
  let selection: EnsureAgentOptions | undefined
  try {
    selection = await service.currentSelection()
  } catch (err) {
    warn(
      mode === 'resume'
        ? `${TAG} 警告：读取默认模型失败，本次 resume 不带 agentOptions（project=${projectId}）：${messageOf(err)}`
        : `${TAG} 警告：读取默认模型失败，本次 create 不带 agentOptions（project=${projectId}）：${messageOf(err)}`,
    )
    return undefined
  }
  if (selection === undefined || selection === null) return undefined
  const options: EnsureAgentOptions = {}
  if (typeof selection.provider === 'string' && selection.provider !== '') {
    options.provider = selection.provider
  }
  if (typeof selection.model === 'string' && selection.model !== '') {
    options.model = selection.model
  }
  if (typeof selection.reasoningEffort === 'string' && selection.reasoningEffort !== '') {
    options.reasoningEffort = selection.reasoningEffort
  }
  return options.provider === undefined && options.model === undefined
    && options.reasoningEffort === undefined
    ? undefined
    : options
}

/**
 * 按 `path === cwd` 找 workspace 并 attach。
 *
 * 新建会话的 cwd 就是工作区根目录，registry 里通常已经登记过它：命中就直接
 * attachSession，**不新建工作区**。只有确实找不到同路径工作区时才 `create(cwd)`
 * 兜底；create 失败只 warn，不影响已建好的会话。既找不到又不支持 create → info
 * 跳过（正常降级）。
 */
async function attachWorkspace(
  deps: EnsureSessionDeps,
  projectId: string,
  cwd: string,
  info: (message: string) => void,
  warn: (message: string) => void,
): Promise<void> {
  const registry = deps.workspaceRegistry
  if (registry === undefined) return

  let listed: EnsureWorkspaceLike[] | undefined
  try {
    listed = (await registry.list()) ?? []
  } catch (err) {
    warn(`${TAG} 警告：读取工作区列表失败，未 attach 会话（project=${projectId}, cwd=${cwd}）：${messageOf(err)}`)
    return
  }

  let workspace: EnsureWorkspaceLike | undefined
  const found = listed.find(
    item => item !== undefined && item !== null
      && typeof item.path === 'string' && samePath(item.path, cwd),
  )
  if (found !== undefined) {
    workspace = found
  } else if (typeof registry.create !== 'function') {
    // cwd 指向的目录不在工作区列表里，属正常情况，不打扰用户。
    info(`${TAG} 未找到 cwd=${cwd} 对应的工作区且 registry 不支持 create，跳过 attach（project=${projectId}）`)
    return
  } else {
    try {
      workspace = await registry.create(cwd)
    } catch (err) {
      warn(`${TAG} 警告：创建工作区失败，跳过 attach（project=${projectId}, cwd=${cwd}）：${messageOf(err)}`)
      return
    }
    if (workspace === undefined || workspace === null) {
      warn(`${TAG} 警告：创建工作区未返回有效工作区，跳过 attach（project=${projectId}, cwd=${cwd}）`)
      return
    }
    info(`${TAG} 已将 ${cwd} 登记为工作区（project=${projectId}）`)
  }

  const attach = workspace.attachSession
  if (typeof attach !== 'function') {
    info(`${TAG} 工作区 ${workspace.path} 不支持 attachSession，跳过 attach（project=${projectId}）`)
    return
  }
  try {
    await attach.call(workspace, projectId)
  } catch (err) {
    warn(`${TAG} 警告：attachSession 失败（project=${projectId}, cwd=${cwd}）：${messageOf(err)}`)
  }
}

/** 会话标题；服务缺失跳过，失败只 warn。 */
function tryRename(
  service: EnsureSessionTitleLike | undefined,
  session: unknown,
  projectId: string,
  warn: (message: string) => void,
): void {
  if (service === undefined) return
  try {
    service.rename(session, projectId)
  } catch (err) {
    warn(`${TAG} 警告：设置会话标题失败（project=${projectId}）：${messageOf(err)}`)
  }
}

/** 权限 preset；服务缺失跳过，失败只 warn。 */
function trySetPermission(
  service: EnsurePermissionPresetsLike | undefined,
  session: unknown,
  warn: (message: string) => void,
  projectId: string,
): void {
  if (service === undefined) return
  try {
    service.set(session, DEFAULT_PERMISSION_PRESET)
  } catch (err) {
    warn(`${TAG} 警告：设置权限 preset 失败（project=${projectId}）：${messageOf(err)}`)
  }
}

/** 目录比较：去掉结尾斜杠后逐字比较（不解析符号链接）。 */
function samePath(left: string, right: string): boolean {
  return stripTrailingSlash(left) === stripTrailingSlash(right)
}

function stripTrailingSlash(value: string): string {
  return value.length > 1 ? value.replace(/\/+$/, '') : value
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
