// features/task-detail/TaskDetailRoute —— 任务详情（可分享路由 `#/projects/:pid/tasks/:tid`）
//
// 规格对照：
//   design/screens/task-detail.md —— 断点（≥1280 桌面 720 抽屉 / 768–1279 平板同抽屉 / <768 移动全屏 sheet
//     + 底部固定操作条，几何由 ui/DetailSheet + ui.css 承接）、结构（头部 → 元信息 → 租约条 → 评审区 →
//     附件 → 描述 → 上下文 → 依赖 → 驳回/失败原因 → 底部操作条）、状态矩阵六态、交互与数据来源；
//   design/flows.md F1（点卡片进详情、关闭 = URL 回看板）、F3（评审：自动带入 output / 驳回必填 /
//     非 2xx 错误条 + 重拉）、F4-3（移动端底部固定操作条、二次确认、触控 ≥44）；
//   design/screens/attachment-preview.md —— 本卡只做附件行的**入口**，正文预览属 T7；
//   docs/tech/web-ui-redo-v0.1.md §8（并发冲突）、§10（错误边界 / 错误条由 features/shell 统一）。
//
// 分层：只 import ui / api / lib / features/shell；不 import Mantine（design/components.md 红线）。
// 路由（App.tsx）由 T4 接好，本卡不改；T5 的 features/board 只作为抽屉背后的上下文，本卡不碰。
import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import type { AttachmentRef } from '../../api/types'
import { Alert, Button, ConfirmModal, DetailSheet } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'
import { ReviewPanel } from './ReviewPanel'
import {
  AttachmentSection,
  DependencySection,
  LeaseBar,
  MarkdownSection,
  MetaGrid,
  ReasonSection,
  RejectBanner,
  SkeletonBody,
} from './sections'
import { isRejected, useTaskDetail } from './use-task-detail'
import type { ReviewDecision } from './use-task-detail'
import './task-detail.css'

export default function TaskDetailRoute() {
  const { projectId = '', taskId = '' } = useParams()
  const navigate = useNavigate()
  const boardUrl = `/projects/${projectId}/tasks`
  const detail = useTaskDetail(taskId)

  // 二次确认（design/screens/task-detail.md「交互」：通过/驳回 → 二次确认 → POST /v1/tasks/decide）
  const [confirm, setConfirm] = useState<ReviewDecision | null>(null)
  // 入口占位说明（附件预览属 T7、重置/删除属 T11）：本卡只展示入口，不在这里发写请求
  const [notice, setNotice] = useState('')

  const task = detail.task
  const close = () => navigate(boardUrl)

  const askDecision = (next: ReviewDecision) => {
    if (!detail.validate(next)) return
    setConfirm(next)
  }

  const runDecision = async () => {
    const pending = confirm
    if (pending === null) return
    const ok = await detail.submit(pending)
    setConfirm(null)
    if (ok) close()
  }

  const openAttachment = (ref: AttachmentRef) => {
    setNotice(`附件预览（T7）尚未接入：${ref.title || ref.material_id}（material_id ${ref.material_id}）`)
  }

  const showRejectBanner = task !== null && isRejected(task)
  const footer = buildFooter()
  const title = task?.title ?? (detail.state === 'loading' ? '加载任务…' : `任务 ${taskId}`)

  /** 底部操作条：与滚动区分离（移动端由 ui.css 的媒体块固定），状态不同操作不同 */
  function buildFooter() {
    // 首拉中 / 首拉失败：只有「关闭 + 重试」，头部与操作条保持稳定（loading 态矩阵）
    if (task === null) {
      return (
        <div className="fx-detail__actions" data-ui="detail-actions">
          <div className="fx-detail__actions-start" data-ui="detail-close">
            <Button variant="default" size="md" onClick={close}>
              关闭
            </Button>
          </div>
          <div className="fx-detail__actions-end" data-ui="detail-retry">
            <Button size="md" loading={detail.loading} onClick={detail.reload}>
              重试
            </Button>
          </div>
        </div>
      )
    }

    if (task.status === 'failed') {
      return (
        <div className="fx-detail__actions" data-ui="detail-actions">
          <div className="fx-detail__actions-start" data-ui="detail-delete">
            <Button variant="danger" size="md" onClick={() => setNotice('「删除」为后续卡片的入口占位：本卡不改任务状态，也不调用 DELETE /v1/tasks')}>
              删除
            </Button>
          </div>
          <div className="fx-detail__actions-end" data-ui="detail-reset">
            <Button
              size="md"
              onClick={() =>
                setNotice('「重置为待处理」由 T11 落地：本卡只提供入口，不会调用 POST /v1/tasks/reset')
              }
            >
              重置为待处理
            </Button>
          </div>
        </div>
      )
    }

    if (detail.reviewing) {
      return (
        <div className="fx-detail__actions" data-ui="detail-actions">
          <div className="fx-detail__actions-start" data-ui="detail-close">
            <Button variant="default" size="md" disabled={detail.submitting} onClick={close}>
              取消
            </Button>
          </div>
          <div className="fx-detail__actions-end">
            <div data-ui="review-reject">
              <Button variant="default" size="md" loading={detail.submitting} onClick={() => askDecision('reject')}>
                驳回
              </Button>
            </div>
            <div data-ui="review-submit">
              <Button size="md" loading={detail.submitting} onClick={() => askDecision('approve')}>
                通过并关闭
              </Button>
            </div>
          </div>
        </div>
      )
    }

    return (
      <div className="fx-detail__actions" data-ui="detail-actions">
        <div className="fx-detail__actions-end" data-ui="detail-close">
          <Button variant="default" size="md" onClick={close}>
            关闭
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div
      className="fx-detail"
      data-ui="task-detail"
      data-state={detail.state}
      data-task-id={taskId}
      data-project-id={projectId}
    >
      <DetailSheet opened title={title} onClose={close} footer={footer}>
        <div className="fx-detail__body">
          {detail.state === 'loading' ? (
            <SkeletonBody />
          ) : (
            <>
              {/* 取数失败：错误条（HTTP status + 重试）；已有分区不整块消失（error 态矩阵） */}
              {detail.error === null ? null : (
                <div data-ui="load-error">
                  <ErrorBar
                    title={task === null ? '详情加载失败' : '详情刷新失败，保留已加载的分区'}
                    message={detail.error.message}
                    detail={detail.error.meta}
                    retryLabel="重试"
                    onRetry={detail.reload}
                    retrying={detail.loading}
                  />
                </div>
              )}

              {/* 评审提交失败：服务端 message（flow F3 第 3 步），并已自动重拉详情 */}
              {detail.submitError === null ? null : (
                <div data-ui="review-error">
                  <ErrorBar
                    title="评审没有提交成功"
                    message={detail.submitError.message}
                    detail={`${detail.submitError.meta} · 已自动重新拉取详情，下方是任务的最新状态`}
                    retryLabel="重试提交"
                    onRetry={() => askDecision(detail.decision)}
                    retrying={detail.submitting}
                  />
                </div>
              )}

              {notice === '' ? null : (
                <div data-ui="detail-notice">
                  <Alert tone="info" title="入口占位说明">
                    {notice}
                  </Alert>
                </div>
              )}

              {task === null ? null : (
                <>
                  <MetaGrid task={task} />
                  <LeaseBar task={task} />
                  {showRejectBanner ? <RejectBanner task={task} /> : null}
                  {detail.reviewing ? (
                    <ReviewPanel
                      decision={detail.decision}
                      onDecisionChange={detail.setDecision}
                      materialOptions={detail.materialOptions}
                      materialId={detail.materialId}
                      onMaterialChange={detail.setMaterialId}
                      reason={detail.reason}
                      onReasonChange={detail.setReason}
                      reasonError={detail.reasonError}
                      submitting={detail.submitting}
                      attachmentsLoaded={detail.attachmentsLoaded}
                    />
                  ) : null}
                  <AttachmentSection attachments={detail.attachments} onOpen={openAttachment} />
                  <MarkdownSection
                    ui="detail-description"
                    title="任务描述"
                    source={task.description_md}
                    emptyLabel="暂无任务描述"
                  />
                  <MarkdownSection
                    ui="detail-context"
                    title="任务上下文"
                    source={task.context_md}
                    emptyLabel="暂无任务上下文"
                  />
                  <DependencySection dependencies={detail.dependencies} />
                  <ReasonSection task={task} />
                </>
              )}
            </>
          )}
        </div>
      </DetailSheet>

      {/* 二次确认：影响面一句话 + 不可恢复提示；提交中遮罩不可关闭（ui/ConfirmModal loading 态） */}
      <ConfirmModal
        opened={confirm !== null}
        title={confirm === 'reject' ? '驳回这次评审？' : '通过这次评审？'}
        impact={
          confirm === 'reject'
            ? '任务会退回执行方，详情顶部留下驳回原因（原文 + 时间），评审区仍可再次提交。'
            : '通过后任务进入已完成，看板列随之变化；提交结论不可回退。'
        }
        confirmLabel={confirm === 'reject' ? '确认驳回' : '确认通过'}
        cancelLabel="取消"
        tone={confirm === 'reject' ? 'danger' : 'primary'}
        loading={detail.submitting}
        onConfirm={runDecision}
        onCancel={() => setConfirm(null)}
      />
    </div>
  )
}
