/**
 * 工具层：「模型可读文本」渲染与统一错误出口。
 *
 * `render` 只做投影（把已校验的规范值排成中文多行文本），不做任何业务判断；
 * 错误只做「原样透传 code + 平台事实 + 下一步」，不重试、不兜底。
 */

import { EchuanClientError } from '../errors.js'
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
