/**
 * 工具层：「模型可读文本」渲染与统一错误出口。
 *
 * `render` 只做投影（把已校验的规范值排成中文多行文本），不做任何业务判断；
 * 错误只做「原样透传 code + 平台事实 + 下一步」，不重试、不兜底。
 */

import { EchuanClientError } from '../errors.js'
import type { InboxResponse, MaterialBody, Project, TaskDetail } from '../client.js'
import type { ClaimValue, RenewedValue, ReportedValue } from './write.js'

/** render 返回的最小文本块形态（等价于 ContentBlock 的 `text` 分支）。 */
export interface TextContent {
  type: 'text'
  text: string
}

/** 构造单个文本内容块。 */
export function textBlock(text: string): TextContent {
  return { type: 'text', text }
}

/* ------------------------------------------------------------------ *
 * 错误出口
 * ------------------------------------------------------------------ */

/**
 * 把 execute 内部抛出的错误转成「带 code 的可读错误」。
 *
 * 平台错误（{@link EchuanClientError} 子类）的 message 已包含「平台事实 + 下一步」，
 * 这里只补上机器可读 code，让模型既能看到 code 也能看到该怎么做。
 */
export function toToolError(err: unknown): never {
  if (err instanceof EchuanClientError) {
    throw new Error(`[${err.code}] ${err.message}`)
  }
  throw err instanceof Error ? err : new Error(String(err))
}

/**
 * 平台对写动作返回 204（空响应）：与契约不符，原样报事实，不猜测结果。
 *
 * `action` 用中文动作名（如「续租」），便于模型定位是哪个调用出的问题。
 */
export function unexpectedEmpty(action: string): never {
  throw new Error(
    `[unexpected_empty] 平台对「${action}」返回 204（无内容），与接口契约不符。`
      + '下一步：核对请求参数与平台版本；续租失败时重新 `echuan_task_claim` 确认租约后再继续。',
  )
}

/* ------------------------------------------------------------------ *
 * 领卡
 * ------------------------------------------------------------------ */

/** 204 的统一可读文案：这是正常结果，不是错误。 */
export const NO_WORK_TEXT =
  '当前无活：平台没有可领取的任务（HTTP 204）。'
  + '下一步：稍后再领（不要把「无活」当错误重试）。'

/** 渲染「当前无活」。 */
export function renderNoWork(): TextContent[] {
  return [textBlock(NO_WORK_TEXT)]
}

/** 渲染领到的任务卡：任务编号 / 标题 / 租约到期 / workdir 必须可见。 */
export function renderClaimed(value: Extract<ClaimValue, { kind: 'claimed' }>): TextContent[] {
  return [
    textBlock(
      [
        '已领到任务卡：',
        `- 任务编号：${value.task_id}`,
        `- 标题：${value.title}`,
        `- 租约到期：${value.lease_until}（TTL ${value.ttl_seconds}s，lease_id=${value.lease_id}）`,
        `- 工作目录：${value.workdir}`,
        `- 产品：${value.product_name}（${value.product_id}）`,
        `- 规格：v${value.spec_version} ${value.spec_digest}`,
        `- 尝试次数：${value.attempt}，优先级：${value.priority}`,
        '',
        '任务正文：',
        value.body_md,
        '',
        '下一步：长任务用 `echuan_task_renew` 续租；做完后用 `echuan_task_report` 提交三段式报告。',
      ].join('\n'),
    ),
  ]
}

/* ------------------------------------------------------------------ *
 * 续租
 * ------------------------------------------------------------------ */

/** 渲染续租结果：新的 lease_id 与到期时间。 */
export function renderRenewed(value: RenewedValue): TextContent[] {
  return [
    textBlock(
      [
        `✅ 续租成功：任务 ${value.task_id}`,
        `- 新 lease_id：${value.lease_id}`,
        `- 到期时间：${value.lease_until}`,
        `- TTL：${value.ttl_seconds}s`,
      ].join('\n'),
    ),
  ]
}

/* ------------------------------------------------------------------ *
 * 交报告
 * ------------------------------------------------------------------ */

/** 渲染报告已提交（平台已把任务转入待验收）。 */
export function renderReported(value: ReportedValue): TextContent[] {
  const status = value.status_name === undefined ? '' : `（平台状态：${value.status_name}）`
  return [
    textBlock(
      [
        `✅ 任务已转待验收：任务编号 ${value.task_id}${status}`,
        '下一步：等待平台验收结论；不要再用旧 lease_id 重复提交同一张卡的结果。',
      ].join('\n'),
    ),
  ]
}

/* ------------------------------------------------------------------ *
 * 只读工具：平台返回 → 模型可直读文本
 *
 * 下面这些 formatter 只做投影：把契约字段排成中文多行文本，不判断业务状态、
 * 不补业务默认值（缺字段就明说缺什么）。截断只发生在长度维度，并标注总字数，
 * 让模型知道「还有内容没看到、可以再用 echuan_material_get 取全文」。
 * ------------------------------------------------------------------ */

/** 只读工具输出的规范值：渲染好的文本（只读工具没有别的消费方）。 */
export interface TextValue {
  text: string
}

/** render 的安全投影：value 不是 `{ text }` 时给空串，绝不抛错。 */
export function renderTextValue(value: unknown): TextContent[] {
  const text = isRecord(value) ? asString(value['text']) : undefined
  return [textBlock(text ?? '')]
}

/** 需求摘要的截断上限（字）。 */
export const REQUIREMENT_MAX_CHARS = 1200
/** 方案摘要（`spec_excerpt`）的截断上限（字）。 */
export const SPEC_EXCERPT_MAX_CHARS = 800
/** 任务正文的截断上限（字）。 */
export const TASK_BODY_MAX_CHARS = 6000
/** 报告标题的截断上限（字）。 */
export const REPORT_TITLE_MAX_CHARS = 200
/** 资料正文的截断上限（字）；资料是唯一要尽量给全的只读资源。 */
export const MATERIAL_MAX_CHARS = 20000

/** 项目状态（`Project.status`）的中文名。 */
const PROJECT_STATUS_LABELS: Record<string, string> = {
  '0': '草稿',
  '1': '方案评审',
  '2': '规划中',
  '3': '计划评审',
  '4': '开发中',
  '5': '交付评审',
  '6': '已验收',
  '7': '已取消',
}

/** 任务状态（`Task.status`）的中文名。 */
const TASK_STATUS_LABELS: Record<string, string> = {
  '0': '待办',
  '1': '就绪',
  '2': '进行中',
  '3': '待验收',
  '4': '已完成',
  '5': '失败',
  '6': '阻塞',
  '7': '已取消',
}

/** 待办条目 `kind` 的中文名。 */
const INBOX_KIND_LABELS: Record<string, string> = {
  project: '项目',
  task: '任务',
}

/** 判断一个未知值是不是普通对象（排除 null / 数组）。 */
export function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

/** 只接受字符串。 */
export function asString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

/** 只接受有限数字。 */
export function asNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

/**
 * 秒数 → 人话（如 `3 小时 12 分`）。
 *
 * 只保留最大的两个单位，避免 `1 天 2 小时 3 分 4 秒` 这类噪音。
 */
export function formatDuration(seconds: number | undefined): string {
  const total = asNumber(seconds)
  if (total === undefined || total < 0) return '停留时长未知'
  const whole = Math.floor(total)
  if (whole < 60) return `${whole} 秒`

  const days = Math.floor(whole / 86_400)
  const hours = Math.floor((whole % 86_400) / 3_600)
  const minutes = Math.floor((whole % 3_600) / 60)
  const secs = whole % 60

  const parts: string[] = []
  if (days > 0) parts.push(`${days} 天`)
  if (hours > 0) parts.push(`${hours} 小时`)
  if (minutes > 0) parts.push(`${minutes} 分`)
  if (parts.length === 0) parts.push(`${secs} 秒`)
  return parts.slice(0, 2).join(' ')
}

/** 按「字」截断（不劈开代理对），并标注完整长度。 */
export function truncateText(text: string, maxChars: number): string {
  const chars = [...text]
  if (chars.length <= maxChars) return text
  return `${chars.slice(0, maxChars).join('')}…（已截断，完整内容共 ${chars.length} 字）`
}

/** 字长（按码点，代理对算一个）。 */
function charLength(text: string): number {
  return [...text].length
}

/** 状态名：`中文名（平台名）`；平台没给名字时只给中文名。 */
function withPlatformName(label: string, platformName: string | undefined): string {
  if (platformName === undefined || platformName === '') return label
  if (platformName === label) return label
  return `${label}（${platformName}）`
}

/** 项目状态 → 中文名；未知状态原样标出数字。 */
export function projectStatusLabel(status: number | undefined, platformName?: string): string {
  const key = status === undefined ? '' : String(status)
  const label = PROJECT_STATUS_LABELS[key] ?? (key === '' ? '状态未知' : `未知状态 ${key}`)
  return withPlatformName(label, platformName)
}

/** 任务状态 → 中文名；未知状态原样标出数字。 */
export function taskStatusLabel(status: number | undefined, platformName?: string): string {
  const key = status === undefined ? '' : String(status)
  const label = TASK_STATUS_LABELS[key] ?? (key === '' ? '状态未知' : `未知状态 ${key}`)
  return withPlatformName(label, platformName)
}

/** 平台字段为 null/空白时的统一占位。 */
function orNone(value: string | undefined): string {
  const trimmed = value?.trim()
  return trimmed === undefined || trimmed === '' ? '无' : trimmed
}

/**
 * 待办列表：一行汇总 + 逐条（编号 / 状态 / 停留时长 / 是否超时 / 标题 / 原因）。
 */
export function formatInbox(res: InboxResponse): string {
  const items = Array.isArray(res.items) ? res.items : []
  const count = asNumber(res.count) ?? items.length
  const staleHours = asNumber(res.review_stale_hours)
  const staleText = staleHours === undefined ? '超时阈值未知' : `超时阈值 ${staleHours} 小时`
  const generatedAt = asString(res.generated_at) ?? '未知'

  if (items.length === 0) {
    return [
      `当前没有待处理项（平台计数 ${count} 条，${staleText}）。`,
      '下一步：稍后再用 `echuan_inbox` 看一眼；要盯具体进度可带编号用 `echuan_project_get` / `echuan_task_get`。',
    ].join('\n')
  }

  const head = count === items.length
    ? `待办共 ${items.length} 条｜${staleText}｜生成时间 ${generatedAt}`
    : `待办共 ${count} 条（本次返回 ${items.length} 条，可用 limit 调整）｜${staleText}｜生成时间 ${generatedAt}`

  const lines = items.map((item) => {
    const kind = asString(item.kind) ?? ''
    const kindLabel = INBOX_KIND_LABELS[kind] ?? (kind === '' ? '未知类型' : kind)
    const id = orNone(asString(item.id))
    const status = asNumber(item.state)
    const statusLabel = kind === 'project' ? projectStatusLabel(status) : taskStatusLabel(status)
    const age = formatDuration(asNumber(item.age_seconds))
    const stale = item.stale === true ? '⚠ 已超时' : '正常'
    const title = orNone(asString(item.title))
    const reason = orNone(asString(item.reason))

    // 平台状态名（state_name）与停留时长放进同一对括号，避免括号套括号。
    const platformName = asString(item.state_name)
    const inner: string[] = []
    if (platformName !== undefined && platformName !== '' && platformName !== statusLabel) {
      inner.push(platformName)
    }
    inner.push(`已停留 ${age}`, stale)

    return `- [${kindLabel}] ${id}：${statusLabel}（${inner.join('，')}）｜${title}｜原因：${reason}`
  })

  return [
    head,
    ...lines,
    '下一步：带编号用 `echuan_project_get` 看项目全景、`echuan_task_get` 看任务详情；'
      + '需求/方案/报告正文用 `echuan_material_get` 取。',
  ].join('\n')
}

/** 任务统计 → `共 N 个｜已完成 M 个｜按状态：待办 3 / 进行中 2`。 */
export function formatTaskStats(stats: unknown): string {
  if (!isRecord(stats)) return '平台未给出'

  const total = asNumber(stats['total']) ?? 0
  const done = asNumber(stats['done']) ?? 0
  const parts = [`共 ${total} 个`, `已完成 ${done} 个`]

  const byStatus = stats['by_status']
  if (isRecord(byStatus)) {
    const buckets = Object.keys(byStatus)
      .sort((left, right) => Number(left) - Number(right))
      .map((key) => {
        const value = asNumber(byStatus[key]) ?? 0
        return `${taskStatusLabel(Number(key))} ${value}`
      })
    if (buckets.length > 0) parts.push(`按状态：${buckets.join(' / ')}`)
  }

  return parts.join('｜')
}

/** 物料引用（`Material` 元信息）→ `标题（v3，material_id=...，12800B）`。 */
function formatMaterialRef(material: unknown, emptyText: string): string {
  if (!isRecord(material)) return emptyText

  const title = orNone(asString(material['title']))
  const version = asNumber(material['version'])
  const size = asNumber(material['size'])
  const materialId = asString(material['material_id'])

  const extras: string[] = []
  if (version !== undefined) extras.push(`v${version}`)
  if (materialId !== undefined) extras.push(`material_id=${materialId}`)
  if (size !== undefined) extras.push(`${size}B`)

  return extras.length === 0 ? title : `${title}（${extras.join('，')}）`
}

/**
 * 项目全景：编号 / 标题 / 状态 / 需求摘要 / 方案标题与版本 / 任务统计 / 原因。
 */
export function formatProject(detail: Project): string {
  const projectId = asString(detail.project_id) ?? asString(detail.id) ?? '（编号缺失）'
  const title = orNone(asString(detail.title))
  const status = projectStatusLabel(asNumber(detail.status), asString(detail.status_name))

  const requirement = asString(detail.requirement_md)?.trim() ?? ''
  const excerpt = asString(detail.spec_excerpt)?.trim() ?? ''

  const lines = [
    `项目 ${projectId}：${title}`,
    `- 状态：${status}`,
    `- 任务统计：${formatTaskStats(detail.task_stats)}`,
    `- 原因说明：${orNone(asString(detail.reason))}`,
    '需求摘要：',
    requirement === '' ? '（需求正文为空）' : truncateText(requirement, REQUIREMENT_MAX_CHARS),
    `方案：${formatMaterialRef(detail.spec, '暂无（平台未给出方案）')}`,
  ]
  if (excerpt !== '') lines.push(`方案摘要：${truncateText(excerpt, SPEC_EXCERPT_MAX_CHARS)}`)
  lines.push('下一步：`echuan_task_get` 看具体任务；需求/方案全文用 `echuan_material_get` 取。')
  return lines.join('\n')
}

/** `depends_on` → `A、B`；空数组/缺失 → `无`。 */
function formatDependsOn(value: unknown): string {
  if (!Array.isArray(value)) return '无'
  const ids = value
    .map(item => asString(item)?.trim())
    .filter((item): item is string => item !== undefined && item !== '')
  return ids.length === 0 ? '无' : ids.join('、')
}

/** 租约（`TaskLease`）→ 一行；没有租约返回 `undefined`。 */
function formatLease(lease: unknown): string | undefined {
  if (!isRecord(lease)) return undefined
  const leaseId = asString(lease['lease_id'])
  const until = asString(lease['until'])
  if (leaseId === undefined && until === undefined) return undefined
  return `lease_id=${leaseId ?? '未知'}，到期 ${until ?? '未知'}`
}

/**
 * 任务详情：编号 / 所属项目 / 标题 / 状态 / 优先级 / 尝试次数 / 正文 / 报告 / 原因 / 租约。
 */
export function formatTask(detail: TaskDetail): string {
  const taskId = asString(detail.task_id) ?? asString(detail.id) ?? '（编号缺失）'
  const title = orNone(asString(detail.title))
  const body = asString(detail.body_md)?.trim() ?? ''
  const attempt = asNumber(detail.attempt_count)
  const maxAttempts = asNumber(detail.max_attempts)
  const leaseLine = formatLease(detail.lease)

  const lines = [`任务 ${taskId}：${title}`]
  const projectId = asString(detail.project_id)
  if (projectId !== undefined && projectId !== '') lines.push(`- 所属项目：${projectId}`)
  lines.push(`- 状态：${taskStatusLabel(asNumber(detail.status), asString(detail.status_name))}`)
  lines.push(`- 优先级：${asNumber(detail.priority) ?? '未知'}`)
  lines.push(`- 尝试次数：${attempt ?? '未知'}/${maxAttempts ?? '未知'}`)
  lines.push(`- 依赖：${formatDependsOn(detail.depends_on)}`)
  lines.push(`- 原因说明：${orNone(asString(detail.reason))}`)
  if (leaseLine !== undefined) lines.push(`- 租约：${leaseLine}`)
  lines.push('', '正文：', body === '' ? '（正文为空）' : truncateText(body, TASK_BODY_MAX_CHARS))
  lines.push(
    `当前报告：${truncateText(
      formatMaterialRef(detail.report, '无（尚未提交报告）'),
      REPORT_TITLE_MAX_CHARS,
    )}`,
  )
  lines.push('下一步：报告全文用 `echuan_material_get`（material_id 见上）取；待办总览用 `echuan_inbox`。')
  return lines.join('\n')
}

/**
 * 资料正文：一行元信息 + 原文（超长按字截断并标注总长）。
 */
export function formatMaterial(material: MaterialBody): string {
  const content = typeof material.content === 'string' ? material.content : ''
  const contentType = material.contentType.trim() === '' ? '未知' : material.contentType
  const meta: string[] = [`content-type ${contentType}`, `${charLength(content)} 字`]
  if (material.etag !== undefined && material.etag !== '') meta.push(`etag ${material.etag}`)
  if (material.materialVersion !== undefined) meta.push(`版本 ${material.materialVersion}`)
  const head = `资料 ${material.materialId ?? '（编号未知）'}｜${meta.join('｜')}`

  if (content.trim() === '') {
    return [
      head,
      '资料正文为空。',
      '下一步：先核对编号是否正确（可用 `echuan_inbox` 找到相关编号，或从项目/任务详情里的 material_id 复制），再重试。',
    ].join('\n')
  }

  return `${head}\n\n${truncateText(content, MATERIAL_MAX_CHARS)}`
}

/**
 * 三个按 id 的只读工具共用「缺参本地拒绝」文案（零请求）。
 *
 * @param subject 中文资源名（项目 / 任务 / 资料）
 * @param extra 额外的编号来源提示（可选，例如资料 id 可从详情里复制）
 */
export function missingIdText(subject: string, extra?: string): string {
  const lines = [`缺少 id：请先用 \`echuan_inbox\` 找到${subject}编号，再用该编号查询。`]
  if (extra !== undefined) lines.push(extra)
  lines.push('本次已本地拒绝，未发出任何请求。')
  return lines.join('\n')
}
