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
import type { V3Attachment, V3ClaimInput, V3FlowStage, V3Project, V3Task } from '../v3-types.js'

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


/** 只读工具输出的规范值：渲染好的文本（只读工具没有别的消费方）。 */
export interface TextValue {
  text: string
}

/** 兜底 JSON 序列化的截断上限（字）。 */
export const FALLBACK_JSON_MAX_CHARS = 4000

/**
 * 兜底渲染：把拿不到 `{ text }` 的规范值序列化成缩进 JSON。
 *
 * 为什么需要：V3 工具返回的是契约原始 JSON（`{ items: [...] }`、`{ kind: 'claimed', task }`），
 * 没有 `text` 字段。渲染层如果直接给空串，模型会以为「调用成功但什么也没发生」——
 * 2026-11 的 issue #65 就是这么来的（领到卡却看不到 task_id / lease_id）。
 *
 * 纪律：**任何输入都不返回空串**（`undefined` / `null` / 空对象 / 空数组 / 空白字符串
 * 都一样），序列化失败时退回 `String(value)`，再不行给一句明确说明。
 */
export function renderFallbackJSON(value: unknown): string {
  let serialized = ''
  try {
    serialized = JSON.stringify(value, null, 2) ?? ''
  } catch {
    serialized = ''
  }
  if (serialized.trim() === '') serialized = String(value)
  if (serialized.trim() === '') {
    return '（工具调用成功，但平台没有返回任何内容，也没有可序列化的字段。）'
      + '下一步：核对请求参数后重试；若反复如此，把现象反馈给平台维护者。'
  }
  return [
    '（工具没有提供可读文本，以下是原始返回的 JSON，已按缩进排版）',
    truncateText(serialized, FALLBACK_JSON_MAX_CHARS),
  ].join('\n')
}

/**
 * render 的安全投影：优先用 `{ text }`；拿不到可读文本时退化为 JSON，**绝不返回空串**。
 */
export function renderTextValue(value: unknown): TextContent[] {
  const text = isRecord(value) ? asString(value['text']) : undefined
  if (text !== undefined && text.trim() !== '') return [textBlock(text)]
  return [textBlock(renderFallbackJSON(value))]
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
 * 对 blocked 卡与 queued 卡都有意义：它把「为什么这张卡还不能被领取 / 派发」
 * 直接写在卡上，而不是让 agent 从依赖列表里自己推断。批次评审通过后卡就进
 * queued，但依赖未验收的卡依然不会被派发，所以这条信息要继续展示。
 */
function formatBlockedBy(value: unknown): string {
  if (!Array.isArray(value)) return ''
  const ids = value
    .map(item => asString(item)?.trim())
    .filter((item): item is string => item !== undefined && item !== '')
  return ids.length === 0 ? '' : `${ids.join('、')}（未验收，验收后才派发）`
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

/* ------------------------------------------------------------------ *
 * V3 工具面：平台 V3 契约 → 模型可直读文本
 *
 * V3 工具返回的是契约原始 JSON（`{ items: [...] }`、`{ kind: 'claimed', task }`），
 * 没有 `text` 字段。下面这些投影函数把原始 JSON 排成中文多行文本；写工具的确认文本
 * 固定回显调用者事后核对要用的编号（task_id / lease_id / material_id / product_id /
 * project_id），这样「领到卡却看不到编号」不会重演（issue #65）。
 * ------------------------------------------------------------------ */

/** V3 任务状态（`V3Task.status`，七态）的中文名。 */
const V3_TASK_STATUS_LABELS: Record<string, string> = {
  pending: '待派发',
  ready: '待领取',
  in_progress: '进行中',
  in_review: '待验收',
  done: '已完成',
  failed: '已失败',
  cancelled: '已取消',
}

/** V3 任务状态 → `中文名（平台名）`；未知状态原样标出。 */
export function v3TaskStatusLabel(status: string | undefined): string {
  if (status === undefined || status === '') return '状态未知'
  const label = V3_TASK_STATUS_LABELS[status]
  return label === undefined ? `未知状态 ${status}` : `${label}（${status}）`
}

/** V3 项目状态（open / closed）的中文名。 */
const V3_PROJECT_STATUS_LABELS: Record<string, string> = {
  open: '进行中',
  closed: '已关闭',
}

/** V3 项目状态 → `中文名（平台名）`；未知状态原样标出。 */
export function v3ProjectStatusLabel(status: string | undefined): string {
  if (status === undefined || status === '') return '状态未知'
  const label = V3_PROJECT_STATUS_LABELS[status]
  return label === undefined ? `未知状态 ${status}` : `${label}（${status}）`
}

/* ------------------------------ 读：任务 ------------------------------ */

/** V3 任务详情：编号 / 项目 / 类别 / 状态 / 尝试次数 / 租约 / 正文 / 上下文。 */
export function formatV3Task(task: V3Task): string {
  const taskId = asString(task.task_id) ?? '（编号缺失）'
  const attempt = asNumber(task.attempt)
  const maxAttempts = asNumber(task.max_attempts)
  const lines = [
    `任务 ${taskId}：${orNone(asString(task.title))}`,
    `- 类别：${asString(task.kind) ?? '未知'}（${workItemKindLabel(asString(task.kind))}）`,
    `- 归属项目：${orNone(asString(task.project_id))}`,
    `- 状态：${v3TaskStatusLabel(asString(task.status))}`,
    `- 优先级：${asNumber(task.priority) ?? '未知'}｜尝试次数：${attempt ?? '未知'}/${maxAttempts ?? '未知'}`,
  ]

  const parentTaskId = asString(task.parent_task_id)
  if (parentTaskId !== undefined && parentTaskId !== '') lines.push(`- 父任务：${parentTaskId}`)

  const leaseId = asString(task.lease_id)
  const leaseUntil = asString(task.lease_until)
  if (leaseId !== undefined || leaseUntil !== undefined) {
    lines.push(`- 租约：lease_id=${leaseId ?? '未知'}，到期 ${leaseUntil ?? '未知'}`)
  }

  const workerId = asString(task.worker_id)
  if (workerId !== undefined && workerId !== '') lines.push(`- worker：${workerId}`)

  const reason = asString(task.reason)?.trim() ?? ''
  if (reason !== '') lines.push(`- 原因说明：${truncateText(reason, LIST_TEXT_MAX_CHARS)}`)

  const created = asString(task.create_time)
  if (created !== undefined && created !== '') lines.push(`- 创建时间：${created}`)
  const updated = asString(task.update_time)
  if (updated !== undefined && updated !== '') lines.push(`- 更新时间：${updated}`)

  const description = asString(task.description_md)?.trim() ?? ''
  lines.push('', '任务正文：', description === '' ? '（正文为空）' : truncateText(description, WORK_ITEM_BODY_MAX_CHARS))

  const context = asString(task.context_md)?.trim() ?? ''
  if (context !== '') lines.push('', '上下文：', truncateText(context, WORK_ITEM_BODY_MAX_CHARS))

  lines.push(
    '下一步：交付用 `echuan_task_report`（需要 task_id + lease_id）；'
      + '同项目其它卡用 `echuan_task_list` 看。',
  )
  return lines.join('\n')
}

/** V3 任务列表：汇总行 + 每条一行（编号 / 标题 / 项目 / 类别 / 状态 / 优先级 / 尝试次数 / 租约）。 */
export function formatV3TaskList(items: readonly V3Task[]): string {
  if (items.length === 0) {
    return '当前没有任务（0 个）。'
  }

  const lines = [`任务共 ${items.length} 个`]
  for (const item of items) {
    const taskId = asString(item.task_id) ?? '（编号缺失）'
    const leaseId = asString(item.lease_id)
    const attempt = asNumber(item.attempt)
    const maxAttempts = asNumber(item.max_attempts)
    lines.push(
      `- ${taskId}：${orNone(asString(item.title))}`
        + `｜项目：${orNone(asString(item.project_id))}`
        + `｜类别：${asString(item.kind) ?? '未知'}（${workItemKindLabel(asString(item.kind))}）`
        + `｜状态：${v3TaskStatusLabel(asString(item.status))}`
        + `｜优先级：${asNumber(item.priority) ?? '未知'}`
        + `｜尝试次数：${attempt ?? '未知'}/${maxAttempts ?? '未知'}`
        + (leaseId === undefined || leaseId === '' ? '' : `｜lease_id=${leaseId}`),
    )
  }
  return lines.join('\n')
}

/** V3 待办（inbox 返回的是任务）：汇总行 + 每条一行。 */
export function formatV3Inbox(items: readonly V3Task[]): string {
  if (items.length === 0) {
    return '当前没有待处理项（0 条）。'
  }

  const lines = [`待办共 ${items.length} 条`]
  for (const item of items) {
    const taskId = asString(item.task_id) ?? '（编号缺失）'
    lines.push(
      `- ${taskId}：${orNone(asString(item.title))}`
        + `｜项目：${orNone(asString(item.project_id))}`
        + `｜类别：${asString(item.kind) ?? '未知'}（${workItemKindLabel(asString(item.kind))}）`
        + `｜状态：${v3TaskStatusLabel(asString(item.status))}`,
    )
  }
  return lines.join('\n')
}

/* ------------------------------ 读：项目 ------------------------------ */

/** V3 项目详情：编号 / 名称 / 产品 / 状态 / 上下文正文。 */
export function formatV3Project(project: V3Project): string {
  const projectId = asString(project.project_id) ?? '（编号缺失）'
  const lines = [
    `项目 ${projectId}：${orNone(asString(project.name))}`,
    `- 产品：${orNone(asString(project.product_id))}`,
    `- 序号：${asNumber(project.seq_no) ?? '未知'}`,
    `- 状态：${v3ProjectStatusLabel(asString(project.status))}`,
  ]

  const closedAt = asString(project.closed_at)
  if (closedAt !== undefined && closedAt !== '') lines.push(`- 关闭时间：${closedAt}`)

  const created = asString(project.create_time)
  if (created !== undefined && created !== '') lines.push(`- 创建时间：${created}`)
  const updated = asString(project.update_time)
  if (updated !== undefined && updated !== '') lines.push(`- 更新时间：${updated}`)

  const context = asString(project.context_md)?.trim() ?? ''
  lines.push('', '项目上下文：', context === '' ? '（上下文为空）' : truncateText(context, MATERIAL_MAX_CHARS))
  return lines.join('\n')
}

/** V3 项目列表：汇总行 + 每条一行（编号 / 名称 / 产品 / 状态 / 更新时间）。 */
export function formatV3ProjectList(projects: readonly V3Project[]): string {
  if (projects.length === 0) {
    return '当前没有项目（0 个）。'
  }

  const lines = [`项目共 ${projects.length} 个`]
  for (const project of projects) {
    const projectId = asString(project.project_id) ?? '（编号缺失）'
    lines.push(
      `- ${projectId}：${orNone(asString(project.name))}`
        + `｜产品：${orNone(asString(project.product_id))}`
        + `｜状态：${v3ProjectStatusLabel(asString(project.status))}`
        + (asString(project.update_time) === undefined
          ? ''
          : `｜更新时间：${asString(project.update_time)}`),
    )
  }
  return lines.join('\n')
}

/* ------------------------------ 读：产品 ------------------------------ */

/** 平台产品回包可能是列表（`{ items }`）或单个详情；统一取出产品数组。 */
export function extractProducts(value: unknown): Product[] {
  if (Array.isArray(value)) {
    return value.filter(isRecord) as unknown as Product[]
  }
  if (!isRecord(value)) return []
  const items = value['items']
  if (Array.isArray(items)) return items.filter(isRecord) as unknown as Product[]
  return asString(value['product_id']) === undefined && asString(value['id']) === undefined
    ? []
    : [value as unknown as Product]
}

/**
 * V3 产品列表/详情：复用 V2 的 {@link formatProductList}（`产品共 N 个` + 每条
 * `product_id / name / git_url`），空集给明确中文提示而不是空串。
 */
export function formatV3Products(value: unknown): string {
  const products = extractProducts(value)
  if (products.length === 0) return '当前没有产品（0 个）。'
  return formatProductList(products)
}

/* ------------------------------ 读：流程环节 ------------------------------ */

/**
 * 从 `GET /v1/flow` 回包里取出 `stages[]`。
 *
 * 形状不对（回包不是对象、`stages` 不是数组）时返回空数组而不是抛错：这是**读**接口，
 * 平台已经把非 2xx 与非法 JSON 拦在客户端了；能走到这里的只有「形状与预期不同」，
 * 渲染层按空清单说明事实，比编一段错误更诚实。
 */
export function extractFlowStages(value: unknown): V3FlowStage[] {
  if (!isRecord(value)) return []
  const stages = value['stages']
  return Array.isArray(stages) ? (stages.filter(isRecord) as unknown as V3FlowStage[]) : []
}

/** 环节 id → 方括号里的那段文本；数字与字符串两种回包都认。 */
function formatStageId(stage: V3FlowStage): string {
  const numeric = asNumber(stage.id)
  if (numeric !== undefined) return String(numeric)
  const text = asString(stage.id)?.trim()
  return text === undefined || text === '' ? '?' : text
}

/**
 * 环节清单：1 行表头（只给条数）+ 每条一行 `[<id>] <name>：<description>`。
 *
 * 渲染口径由方案的实现约束定死（§7.2）：每条**只有** id / name / 阶段描述三个值，
 * 顺序固定；描述为空时省略冒号与空串（写成 `[2] design`）；`label` / `material` /
 * `derive` / `source` / `fingerprint` 一律不出现（N7）——模型拿这份清单是去选环节
 * `name` 的，多给的字段只会诱导它把 label 当成 name 发回来。
 */
export function formatV3StageList(value: unknown): string {
  const stages = extractFlowStages(value)
  const lines = [`环节清单（共 ${stages.length} 个）`]
  for (const stage of stages) {
    const name = asString(stage.name)?.trim() ?? ''
    const description = asString(stage.description)?.trim() ?? ''
    const head = `[${formatStageId(stage)}] ${name === '' ? '（名称缺失）' : name}`
    lines.push(description === '' ? head : `${head}：${description}`)
  }
  return lines.join('\n')
}

/* ------------------------- 写工具：成功确认文本 ------------------------- */

/**
 * 领卡成功：`task_id` 与 `lease_id` 必须回显。
 *
 * 这是 issue #65 里危害最高的一处：`claimed` 分支此前渲染成空串，agent 领到卡
 * 却看不到 task_id / lease_id，只能误判成「没领到」。
 */
export function formatV3Claimed(task: V3Task, inputs: readonly V3ClaimInput[] = []): string {
  const taskId = asString(task.task_id) ?? '（编号缺失）'
  const leaseId = asString(task.lease_id)
  const attempt = asNumber(task.attempt)
  const maxAttempts = asNumber(task.max_attempts)
  const lines = [
    `✅ 已领到任务 ${taskId}：${orNone(asString(task.title))}`,
    `- task_id：${taskId}`,
    `- lease_id：${leaseId ?? '（平台未下发租约，先用 `echuan_task_get` 核对）'}`,
    `- 租约到期：${asString(task.lease_until) ?? '未知'}`,
    `- 归属项目：${orNone(asString(task.project_id))}`
      + `｜类别：${asString(task.kind) ?? '未知'}（${workItemKindLabel(asString(task.kind))}）`,
    `- 状态：${v3TaskStatusLabel(asString(task.status))}｜尝试次数：${attempt ?? '未知'}/${maxAttempts ?? '未知'}`,
  ]

  if (inputs.length > 0) {
    lines.push('- 输入物料：')
    for (const input of inputs) {
      const materialId = asString(input.material_id) ?? '（编号缺失）'
      const extra: string[] = []
      const kind = asString(input.kind)
      if (kind !== undefined && kind !== '') extra.push(`kind=${kind}`)
      const role = asString(input.role)
      if (role !== undefined && role !== '') extra.push(`role=${role}`)
      lines.push(`  - material_id=${materialId}${extra.length === 0 ? '' : `（${extra.join('，')}）`}：${orNone(asString(input.title))}`)
    }
  }

  const description = asString(task.description_md)?.trim() ?? ''
  if (description !== '') {
    lines.push('', '任务正文：', truncateText(description, WORK_ITEM_BODY_MAX_CHARS))
  }

  lines.push(
    '下一步：按任务正文开工；交付用 `echuan_task_report`（需上面的 task_id + lease_id），'
      + '租约将到期时用 `echuan_task_renew` 续租。',
  )
  return lines.join('\n')
}

/** 续租成功：回显任务编号与本次可用的 lease_id（平台没回包时沿用传入值并注明）。 */
export function formatV3Renewed(
  input: { task_id: string; lease_id: string },
  value: unknown,
): string {
  const record = isRecord(value) ? value : {}
  const newLeaseId = asString(record['lease_id'])
  const leaseUntil = asString(record['lease_until'])
  return [
    `✅ 续租成功：任务 ${input.task_id}`,
    newLeaseId === undefined || newLeaseId === ''
      ? `- lease_id（平台未回包，沿用传入值）：${input.lease_id}`
      : `- 新 lease_id：${newLeaseId}`,
    `- 到期时间：${leaseUntil ?? '未知'}`,
    '下一步：用上面的 lease_id 交报告（`echuan_task_report`）；旧 lease_id 不再可用。',
  ].join('\n')
}

/** 交报告成功：回显 task_id / material_id 与平台状态（待验收）。 */
export function formatV3Reported(input: {
  task_id: string
  material_id: string
  status: string | undefined
}): string {
  return [
    `✅ 报告已提交，任务转入待验收：${input.task_id}`,
    `- task_id：${input.task_id}`,
    `- material_id：${input.material_id}`,
    `- 平台状态：${v3TaskStatusLabel(input.status)}`,
    '下一步：等待平台验收结论；不要再用旧 lease_id 重复提交同一张卡的结果。',
  ].join('\n')
}

/** 建任务成功：回显 task_id / 项目 / 类别 / 状态，便于后续查询与派发。 */
export function formatV3TaskCreated(task: V3Task): string {
  return [
    `✅ 任务已创建：${asString(task.task_id) ?? '（编号缺失）'}`,
    `- task_id：${asString(task.task_id) ?? '（编号缺失）'}`,
    `- 归属项目：${orNone(asString(task.project_id))}`
      + `｜类别：${asString(task.kind) ?? '未知'}（${workItemKindLabel(asString(task.kind))}）`,
    `- 标题：${orNone(asString(task.title))}`,
    `- 状态：${v3TaskStatusLabel(asString(task.status))}`,
    '下一步：用 `echuan_task_list`（带 project_id）核对队列；依赖未满足的卡在平台侧保持 pending。',
  ].join('\n')
}

/** 改任务成功：回显 task_id / 标题 / 状态。 */
export function formatV3TaskUpdated(task: V3Task): string {
  return [
    `✅ 任务已更新：${asString(task.task_id) ?? '（编号缺失）'}`,
    `- task_id：${asString(task.task_id) ?? '（编号缺失）'}`,
    `- 标题：${orNone(asString(task.title))}`,
    `- 状态：${v3TaskStatusLabel(asString(task.status))}`,
    '下一步：用 `echuan_task_get` 复核正文；已领取的任务平台不允许改，需走返工。',
  ].join('\n')
}

/** 挂附件成功：回显 material_id / role / 挂载对象。 */
export function formatV3Attached(
  input: { project_id?: string | undefined; task_id?: string | undefined; material_id: string; role: string },
  attachment: V3Attachment,
): string {
  const target = input.task_id !== undefined && input.task_id !== ''
    ? `任务 ${input.task_id}`
    : `项目 ${input.project_id ?? '（未指定）'}`
  const lines = [
    `✅ 附件已挂到${target}`,
    `- material_id：${input.material_id}`,
    `- role：${input.role}`,
  ]
  const attachmentId = asNumber(attachment.id)
  if (attachmentId !== undefined) lines.push(`- attachment_id：${attachmentId}`)
  lines.push('下一步：用 `echuan_material_get` 按 material_id 复核正文；附件不需要重复挂。')
  return lines.join('\n')
}

/** 建项目成功：回显 project_id / 名称 / 产品 / 状态。 */
export function formatV3ProjectCreated(project: V3Project): string {
  return [
    `✅ 项目已创建：${asString(project.project_id) ?? '（编号缺失）'}`,
    `- project_id：${asString(project.project_id) ?? '（编号缺失）'}`,
    `- 名称：${orNone(asString(project.name))}`,
    `- 产品：${orNone(asString(project.product_id))}`,
    `- 状态：${v3ProjectStatusLabel(asString(project.status))}`,
    '下一步：用 `echuan_task_create` 往这个项目里建任务（项目本身不携带需求正文）。',
  ].join('\n')
}

/** 关闭项目成功：回显 project_id，并说明 force 语义（会取消未完成任务）。 */
export function formatV3ProjectClosed(projectId: string, force: boolean): string {
  return [
    `✅ 项目已关闭：${projectId}`,
    ...(force
      ? ['- force=true：该项目下未完成的任务已被平台取消（不可自动复活）']
      : ['- 本次未传 force：若仍有未完成任务，平台会拒绝关闭']),
    '下一步：用 `echuan_project_list`（status=closed）核对；重开用 `echuan_project_reopen`（已取消的任务不会复活）。',
  ].join('\n')
}

/** 重开项目成功：回显 project_id。 */
export function formatV3ProjectReopened(projectId: string): string {
  return [
    `✅ 项目已重开：${projectId}`,
    '- 已取消的任务不会复活：需要继续的工作要重新建任务。',
    '下一步：用 `echuan_project_get` 看上下文，`echuan_task_list` 看现有任务。',
  ].join('\n')
}

/** 建产品成功：回显 product_id / 名称 / 仓库地址。 */
export function formatV3ProductCreated(product: Product): string {
  return [
    `✅ 产品已创建：${asString(product.product_id) ?? asString(product.id) ?? '（编号缺失）'}`,
    `- product_id：${asString(product.product_id) ?? asString(product.id) ?? '（编号缺失）'}`,
    `- 名称：${orNone(asString(product.name))}`,
    `- 仓库：${orNone(asString(product.git_url))}`,
    '下一步：用 `echuan_project_create`（带 product_id）建项目容器。',
  ].join('\n')
}
