/**
 * 工具层：「模型可读文本」渲染与统一错误出口。
 *
 * `render` 只做投影（把已校验的规范值排成中文多行文本），不做任何业务判断；
 * 错误只做「原样透传 code + 平台事实 + 下一步」，不重试、不兜底。
 */

import { EchuanApiError, EchuanClientError, formatErrorDetails } from '../errors.js'
import type {
  InboxResponse,
  MaterialBody,
  Product,
  Project,
  WorkItemDetail,
} from '../client.js'
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
    // 平台 400 等错误带 `details`（field/reason）时一并渲染，agent 才能知道缺哪个字段。
    // 客户端已把它拼进 message 的情况下不重复追加（保留 `EchuanApiError.details` 原结构）。
    const detailText = err instanceof EchuanApiError ? formatErrorDetails(err.details) : ''
    const suffix = detailText !== '' && !err.message.includes(detailText) ? detailText : ''
    throw new Error(`[${err.code}] ${err.message}${suffix}`)
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
      + '下一步：核对请求参数与平台版本；续租失败时重新 `echuan_work_claim` 确认租约后再继续。',
  )
}

/* ------------------------------------------------------------------ *
 * 领卡
 * ------------------------------------------------------------------ */

/** 204 的统一可读文案：这是正常结果，不是错误。 */
export const NO_WORK_TEXT =
  '当前无活：平台没有可领取的 work item（HTTP 204）。'
  + '下一步：稍后再领（不要把「无活」当错误重试）。'

/** 渲染「当前无活」。 */
export function renderNoWork(): TextContent[] {
  return [textBlock(NO_WORK_TEXT)]
}

/** 渲染领到的卡：只陈述事实（编号 / kind / 标题 / 租约 / 产品 / 规格 / inputs / 正文）。 */
export function renderClaimed(value: Extract<ClaimValue, { kind: 'claimed' }>): TextContent[] {
  const lines = [
    '已领到 work item：',
    `- 工作项编号：${value.work_id}`,
    `- 类别：${value.work_kind}（${workItemKindLabel(value.work_kind)}）`,
    `- 标题：${value.title}`,
    `- 租约到期：${value.lease_until}（TTL ${value.ttl_seconds}s，lease_id=${value.lease_id}）`,
    `- 产品：${value.product_name}（${value.product_id}）`,
    `- 规格：v${value.spec_version} ${value.spec_digest}`,
    `- 尝试次数：${value.attempt}，优先级：${value.priority}`,
  ]

  lines.push('- 输入物料：')
  if (value.inputs.length === 0) {
    lines.push('  - 无')
  } else {
    for (const input of value.inputs) {
      lines.push(`  - material_id=${input.material_id}，kind=${input.kind}，版本 v${input.version}`)
    }
  }

  lines.push('', '工作项正文：', value.body_md)
  return [textBlock(lines.join('\n'))]
}

/* ------------------------------------------------------------------ *
 * 续租
 * ------------------------------------------------------------------ */

/** 渲染续租结果：新的 lease_id 与到期时间。 */
export function renderRenewed(value: RenewedValue): TextContent[] {
  return [
    textBlock(
      [
        `✅ 续租成功：工作项 ${value.work_id}`,
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

/** 渲染报告已提交（平台已把工作项转入待验收）。 */
export function renderReported(value: ReportedValue): TextContent[] {
  const state = value.state === undefined ? '' : `（平台状态：${value.state}）`
  return [
    textBlock(
      [
        `✅ 工作项已提交待验收：工作项编号 ${value.work_id}${state}`,
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

/** 工作项正文的截断上限（字）。 */
export const WORK_ITEM_BODY_MAX_CHARS = 6000
/** 报告标题的截断上限（字）。 */
export const REPORT_TITLE_MAX_CHARS = 200
/** 资料正文的截断上限（字）；资料是唯一要尽量给全的只读资源。 */
export const MATERIAL_MAX_CHARS = 20000

/** 项目状态（`Project.status`）的中文名（九态）。 */
const PROJECT_STATUS_LABELS: Record<string, string> = {
  '0': '需求草稿',
  '1': '需求评审',
  '2': '方案草稿',
  '3': '方案评审',
  '4': '开发任务草稿',
  '5': '开发任务评审',
  '6': '待完成',
  '7': '已完成',
  '8': '已取消',
}

/** 工作项状态（`WorkItem.state`）的中文名。 */
const WORK_ITEM_STATE_LABELS: Record<string, string> = {
  blocked: '待评审',
  queued: '待开发',
  leased: '开发中',
  submitted: '待验收',
  accepted: '已完成',
  failed: '已失败',
  cancelled: '已取消',
}

/** 工作项类别（`kind`）的中文名。 */
const WORK_ITEM_KIND_LABELS: Record<string, string> = {
  requirement: '需求',
  design: '方案',
  breakdown: '拆解',
  dev: '开发',
}

/** 待办条目 `kind` 的中文名。 */
const INBOX_KIND_LABELS: Record<string, string> = {
  project: '项目',
  work_item: '工作项',
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
export function projectStatusLabel(
  status: number | string | undefined,
  platformName?: string,
): string {
  const key = status === undefined ? '' : String(status)
  const label = PROJECT_STATUS_LABELS[key] ?? (key === '' ? '状态未知' : `未知状态 ${key}`)
  return withPlatformName(label, platformName)
}

/** 工作项状态 → 中文名；未知状态原样标出。 */
export function workItemStateLabel(state: string | undefined): string {
  if (state === undefined || state === '') return '状态未知'
  return WORK_ITEM_STATE_LABELS[state] ?? `未知状态 ${state}`
}

/** 工作项类别 → 中文名；未知类别原样标出。 */
export function workItemKindLabel(kind: string | undefined): string {
  if (kind === undefined || kind === '') return '类别未知'
  return WORK_ITEM_KIND_LABELS[kind] ?? kind
}

/** 平台字段为 null/空白时的统一占位。 */
function orNone(value: string | undefined): string {
  const trimmed = value?.trim()
  return trimmed === undefined || trimmed === '' ? '无' : trimmed
}

/**
 * 待办列表：一行汇总 + 逐条（类型 / 编号 / 状态 / 停留时长 / 是否超时 / 标题 / 原因）。
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
      '下一步：稍后再用 `echuan_inbox` 看一眼；要盯具体进度可带编号用 `echuan_project_get` / `echuan_work_get`。',
    ].join('\n')
  }

  const head = count === items.length
    ? `待办共 ${items.length} 条｜${staleText}｜生成时间 ${generatedAt}`
    : `待办共 ${count} 条（本次返回 ${items.length} 条，可用 limit 调整）｜${staleText}｜生成时间 ${generatedAt}`

  const lines = items.map((item) => {
    const kind = asString(item.kind) ?? ''
    const kindLabel = INBOX_KIND_LABELS[kind] ?? (kind === '' ? '未知类型' : kind)
    const id = orNone(asString(item.id))
    const state = asString(item.state)
    const statusLabel = kind === 'project'
      ? projectStatusLabel(state)
      : workItemStateLabel(state)
    const age = formatDuration(asNumber(item.age_seconds))
    const stale = item.stale === true ? '⚠ 已超时' : '正常'
    const title = orNone(asString(item.title))
    const reason = orNone(asString(item.reason))

    const inner: string[] = []
    const platformName = asString(item.state_name)
    if (platformName !== undefined && platformName !== '' && platformName !== statusLabel) {
      inner.push(platformName)
    }
    inner.push(`已停留 ${age}`, stale)

    return `- [${kindLabel}] ${id}：${statusLabel}（${inner.join('，')}）｜${title}｜原因：${reason}`
  })

  return [
    head,
    ...lines,
    '下一步：带编号用 `echuan_project_get` 看项目全景、`echuan_work_get` 看工作项详情；'
      + '需求/方案/报告正文用 `echuan_material_get` 取。',
  ].join('\n')
}

/**
 * 工作项统计 → `共 N 个｜已完成 M 个｜按状态：待开发 3 / 开发中 2`。
 *
 * 平台项目详情把统计拆成 `work_item_total` / `work_item_done` /
 * `work_item_by_state` 三个字段，这里只做投影。
 */
export function formatWorkItemStats(
  total: unknown,
  done: unknown,
  byState: unknown,
): string {
  const totalCount = asNumber(total) ?? 0
  const doneCount = asNumber(done) ?? 0
  const parts = [`共 ${totalCount} 个`, `已完成 ${doneCount} 个`]

  if (isRecord(byState)) {
    const buckets = Object.keys(byState)
      .sort()
      .map((key) => {
        const value = asNumber(byState[key]) ?? 0
        return `${workItemStateLabel(key)} ${value}`
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
 * 项目全景：编号 / 标题 / 状态 / 工作项统计 / 原因 / 需求与方案的物料索引。
 *
 * 平台详情不再下发正文，这里只给 `material_id` 等元信息；正文由模型按需
 * 调 `echuan_material_get` 取，所以输出末尾固定提示这一步。
 */
export function formatProject(detail: Project): string {
  const projectId = asString(detail.project_id) ?? asString(detail.id) ?? '（编号缺失）'
  const title = orNone(asString(detail.title))
  const status = projectStatusLabel(asNumber(detail.status), asString(detail.status_name))
  const reason = formatProjectReason(detail)

  return [
    `项目 ${projectId}：${title}`,
    `- 状态：${status}`,
    `- 工作项统计：${formatWorkItemStats(
      detail.work_item_total,
      detail.work_item_done,
      detail.work_item_by_state,
    )}`,
    `- 最近一次驳回原因：${reason}`,
    `- 需求：${formatMaterialRef(detail.requirement, '暂无')}`,
    `- 方案：${formatMaterialRef(detail.spec, '暂无')}`,
    '下一步：正文用 `echuan_material_get` 按 `material_id` 取；工作项用 `echuan_work_get` 看。',
  ].join('\n')
}

/** 草稿态（0/2/4）：项目停在需要产出物的地方，此时 reason 就是待改的东西。 */
const DRAFT_PROJECT_STATUSES = new Set(['0', '2', '4'])

/**
 * 项目原因 → 一句能指导动作的话。
 *
 * 平台只存了一段自由文本（最近一次驳回意见）。草稿态说明这段文字是在等修改，
 * 评审态说明它是在等三方判断；两种语义混在一起时 agent 只能看到「原因」两个字，
 * 不知道该动手还是该等人。这里按状态把两种意思分清楚。
 */
function formatProjectReason(detail: Project): string {
  const reason = asString(detail.reason)?.trim() ?? ''
  if (reason === '') return '无'
  const status = asNumber(detail.status)
  if (status !== undefined && DRAFT_PROJECT_STATUSES.has(String(status))) {
    return `${reason}（项目当前在草稿态：按该原因修改后重新提交评审）`
  }
  return reason
}

/**
 * `blocked_by` → `A、B（未验收）`；空/缺失 → 空串。
 *
 * 只对 blocked 卡有意义：它把「为什么还没轮到这张卡」直接写在卡上，
 * 而不是让 agent 从依赖列表里自己推断。
 */
function formatBlockedBy(value: unknown): string {
  if (!Array.isArray(value)) return ''
  const ids = value
    .map(item => asString(item)?.trim())
    .filter((item): item is string => item !== undefined && item !== '')
  return ids.length === 0 ? '' : `${ids.join('、')}（未验收，通过后自动放行）`
}

/** `depends_on` → `A、B`；空数组/缺失 → `无`。 */
function formatDependsOn(value: unknown): string {
  if (!Array.isArray(value)) return '无'
  const ids = value
    .map(item => asString(item)?.trim())
    .filter((item): item is string => item !== undefined && item !== '')
  return ids.length === 0 ? '无' : ids.join('、')
}

/** 租约（`WorkItemLease`）→ 一行；没有租约返回 `undefined`。 */
function formatLease(lease: unknown): string | undefined {
  if (!isRecord(lease)) return undefined
  const leaseId = asString(lease['lease_id'])
  const until = asString(lease['until'])
  if (leaseId === undefined && until === undefined) return undefined
  return `lease_id=${leaseId ?? '未知'}，到期 ${until ?? '未知'}`
}

/**
 * 工作项详情：编号 / 类别 / 所属项目 / 标题 / 状态 / 优先级 / 尝试次数 / 正文 / 结果 / 原因 / 租约。
 */
export function formatWorkItem(detail: WorkItemDetail): string {
  const workId = asString(detail.work_id) ?? asString(detail.id) ?? '（编号缺失）'
  const title = orNone(asString(detail.title))
  const body = asString(detail.body_md)?.trim() ?? ''
  const attempt = asNumber(detail.attempt)
  const maxAttempts = asNumber(detail.max_attempts)
  const leaseLine = formatLease(detail.lease)

  const lines = [`工作项 ${workId}：${title}`]
  const kind = asString(detail.kind)
  if (kind !== undefined && kind !== '') lines.push(`- 类别：${kind}（${workItemKindLabel(kind)}）`)
  const projectId = asString(detail.project_id)
  if (projectId !== undefined && projectId !== '') lines.push(`- 所属项目：${projectId}`)
  lines.push(`- 状态：${workItemStateLabel(asString(detail.state))}`)
  lines.push(`- 优先级：${asNumber(detail.priority) ?? '未知'}`)
  lines.push(`- 尝试次数：${attempt ?? '未知'}/${maxAttempts ?? '未知'}`)
  lines.push(`- 依赖：${formatDependsOn(detail.depends_on)}`)
  const blockedBy = formatBlockedBy(detail.blocked_by)
  if (blockedBy !== '') lines.push(`- 等待依赖：${blockedBy}`)
  const reason = asString(detail.reason)?.trim() ?? ''
  lines.push(`- 原因说明：${reason === '' ? '无' : reason}`)
  if (reason !== '') {
    lines.push(
      `- 返工：是（此前被驳回，当前是第 ${attempt ?? '?'} 次尝试）——只按上面的原因修改，不要整卡重做`,
    )
  }
  if (leaseLine !== undefined) lines.push(`- 租约：${leaseLine}`)
  lines.push('', '正文：', body === '' ? '（正文为空）' : truncateText(body, WORK_ITEM_BODY_MAX_CHARS))
  lines.push(
    `交付结果：${truncateText(
      formatMaterialRef(detail.result, '无（尚未提交报告）'),
      REPORT_TITLE_MAX_CHARS,
    )}`,
  )
  lines.push(
    '下一步：结果全文用 `echuan_material_get`（material_id 见上）取；待办总览用 `echuan_inbox`。',
  )
  return lines.join('\n')
}

/** 列表项里描述/原因的截断上限（字）。 */
export const LIST_TEXT_MAX_CHARS = 200

/**
 * 产品列表：编号 / 名称 / 仓库 / 许可 / 项目数 / 描述 / 更新时间。
 *
 * `git_url` 必须出现在每一行：Agent 靠它判断该拉哪个仓库，拿不到就会猜路径。
 */
export function formatProductList(products: readonly Product[]): string {
  const lines = [`产品共 ${products.length} 个`]
  for (const product of products) {
    const productId = asString(product.product_id) ?? asString(product.id) ?? '（编号缺失）'
    const name = orNone(asString(product.name))
    const gitUrl = orNone(asString(product.git_url))
    const license = orNone(asString(product.license))
    const projectCount = asNumber(product.project_count)
    lines.push(
      `- ${productId}：${name}｜仓库：${gitUrl}｜许可：${license}｜项目数：${projectCount ?? '未知'}`,
    )
    const description = asString(product.description)?.trim() ?? ''
    if (description !== '') {
      lines.push(`  描述：${truncateText(description, LIST_TEXT_MAX_CHARS)}`)
    }
    const updated = asString(product.update_time)
    if (updated !== undefined && updated !== '') lines.push(`  更新时间：${updated}`)
  }
  return lines.join('\n')
}

/** 项目列表：编号 / 标题 / 状态 / 工作项统计 / 原因 / 更新时间。 */
export function formatProjectList(projects: readonly Project[]): string {
  const lines = [`项目共 ${projects.length} 个`]
  for (const project of projects) {
    const projectId = asString(project.project_id) ?? asString(project.id) ?? '（编号缺失）'
    const title = orNone(asString(project.title))
    const status = projectStatusLabel(asNumber(project.status), asString(project.status_name))
    lines.push(
      `- ${projectId}：${title}｜状态：${status}｜工作项统计：${formatWorkItemStats(
        project.work_item_total,
        project.work_item_done,
        project.work_item_by_state,
      )}`,
    )
    const reason = asString(project.reason)?.trim() ?? ''
    if (reason !== '') lines.push(`  原因：${truncateText(reason, LIST_TEXT_MAX_CHARS)}`)
    const updated = asString(project.update_time)
    if (updated !== undefined && updated !== '') lines.push(`  更新时间：${updated}`)
  }
  return lines.join('\n')
}

/** 工作项列表：序号 / 编号 / kind / 标题 / 状态 / 优先级 / 尝试次数 / 依赖。 */
export function formatWorkItemList(items: readonly WorkItemDetail[]): string {
  const lines = [`工作项共 ${items.length} 个`]
  for (const item of items) {
    const workId = asString(item.work_id) ?? asString(item.id) ?? '（编号缺失）'
    const seq = asNumber(item.seq_no)
    const seqLabel = seq === undefined ? '' : `#${seq} `
    const attempt = asNumber(item.attempt)
    const kind = asString(item.kind)
    lines.push(
      `- ${seqLabel}${workId}：${orNone(asString(item.title))}`
        + `｜类别：${kind === undefined ? '未知' : `${kind}（${workItemKindLabel(kind)}）`}`
        + `｜状态：${workItemStateLabel(asString(item.state))}`
        + `｜优先级：${asNumber(item.priority) ?? '未知'}`
        + `｜尝试次数：${attempt ?? '未知'}`
        + `｜依赖：${formatDependsOn(item.depends_on)}`
        + (formatBlockedBy(item.blocked_by) === ''
          ? ''
          : `｜等待依赖：${formatBlockedBy(item.blocked_by)}`),
    )
  }
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
      '下一步：先核对编号是否正确（可用 `echuan_inbox` 找到相关编号，或从项目/工作项详情里的 material_id 复制），再重试。',
    ].join('\n')
  }

  return `${head}\n\n${truncateText(content, MATERIAL_MAX_CHARS)}`
}

/**
 * 三个按 id 的只读工具共用「缺参本地拒绝」文案（零请求）。
 *
 * @param subject 中文资源名（项目 / 工作项 / 资料）
 * @param extra 额外的编号来源提示（可选，例如资料 id 可从详情里复制）
 */
export function missingIdText(subject: string, extra?: string): string {
  const lines = [`缺少 id：请先用 \`echuan_inbox\` 找到${subject}编号，再用该编号查询。`]
  if (extra !== undefined) lines.push(extra)
  lines.push('本次已本地拒绝，未发出任何请求。')
  return lines.join('\n')
}
