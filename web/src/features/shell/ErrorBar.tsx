// features/shell/ErrorBar —— 统一错误条骨架（api 错误 / 渲染错误共用）
//
// 规格：design/components.md §ui/Alert（danger = 可重试的错误；loading = 动作按钮「重试中」；
// 反例 = 只写「出错了」）。本文件只做「骨架」：
//   * 不写死任何接口文案——标题、说明、重试文案全部由调用方传入；
//   * 不自己发请求、不自己判断 HTTP 语义（调用方把 ApiError 收敛成 message/detail 再传进来）；
//   * 标题 / 说明 / 技术细节 / 动作全部由本组件自绘，不借用 ui/Alert 的 title 槽：
//     这样前景色与背景色成对定义在 shell.css 的 .fx-error-bar-alert 上，不依赖宿主页面背景。
//     （Tester 复核项 D2：旧实现把 dark 令牌的 --text-primary 叠在旧浅色页 + Mantine light 变体
//      的浅色底上，实测 message 1.12:1 / detail 1.92:1 / title 2.78:1，均不可读。）
//   * 动作成组：重试与额外动作同一行、同一对齐、间距走令牌（Tester 复核项 D7）。
//   * 形态：单行横幅 + 动作右置（issue #89 B1）——标题 / 说明 / 技术细节在横幅内一行内联，
//     动作组用 `margin-inline-start: auto` 贴右；窄屏文本换行、动作仍贴右。
import type { ReactNode } from 'react'
import { Alert, Button } from '../../ui'

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
  /** 额外动作（如「返回看板」「在新标签打开原文」），与重试同一行 */
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
  const hasActions = Boolean(onRetry) || Boolean(extraActions)

  return (
    <Alert
      tone="danger"
      // 自带表面：shell.css 的 .fx-error-bar-alert 单独给出背景与前景，不读宿主页面背景
      className={className ? `fx-error-bar-alert ${className}` : 'fx-error-bar-alert'}
    >
      <div className="fx-error-bar" data-ui="error-bar" data-surface="self">
        {/* 单行横幅（issue #89 B1）：标题 / 说明 / 技术细节内联在同一行，动作组贴右。
            data-detail 让 CSS 知道「这一行有没有技术细节」：有细节时溢出一律由细节吸收，
            没有细节时才让标题 / 说明自己收缩（review B1-1 / B1-2）。 */}
        <p className="fx-error-bar__text" data-detail={detail ? 'on' : 'off'}>
          <span className="fx-error-bar__title">{title}</span>
          {message ? <span className="fx-error-bar__message">{message}</span> : null}
          {detail ? <span className="fx-error-bar__detail">{detail}</span> : null}
        </p>
        {hasActions ? (
          <div className="fx-error-bar__actions" data-ui="error-bar-actions">
            {onRetry ? (
              // danger 描边而不是 primary：错误态下不出现第二处主操作（AC5 口径）
              <Button variant="danger" size="sm" loading={retrying} onClick={onRetry}>
                {retryLabel}
              </Button>
            ) : null}
            {extraActions}
          </div>
        ) : null}
      </div>
    </Alert>
  )
}
