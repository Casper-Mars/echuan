import { getProject, listProjectMaterials, materialDownloadUrl } from '../api/client'
import type { MaterialChain, MaterialVersion, ProjectDetail } from '../api/types'
import { usePolling } from '../hooks/usePolling'
import {
  TASK_COLUMNS,
  formatBytes,
  formatDateTime,
  formatSince,
  projectStatusLabel,
} from '../lib/format'
import { ErrorNotice, LoadingState } from './States'
import { Markdown } from './Markdown'
import { ProjectActions } from './ProjectActions'

interface ProjectBundle {
  detail: ProjectDetail
  requirementChain: MaterialChain
  specChain: MaterialChain
}

/** 版本链按版本号倒序（最新在前），保证 ≥2 版时顺序稳定。 */
function sortedVersions(chain: MaterialChain): MaterialVersion[] {
  return [...chain.items].sort((a, b) => b.version - a.version)
}

function ChainList({
  title,
  chain,
  currentMaterialId,
}: {
  title: string
  chain: MaterialChain
  currentMaterialId?: string
}) {
  const items = sortedVersions(chain)
  return (
    <section className="detail-section">
      <h3>
        {title} <span className="muted">（{items.length} 版）</span>
      </h3>
      {items.length === 0 && <p className="muted">暂无版本</p>}
      {items.length > 0 && (
        <ul className="chain-list">
          {items.map((item) => {
            const isCurrent = item.material_id === currentMaterialId
            return (
              <li key={item.material_id} className={isCurrent ? 'chain-item is-current' : 'chain-item'}>
                <span className="code">v{item.version}</span>
                {isCurrent && <span className="chip chip-current">当前</span>}
                <span className="chain-title ellipsis" title={item.title}>
                  {item.title || '（无标题）'}
                </span>
                <span className="muted">{item.uploaded_by || '—'}</span>
                <span className="muted">{formatDateTime(item.create_time)}</span>
                <span className="muted">{formatBytes(item.size)}</span>
                <a
                  className="link"
                  href={materialDownloadUrl(item.material_id)}
                  download
                  onClick={(event) => event.stopPropagation()}
                >
                  下载
                </a>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

/**
 * 项目详情抽屉：需求正文、方案当前版摘要与版本链、任务进度、
 * reason / last_actor / update_time，以及立项/批准/驳回/取消动作。
 * 自身 5s 轮询；编辑中的输入不会被覆盖（usePolling 的 isUserEditing 守卫）。
 */
export function ProjectDetailDrawer({
  projectId,
  onChanged,
}: {
  projectId: string
  onChanged: () => void
}) {
  const { data, error, loading, refreshing, refresh } = usePolling<ProjectBundle>(
    async (signal) => {
      const [detail, requirementChain, specChain] = await Promise.all([
        getProject(projectId, signal),
        listProjectMaterials(projectId, 1, signal),
        listProjectMaterials(projectId, 2, signal),
      ])
      return { detail, requirementChain, specChain }
    },
    [projectId],
  )

  if (!data) {
    return (
      <div className="detail">
        {loading && <LoadingState label="正在加载项目详情…" />}
        {error && <ErrorNotice error={error} onRetry={refresh} />}
      </div>
    )
  }

  const { detail, requirementChain, specChain } = data
  const byStatus = detail.task_stats.by_status ?? {}
  const changed = () => {
    refresh()
    onChanged()
  }

  return (
    <div className="detail">
      {error && <ErrorNotice error={error} onRetry={refresh} />}

      <div className="detail-summary">
        <span className={`badge status-${detail.status}`}>{projectStatusLabel(detail.status)}</span>
        <span className="mono">版本 v{detail.version}</span>
        <span>停留 {formatSince(detail.update_time)}</span>
        {refreshing && <span className="muted">刷新中…</span>}
      </div>

      <dl className="meta-grid">
        <div>
          <dt>last_actor</dt>
          <dd>{detail.last_actor || '—'}</dd>
        </div>
        <div>
          <dt>update_time</dt>
          <dd>{formatDateTime(detail.update_time)}</dd>
        </div>
        <div>
          <dt>create_time</dt>
          <dd>{formatDateTime(detail.create_time)}</dd>
        </div>
        <div>
          <dt>序号</dt>
          <dd>#{detail.seq_no}</dd>
        </div>
      </dl>

      <section className="detail-section">
        <h3>reason</h3>
        <p className={detail.reason ? 'reason-text' : 'muted'}>{detail.reason || '—'}</p>
      </section>

      <ProjectActions project={detail} onChanged={changed} className="detail-actions" />

      <section className="detail-section">
        <h3>任务进度</h3>
        <div className="progress-grid">
          <div className="progress-total">
            <strong>
              {detail.task_stats.done}/{detail.task_stats.total}
            </strong>
            <span className="muted">已完成</span>
          </div>
          {TASK_COLUMNS.map((column) => (
            <div key={column.status} className="progress-cell">
              <span className="muted">{column.label}</span>
              <strong>{byStatus[String(column.status)] ?? 0}</strong>
            </div>
          ))}
        </div>
      </section>

      <section className="detail-section">
        <h3>需求正文</h3>
        <Markdown source={detail.requirement_md} empty="暂无需求正文" />
      </section>

      <ChainList
        title="需求版本链"
        chain={requirementChain}
        currentMaterialId={detail.requirement?.material_id}
      />

      <section className="detail-section">
        <h3>
          方案当前版摘要
          {detail.spec && <span className="muted">（v{detail.spec.version}）</span>}
        </h3>
        <Markdown source={detail.spec_excerpt} empty="暂无方案" />
        {detail.spec && (
          <p className="download-line">
            <a className="link" href={materialDownloadUrl(detail.spec.material_id)} download>
              下载方案原件
            </a>
            <span className="muted">
              v{detail.spec.version} · {formatBytes(detail.spec.size)} ·{' '}
              {detail.spec.uploaded_by || '—'}
            </span>
          </p>
        )}
      </section>

      <ChainList title="方案版本链" chain={specChain} currentMaterialId={detail.spec?.material_id} />
    </div>
  )
}
