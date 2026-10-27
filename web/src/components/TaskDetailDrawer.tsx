import { getTask, materialDownloadUrl } from '../api/client'
import { usePolling } from '../hooks/usePolling'
import {
  formatBytes,
  formatDateTime,
  formatSince,
  taskStatusLabel,
} from '../lib/format'
import { ErrorNotice, LoadingState } from './States'
import { Markdown } from './Markdown'
import { TaskActions } from './TaskActions'

/**
 * 任务详情抽屉：描述、报告（元信息 + 下载）、依赖、尝试次数、
 * reason / last_actor / update_time，以及验收/返工/重排/放弃动作。
 */
export function TaskDetailDrawer({
  taskId,
  onChanged,
}: {
  taskId: string
  onChanged: () => void
}) {
  const { data, error, loading, refreshing, refresh } = usePolling(
    (signal) => getTask(taskId, signal),
    [taskId],
  )

  if (!data) {
    return (
      <div className="detail">
        {loading && <LoadingState label="正在加载任务详情…" />}
        {error && <ErrorNotice error={error} onRetry={refresh} />}
      </div>
    )
  }

  const changed = () => {
    refresh()
    onChanged()
  }

  return (
    <div className="detail">
      {error && <ErrorNotice error={error} onRetry={refresh} />}

      <div className="detail-summary">
        <span className={`badge status-${data.status}`}>{taskStatusLabel(data.status)}</span>
        <span className="mono">版本 v{data.version}</span>
        <span>
          尝试 {data.attempt_count}/{data.max_attempts}
        </span>
        <span>停留 {formatSince(data.update_time)}</span>
        {refreshing && <span className="muted">刷新中…</span>}
      </div>

      <dl className="meta-grid">
        <div>
          <dt>last_actor</dt>
          <dd>{data.last_actor || '—'}</dd>
        </div>
        <div>
          <dt>update_time</dt>
          <dd>{formatDateTime(data.update_time)}</dd>
        </div>
        <div>
          <dt>create_time</dt>
          <dd>{formatDateTime(data.create_time)}</dd>
        </div>
        <div>
          <dt>priority</dt>
          <dd>{data.priority}</dd>
        </div>
        {data.lease && (
          <div>
            <dt>租约到期</dt>
            <dd>{formatDateTime(data.lease.until)}</dd>
          </div>
        )}
      </dl>

      <section className="detail-section">
        <h3>reason</h3>
        <p className={data.reason ? 'reason-text' : 'muted'}>{data.reason || '—'}</p>
      </section>

      <TaskActions task={data} onChanged={changed} className="detail-actions" />

      <section className="detail-section">
        <h3>任务描述</h3>
        <Markdown source={data.body_md} empty="暂无任务描述" />
      </section>

      <section className="detail-section">
        <h3>报告</h3>
        {data.report ? (
          <div className="report-box">
            <div className="report-meta">
              <span className="code">v{data.report.version}</span>
              <span>{data.report.uploaded_by || '—'}</span>
              <span className="muted">{formatDateTime(data.report.create_time)}</span>
              <span className="muted">{formatBytes(data.report.size)}</span>
            </div>
            <p className="mono muted ellipsis" title={data.report.sha256}>
              sha256: {data.report.sha256}
            </p>
            <a className="action action-primary" href={materialDownloadUrl(data.report.material_id)} download>
              下载报告
            </a>
          </div>
        ) : (
          <p className="muted">尚未提交报告</p>
        )}
      </section>

      <section className="detail-section">
        <h3>依赖任务</h3>
        {data.depends_on.length === 0 ? (
          <p className="muted">—</p>
        ) : (
          <div className="chips">
            {data.depends_on.map((dependency) => (
              <span className="chip" key={dependency}>
                {dependency}
              </span>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}
