/**
 * 客户端错误模型：所有失败路径都收敛到这里的几个类。
 *
 * 契约要点（api/openapi.yaml）：
 * - 错误体统一为 `{ code, message, details, request_id }`，`code` 必须保留；
 * - `message` 不能只回放服务端英文原文，还要告诉调用方「下一步该做什么」；
 * - 429 需要把 `Retry-After` 换算成 `retryAfterMs` 交给调用方，客户端**不自动重试**。
 *
 * 本模块不依赖 `client.ts`，便于单测直接断言错误映射，也避免循环引用。
 */

/** 平台错误体里的 `details` 元素（无明细时平台返回 `[]`）。 */
export interface ErrorDetail {
  field?: string
  reason?: string
}

/** 所有客户端错误的基类：保证任何失败都带 `code`。 */
export class EchuanClientError extends Error {
  /** 机器可读错误码；平台错误沿用平台 code，本地错误使用 `invalid_argument` 等。 */
  readonly code: string

  constructor(code: string, message: string) {
    super(message)
    this.name = new.target.name
    this.code = code
  }
}

/** 发请求前的本地校验失败（参数非法、metadata 超限、不可序列化等）。 */
export class EchuanValidationError extends EchuanClientError {
  /** 出问题的参数名（能定位到时）。 */
  readonly field: string | undefined

  constructor(message: string, code = 'invalid_argument', field?: string) {
    super(code, message)
    this.field = field
  }
}

/** 平台返回的非 2xx 错误，字段与 `api/openapi.yaml#/components/schemas/Error` 对应。 */
export interface EchuanApiErrorInit {
  code: string
  status: number
  message: string
  platformMessage: string
  hint: string
  details: ErrorDetail[]
  requestId?: string
  retryAfterMs?: number
}

export class EchuanApiError extends EchuanClientError {
  /** HTTP 状态码（409 / 413 / 429 …）。 */
  readonly status: number
  /** 平台原始 message，保留用于排障，不要求调用方解析。 */
  readonly platformMessage: string
  /** 「下一步该做什么」的中文提示。 */
  readonly hint: string
  /** 平台 `details`，无明细时为 `[]`。 */
  readonly details: readonly ErrorDetail[]
  /** 关联 id：优先取错误体 `request_id`，其次取 `X-Request-Id` 响应头。 */
  readonly requestId: string | undefined
  /** 仅 429：按 `Retry-After` 换算的建议退避毫秒数（缺失时为保守默认值）。 */
  readonly retryAfterMs: number | undefined

  constructor(init: EchuanApiErrorInit) {
    super(init.code, init.message)
    this.status = init.status
    this.platformMessage = init.platformMessage
    this.hint = init.hint
    this.details = init.details
    this.requestId = init.requestId
    this.retryAfterMs = init.retryAfterMs
  }
}

/** 请求超时（由 `config.requestTimeoutMs` 控制），消息里带实际耗时。 */
export class EchuanTimeoutError extends EchuanClientError {
  /** 配置的超时上限（毫秒）。 */
  readonly timeoutMs: number
  /** 实际等待耗时（毫秒）。 */
  readonly elapsedMs: number

  constructor(timeoutMs: number, elapsedMs: number) {
    super(
      'timeout',
      `请求驿传平台超时：已等待 ${elapsedMs}ms（上限 ${timeoutMs}ms）。`
        + '下一步：按退避稍后重试；若持续超时，检查 baseUrl 与网络，或缩小请求体。',
    )
    this.timeoutMs = timeoutMs
    this.elapsedMs = elapsedMs
  }
}

/**
 * 网络层失败（连接被拒、DNS 失败、提前断开等）。
 *
 * 消息只保留可读结论，**不携带原始堆栈/异常文本**，避免把干扰信息甩给模型。
 */
export class EchuanNetworkError extends EchuanClientError {
  /** 从发起请求到失败的耗时（毫秒）。 */
  readonly elapsedMs: number

  constructor(elapsedMs: number) {
    super(
      'network_error',
      `无法连接驿传平台：网络请求失败（耗时 ${elapsedMs}ms）。`
        + '下一步：检查 baseUrl 是否可达、平台进程是否已启动、网络/代理是否正常，然后重试。',
    )
    this.elapsedMs = elapsedMs
  }
}

/** 429 缺失 `Retry-After` 时的保守默认退避（毫秒）。 */
export const DEFAULT_RETRY_AFTER_MS = 5_000

/**
 * 各错误码对应的「下一步」。
 *
 * 平台错误码枚举见 `api/openapi.yaml#/components/schemas/Error`；
 * 新增码（`worker_unknown` / `worker_busy` / `lease_lost`）的处置口径见
 * `docs/worker-ops.md` 第 3 节。
 */
const CODE_HINTS: Record<string, string> = {
  invalid_argument:
    '下一步：检查请求参数——标识一律走 query（`?id=...`），请求体字段需符合 api/openapi.yaml 的定义，修正后重试；不要原样重发。',
  not_found:
    '下一步：核对 id 是否正确，或先列取列表确认资源仍然存在，再重试。',
  invalid_state:
    '下一步：先重新读取任务/项目的最新状态，按状态机选择当前允许的动作，不要原样重试。',
  invalid_version:
    '下一步：重新读取资源拿到最新版本号，再带正确的 If-Match 重试。',
  conflict:
    '下一步：重新拉取最新数据后再决定，不要原样重试。',
  lease_lost:
    '下一步：租约已失效（过期 / 被回收 / lease_id 不匹配），立即停止执行这张卡，旧结果不可再提交；'
    + '重新调用 `echuan_task_claim` 领卡后再继续。',
  material_required:
    '下一步：补全报告内容后再提交（`echuan_task_report` 的 `summary_md` 必填且不能为空白），'
    + '不要原样重试。',
  too_large:
    '下一步：缩小请求体（精简 metadata、缩短报告或分段提交）后重试；不要原样重发。',
  rate_limited:
    '下一步：按 retryAfterMs 退避后再试，客户端不会自动重试，不要立即打满。',
  internal:
    '下一步：稍后按退避重试；若持续出现，把 requestId 反馈给平台维护者。',
  method_not_allowed:
    '下一步：这是客户端实现与契约不一致，升级/修正插件后再试。',
  timeout:
    '下一步：按退避重试；持续超时则检查平台负载与网络。',
  range_not_satisfiable:
    '下一步：去掉 Range 头，或按资源总长度重新请求。',
  worker_unknown:
    '下一步：本地 worker 未注册（或平台已丢弃该 id），等下一次心跳自动重新注册后再试；'
    + '不要手工伪造 worker_id，也不要重新领卡硬试。',
  worker_busy:
    '下一步：你已持卡，先完成当前卡并用 `echuan_task_report` 汇报，再领新卡；不要并发抢卡。',
}

/** 错误体缺失 code 时，按 HTTP 状态兜底（`docs/worker-ops.md` 第 3 节）。 */
export function fallbackCodeForStatus(status: number): string {
  switch (status) {
    case 400:
      return 'invalid_argument'
    case 404:
      return 'not_found'
    case 409:
      return 'conflict'
    case 413:
      return 'too_large'
    case 416:
      return 'range_not_satisfiable'
    case 422:
      return 'material_required'
    case 429:
      return 'rate_limited'
    default:
      return status >= 500 ? 'internal' : `http_${status}`
  }
}

/** 按错误码取「下一步」；未知码再按状态兜底，仍未知则给出通用建议。 */
export function hintForCode(code: string, status: number): string {
  const byCode = CODE_HINTS[code]
  if (byCode !== undefined) return byCode

  const fallback = fallbackCodeForStatus(status)
  const byStatus = CODE_HINTS[fallback]
  if (byStatus !== undefined && fallback !== code) return byStatus

  return '下一步：把 code 与 requestId 反馈给平台维护者，确认契约后再重试。'
}

/**
 * 把平台 `details` 排成短括号说明，例如 `（title: is required）`。
 *
 * 多个明细用 ` · ` 分隔；`field` / `reason` 缺失的一侧自动省略，
 * 两者都缺失的条目直接丢弃。没有可展示内容时返回空串。
 */
export function formatErrorDetails(details: readonly ErrorDetail[]): string {
  const parts: string[] = []
  for (const detail of details) {
    const field = detail.field?.trim() ?? ''
    const reason = detail.reason?.trim() ?? ''
    if (field !== '' && reason !== '') {
      parts.push(`${field}: ${reason}`)
    } else if (field !== '') {
      parts.push(field)
    } else if (reason !== '') {
      parts.push(reason)
    }
  }
  return parts.length === 0 ? '' : `（${parts.join(' · ')}）`
}

/**
 * 拼装最终给模型看的中文错误消息：平台事实（含 details）+ 下一步 +（429）退避说明。
 *
 * `details` 紧跟在平台 message 后，便于 agent 直接看到 `field/reason`；
 * 不改变 `EchuanApiError.details` 的结构。
 */
export function composeApiMessage(
  status: number,
  code: string,
  platformMessage: string,
  hint: string,
  retryNote?: string,
  details: readonly ErrorDetail[] = [],
): string {
  const detailText = formatErrorDetails(details)
  const base = platformMessage === ''
    ? `平台返回 ${status} ${code}${detailText}。`
    : `平台返回 ${status} ${code}：${platformMessage}${detailText}。`
  return retryNote === undefined ? `${base}${hint}` : `${base}${hint}${retryNote}`
}

/**
 * 解析 `Retry-After`：支持「秒数」与 HTTP-date 两种形态。
 *
 * @returns 毫秒数；缺失或无法解析时返回 `undefined`（由调用方给保守默认值）。
 */
export function parseRetryAfter(
  value: string | null | undefined,
  now: number = Date.now(),
): number | undefined {
  if (value === null || value === undefined) return undefined
  const trimmed = value.trim()
  if (trimmed === '') return undefined

  if (/^\d+$/.test(trimmed)) {
    return Number(trimmed) * 1_000
  }

  const at = Date.parse(trimmed)
  if (Number.isNaN(at)) return undefined
  return Math.max(0, at - now)
}
