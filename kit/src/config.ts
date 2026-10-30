/**
 * dsh-echuan 配置声明（技术方案 v0.6 §5）。
 *
 * 用 Cordis 原生 Schema（`@deepseek-ai/schemastery`）声明 6 项配置，宿主在插件激活前
 * 用这份 Schema 校验 patch 行里的 `config`，并把默认值填好再传给 `apply`。
 * `resolveConfig` 是**唯一**的归一化出口：`apply` 与单测都走它，避免两套默认值。
 *
 * 注意 `worker` 是嵌套对象：`config.worker.poll.enable` / `config.worker.metadata`。
 */
import Schema from '@deepseek-ai/schemastery'

/** 平台地址默认值。 */
export const DEFAULT_BASE_URL = 'http://127.0.0.1:8720'
/** `X-Actor` 默认值：写操作身份，**不是** worker_id。 */
export const DEFAULT_ACTOR = 'agent:echuan'
/** 单请求超时默认值（毫秒）。 */
export const DEFAULT_REQUEST_TIMEOUT_MS = 35_000
/** `worker.metadata` 序列化后的上限（字节）。 */
export const WORKER_METADATA_MAX_BYTES = 8 * 1024

/** 插件收到的原始配置：字段均可缺省，由 Schema 的默认值补齐。 */
export interface ConfigInput {
  baseUrl?: string | null
  actor?: string | null
  requestTimeoutMs?: number | null
  exclude?: string[] | null
  worker?: {
    poll?: { enable?: boolean | null } | null
    metadata?: Record<string, unknown> | null
  } | null
}

/** 归一化后的运行期配置。 */
export interface ResolvedConfig {
  /** 已去掉结尾斜杠的平台地址。 */
  baseUrl: string
  /** `X-Actor` 声明值。 */
  actor: string
  /** 单请求超时（毫秒）。 */
  requestTimeoutMs: number
  /** 不注册的工具名（已 trim、去重、保序）；默认 `[]` 表示全部注册。 */
  exclude: string[]
  worker: {
    poll: {
      /** 是否启用定时心跳；默认 false。 */
      enable: boolean
    }
    /** 任意 JSON 对象，原样上报。 */
    metadata: Record<string, unknown>
  }
}

/**
 * 配置 Schema：导出的名字必须是 `Config`，宿主按此名读取。
 *
 * 类型显式标注（而非依赖推断）是因为 schemastery 的实例类型引用了
 * `@deepseek-ai/cosmokit` 的 `Dict`：不标注时 `declaration` 生成会报
 * TS2742「inferred type cannot be named」。
 */
export const Config: Schemastery<ConfigInput, ResolvedConfig> = Schema.object({
  baseUrl: Schema.string()
    .default(DEFAULT_BASE_URL)
    .description('驿传平台地址，例如 http://127.0.0.1:8720（结尾斜杠会被去掉）'),
  actor: Schema.string()
    .default(DEFAULT_ACTOR)
    .description('X-Actor 声明值，写操作身份；不是 worker_id'),
  requestTimeoutMs: Schema.natural()
    .min(1)
    .default(DEFAULT_REQUEST_TIMEOUT_MS)
    .description('单请求超时（毫秒）'),
  exclude: Schema.array(Schema.string())
    .default([])
    .description('不注册的工具名；默认全部注册（只影响本进程注册，不改 HTTP 契约）'),
  worker: Schema.object({
    poll: Schema.object({
      enable: Schema.boolean()
        .default(false)
        .description('是否由插件定时心跳并拉取任务；关闭时插件不创建任何定时器'),
    })
      .default({})
      .description('心跳与任务拉取'),
    metadata: Schema.dict(Schema.any())
      .default({})
      .description('任意 JSON 对象，随心跳原样上报（序列化后 ≤ 8KiB）'),
  })
    .default({})
    .description('worker 声明与心跳配置'),
}).description('dsh-echuan 插件配置')

/** 无配置时的完整默认值（单测与文档可直接引用）。 */
export const DEFAULT_CONFIG: ResolvedConfig = {
  baseUrl: DEFAULT_BASE_URL,
  actor: DEFAULT_ACTOR,
  requestTimeoutMs: DEFAULT_REQUEST_TIMEOUT_MS,
  exclude: [],
  worker: { poll: { enable: false }, metadata: {} },
}

/**
 * 把任意输入（含 `undefined` / `null` / 部分字段）归一化成完整的运行期配置。
 *
 * 行为：
 * - 缺省字段回落默认值（Schema 的 `.default()` 负责）；
 * - `baseUrl` 去掉结尾斜杠，为空则回落默认值；
 * - `worker.metadata` 原样保留（不做深拷贝、不做键裁剪）；
 * - `exclude` 每项做 `trim`、去重（保序），空白项抛 `TypeError`；工具名是否合法
 *   由 `tool-names.ts` 的 `validateExclude` 在插件入口校验（config 不依赖 dsh-tools）。
 *
 * 输入非法（如 `requestTimeoutMs` 传字符串）时按 Schema 语义抛 `TypeError`，
 * 由宿主在激活阶段报错——配置错误不应被静默吞掉。
 */
export function resolveConfig(input?: ConfigInput | null): ResolvedConfig {
  const resolved = Config(input ?? {})
  const baseUrl = normalizeBaseUrl(resolved.baseUrl)
  const actor = (resolved.actor ?? '').trim()
  return {
    baseUrl,
    // S-02：actor 是写操作身份，前后空白没有语义；trim 后为空则回落默认值。
    actor: actor === '' ? DEFAULT_ACTOR : actor,
    requestTimeoutMs: resolved.requestTimeoutMs,
    exclude: normalizeExclude(resolved.exclude),
    worker: {
      poll: { enable: resolved.worker.poll.enable },
      metadata: resolved.worker.metadata,
    },
  }
}

/**
 * 归一化 `exclude`：trim、去重（保序）、空白项抛错。
 *
 * 这里只做**形态**归一化，不判断工具名是否合法（那是 `tool-names.ts` 的职责，
 * 它在插件入口同步执行）。空白项不能静默丢弃：那会让「以为排除了某工具」变成
 * 「其实没排除」，属于配置错误。
 */
function normalizeExclude(exclude?: readonly string[] | null): string[] {
  if (exclude === undefined || exclude === null || exclude.length === 0) return []
  const normalized: string[] = []
  const seen = new Set<string>()
  for (const item of exclude) {
    const name = item.trim()
    if (name === '') {
      throw new TypeError(
        'exclude 不能包含空白工具名。请填写完整工具名，或直接删掉该条目（exclude: [] 表示不排除）。',
      )
    }
    if (seen.has(name)) continue
    seen.add(name)
    normalized.push(name)
  }
  return normalized
}

/**
 * 归一化并校验 `baseUrl`（S-03）。
 *
 * - 空白 → 默认值；
 * - 必须是合法 URL，且协议为 http/https；
 * - 不允许带账号、查询串或片段（它们会与 `/v1` 路径拼接产生歧义）；
 * - 去掉结尾斜杠，避免与 `/v1/...` 拼出双斜杠。
 *
 * 非法输入直接抛带中文说明的 `TypeError`，**不要**把坏 URL 交给 fetch。
 */
function normalizeBaseUrl(value?: string | null): string {
  const raw = (value ?? '').trim()
  if (raw === '') return DEFAULT_BASE_URL

  let url: URL
  try {
    url = new URL(raw)
  } catch {
    throw new TypeError(
      `baseUrl 不是合法的 URL：${raw}。请改成 http://host:port 形式（仅支持 http/https）。`,
    )
  }

  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new TypeError(
      `baseUrl 只支持 http/https，收到 ${url.protocol}//：${raw}。请检查配置项 baseUrl。`,
    )
  }
  if (url.username !== '' || url.password !== '') {
    throw new TypeError(
      `baseUrl 不能包含账号或密码：${raw}。请把凭据移到部署环境，不要写进平台地址。`,
    )
  }
  if (url.search !== '' || url.hash !== '') {
    throw new TypeError(
      `baseUrl 不能带查询串或片段：${raw}。请只保留协议、主机与端口（可带路径前缀）。`,
    )
  }

  return raw.replace(/\/+$/, '')
}

/**
 * `worker.metadata` 序列化后的字节数（UTF-8）。
 *
 * 用于 `apply` 阶段的超限告警与客户端的发送前置校验（D-01），不触发截断——
 * 上报内容由平台契约决定，插件不替调用方做有损处理。
 *
 * S-04：含循环引用 / BigInt 等不可序列化值时**抛错**，不能返回 0 让预算告警失效。
 */
export function metadataByteLength(metadata: Record<string, unknown>): number {
  let json: string | undefined
  try {
    json = JSON.stringify(metadata)
  } catch (err) {
    const reason = err instanceof Error ? err.message : String(err)
    throw new TypeError(
      `worker.metadata 无法序列化为 JSON（${reason}）。`
        + '请移除循环引用 / BigInt 等不可序列化值后再试。',
    )
  }
  if (json === undefined) {
    throw new TypeError(
      'worker.metadata 无法序列化为 JSON。请传入普通对象，而不是函数或 Symbol。',
    )
  }
  return Buffer.byteLength(json, 'utf8')
}
