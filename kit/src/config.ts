/**
 * dsh-echuan 配置声明（技术方案 v0.6 §5）。
 *
 * 用 Cordis 原生 Schema（`@deepseek-ai/schemastery`）声明 5 项配置，宿主在插件激活前
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
  worker: { poll: { enable: false }, metadata: {} },
}

/**
 * 把任意输入（含 `undefined` / `null` / 部分字段）归一化成完整的运行期配置。
 *
 * 行为：
 * - 缺省字段回落默认值（Schema 的 `.default()` 负责）；
 * - `baseUrl` 去掉结尾斜杠，为空则回落默认值；
 * - `worker.metadata` 原样保留（不做深拷贝、不做键裁剪）。
 *
 * 输入非法（如 `requestTimeoutMs` 传字符串）时按 Schema 语义抛 `TypeError`，
 * 由宿主在激活阶段报错——配置错误不应被静默吞掉。
 */
export function resolveConfig(input?: ConfigInput | null): ResolvedConfig {
  const resolved = Config(input ?? {})
  const baseUrl = (resolved.baseUrl ?? '').trim().replace(/\/+$/, '')
  return {
    baseUrl: baseUrl === '' ? DEFAULT_BASE_URL : baseUrl,
    actor: resolved.actor,
    requestTimeoutMs: resolved.requestTimeoutMs,
    worker: {
      poll: { enable: resolved.worker.poll.enable },
      metadata: resolved.worker.metadata,
    },
  }
}

/**
 * `worker.metadata` 序列化后的字节数（UTF-8）。
 *
 * 只用于在 `apply` 阶段给出超限告警，不截断数据——上报内容由平台契约决定，
 * 插件不替调用方做有损处理。
 */
export function metadataByteLength(metadata: Record<string, unknown>): number {
  try {
    return Buffer.byteLength(JSON.stringify(metadata) ?? '', 'utf8')
  } catch {
    // 含循环引用 / BigInt 等不可序列化值：按 0 处理，由上报时的错误暴露问题。
    return 0
  }
}
