// features/inbox/InboxPage —— 收件箱三区（待审核 / 待领取 / 派单队列），路由 `#/inbox`
//
// 规格对照：
//   design/screens/inbox.md   页头（说明 + 刷新）→ 三区（标题 + 计数 + 卡片网格 / 空行文案）；
//                             四态矩阵 default / empty / loading / error；卡片网格随宽度自适应；
//                             数据来源 `GET /v1/inbox`（待审核 / 待领取）+ `GET /v1/tasks?status=ready`（派单队列区）
//   design/screens/queue.md   队列区的「按项目分组」是 `#/queue` 屏的事，收件箱这里是合并进来的一区（扁平网格）
//   design/flows.md F1        点卡片进详情路由 `#/projects/:pid/tasks/:tid`；`in_review` 卡片直接进评审（同一路由）
//   design/components.md      只用 ui/ 组件（TaskCard·EmptyState·SkeletonCard·Alert·Button）+ shell/ErrorBar 骨架
//
// 分层：本文件不直接 import Mantine（红线在 ui/**），也不引 pages/** 的旧实现（旧 InboxPage 由 T11 收口）。
// 自动刷新：两路数据各用一个 useAutoRefresh（间隔见 lib/refresh 的 REFRESH_INTERVALS），语义沿用既有实现：
//   单飞、失败保留上次数据、手工刷新不排队、写后补拉走 'write'。
import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../../api/client'
import type { Task } from '../../api/types'
import { useAutoRefresh } from '../../hooks/useAutoRefresh'
import { REFRESH_INTERVALS } from '../../lib/refresh'
import { Alert, Button, EmptyState, SkeletonCard, TaskCard } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'
import { boardPath } from '../home/last-project'
import { bucketInbox, detailPath, timeOf } from './buckets'
import './inbox.css'

/** 派单队列区的数据源：与 `#/queue` 同一接口、同一 limit（design/screens/queue.md「数据来源」） */
const QUEUE_QUERY = { status: 'ready' as const, limit: 300 }

/** 无数据时的稳定空数组：避免每渲染新建引用，也避免把空态当成 loading 态 */
const EMPTY_TASKS: Task[] = []

/** 每区 loading 时的骨架卡数量（design/screens/inbox.md loading 行：三区各 3 个） */
const SECTION_SKELETONS = 3

type InboxState = 'default' | 'empty' | 'loading' | 'error'

interface SectionSpec {
  key: 'review' | 'ready' | 'queue'
  title: string
  /** 计数后面的补充说明（design/refs：`2 个 · 需要你拍板`） */
  hint: string
  /** 本区为空时的行内文案（design/refs 的三个空行文案） */
  emptyText: string
}

/** 三区的固定枚举（顺序即 design/refs 的从上到下） */
const SECTIONS: SectionSpec[] = [
  { key: 'review', title: '待审核', hint: '需要你拍板', emptyText: '没有待审核任务。' },
  { key: 'ready', title: '待领取', hint: 'worker 按 kind 拉取', emptyText: '没有待领取任务。' },
  { key: 'queue', title: '派单队列', hint: '依赖已满足，等待领取', emptyText: '队列是空的，worker 可以休息一下。' },
]

export function InboxPage() {
  const navigate = useNavigate()

  const inbox = useAutoRefresh({ intervalMs: REFRESH_INTERVALS.inbox, load: () => api.inbox() })
  const queue = useAutoRefresh({ intervalMs: REFRESH_INTERVALS.queue, load: () => api.listTasks(QUEUE_QUERY) })

  const inboxItems = inbox.data?.items ?? EMPTY_TASKS
  const queueItems = queue.data?.items ?? EMPTY_TASKS
  const { review, ready, dropped } = useMemo(() => bucketInbox(inboxItems), [inboxItems])

  // 两路各自的 loading / error：某一路先回来就先渲染那一路，三区框架始终在（design/screens/inbox.md error 行）
  const inboxLoading = inbox.data === undefined && inbox.error === ''
  const queueLoading = queue.data === undefined && queue.error === ''
  const errored = inbox.error !== '' || queue.error !== ''

  const hasData = inbox.data !== undefined || queue.data !== undefined
  const isLoading = inboxLoading || queueLoading
  // 整块空态：两路都已取回且三区全空（design/screens/inbox.md empty 行：收件箱清空 → 整块空态 + 打开看板）
  const isEmpty =
    !errored && inbox.data !== undefined && queue.data !== undefined && review.length === 0 && ready.length === 0 && queueItems.length === 0

  const state: InboxState = errored ? 'error' : isLoading ? 'loading' : isEmpty ? 'empty' : 'default'

  const counts: Record<SectionSpec['key'], Task[]> = { review, ready, queue: queueItems }
  const sectionLoading: Record<SectionSpec['key'], boolean> = {
    review: inboxLoading,
    ready: inboxLoading,
    queue: queueLoading,
  }

  const refreshAll = () => {
    inbox.refresh()
    queue.refresh()
  }

  const openTask = (task: Task) => navigate(detailPath(task))

  const errorTitle =
    inbox.error !== '' && queue.error !== ''
      ? '收件箱与派单队列加载失败'
      : inbox.error !== ''
        ? '收件箱加载失败'
        : '派单队列加载失败'

  const errorDetail = [
    inbox.error === '' ? null : `${inbox.error}（GET /v1/inbox）`,
    queue.error === '' ? null : `${queue.error}（GET /v1/tasks?status=ready）`,
  ]
    .filter((value): value is string => value !== null)
    .join('；')

  return (
    <div className="ix" data-ui="inbox" data-inbox-state={state}>
      <header className="ix-head" data-ui="inbox-head">
        <div className="ix-head__main">
          <h1 className="ix-head__title">收件箱</h1>
          <p className="ix-head__sub">待审核优先处理；待领取由 worker 自动拉取</p>
        </div>
        <div className="ix-head__actions">
          <Button variant="ghost" size="sm" onClick={refreshAll}>
            刷新
          </Button>
        </div>
      </header>

      {/* 错误条：失败保留上次数据（hook 语义），分区框架不塌；重试同时触发两路 */}
      {errored ? (
        <ErrorBar
          className="ix-error"
          title={errorTitle}
          message={
            hasData
              ? '当前显示的是上一次成功取到的任务，可以继续操作。'
              : '还没有取到任何任务，重试一次；自动刷新也会继续重试。'
          }
          detail={errorDetail}
          retryLabel="重试"
          onRetry={refreshAll}
        />
      ) : null}

      {/* 异常数据：状态不在 in_review / ready 内的条目不属于任何区，单独说明（不静默丢弃） */}
      {dropped === 0 ? null : (
        <Alert tone="info" className="ix-notice">
          <span data-ui="inbox-unknown">
            {dropped} 条任务的状态不在「待审核 / 待领取」内，已从三区排除（不计入任何区计数）
          </span>
        </Alert>
      )}

      {isEmpty ? (
        <EmptyState
          className="ix-empty"
          title="收件箱已清空"
          description="没有待审核、待领取或待派发的任务。去创建任务或等 worker 回报。"
          actionLabel="打开看板"
          onAction={() => navigate(boardPath())}
        />
      ) : (
        SECTIONS.map((spec) => {
          const tasks = counts[spec.key]
          const loading = sectionLoading[spec.key]
          return (
            <section className="ix-section" key={spec.key} data-ui={`inbox-section-${spec.key}`} data-count={tasks.length}>
              <div className="ix-section__head">
                <h2 className="ix-section__title">{spec.title}</h2>
                <span className="ix-section__count" data-ui={`inbox-count-${spec.key}`}>
                  {loading ? '— 个' : `${tasks.length} 个`}
                  {' · '}
                  {spec.hint}
                </span>
              </div>
              {loading ? (
                <div className="ix-grid ix-grid--skeleton" data-ui={`inbox-skeleton-${spec.key}`}>
                  {Array.from({ length: SECTION_SKELETONS }, (_, index) => (
                    <SkeletonCard key={index} className="ix-skeleton" lines={3} />
                  ))}
                </div>
              ) : tasks.length === 0 ? (
                <p className="ix-section__empty" data-ui={`inbox-empty-${spec.key}`}>
                  {spec.emptyText}
                </p>
              ) : (
                <div className="ix-grid" data-ui={`inbox-grid-${spec.key}`}>
                  {tasks.map((task, index) => (
                    <TaskCard
                      key={task.task_id}
                      className="ix-card"
                      title={task.title}
                      taskId={task.task_id}
                      kind={task.kind}
                      status={task.status}
                      priority={task.priority}
                      attempts={task.attempt}
                      updatedAt={timeOf(task.update_time)}
                      // design/refs default 态：待审核首卡（本区最高优先级）品牌描边强调，与看板 in_review 首卡同一口径
                      focused={spec.key === 'review' && index === 0}
                      ariaLabel={`${task.title}（${spec.title}）`}
                      onClick={() => openTask(task)}
                    />
                  ))}
                </div>
              )}
            </section>
          )
        })
      )}
    </div>
  )
}
