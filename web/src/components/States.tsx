import type { ReactNode } from 'react'
import { ApiError, describeError } from '../api/client'
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
  const message = error instanceof Error ? error.message : String(error)

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
            <li key={index}>{typeof detail === 'string' ? detail : JSON.stringify(detail)}</li>
          ))}
        </ul>
      )}
      <div className="notice-meta">
        <span>code: {apiError?.code ?? 'network_error'}</span>
        <span>request_id: {apiError?.requestId || '—'}</span>
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

/** 本任务仅提供占位内容，真正的详情/决策交互属于任务-15。 */
export function DrawerPlaceholder({
  kind,
  id,
  status,
}: {
  kind: string
  id: string
  status?: string
  children?: ReactNode
}) {
  return (
    <div className="placeholder">
      <p className="placeholder-badge">
        {kind} · {id}
      </p>
      {status && <p>当前状态：{status}</p>}
      <p className="muted">抽屉容器已就绪；详情内容与决策交互将在任务-15 实现。</p>
      <label className="field">
        <span>备注（占位输入，用于验证轮询不覆盖输入）</span>
        <textarea rows={4} placeholder="在此输入时，页面轮询不会覆盖已有数据…" />
      </label>
    </div>
  )
}
