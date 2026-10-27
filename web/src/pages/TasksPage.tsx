import { useParams } from 'react-router-dom'
import { listTasks } from '../api/client'
import { itemsOf } from '../api/types'
import { useDrawer } from '../components/DrawerContext'
import {
  DrawerPlaceholder,
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { usePolling } from '../hooks/usePolling'
import { TASK_COLUMNS, formatSince, taskStatusLabel } from '../lib/format'

/** 任务看板：按状态分列的最小呈现（决策交互属于任务-15）。 */
export function TasksPage() {
  const openDrawer = useDrawer()
  const { projectId = '' } = useParams()

  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(
    (signal) => listTasks(projectId, signal),
    [projectId],
  )
  const tasks = itemsOf(data)
  const done = tasks.filter((task) => task.status === 4).length

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>任务看板</h1>
          <p className="muted">
            项目 <span className="code">{projectId}</span> · 共 {tasks.length} 个任务，已完成 {done} 个
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

      {!data && loading && <LoadingState label="正在加载任务…" />}
      {!data && !loading && error && (
        <EmptyState title="任务加载失败" hint="页面会自动重试；也可以点击上方“重试”。" />
      )}
      {data && tasks.length === 0 && (
        <EmptyState title="暂无任务" hint="该项目的任务计划尚未生成。" />
      )}

      {tasks.length > 0 && (
        <div className="board">
          {TASK_COLUMNS.map((column) => {
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
                          title: `任务 ${task.task_id}`,
                          body: (
                            <DrawerPlaceholder
                              kind="任务"
                              id={task.task_id}
                              status={taskStatusLabel(task.status)}
                            />
                          ),
                        })
                      }
                    >
                      <div className="card-head">
                        <span className="code">{task.task_id}</span>
                        <span className="muted">
                          #{task.seq_no}
                        </span>
                      </div>
                      <h3 className="board-card-title">{task.title}</h3>
                      <footer className="board-card-foot">
                        <span>
                          尝试 {task.attempt_count}/{task.max_attempts}
                        </span>
                        <span>停留 {formatSince(task.update_time)}</span>
                      </footer>
                    </article>
                  ))}
                  {columnTasks.length === 0 && <p className="board-empty">暂无</p>}
                </div>
              </section>
            )
          })}
        </div>
      )}
    </section>
  )
}
