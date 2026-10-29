import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ApiError, getProject, listTasks } from '../api/client'
import type { Task, TaskDecision } from '../api/types'
import { itemsOf } from '../api/types'
import { useDrawer } from '../components/DrawerContext'
import { TaskActionButtons, TaskActionDialogs } from '../components/TaskActions'
import { TaskDetailDrawer } from '../components/TaskDetailDrawer'
import {
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { usePolling } from '../hooks/usePolling'
import { useWorkerPresence } from '../hooks/useWorkerPresence'
import {
  TASK_COLUMNS,
  formatSince,
  projectStatusLabel,
} from '../lib/format'

/**
 * 任务看板：按 status 分列，卡片展示编号/标题/尝试次数/reason；
 * 按状态提供验收/返工（待验收）与重排/放弃（已失败）按钮。
 */
export function TasksPage() {
  const navigate = useNavigate()
  const openDrawer = useDrawer()
  const { productId = '', projectId = '' } = useParams()
  const [showEmpty, setShowEmpty] = useState(false)
  // 卡片会在列间移动；弹窗提升到页面层，避免决策过程中被卸载而丢失 reason。
  const [action, setAction] = useState<{ task: Task; decision: TaskDecision } | null>(null)

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
  const columns = showEmpty
    ? TASK_COLUMNS
    : TASK_COLUMNS.filter((column) => tasks.some((task) => task.status === column.status))

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
          <label className="toggle">
            <input
              type="checkbox"
              checked={showEmpty}
              onChange={(event) => setShowEmpty(event.target.checked)}
            />
            显示空列
          </label>
        </div>
      </header>

      <ErrorNotice error={error} onRetry={refresh} />

      {!data && loading && <LoadingState label="正在加载任务…" />}
      {!data && !loading && error && (
        <EmptyState title="任务加载失败" hint="页面会自动重试；也可以点击上方“重试”。" />
      )}
      {data && tasks.length === 0 && (
        <EmptyState title="暂无任务" hint="该项目的任务计划尚未生成。" />
      )}

      {tasks.length > 0 && (
        <div className="board">
          {columns.map((column) => {
            const columnTasks = tasks.filter((task) => task.status === column.status)
            return (
              <section className="board-column" key={column.status}>
                <header className="board-column-head">
                  <span>{column.label}</span>
                  <span className="count">{columnTasks.length}</span>
                </header>
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
    </section>
  )
}
