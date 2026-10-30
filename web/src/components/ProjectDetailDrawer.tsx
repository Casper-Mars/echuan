import { useContext, useEffect, useState } from 'react'
import { getProject, listProjectMaterials, materialDownloadUrl, projectPackageUrl } from '../api/client'
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
import { ProjectActions } from './ProjectActions'
import { ProjectFormDialog } from './ProjectFormDialog'
import { DrawerTitleContext } from './DrawerContext'
import { SpecDocuments } from './SpecDocuments'

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
                <span className="code">第 {item.version} 版</span>
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
 * 项目详情抽屉：需求/方案版本链、方案当前版元信息、任务进度、
 * reason / last_actor / update_time，以及立项/批准/驳回/取消动作。
 * 正文不在详情里返回，需要时按 material_id 单独下载。
 * 自身 5s 轮询；编辑中的输入不会被覆盖（usePolling 的 isUserEditing 守卫）。
 */
export function ProjectDetailDrawer({
  projectId,
  onChanged,
}: {
  projectId: string
  onChanged: () => void
}) {
  const [editing, setEditing] = useState<'title' | 'requirement' | null>(null)
  const updateDrawerTitle = useContext(DrawerTitleContext)
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

  useEffect(() => {
    if (data) updateDrawerTitle(`项目 ${data.detail.project_id} · ${data.detail.title || '未命名'}`)
  }, [data?.detail.project_id, data?.detail.title, updateDrawerTitle])

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
        <span className="mono">第 {detail.version} 版</span>
        <span>停留 {formatSince(detail.update_time)}</span>
        {refreshing && <span className="muted">刷新中…</span>}
      </div>

      <dl className="meta-grid">
        <div>
          <dt>最近操作人</dt>
          <dd>{detail.last_actor || '—'}</dd>
        </div>
        <div>
          <dt>更新时间</dt>
          <dd>{formatDateTime(detail.update_time)}</dd>
        </div>
        <div>
          <dt>创建时间</dt>
          <dd>{formatDateTime(detail.create_time)}</dd>
        </div>
        <div>
          <dt>序号</dt>
          <dd>#{detail.seq_no}</dd>
        </div>
      </dl>

      <section className="detail-section">
        <h3>原因</h3>
        <p className={detail.reason ? 'reason-text' : 'muted'}>{detail.reason || '—'}</p>
      </section>

      <ProjectActions project={detail} onChanged={changed} className="detail-actions" />
      <div className="detail-actions">
        <button type="button" className="action" onClick={() => setEditing('title')}>编辑标题</button>
        {(detail.status === 0 || detail.status === 1) && (
          <button type="button" className="action" onClick={() => setEditing('requirement')}>编辑需求</button>
        )}
      </div>

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

      <ChainList
        title="需求版本链"
        chain={requirementChain}
        currentMaterialId={detail.requirement?.material_id}
      />

      <section className="detail-section">
        <h3>项目资料包</h3>
        {!detail.requirement && !detail.spec && <p className="muted">暂无资料</p>}
        {(detail.requirement || detail.spec) && (
          <p className="download-line">
            <a className="link" href={projectPackageUrl(detail.project_id)} download>
              下载项目资料包
            </a>
            <span className="muted">
              {detail.requirement ? `需求第 ${detail.requirement.version} 版` : '无需求'}
              {' · '}
              {detail.spec ? `最近方案第 ${detail.spec.version} 版` : '无方案'}
            </span>
          </p>
        )}
      </section>

      <SpecDocuments projectId={projectId} status={detail.status} onChanged={changed} />
      <ChainList title="方案提交历史" chain={specChain} currentMaterialId={detail.spec?.material_id} />
      {editing && (
        <ProjectFormDialog mode={editing} project={detail} onClose={() => setEditing(null)} onSaved={changed} />
      )}
    </div>
  )
}
