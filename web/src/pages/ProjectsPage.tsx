import { useNavigate, useParams } from 'react-router-dom'
import { listProjects } from '../api/client'
import { itemsOf } from '../api/types'
import { useDrawer } from '../components/DrawerContext'
import {
  DetailButton,
  DrawerPlaceholder,
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { usePolling } from '../hooks/usePolling'
import { formatSince, projectStatusLabel } from '../lib/format'
import { isInteractiveTarget } from '../lib/interaction'

/** 项目列表：标题/状态/停留时长，点击进入任务看板。 */
export function ProjectsPage() {
  const navigate = useNavigate()
  const openDrawer = useDrawer()
  const { productId = '' } = useParams()

  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(
    (signal) => listProjects(productId, signal),
    [productId],
  )
  const projects = itemsOf(data)

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>项目</h1>
          <p className="muted">
            产品 <span className="code">{productId}</span> 下的需求项目
          </p>
        </div>
        <RefreshBar
          lastUpdated={lastUpdated}
          refreshing={refreshing}
          error={error}
          onRefresh={refresh}
        />
      </header>

      <ErrorNotice error={error} onRetry={refresh} />

      {!data && loading && <LoadingState label="正在加载项目…" />}
      {!data && !loading && error && (
        <EmptyState title="项目加载失败" hint="页面会自动重试；也可以点击上方“重试”。" />
      )}
      {data && projects.length === 0 && (
        <EmptyState title="暂无项目" hint="该产品下还没有需求项目。" />
      )}

      {projects.length > 0 && (
        <div className="list">
          {projects.map((project) => (
            <article
              key={project.project_id}
              className="row project-row"
              onClick={(event) => {
                if (isInteractiveTarget(event.target)) return
                navigate(
                  `/products/${encodeURIComponent(productId)}/projects/${encodeURIComponent(
                    project.project_id,
                  )}/tasks`,
                )
              }}
            >
              <div className="row-main">
                <div className="row-title">
                  <span className="code">{project.project_id}</span>
                  <h2>{project.title || '（未命名项目）'}</h2>
                </div>
                <div className="row-meta">
                  <span className={`badge status-${project.status}`}>
                    {projectStatusLabel(project.status)}
                  </span>
                  <span>停留 {formatSince(project.update_time)}</span>
                  <span>
                    任务 {project.task_stats.done}/{project.task_stats.total}
                  </span>
                </div>
              </div>
              <div className="row-actions">
                <DetailButton
                  onClick={() =>
                    openDrawer({
                      title: `项目 ${project.project_id}`,
                      body: (
                        <DrawerPlaceholder
                          kind="项目"
                          id={project.project_id}
                          status={projectStatusLabel(project.status)}
                        />
                      ),
                    })
                  }
                />
                <span className="link">任务看板 →</span>
              </div>
            </article>
          ))}
        </div>
      )}
    </section>
  )
}
