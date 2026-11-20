// features/queue/QueuePage —— 派单队列（按项目分组），路由 `#/queue`（旧书签兼容，主入口已并入收件箱）
//
// 规格对照：
//   design/screens/queue.md   页头（待派发计数 + 刷新）→ 每个项目一组（项目 id mono + 任务数 + 卡片网格）；
//                             五态矩阵 default / empty / loading / error / long-list；<768 单列
//   design/screens/inbox.md   队列同时作为收件箱第三区出现（`features/inbox`），两处共用同一接口与同一 limit
//   design/flows.md F1        点卡片进详情路由 `#/projects/:pid/tasks/:tid`
//   design/components.md      只用 ui/ 组件（TaskCard·EmptyState·SkeletonCard·Button）+ shell/ErrorBar 骨架
//
// 分层：本文件不直接 import Mantine（红线在 ui/**），也不引 pages/** 的旧实现（旧 QueuePage 由 T11 收口）。
// 入口现状：侧栏 / TabBar 的 4 个一级入口不含「派单队列」（features/shell/nav.tsx），
// 但本路由保留——直接打开 `#/queue` 仍然渲染本页，不做重定向。
import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../../api/client'
import type { Task } from '../../api/types'
import { useAutoRefresh } from '../../hooks/useAutoRefresh'
import { REFRESH_INTERVALS } from '../../lib/refresh'
import { Button, EmptyState, SkeletonCard, TaskCard } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'
import { boardPath } from '../home/last-project'
import { detailPath, groupByProject, timeOf } from './group'
import './queue.css'

/** 队列接口：只取 ready（依赖已满足、等待 worker 领取），limit 与看板一致（design/screens/queue.md「数据来源」） */
const QUEUE_QUERY = { status: 'ready' as const, limit: 300 }

/** 无数据时的稳定空数组：避免每渲染新建引用，也避免把空态当成 loading 态 */
const EMPTY_TASKS: Task[] = []

/** loading 态的骨架卡数量（design/screens/queue.md loading 行：4 个 SkeletonCard） */
const SKELETONS = 4

type QueueState = 'default' | 'empty' | 'loading' | 'error'

export function QueuePage() {
  const navigate = useNavigate()
  const { data, error, refresh } = useAutoRefresh({
    intervalMs: REFRESH_INTERVALS.queue,
    load: () => api.listTasks(QUEUE_QUERY),
  })

  const items = data?.items ?? EMPTY_TASKS
  const groups = useMemo(() => groupByProject(items), [items])

  const loading = data === undefined && error === ''
  const isEmpty = error === '' && data !== undefined && items.length === 0
  const state: QueueState = error !== '' ? 'error' : loading ? 'loading' : isEmpty ? 'empty' : 'default'

  return (
    <div className="qx" data-ui="queue" data-queue-state={state}>
      <header className="qx-head" data-ui="queue-head">
        <div className="qx-head__main">
          <h1 className="qx-head__title">派单队列</h1>
          <p className="qx-head__sub">只展示 ready：依赖已满足、等待 worker 领取</p>
        </div>
        <div className="qx-head__actions">
          {/* 待派发计数 == 队列接口返回条数（loading 时未知，显示 —） */}
          <span className="qx-head__count" data-ui="queue-count">
            {loading ? '— 待派发' : `${items.length} 待派发`}
          </span>
          <Button variant="ghost" size="sm" onClick={() => refresh()}>
            刷新
          </Button>
        </div>
      </header>

      {/* 错误条：失败保留上次数据（hook 语义），分组框架不塌 */}
      {error === '' ? null : (
        <ErrorBar
          className="qx-error"
          title={data === undefined ? '队列加载失败' : '队列刷新失败，保留上次数据'}
          message={
            data === undefined
              ? '还没有取到任何任务，重试一次；自动刷新也会继续重试。'
              : '当前显示的是上一次成功取到的任务，可以继续操作。'
          }
          detail={`${error}（GET /v1/tasks?status=ready）`}
          retryLabel="重试"
          onRetry={() => refresh()}
        />
      )}

      {loading ? (
        <div className="qx-loading" data-ui="queue-skeleton">
          {Array.from({ length: SKELETONS }, (_, index) => (
            <SkeletonCard key={index} className="qx-skeleton" lines={2} />
          ))}
        </div>
      ) : isEmpty ? (
        <EmptyState
          className="qx-empty"
          title="队列是空的"
          description="没有 ready 状态的任务。看板里把 pending 推进到 ready 后，任务会出现在这里。"
          actionLabel="打开看板"
          onAction={() => navigate(boardPath())}
        />
      ) : (
        groups.map((group) => (
          <section
            className="qx-section"
            key={group.projectId}
            data-ui="queue-group"
            data-project={group.projectId}
            data-count={group.tasks.length}
          >
            <div className="qx-section__head">
              <h2 className="qx-section__title qx-mono">{group.projectId}</h2>
              <span className="qx-section__count" data-ui="queue-group-count">
                {group.tasks.length} 个任务
              </span>
            </div>
            <div className="qx-grid" data-ui="queue-grid">
              {group.tasks.map((task) => (
                <TaskCard
                  key={task.task_id}
                  className="qx-card"
                  title={task.title}
                  taskId={task.task_id}
                  kind={task.kind}
                  status={task.status}
                  priority={task.priority}
                  attempts={task.attempt}
                  updatedAt={timeOf(task.update_time)}
                  ariaLabel={`${task.title}（${group.projectId}）`}
                  onClick={() => navigate(detailPath(task))}
                />
              ))}
            </div>
          </section>
        ))
      )}
    </div>
  )
}
