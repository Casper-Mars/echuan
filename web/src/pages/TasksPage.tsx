import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ApiError, deleteTask, getProject, listTasks } from '../api/client'
import type { Task, TaskDecision } from '../api/types'
import { itemsOf } from '../api/types'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useDrawer } from '../components/DrawerContext'
import { TaskActionButtons, TaskActionDialogs } from '../components/TaskActions'
import { TaskDetailDrawer } from '../components/TaskDetailDrawer'
import { TaskFormDialog } from '../components/TaskFormDialog'
import { TaskReviewDialog } from '../components/TaskReviewDialog'
import {
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { usePolling } from '../hooks/usePolling'
import { useWorkerPresence } from '../hooks/useWorkerPresence'
import {
  TASK_BOARD_COLUMNS,
  TASK_BOARD_TRAILING_COLUMNS,
  formatDateTime,
  formatSince,
  projectStatusLabel,
} from '../lib/format'

/** 只有任务草稿(4)/任务评审(5)/待完成(6) 允许加卡。 */
const ADD_TASK_STATUSES = new Set([4, 5, 6])

/**
 * 任务看板：待评审(0) 非空时单独成列并带整批评审；
 * 待开发/开发中/待验收/已完成四列常驻；已失败/阻塞/已取消仅非空显示。
 * 卡片按状态提供验收/返工与重排/放弃入口。
 */
export function TasksPage() {
  const navigate = useNavigate()
  const openDrawer = useDrawer()
  const { productId = '', projectId = '' } = useParams()
  // 卡片会在列间移动；弹窗提升到页面层，避免决策过程中被卸载而丢失 reason。
  const [action, setAction] = useState<{ task: Task; decision: TaskDecision } | null>(null)
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Task | null>(null)
  const [deleting, setDeleting] = useState<Task | null>(null)
  const [review, setReview] = useState<'approve' | 'reject' | null>(null)

  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(
    async (signal) => {
      const [tasks, project] = await Promise.all([
        listTasks(projectId, signal),
        getProject(projectId, signal),
      ])
      return { tasks, project }
    },
    [projectId],
  )

  const tasks = itemsOf(data?.tasks)
  const project = data?.project

  // 记忆里的项目（或它所属的产品）已被删除时后端返回 404：回项目列表。
  // 若产品也没了，ProjectsPage 会再因 404 回产品列表，整条回退链单向向上、不会来回震荡。
  useEffect(() => {
    if (error instanceof ApiError && error.status === 404) {
      navigate(`/products/${encodeURIComponent(productId)}/projects`, { replace: true })
    }
  }, [error, navigate, productId])

  // 执行者在线状态单独轮询：worker 拉取失败不影响看板本身。
  // 只查本页任务涉及的 worker；首页 200 条未命中的再逐个补查（见 useWorkerPresence）。
  const executorIds = useMemo(
    () => tasks.map((task) => task.worker_id).filter((id): id is string => id !== null),
    [tasks],
  )
  const { presenceOf, known: workersKnown } = useWorkerPresence(executorIds)

  const done = tasks.filter((task) => task.status === 4).length
  const backlog = tasks.filter((task) => task.status === 0)
  const reviewReason = project?.tasks_review_reason ?? null
  const rejectedAt = project?.tasks_review_rejected_at ?? null
  const canAddTask = project ? ADD_TASK_STATUSES.has(project.status) : false

  // 待评审列：有卡才出现；四列常驻；附加三列非空才出现。
  const columns = [
    ...TASK_BOARD_COLUMNS.filter(
      (column) => column.status !== 0 || backlog.length > 0,
    ),
    ...TASK_BOARD_TRAILING_COLUMNS.filter((column) =>
      tasks.some((task) => task.status === column.status),
    ),
  ]

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>任务看板</h1>
          <p className="muted">
            产品 <span className="code">{productId}</span> · 项目{' '}
            <span className="code">{projectId}</span>
            {project ? ` · ${project.title}` : ''} · 共 {tasks.length} 个任务，已完成 {done} 个
          </p>
          {project && (
            <p className="board-context">
              <span className={`badge status-${project.status}`}>
                {projectStatusLabel(project.status)}
              </span>
              <span className="muted">项目原因：{project.reason || '—'}</span>
            </p>
          )}
        </div>
        <div className="page-head-actions">
          <RefreshBar
            lastUpdated={lastUpdated}
            refreshing={refreshing}
            error={error}
            onRefresh={refresh}
          />
          <button
            type="button"
            className="action action-primary"
            onClick={() => setCreating(true)}
            disabled={!canAddTask}
            title={
              canAddTask
                ? undefined
                : '仅任务草稿、任务评审、待完成阶段可以添加任务'
            }
          >
            添加任务
          </button>
        </div>
      </header>

      <ErrorNotice error={error} onRetry={refresh} />

      {!data && loading && <LoadingState label="正在加载任务…" />}
      {!data && !loading && error && (
        <EmptyState title="任务加载失败" hint="页面会自动重试；也可以点击上方“重试”。" />
      )}

      {data && (
        <div className="board">
          {columns.map((column) => {
            const columnTasks = tasks.filter((task) => task.status === column.status)
            const isReview = column.status === 0
            return (
              <section
                className={isReview ? 'board-column board-column-review' : 'board-column'}
                key={column.status}
              >
                <header className="board-column-head">
                  <span>{column.label}</span>
                  <span className="count">{columnTasks.length}</span>
                </header>

                {isReview && (
                  <>
                    <div className="board-review-actions">
                      <button
                        type="button"
                        className="action action-primary action-compact"
                        onClick={() => setReview('approve')}
                        disabled={columnTasks.length === 0}
                      >
                        全部通过
                      </button>
                      <button
                        type="button"
                        className="action action-danger action-compact"
                        onClick={() => setReview('reject')}
                        disabled={Boolean(reviewReason)}
                        title={
                          reviewReason
                            ? '已驳回，修改任意一张卡后可再次驳回'
                            : undefined
                        }
                      >
                        全部驳回
                      </button>
                    </div>

                    {reviewReason && (
                      <div className="review-banner" role="status">
                        <strong>任务清单已驳回：</strong>
                        <span className="review-banner-reason">{reviewReason}</span>
                        <span className="review-banner-meta">
                          操作人 {project?.last_actor || '—'} · {formatDateTime(rejectedAt)}
                        </span>
                      </div>
                    )}
                  </>
                )}

                <div className="board-cards">
                  {columnTasks.map((task) => {
                    // 只有「拿到过 worker 数据」且「明确查到 offline」才告警；
                    // 未命中首页且补查失败时状态未知，不能误判为离线。
                    const executorPresence = task.worker_id
                      ? presenceOf(task.worker_id)
                      : null
                    const executorOffline =
                      workersKnown && executorPresence === 'offline'
                    return (
                      <article
                        key={task.task_id}
                        className={
                          executorOffline
                            ? 'board-card board-card-executor-offline'
                            : 'board-card'
                        }
                        onClick={() =>
                          openDrawer({
                            title: `任务 ${task.task_id} · ${task.title}`,
                            body: (
                              <TaskDetailDrawer taskId={task.task_id} onChanged={refresh} />
                            ),
                          })
                        }
                      >
                        <div className="card-head">
                          <span className="code">{task.task_id}</span>
                          <span className="muted">#{task.seq_no}</span>
                        </div>
                        <h3 className="board-card-title">{task.title}</h3>
                        {task.worker_id && (
                          <p
                            className={
                              executorOffline
                                ? 'board-card-worker offline'
                                : 'board-card-worker'
                            }
                          >
                            <span className="mono" title={task.worker_id}>
                              ⚙ {task.worker_id}
                            </span>
                            {executorOffline && (
                              <span className="worker-offline-flag">⚠ 执行者已离线</span>
                            )}
                          </p>
                        )}
                        <p
                          className={
                            task.reason ? 'board-card-reason' : 'board-card-reason muted'
                          }
                          title={task.reason ?? undefined}
                        >
                          原因：{task.reason || '—'}
                        </p>
                        <footer className="board-card-foot">
                          <span>
                            尝试 {task.attempt_count}/{task.max_attempts}
                          </span>
                          <span>停留 {formatSince(task.update_time)}</span>
                        </footer>

                        {isReview && (
                          // 动卡即解锁：编辑/删除后后端清空驳回字段，提示条消失、驳回按钮恢复。
                          <div
                            className="board-card-admin"
                            onClick={(event) => event.stopPropagation()}
                          >
                            <button
                              type="button"
                              className="ghost ghost-mini"
                              onClick={() => setEditing(task)}
                            >
                              编辑
                            </button>
                            <button
                              type="button"
                              className="ghost ghost-mini"
                              onClick={() => setDeleting(task)}
                            >
                              删除
                            </button>
                          </div>
                        )}

                        <TaskActionButtons
                          task={task}
                          onAction={(decision) => setAction({ task, decision })}
                          className="board-card-actions"
                        />
                      </article>
                    )
                  })}
                  {columnTasks.length === 0 && <p className="board-empty">暂无</p>}
                </div>
              </section>
            )
          })}
        </div>
      )}

      {action && (
        <TaskActionDialogs
          task={action.task}
          decision={action.decision}
          onClose={() => setAction(null)}
          onChanged={refresh}
        />
      )}

      {creating && (
        <TaskFormDialog
          mode="create"
          projectId={projectId}
          onClose={() => setCreating(false)}
          onSaved={refresh}
        />
      )}

      {editing && (
        <TaskFormDialog
          mode="edit"
          projectId={projectId}
          task={editing}
          onClose={() => setEditing(null)}
          onSaved={refresh}
        />
      )}

      {deleting && (
        <ConfirmDialog
          title="删除任务"
          message={
            <>
              确认删除任务 <span className="code">{deleting.task_id}</span>「{deleting.title}」？
              删除后不可恢复。
            </>
          }
          confirmLabel="确认删除"
          danger
          onClose={() => setDeleting(null)}
          onConfirm={async () => {
            await deleteTask(deleting.task_id)
            refresh()
          }}
          onStale={refresh}
        />
      )}

      {review && project && (
        <TaskReviewDialog
          project={project}
          decision={review}
          count={backlog.length}
          onClose={() => setReview(null)}
          onChanged={refresh}
        />
      )}
    </section>
  )
}
