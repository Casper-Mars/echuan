// features/shell/ErrorBar —— 统一错误条骨架（api 错误 / 渲染错误共用）
//
// 规格：design/components.md §ui/Alert（danger = 可重试的错误；loading = 动作按钮「重试中」；
// 反例 = 只写「出错了」）。本文件只做「骨架」：
//   * 不写死任何接口文案——标题、说明、重试文案全部由调用方传入；
//   * 不自己发请求、不自己判断 HTTP 语义（调用方把 ApiError 收敛成 message/detail 再传进来）；
//   * 动作按钮固定走 ui/Button，重试中的 loading 交给 ui/Alert 的 actionLoading。
// 为什么要这层骨架：技术方案 §10 要求「所有 ApiError 统一转为用户可读错误条（含 HTTP 状态与服务端
// message）」，T5–T9 的页面共用同一个错误条，文案只换不新写一套。
import type { ReactNode } from 'react'
import { Alert } from '../../ui'

export interface ErrorBarProps {
  /** 一句话说明发生了什么（不传则用「操作未完成」） */
  title?: string
  /** 用户可读说明：为什么 + 下一步 */
  message?: ReactNode
  /** 技术细节（HTTP 状态 / 服务端 message / 异常栈首行），弱化展示 */
  detail?: string
  /** 重试按钮文案（默认「重试」）；不传 onRetry 则不渲染重试按钮 */
  retryLabel?: string
  onRetry?: () => void
  /** 重试中：按钮进入 loading（design/components.md §ui/Alert loading 状态） */
  retrying?: boolean
  /** 额外动作（如「返回看板」「在新标签打开原文」），渲染在错误条内 */
  extraActions?: ReactNode
  className?: string
}

export function ErrorBar({
  title = '操作未完成',
  message,
  detail,
  retryLabel = '重试',
  onRetry,
  retrying = false,
  extraActions,
  className,
}: ErrorBarProps) {
  return (
    <Alert
      tone="danger"
      title={title}
      className={className}
      actionLabel={onRetry ? retryLabel : undefined}
      onAction={onRetry}
      actionLoading={retrying}
    >
      <div className="fx-error-bar" data-ui="error-bar">
        {message ? <p className="fx-error-bar__message">{message}</p> : null}
        {detail ? <p className="fx-error-bar__detail">{detail}</p> : null}
        {extraActions ? <div className="fx-error-bar__actions">{extraActions}</div> : null}
      </div>
    </Alert>
  )
}
