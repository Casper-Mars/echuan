// features/board/BoardPage —— 看板主路径（`#/projects/:pid/tasks`）
//
// 规格对照：
//   design/screens/board.md      7 列固定枚举 / 列头 sticky + 计数 / done·cancelled 默认折叠 56 竖条 /
//                                in_review 列品牌描边 / 六态矩阵（default·empty·loading·error·collapsed·long-list）
//   design/flows.md F1           看板 → 点卡 → 详情路由；筛选进 URL；折叠偏好存 localStorage；15s 自动刷新
//   design/flows.md F4 M1        移动端列宽 min(86vw,320) + scroll-snap + 列头 sticky + 「第 N / 7 列」指示器
//   design/components.md         只用 ui/ 组件（BoardColumn·TaskCard·StatusPill·KindBadge·SelectField·EmptyState·SkeletonCard）
//
// 分层：本文件不直接 import Mantine 的包（红线，验收用 grep 查 features/pages/components）。
// 错误条复用 features/shell/ErrorBar（T4），列表失败时保留上次数据、不整屏转空（AC4）。
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api } from '../../api/client'
import type { Project, Task, TaskStatus } from '../../api/types'
import { useAutoRefresh } from '../../hooks/useAutoRefresh'
import { KINDS, kindLabel } from '../../lib/labels'
import { REFRESH_INTERVALS, isAutoRefreshEnabled, subscribeAutoRefresh } from '../../lib/refresh'
import { Alert, BoardColumn, Button, EmptyState, SelectField, TaskCard } from '../../ui'
import type { SelectOption } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'
import { BOARD_COLUMNS, readCollapsed, writeCollapsed } from './collapsed'
import { CreateTaskSheet } from './CreateTaskSheet'
import { useBoardFilters } from './use-board-filters'
import { useColumnCursor } from './use-column-cursor'
import './board.css'

/** 筛选项的中文文案：状态筛选与 kind 无关，所以不复用 lib/labels 的 kind 维度文案 */
const STATUS_OPTIONS: SelectOption[] = [
  { value: 'pending', label: '待领取' },
  { value: 'ready', label: '待开工' },
  { value: 'in_progress', label: '进行中' },
  { value: 'in_review', label: '待审核' },
  { value: 'done', label: '已完成' },
  { value: 'failed', label: '失败' },
  { value: 'cancelled', label: '已取消' },
]

const KIND_OPTIONS: SelectOption[] = KINDS.map((kind) => ({ value: kind, label: kindLabel(kind) }))

/** 卡片的更新时间只显示时分（design/refs 卡片元信息行末尾是 18:12 这样的时间） */
const TIME_FORMAT = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })

function timeOf(value?: string | null): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : TIME_FORMAT.format(date)
}

const EMPTY_TASKS: Task[] = []

type BoardState = 'default' | 'empty' | 'loading' | 'error'

export function BoardPage() {
  const { projectId = '' } = useParams()
  const navigate = useNavigate()
  const filters = useBoardFilters()

  const [project, setProject] = useState<Project | null>(null)
  const [collapsed, setCollapsed] = useState<TaskStatus[]>(readCollapsed)
  const [createOpen, setCreateOpen] = useState(false)

  // 15s 自动刷新：scope = 项目 + 筛选组合，任一变化即换代（清定时器、立即重拉、丢弃旧代响应）。
  const { data, error, refresh } = useAutoRefresh({
    intervalMs: REFRESH_INTERVALS.tasks,
    scope: `${projectId}|${filters.kind}|${filters.status}`,
    load: () =>
      api.listTasks({
        project_id: projectId,
        kind: filters.kind || undefined,
        status: filters.status || undefined,
        limit: 300,
      }),
  })

  // 顶栏 / 页头要项目名，但它不是看板数据源：单独取，失败也不影响看板渲染（页头回退显示 project_id）。
  useEffect(() => {
    let alive = true
    api
      .getProject(projectId)
      .then((value) => {
        if (alive) setProject(value)
      })
      .catch(() => {
        if (alive) setProject(null)
      })
    return () => {
      alive = false
    }
  }, [projectId])

  const tasks = data?.items ?? EMPTY_TASKS

  // 归桶 + 列内排序（design/screens/board.md「数据来源」：7 列固定枚举，列内 priority 降序、update_time 降序）。
  // 关键点：status 不在 7 个枚举内的任务**不进任何列**，因此也不进页头/看板条的计数——
  // 页头总数恒等于「七列计数之和」，异常数据不会造成「页头 14、7 列加起来 12」这种对不上的账。
  // 被排除的条目单独提示（不静默丢弃），正常数据下这段提示不出现。
  const { grouped, dropped } = useMemo(() => {
    const map = new Map<TaskStatus, Task[]>()
    for (const column of BOARD_COLUMNS) map.set(column, [])
    let unknown = 0
    for (const task of tasks) {
      const bucket = map.get(task.status)
      if (!bucket) {
        unknown += 1
        continue
      }
      bucket.push(task)
    }
    for (const list of map.values()) {
      list.sort((a, b) => b.priority - a.priority || b.update_time.localeCompare(a.update_time))
    }
    return { grouped: map, dropped: unknown }
  }, [tasks])

  // 已归桶条目数 = 页头总数 = 七列计数之和（AC：三者必须一致）
  const bucketed = tasks.length - dropped

  const cursor = useColumnCursor(BOARD_COLUMNS.length)

  const autoRefreshOn = useSyncExternalStore(subscribeAutoRefresh, isAutoRefreshEnabled, () => true)
  // design/refs 页头第三段是「15s 自动刷新」；开关全局关掉时如实说明（lib/refresh 的单例）
  const refreshLabel = autoRefreshOn
    ? `${Math.round(REFRESH_INTERVALS.tasks / 1000)}s 自动刷新`
    : '自动刷新已关闭'

  // 折叠切换：状态 + 偏好一起更新（写入 localStorage，刷新后保持），顺序按 7 列固定枚举归一。
  const onToggle = useCallback((status: TaskStatus, nextCollapsed: boolean) => {
    setCollapsed((prev) => {
      const result = nextCollapsed
        ? BOARD_COLUMNS.filter((column) => column === status || prev.includes(column))
        : prev.filter((column) => column !== status)
      writeCollapsed(result)
      return result
    })
  }, [])

  // 状态判定（六态里的 default / empty / loading / error 由数据推得；collapsed / long-list 由偏好与数据量表达）
  const loading = data === undefined && error === ''
  const isEmpty = data !== undefined && bucketed === 0
  const state: BoardState = error !== '' && data !== undefined ? 'error' : loading ? 'loading' : isEmpty ? 'empty' : 'default'

  const openTask = (taskId: string) => navigate(`/projects/${projectId}/tasks/${taskId}`)

  const columns = (
    <div
      className="bp-board"
      data-ui="board-scroller"
      ref={cursor.ref}
      // 桌面隐藏滚动条（对齐 design/refs），键盘仍可横向滚动：容器可聚焦 + 方向键。
      tabIndex={0}
      role="group"
      aria-label="看板列（左右方向键横向滚动）"
    >
      {BOARD_COLUMNS.map((status) => {
        const list = grouped.get(status) ?? EMPTY_TASKS
        const isCollapsed = collapsed.includes(status)
        return (
          <BoardColumn
            key={status}
            className="bp-col"
            title={status}
            status={status}
            count={list.length}
            collapsed={isCollapsed}
            accent={status === 'in_review'}
            loading={loading}
            onToggle={(next) => onToggle(status, next)}
          >
            {list.map((task, index) => (
              <TaskCard
                key={task.task_id}
                title={task.title}
                taskId={task.task_id}
                kind={task.kind}
                status={task.status}
                priority={task.priority}
                attempts={task.attempt}
                maxAttempts={task.max_attempts}
                updatedAt={timeOf(task.update_time)}
                // design/screens/board.md default 态：「P60 待审卡片高亮描边」——
                // in_review 列首卡（本列最高优先级）借 TaskCard 的 focus 变体做强调，不是「当前打开的任务」。
                focused={status === 'in_review' && index === 0}
                onClick={() => openTask(task.task_id)}
              />
            ))}
          </BoardColumn>
        )
      })}
    </div>
  )

  return (
    <div className="bp" data-ui="board" data-board-state={state}>
      <header className="bp-head" data-ui="board-head">
        <div className="bp-head__main">
          <h1 className="bp-head__title">{project?.name ?? projectId}</h1>
          <p className="bp-head__meta">
            <span className="bp-head__id">{projectId}</span>
            <span className="bp-head__sep" aria-hidden="true">
              {' · '}
            </span>
            <span className="bp-head__total" data-ui="board-total">
              {data === undefined ? '—' : `${bucketed} 个任务`}
            </span>
            <span className="bp-head__sep" aria-hidden="true">
              {' · '}
            </span>
            <span className="bp-head__refresh">{refreshLabel}</span>
          </p>
        </div>
        <div className="bp-head__actions">
          <SelectField
            id="bp-filter-kind"
            label="类型"
            data={KIND_OPTIONS}
            value={filters.kind || null}
            onChange={(value) => filters.setKind(value ?? '')}
            placeholder="全部类型"
            clearable
          />
          <SelectField
            id="bp-filter-status"
            label="状态"
            data={STATUS_OPTIONS}
            value={filters.status || null}
            onChange={(value) => filters.setStatus(value ?? '')}
            placeholder="全部状态"
            clearable
          />
          <Button onClick={() => setCreateOpen(true)}>新建任务</Button>
        </div>
      </header>

      {/* AC4：列表失败保留上次数据 + 错误条，不整屏转空。首拉失败时也没有上次数据，同样只多一条错误条。 */}
      {error === '' ? null : (
        <ErrorBar
          className="bp-error"
          title={data === undefined ? '看板加载失败' : '看板刷新失败，保留上次数据'}
          message={data === undefined ? '还没有取到任何任务，重试一次；自动刷新也会继续重试。' : '当前显示的是上一次成功取到的任务，可以继续操作。'}
          detail={`${error}（GET /v1/tasks）`}
          retryLabel="重试"
          onRetry={() => refresh()}
        />
      )}

      {dropped === 0 ? null : (
        <Alert tone="info" className="bp-notice">
          <span data-ui="board-unknown">
            {dropped} 条任务的状态不在 7 列枚举内，已从看板排除（不计入页头与列计数）
          </span>
        </Alert>
      )}

      {isEmpty && !loading ? null : (
        <div className="bp-bar" data-ui="board-bar">
          <span className="bp-bar__total">
            {BOARD_COLUMNS.length} 列 · {data === undefined ? '—' : `${bucketed} 个任务`}
            {' '}
          </span>
          <span className="bp-bar__cursor" data-ui="board-cursor">
            第 {cursor.index + 1} / {BOARD_COLUMNS.length} 列 · {BOARD_COLUMNS[cursor.index]}
          </span>
          <span className="bp-bar__hint">横向滑动查看后续列</span>
        </div>
      )}

      {isEmpty && !filters.active ? (
        <EmptyState
          className="bp-empty"
          title="这个项目还没有任务"
          description="任务卡会按状态落进 7 列。先新建一条，或把 pending 的任务推到 ready。"
          actionLabel="新建任务"
          onAction={() => setCreateOpen(true)}
        />
      ) : isEmpty ? (
        <EmptyState
          className="bp-empty"
          title="没有符合筛选条件的任务"
          description="当前筛选（类型 / 状态）在这个项目里没有命中任何任务，清掉筛选可以回到完整看板。"
          actionLabel="清除筛选"
          actionVariant="default"
          onAction={filters.clear}
        />
      ) : (
        columns
      )}

      <CreateTaskSheet
        opened={createOpen}
        projectId={projectId}
        onClose={() => setCreateOpen(false)}
        onCreated={() => {
          setCreateOpen(false)
          // 写后补拉走 hook 的 'write'：在飞则排队，本轮收口补跑 1 次，不与自动刷新叠发。
          refresh('write')
        }}
      />
    </div>
  )
}
