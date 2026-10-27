import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { getProject, listTasks } from '../api/client'
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
              <span className="muted">项目 reason：{project.reason || '—'}</span>
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
                  {columnTasks.map((task) => (
                    <article
                      key={task.task_id}
                      className="board-card"
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
                      <p className={task.reason ? 'board-card-reason' : 'board-card-reason muted'} title={task.reason ?? undefined}>
                        reason：{task.reason || '—'}
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
                  ))}
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
