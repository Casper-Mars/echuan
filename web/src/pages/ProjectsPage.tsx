import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ApiError, getProduct, listProjects } from '../api/client'
import type { Project } from '../api/types'
import { itemsOf } from '../api/types'
import { useDrawer } from '../components/DrawerContext'
import {
  ProjectActionButtons,
  ProjectActionDialogs,
  type ProjectActionRequest,
} from '../components/ProjectActions'
import { ProjectDetailDrawer } from '../components/ProjectDetailDrawer'
import { ProjectFormDialog } from '../components/ProjectFormDialog'
import {
  DetailButton,
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { usePolling } from '../hooks/usePolling'
import {
  PROJECT_STATUS_OPTIONS,
  formatSince,
  isPendingProjectStatus,
  isPendingStale,
  projectStatusLabel,
} from '../lib/format'
import { isInteractiveTarget } from '../lib/interaction'

type ProjectFilter = 'pending' | 'all' | number

/**
 * 项目列表：默认只显示「待我决策」（status 1/3/5），可切换全部或具体状态。
 * 行内展示标题/状态/停留时长/reason，并提供立项、批准/驳回、取消与详情。
 */
export function ProjectsPage() {
  const navigate = useNavigate()
  const openDrawer = useDrawer()
  const { productId = '' } = useParams()
  const [filter, setFilter] = useState<ProjectFilter>('pending')
  const [creating, setCreating] = useState(false)
  // 弹窗状态放在页面层：项目离开当前筛选时行会被卸载，但用户正在填写的弹窗必须保留。
  const [action, setAction] = useState<{ project: Project; request: ProjectActionRequest } | null>(null)

  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(
    (signal) => listProjects(productId, signal),
    [productId],
  )
  const projects = itemsOf(data)
  const pendingCount = projects.filter((project) => isPendingProjectStatus(project.status)).length

  // 记忆里的产品已被删除时回产品列表，而不是卡在错误页反复轮询。
  // 只在 404 时重定向一次，网络抖动等其它错误继续由 usePolling 自动重试。
  useEffect(() => {
    if (error instanceof ApiError && error.status === 404) {
      navigate('/products', { replace: true })
    }
  }, [error, navigate])

  // 后端对不存在的 product_id 只返回空列表（200）而非 404，仅靠上面的错误判断
  // 会停在“一个不存在的产品的空项目页”。这里每次进入页面补一次产品存在性校验（不轮询），
  // 产品已删则回产品列表，保证“任务 → 项目 → 产品”的回退链能收敛。
  useEffect(() => {
    const controller = new AbortController()
    getProduct(productId, controller.signal).catch((err: unknown) => {
      if (err instanceof ApiError && err.status === 404) {
        navigate('/products', { replace: true })
      }
    })
    return () => controller.abort()
  }, [productId, navigate])

  const visible =
    filter === 'all'
      ? projects
      : filter === 'pending'
        ? projects.filter((project) => isPendingProjectStatus(project.status))
        : projects.filter((project) => project.status === filter)

  const statusCount = (status: number) => projects.filter((project) => project.status === status).length

  const handleDecided = (updated: Project) => {
    // 批准/取消后项目会离开“待我决策”，切到「全部」让新状态与 reason 立即可见。
    if (filter === 'pending' && !isPendingProjectStatus(updated.status)) setFilter('all')
  }

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>项目</h1>
          <p className="muted">
            产品 <span className="code">{productId}</span> 下的需求项目
          </p>
        </div>
        <div className="page-head-actions">
          <RefreshBar
            lastUpdated={lastUpdated}
            refreshing={refreshing}
            error={error}
            onRefresh={refresh}
          />
          <button type="button" className="action action-primary" onClick={() => setCreating(true)}>
            新建需求项目
          </button>
        </div>
      </header>

      <ErrorNotice error={error} onRetry={refresh} />

      <div className="filter-bar" role="group" aria-label="项目状态筛选">
        <button
          type="button"
          className={filter === 'pending' ? 'filter-chip is-active' : 'filter-chip'}
          onClick={() => setFilter('pending')}
        >
          待我决策 <span className="count">{pendingCount}</span>
        </button>
        <button
          type="button"
          className={filter === 'all' ? 'filter-chip is-active' : 'filter-chip'}
          onClick={() => setFilter('all')}
        >
          全部 <span className="count">{projects.length}</span>
        </button>
        {PROJECT_STATUS_OPTIONS.map((option) => (
          <button
            key={option.status}
            type="button"
            className={filter === option.status ? 'filter-chip is-active' : 'filter-chip'}
            onClick={() => setFilter(option.status)}
          >
            {option.label} <span className="count">{statusCount(option.status)}</span>
          </button>
        ))}
      </div>

      {!data && loading && <LoadingState label="正在加载项目…" />}
      {!data && !loading && error && (
        <EmptyState title="项目加载失败" hint="页面会自动重试；也可以点击上方“重试”。" />
      )}
      {data && projects.length === 0 && (
        <EmptyState title="暂无项目" hint="点击右上角“新建需求项目”创建第一条需求。" />
      )}
      {data && projects.length > 0 && visible.length === 0 && (
        <EmptyState title="该筛选下没有项目" hint="切换到“全部”或其他状态查看更多。" />
      )}

      {visible.length > 0 && (
        <div className="list">
          {visible.map((project) => {
            const stale = isPendingProjectStatus(project.status) && isPendingStale(project.update_time)
            return (
              <article
                key={project.project_id}
                className={stale ? 'row project-row is-stale' : 'row project-row'}
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
                    <span className={stale ? 'stale-text' : undefined} title={stale ? '待决策超过 72 小时' : undefined}>
                      停留 {formatSince(project.update_time)}
                      {stale && ' · 超 72h'}
                    </span>
                    <span>
                      任务 {project.task_stats.done}/{project.task_stats.total}
                    </span>
                    <span className="mono">第 {project.version} 版</span>
                  </div>
                  <p className={project.reason ? 'row-reason' : 'row-reason muted'}>
                    原因：{project.reason || '—'}
                  </p>
                </div>
                <div className="row-actions">
                  <ProjectActionButtons
                    project={project}
                    onAction={(request) => setAction({ project, request })}
                  />
                  <DetailButton
                    onClick={() =>
                      openDrawer({
                        title: `项目 ${project.project_id} · ${project.title || '未命名'}`,
                        body: (
                          <ProjectDetailDrawer projectId={project.project_id} onChanged={refresh} />
                        ),
                      })
                    }
                  />
                  <span className="link">任务看板 →</span>
                </div>
              </article>
            )
          })}
        </div>
      )}

      {creating && (
        <ProjectFormDialog
          mode="create"
          productId={productId}
          onClose={() => setCreating(false)}
          onSaved={() => { setFilter('all'); refresh() }}
        />
      )}

      {action && (
        <ProjectActionDialogs
          project={action.project}
          request={action.request}
          onClose={() => setAction(null)}
          onChanged={refresh}
          onDecided={handleDecided}
        />
      )}
    </section>
  )
}
