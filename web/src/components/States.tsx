import { ApiError, apiErrorSummary, describeError, errorDetailMessage } from '../api/client'
import { formatClock } from '../lib/format'

export function LoadingState({ label = '加载中…' }: { label?: string }) {
  return (
    <div className="state-panel loading" role="status" aria-live="polite">
      <span className="spinner" aria-hidden="true" />
      <span>{label}</span>
    </div>
  )
}

export function EmptyState({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="state-panel empty">
      <strong>{title}</strong>
      {hint && <p className="muted">{hint}</p>}
    </div>
  )
}

/**
 * 统一错误提示：展示分档文案 + 后端错误体的 code / request_id。
 * 网络失败（无 request_id）也会给出可读提示。
 */
export function ErrorNotice({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  if (!error) return null
  const { title, hint } = describeError(error)
  const apiError = error instanceof ApiError ? error : null
  const message = apiError ? apiErrorSummary(apiError) : '无法连接服务，请检查网络或服务是否已启动。'

  return (
    <div className="notice error" role="alert">
      <div className="notice-head">
        <strong>{title}</strong>
        {onRetry && (
          <button type="button" className="ghost" onClick={onRetry}>
            重试
          </button>
        )}
      </div>
      <p>{hint}</p>
      <p className="notice-message">{message}</p>
      {apiError && apiError.details.length > 0 && (
        <ul className="notice-details">
          {apiError.details.slice(0, 5).map((detail, index) => (
            <li key={index}>{errorDetailMessage(detail)}</li>
          ))}
        </ul>
      )}
      <div className="notice-meta">
        <span>请求编号：{apiError?.requestId || '—'}</span>
      </div>
    </div>
  )
}

export interface RefreshBarProps {
  lastUpdated: Date | null
  refreshing: boolean
  error: unknown
  onRefresh: () => void
  intervalSeconds?: number
}

/** 5s 轮询的可视化指示：最近刷新时间 + 手动刷新。 */
export function RefreshBar({
  lastUpdated,
  refreshing,
  error,
  onRefresh,
  intervalSeconds = 5,
}: RefreshBarProps) {
  return (
    <div className="refreshbar">
      <span className={`dot ${error ? 'dot-error' : 'dot-ok'}`} aria-hidden="true" />
      <span className="refresh-time">
        {error
          ? '刷新失败，自动重试中'
          : lastUpdated
            ? `最近刷新 ${formatClock(lastUpdated)}`
            : '等待首次刷新'}
      </span>
      <span className="muted">每 {intervalSeconds} 秒自动刷新</span>
      <button type="button" className="ghost" onClick={onRefresh} disabled={refreshing}>
        {refreshing ? '刷新中…' : '手动刷新'}
      </button>
    </div>
  )
}

/** 卡片/行内的“详情”入口，配合抽屉使用。 */
export function DetailButton({ onClick, label = '详情' }: { onClick: () => void; label?: string }) {
  return (
    <button
      type="button"
      className="ghost"
      onClick={(event) => {
        event.stopPropagation()
        onClick()
      }}
    >
      {label}
    </button>
  )
}
