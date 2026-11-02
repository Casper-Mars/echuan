import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ApiError, deleteWorkItem, getProject, listWorkItems } from '../api/client'
import type { WorkItem, WorkItemKind, WorkItemState } from '../api/types'
import { itemsOf } from '../api/types'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useDrawer } from '../components/DrawerContext'
import { WorkItemActionButtons, WorkItemActionDialogs } from '../components/WorkItemActions'
import { WorkItemDetailDrawer } from '../components/WorkItemDetailDrawer'
import { WorkItemFormDialog } from '../components/WorkItemFormDialog'
import { WorkItemReviewDialog } from '../components/WorkItemReviewDialog'
import {
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { usePolling } from '../hooks/usePolling'
import { useWorkerPresence } from '../hooks/useWorkerPresence'
import {
  WORK_ITEM_BOARD_COLUMNS,
  WORK_ITEM_KINDS,
  WORK_ITEM_STATE_COLUMNS,
  WORK_ITEM_TRAILING_COLUMNS,
  formatDateTime,
  formatSince,
  projectStatusLabel,
  workItemKindLabel,
} from '../lib/format'
import type { WorkItemActionRequest } from '../components/WorkItemActions'

/** 只有草稿(4)/评审(5)/待完成(6) 允许加卡。 */
const ADD_WORK_ITEM_STATUSES = new Set([4, 5, 6])

const ALL_KINDS: WorkItemKind[] = [...WORK_ITEM_KINDS]

/**
 * 工作项看板：blocked（待评审）非空时单独成列并带整批评审；
 * 待开发/开发中/待验收/已完成四列常驻；已失败/已取消仅非空显示。
 * 页头提供 kind 四类多选与 state 下拉筛选，筛选只作用于已拉取的列表，
 * 列计数随筛选结果变化。卡片显式展示 kind 徽标。
 */
export function WorkItemsPage() {
  const navigate = useNavigate()
  const openDrawer = useDrawer()
  const { productId = '', projectId = '' } = useParams()
  // 卡片会在列间移动；弹窗提升到页面层，避免决策过程中被卸载而丢失 reason。
  const [action, setAction] = useState<{ item: WorkItem; request: WorkItemActionRequest } | null>(null)
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<WorkItem | null>(null)
  const [deleting, setDeleting] = useState<WorkItem | null>(null)
  const [review, setReview] = useState<'approve' | 'reject' | null>(null)
  const [kinds, setKinds] = useState<WorkItemKind[]>(ALL_KINDS)
  const [stateFilter, setStateFilter] = useState<'all' | WorkItemState>('all')

  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(
    async (signal) => {
      const [items, project] = await Promise.all([
        listWorkItems(projectId, signal),
        getProject(projectId, signal),
      ])
      return { items, project }
    },
    [projectId],
  )

  const items = itemsOf(data?.items)
  const project = data?.project

  // 记忆里的项目（或它所属的产品）已被删除时后端返回 404：回项目列表。
  // 若产品也没了，ProjectsPage 会再因 404 回产品列表，整条回退链单向向上、不会来回震荡。
  useEffect(() => {
    if (error instanceof ApiError && error.status === 404) {
      navigate(`/products/${encodeURIComponent(productId)}/projects`, { replace: true })
    }
  }, [error, navigate, productId])

  const filteredItems = useMemo(
    () =>
      items.filter(
        (item) =>
          kinds.includes(item.kind) &&
          (stateFilter === 'all' || item.state === stateFilter),
      ),
    [items, kinds, stateFilter],
  )

  // 执行者在线状态单独轮询：worker 拉取失败不影响看板本身。
  const executorIds = useMemo(
    () => filteredItems.map((item) => item.worker_id).filter((id): id is string => id !== null),
    [filteredItems],
  )
  const { presenceOf, known: workersKnown } = useWorkerPresence(executorIds)

  const done = filteredItems.filter((item) => item.state === 'accepted').length
  const backlog = items.filter((item) => item.state === 'blocked')
  const reviewReason = project?.tasks_review_reason ?? null
  const rejectedAt = project?.tasks_review_rejected_at ?? null
  const canAddWorkItem = project ? ADD_WORK_ITEM_STATUSES.has(project.status) : false

  // 待评审列：有卡才出现；四列常驻；附加两列非空才出现。
  const columns = [
    ...WORK_ITEM_BOARD_COLUMNS.filter(
      (column) => column.state !== 'blocked' || filteredItems.some((item) => item.state === 'blocked'),
    ),
    ...WORK_ITEM_TRAILING_COLUMNS.filter((column) =>
      filteredItems.some((item) => item.state === column.state),
    ),
  ]

  const toggleKind = (kind: WorkItemKind) => {
    setKinds((current) =>
      current.includes(kind) ? current.filter((value) => value !== kind) : [...current, kind],
    )
  }

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>工作项看板</h1>
          <p className="muted">
            产品 <span className="code">{productId}</span> · 项目{' '}
            <span className="code">{projectId}</span>
            {project ? ` · ${project.title}` : ''} · 共 {filteredItems.length} 个工作项，已完成{' '}
            {done} 个
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
            disabled={!canAddWorkItem}
            title={
              canAddWorkItem
                ? undefined
                : '仅草稿、评审、待完成阶段可以添加工作项'
            }
          >
            添加工作项
          </button>
        </div>
      </header>

      <ErrorNotice error={error} onRetry={refresh} />

      <div className="filter-bar">
        <span className="filter-label muted">类型</span>
        <div className="kind-filter" role="group" aria-label="工作项类型筛选">
          <button
            type="button"
            className={kinds.length === ALL_KINDS.length ? 'filter-chip is-active' : 'filter-chip'}
            aria-pressed={kinds.length === ALL_KINDS.length}
            onClick={() => setKinds(ALL_KINDS)}
          >
            全部
          </button>
          {WORK_ITEM_KINDS.map((kind) => {
            const active = kinds.includes(kind)
            return (
              <button
                key={kind}
                type="button"
                className={`filter-chip kind-${kind}${active ? ' is-active' : ''}`}
                aria-pressed={active}
                onClick={() => toggleKind(kind)}
              >
                {workItemKindLabel(kind)}
              </button>
            )
          })}
        </div>
        <label className="filter-select">
          <span className="muted">状态</span>
          <select
            aria-label="工作项状态筛选"
            value={stateFilter}
            onChange={(event) => setStateFilter(event.target.value as 'all' | WorkItemState)}
          >
            <option value="all">全部</option>
            {WORK_ITEM_STATE_COLUMNS.map((option) => (
              <option key={option.state} value={option.state}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
      </div>

      {!data && loading && <LoadingState label="正在加载工作项…" />}
      {!data && !loading && error && (
        <EmptyState title="工作项加载失败" hint="页面会自动重试；也可以点击上方“重试”。" />
      )}

      {data && (
        <div className="board">
          {columns.map((column) => {
            const columnItems = filteredItems.filter((item) => item.state === column.state)
            const isReview = column.state === 'blocked'
            return (
              <section
                className={isReview ? 'board-column board-column-review' : 'board-column'}
                key={column.state}
              >
                <header className="board-column-head">
                  <span>{column.label}</span>
                  <span className="count">{columnItems.length}</span>
                </header>

                {isReview && (
                  <>
                    <div className="board-review-actions">
                      <button
                        type="button"
                        className="action action-primary action-compact"
                        onClick={() => setReview('approve')}
                        disabled={columnItems.length === 0}
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
                        <strong>工作项清单已驳回：</strong>
                        <span className="review-banner-reason">{reviewReason}</span>
                        <span className="review-banner-meta">
                          操作人 {project?.last_actor || '—'} · {formatDateTime(rejectedAt)}
                        </span>
                      </div>
                    )}
                  </>
                )}

                <div className="board-cards">
                  {columnItems.map((item) => {
                    // 只有「拿到过 worker 数据」且「明确查到 offline」才告警；
                    // 未命中首页且补查失败时状态未知，不能误判为离线。
                    const executorPresence = item.worker_id
                      ? presenceOf(item.worker_id)
                      : null
                    const executorOffline =
                      workersKnown && executorPresence === 'offline'
                    return (
                      <article
                        key={item.work_id}
                        className={
                          executorOffline
                            ? 'board-card board-card-executor-offline'
                            : 'board-card'
                        }
                        onClick={() =>
                          openDrawer({
                            title: `工作项 ${item.work_id} · ${item.title}`,
                            body: (
                              <WorkItemDetailDrawer workId={item.work_id} onChanged={refresh} />
                            ),
                          })
                        }
                      >
                        <div className="card-head">
                          <span className="code">{item.work_id}</span>
                          <span className="muted">#{item.seq_no}</span>
                        </div>
                        <p className="board-card-kind">
                          <span className="muted">类型：</span>
                          <span className={`badge kind-${item.kind}`}>
                            {workItemKindLabel(item.kind)}
                          </span>
                        </p>
                        <h3 className="board-card-title">{item.title}</h3>
                        {item.worker_id && (
                          <p
                            className={
                              executorOffline
                                ? 'board-card-worker offline'
                                : 'board-card-worker'
                            }
                          >
                            <span className="mono" title={item.worker_id}>
                              ⚙ {item.worker_id}
                            </span>
                            {executorOffline && (
                              <span className="worker-offline-flag">⚠ 执行者已离线</span>
                            )}
                          </p>
                        )}
                        <p
                          className={
                            item.reason ? 'board-card-reason' : 'board-card-reason muted'
                          }
                          title={item.reason ?? undefined}
                        >
                          原因：{item.reason || '—'}
                        </p>
                        <footer className="board-card-foot">
                          <span>
                            尝试 {item.attempt}/{item.max_attempts}
                          </span>
                          <span>停留 {formatSince(item.update_time)}</span>
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
                              onClick={() => setEditing(item)}
                            >
                              编辑
                            </button>
                            <button
                              type="button"
                              className="ghost ghost-mini"
                              onClick={() => setDeleting(item)}
                            >
                              删除
                            </button>
                          </div>
                        )}

                        <WorkItemActionButtons
                          item={item}
                          onAction={(request) => setAction({ item, request })}
                          className="board-card-actions"
                        />
                      </article>
                    )
                  })}
                  {columnItems.length === 0 && <p className="board-empty">暂无</p>}
                </div>
              </section>
            )
          })}
        </div>
      )}

      {action && (
        <WorkItemActionDialogs
          item={action.item}
          request={action.request}
          onClose={() => setAction(null)}
          onChanged={refresh}
        />
      )}

      {creating && (
        <WorkItemFormDialog
          mode="create"
          projectId={projectId}
          onClose={() => setCreating(false)}
          onSaved={refresh}
        />
      )}

      {editing && (
        <WorkItemFormDialog
          mode="edit"
          projectId={projectId}
          item={editing}
          onClose={() => setEditing(null)}
          onSaved={refresh}
        />
      )}

      {deleting && (
        <ConfirmDialog
          title="删除工作项"
          message={
            <>
              确认删除工作项 <span className="code">{deleting.work_id}</span>「{deleting.title}」？
              删除后不可恢复。
            </>
          }
          confirmLabel="确认删除"
          danger
          onClose={() => setDeleting(null)}
          onConfirm={async () => {
            await deleteWorkItem(deleting.work_id)
            refresh()
          }}
          onStale={refresh}
        />
      )}

      {review && project && (
        <WorkItemReviewDialog
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
