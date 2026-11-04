import { useState } from 'react'
import { getWorkItem, materialDownloadUrl } from '../api/client'
import { usePolling } from '../hooks/usePolling'
import {
  formatBytes,
  formatDateTime,
  formatSince,
  workItemKindLabel,
  workItemStateLabel,
} from '../lib/format'
import { ErrorNotice, LoadingState } from './States'
import { Markdown } from './Markdown'
import { MaterialReader } from './MaterialReader'
import { WorkItemActions } from './WorkItemActions'

/**
 * 工作项详情抽屉：kind/state、描述、结果资料（元信息 + 下载）、依赖、
 * 尝试次数、租约、reason / create_time / update_time，以及验收/返工/重排动作。
 * dev 类型的展示字段与 v1 任务详情保持一致。
 */
export function WorkItemDetailDrawer({
  workId,
  onChanged,
}: {
  workId: string
  onChanged: () => void
}) {
  const { data, error, loading, refreshing, refresh } = usePolling(
    (signal) => getWorkItem(workId, signal),
    [workId],
  )
  const [reading, setReading] = useState(false)

  if (!data) {
    return (
      <div className="detail">
        {loading && <LoadingState label="正在加载工作项详情…" />}
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
        <span className={`badge state-${data.state}`}>{workItemStateLabel(data.state)}</span>
        <span className={`badge kind-${data.kind}`}>类型：{workItemKindLabel(data.kind)}</span>
        <span className="mono">第 {data.version} 版</span>
        <span>
          尝试 {data.attempt}/{data.max_attempts}
        </span>
        <span>停留 {formatSince(data.update_time)}</span>
        {refreshing && <span className="muted">刷新中…</span>}
      </div>

      <dl className="meta-grid">
        <div>
          <dt>工作项 ID</dt>
          <dd className="mono">{data.work_id}</dd>
        </div>
        <div>
          <dt>所属项目</dt>
          <dd className="mono">{data.project_id}</dd>
        </div>
        <div>
          <dt>优先级</dt>
          <dd>{data.priority}</dd>
        </div>
        <div>
          <dt>执行节点</dt>
          <dd className="mono">{data.worker_id || '—'}</dd>
        </div>
        <div>
          <dt>更新时间</dt>
          <dd>{formatDateTime(data.update_time)}</dd>
        </div>
        <div>
          <dt>创建时间</dt>
          <dd>{formatDateTime(data.create_time)}</dd>
        </div>
        {data.lease && (
          <div>
            <dt>租约到期</dt>
            <dd>{formatDateTime(data.lease.until)}</dd>
          </div>
        )}
      </dl>

      <section className="detail-section">
        <h3>原因</h3>
        <p className={data.reason ? 'reason-text' : 'muted'}>{data.reason || '—'}</p>
      </section>

      <WorkItemActions item={data} onChanged={changed} className="detail-actions" />

      <section className="detail-section">
        <h3>工作项描述</h3>
        <Markdown source={data.body_md} empty="暂无工作项描述" />
      </section>

      <section className="detail-section">
        <h3>结果资料</h3>
        {data.result ? (
          <div className="report-box">
            <div className="report-meta">
              <span className="code">第 {data.result.version} 版</span>
              <span>{data.result.uploaded_by || '—'}</span>
              <span className="muted">{formatDateTime(data.result.create_time)}</span>
              <span className="muted">{formatBytes(data.result.size)}</span>
            </div>
            <p className="mono muted ellipsis" title={data.result.sha256}>
              内容校验值：{data.result.sha256}
            </p>
            <div className="report-actions">
              <button
                type="button"
                className="action action-primary"
                onClick={() => setReading(true)}
              >
                阅读
              </button>
              <a className="action" href={materialDownloadUrl(data.result.material_id)} download>
                下载结果资料
              </a>
            </div>
          </div>
        ) : (
          <p className="muted">尚未提交结果资料</p>
        )}
      </section>

      <section className="detail-section">
        <h3>依赖工作项</h3>
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

      {reading && data.result && (
        <MaterialReader material={data.result} onClose={() => setReading(false)} />
      )}
    </div>
  )
}
