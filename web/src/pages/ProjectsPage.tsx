import { useCallback, useEffect, useRef, useState, type MouseEvent } from 'react'
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
  formatDateTime,
  formatSince,
  isPendingProjectStatus,
  isPendingStale,
  projectStatusLabel,
} from '../lib/format'
import { copyText } from '../lib/clipboard'
import { isInteractiveTarget } from '../lib/interaction'

/** 列表行内复制图标（复制态与成功态共用内联 SVG，避免引入图标依赖）。 */
function CopyIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <rect x="9" y="9" width="12" height="12" rx="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
    </svg>
  )
}

function CheckIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M20 6 9 17l-5-5" />
    </svg>
  )
}

/** 'all' 表示不加 status 参数；数字走服务端单值过滤。 */
type ProjectStatusFilter = 'all' | number

const PAGE_SIZE = 20

/**
 * 项目列表：默认显示「全部」，状态下拉框切换即请求服务端过滤。
 * 分页也在服务端完成：下一页用响应的 next_cursor，上一页用页面内 cursor 栈。
 */
export function ProjectsPage() {
  const navigate = useNavigate()
  const openDrawer = useDrawer()
  const { productId = '' } = useParams()
  const [status, setStatus] = useState<ProjectStatusFilter>('all')
  // 首页 cursor 为 undefined；栈里存的是「进入下一页前所在页」的 cursor。
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [cursorStack, setCursorStack] = useState<(string | undefined)[]>([])
  const [creating, setCreating] = useState(false)
  // 弹窗状态放在页面层：项目离开当前筛选时行会被卸载，但用户正在填写的弹窗必须保留。
  const [action, setAction] = useState<{ project: Project; request: ProjectActionRequest } | null>(null)
  // 按 project_id 记录复制反馈，避免多行同时进入“已复制”状态。
  const [copyState, setCopyState] = useState<{ id: string; ok: boolean } | null>(null)
  const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(
    () => () => {
      if (copyTimerRef.current) clearTimeout(copyTimerRef.current)
    },
    [],
  )

  const copyProjectId = useCallback(async (event: MouseEvent<HTMLButtonElement>, projectId: string) => {
    // 行本身有点击导航，复制按钮不能冒泡触发行跳转。
    event.stopPropagation()
    const ok = await copyText(projectId)
    setCopyState({ id: projectId, ok })
    if (copyTimerRef.current) clearTimeout(copyTimerRef.current)
    copyTimerRef.current = setTimeout(() => setCopyState(null), ok ? 1500 : 2500)
  }, [])

  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(
    (signal) =>
      listProjects(productId, {
        status: status === 'all' ? undefined : status,
        cursor,
        limit: PAGE_SIZE,
        signal,
      }),
    [productId, status, cursor],
  )
  const projects = itemsOf(data)
  const hasPrev = cursorStack.length > 0
  const pageNo = cursorStack.length + 1

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

  // 切换产品时游标不再属于同一列表，回到第一页。
  useEffect(() => {
    setCursor(undefined)
    setCursorStack([])
  }, [productId])

  // 切换筛选：同时归零 cursor 与栈，React 批处理下只会发一次「新筛选 + 首页」请求。
  const handleStatusChange = (value: string) => {
    setStatus(value === 'all' ? 'all' : Number(value))
    setCursor(undefined)
    setCursorStack([])
  }

  const goNext = () => {
    const next = data?.next_cursor
    if (!next) return
    setCursorStack((prev) => [...prev, cursor])
    setCursor(next)
  }

  const goPrev = () => {
    if (cursorStack.length === 0) return
    setCursor(cursorStack[cursorStack.length - 1])
    setCursorStack(cursorStack.slice(0, -1))
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

      <div className="filter-bar">
        <label className="filter-select">
          <span className="muted">状态</span>
          <select
            aria-label="项目状态筛选"
            value={status === 'all' ? 'all' : String(status)}
            onChange={(event) => handleStatusChange(event.target.value)}
          >
            <option value="all">全部</option>
            {PROJECT_STATUS_OPTIONS.map((option) => (
              <option key={option.status} value={option.status}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
      </div>

      {!data && loading && <LoadingState label="正在加载项目…" />}
      {!data && !loading && error && (
        <EmptyState title="项目加载失败" hint="页面会自动重试；也可以点击上方“重试”。" />
      )}
      {data && projects.length === 0 && !hasPrev && (
        <EmptyState
          title={status === 'all' ? '暂无项目' : '该筛选下暂无项目'}
          hint={
            status === 'all'
              ? '点击右上角“新建需求项目”创建第一条需求。'
              : '切换到“全部”或其他状态查看更多。'
          }
        />
      )}
      {data && projects.length === 0 && hasPrev && (
        <EmptyState title="本页已无项目" hint="数据可能已被删除或状态已变化，请回到上一页。" />
      )}

      {projects.length > 0 && (
        <div className="list">
          {projects.map((project) => {
            const stale = isPendingProjectStatus(project.status) && isPendingStale(project.update_time)
            // 任务草稿去拆解，任务评审去评审；其余状态仍进同一张看板。
            const boardEntry =
              project.status === 4 ? '去拆解 →' : project.status === 5 ? '去评审 →' : '任务看板 →'
            // 驳回字段只可能落在任务评审(5)/待完成(6)；任一存在都直接展示。
            const taskReviewReason = project.tasks_review_reason
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
                    <button
                      type="button"
                      className={`copy-icon-button${
                        copyState?.id === project.project_id && copyState.ok ? ' is-copied' : ''
                      }${copyState?.id === project.project_id && !copyState.ok ? ' is-failed' : ''}`}
                      aria-label={`复制项目编号 ${project.project_id}`}
                      title={
                        copyState?.id === project.project_id && copyState.ok
                          ? '已复制'
                          : `复制项目编号 ${project.project_id}`
                      }
                      onClick={(event) => void copyProjectId(event, project.project_id)}
                    >
                      {copyState?.id === project.project_id && copyState.ok ? (
                        <CheckIcon />
                      ) : (
                        <CopyIcon />
                      )}
                    </button>
                    {copyState?.id === project.project_id && !copyState.ok && (
                      <span className="copy-failed">复制失败，请手动选中</span>
                    )}
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
                  {taskReviewReason && (
                    <p className="row-reason row-reason-rejected">
                      任务清单已驳回：{taskReviewReason}
                      <span className="muted">
                        {' '}· {project.last_actor || '—'} ·{' '}
                        {formatDateTime(project.tasks_review_rejected_at)}
                      </span>
                    </p>
                  )}
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
                  <span className="link">{boardEntry}</span>
                </div>
              </article>
            )
          })}
        </div>
      )}

      {data && (projects.length > 0 || hasPrev) && (
        <nav className="pager" aria-label="项目分页">
          <button type="button" className="action" onClick={goPrev} disabled={!hasPrev}>
            上一页
          </button>
          <span className="muted">第 {pageNo} 页</span>
          <button type="button" className="action" onClick={goNext} disabled={!data.next_cursor}>
            下一页
          </button>
        </nav>
      )}

      {creating && (
        <ProjectFormDialog
          mode="create"
          productId={productId}
          onClose={() => setCreating(false)}
          onSaved={() => {
            setStatus('all')
            setCursor(undefined)
            setCursorStack([])
            refresh()
          }}
        />
      )}

      {action && (
        <ProjectActionDialogs
          project={action.project}
          request={action.request}
          onClose={() => setAction(null)}
          onChanged={refresh}
        />
      )}
    </section>
  )
}
