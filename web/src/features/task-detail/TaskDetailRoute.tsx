// features/task-detail/TaskDetailRoute —— 任务详情（可分享路由 `#/projects/:pid/tasks/:tid`）
//
// 规格对照：
//   design/screens/task-detail.md —— 断点（≥1280 桌面 720 抽屉 / 768–1279 平板同抽屉 / <768 移动全屏 sheet
//     + 底部固定操作条，几何由 ui/DetailSheet + ui.css 承接）、结构（头部 → 元信息 → 租约条 → 评审区 →
//     附件 → 描述 → 上下文 → 依赖 → 驳回/失败原因 → 底部操作条）、状态矩阵六态、交互与数据来源；
//   design/flows.md F1（点卡片进详情、关闭 = URL 回看板）、F3（评审：自动带入 output / 驳回必填 /
//     非 2xx 错误条 + 重拉）、F4-3（移动端底部固定操作条、二次确认、触控 ≥44）；
//   design/screens/attachment-preview.md —— 点附件在**详情内**开二级预览面板（T7）：桌面两栏
//     （左列表 280 + 右正文），移动上下堆叠；返回详情保留滚动位置，绝不新开标签；
//   docs/tech/web-ui-redo-v0.1.md §8（并发冲突）、§10（错误边界 / 错误条由 features/shell 统一）。
//
// 分层：只 import ui / api / lib / features/shell；不 import Mantine（design/components.md 红线）。
// 路由（App.tsx）由 T4 接好，本卡不改；T5 的 features/board 只作为抽屉背后的上下文，本卡不碰。
import { useLayoutEffect, useRef, useState } from 'react'
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
import { AttachmentPreviewSurface, usePreviewSheet, usePreviewTrigger } from './attachment-preview'
import type { ReviewDecision } from './use-task-detail'
import './task-detail.css'

export default function TaskDetailRoute() {
  const { projectId = '', taskId = '' } = useParams()
  const navigate = useNavigate()
  const boardUrl = `/projects/${projectId}/tasks`
  const detail = useTaskDetail(taskId)

  // 二次确认（design/screens/task-detail.md「交互」：通过/驳回 → 二次确认 → POST /v1/tasks/decide）
  const [confirm, setConfirm] = useState<ReviewDecision | null>(null)
  // 入口占位说明（重置/删除属 T11）：本卡只展示入口，不在这里发写请求；附件预览已由 T7 真正落地
  const [notice, setNotice] = useState('')

  const task = detail.task
  const close = () => navigate(boardUrl)

  /**
   * 附件预览层（T7 / issue #128）。
   *
   * 形态：预览**不是**第二个抽屉，也不是叠在抽屉上的浮层——
   *   ≥1024：整页居中的 80vw×80vh 浮层（ui/ContentModal，底图是看板整页），
   *          预览期间详情抽屉整棵隐去（ui/DetailSheet 的 concealed）→「整个页面 + 居中」；
   *   <1024：仍是详情抽屉里的两栏面板（列表 280 + 正文，<768 上下堆叠）。
   * 容器选择在 features/task-detail/attachment-preview 的 surface 里做，本组件只持有 preview 状态。
   * 头部（角色徽标 + 标题 + 关闭）在浮层里由 ContentModal 承接、<1024 由抽屉头部承接，
   * 关闭键在预览态即「返回详情」（预览层的退出，不是关掉详情）。
   */
  const [preview, setPreview] = useState<AttachmentRef | null>(null)
  // 详情正文槽的实体（在抽屉内部；滚动位置探测要它，见 scrollSlot）
  const bodyRef = useRef<HTMLDivElement | null>(null)
  // 进预览前详情滚动槽的位置；返回详情时原值写回（flows F2 第 4 步「滚动位置保留」）
  const scrollTopRef = useRef(0)

  /**
   * 找最近的滚动祖先（详情正文槽属于 ui/DetailSheet，页面层不 import 它的 DOM 结构，只能按可滚动性探测）。
   * 必须从**抽屉内部**的节点往上找：外层容器 `.fx-detail` 在 portal 之外，往上走会摸到看板的滚动容器。
   */
  const scrollSlot = (): HTMLElement | null => {
    let node: HTMLElement | null = bodyRef.current?.parentElement ?? null
    while (node !== null) {
      const overflow = window.getComputedStyle(node).overflowY
      if (overflow === 'auto' || overflow === 'scroll') return node
      node = node.parentElement
    }
    return null
  }

  // <1024 预览时详情正文会撤下（task-detail.css 的 max-width:63.99em 块里 display:none），
  // 滚动槽的 scrollTop 会被浏览器夹到 0，所以位置在**进入**时记账、在**返回**后的第一帧写回。
  // ≥1024 时抽屉只是 visibility:hidden（布局盒与滚动槽保留），这里写回的是同一个值，无副作用。
  useLayoutEffect(() => {
    if (preview !== null) return
    const slot = scrollSlot()
    if (slot !== null && scrollTopRef.current > 0) slot.scrollTop = scrollTopRef.current
  }, [preview])

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

  // 触发行（附件行）的引用：关闭预览后焦点要还给它（方案 §11 R3）
  const trigger = usePreviewTrigger()

  const openAttachment = (ref: AttachmentRef) => {
    // 进入预览层：先记详情滚动位置与触发行，再开面板（同一 material_id 的并发请求由 use-attachment-preview 去重）
    scrollTopRef.current = scrollSlot()?.scrollTop ?? 0
    trigger.capture()
    setPreview(ref)
  }

  /** 返回详情：清掉预览层（滚动位置由上面的 layout effect 写回），焦点还给触发行 */
  const closePreview = () => {
    trigger.restore()
    setPreview(null)
  }

  const showRejectBanner = task !== null && isRejected(task)
  const footer = buildFooter()
  const detailTitle = task?.title ?? (detail.state === 'loading' ? '加载任务…' : `任务 ${taskId}`)
  // 预览态：头部换成「角色徽标 + 附件标题」；关闭键语义变成「返回详情」；
  // ≥1024 时 concealed=true 让抽屉整棵让位给 ui/ContentModal（画面里没有侧边抽屉）
  const previewSheet = usePreviewSheet(preview, detailTitle)
  const onSheetClose = preview === null ? close : closePreview

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
      data-preview={preview === null ? 'closed' : 'open'}
      data-task-id={taskId}
      data-project-id={projectId}
    >
      <DetailSheet
        opened
        title={previewSheet.title}
        onClose={onSheetClose}
        closeLabel={previewSheet.closeLabel}
        concealed={previewSheet.concealed}
        footer={preview === null ? footer : undefined}
      >
        {/* 详情正文：<1024 预览时由同断点 CSS 撤下（组件状态保留、滚动位置由 layout effect 写回）；
            ≥1024 预览时抽屉整棵 visibility:hidden（布局盒保留），正文不动 */}
        <div
          className="fx-detail__body"
          data-preview={preview === null ? 'closed' : 'open'}
          ref={bodyRef}
        >
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

        {/* 预览 surface：≥1024 渲染整页居中浮层（portal 到 body，抽屉此时已让位）；<1024 直接把面板铺在本槽里 */}
        {preview === null ? null : (
          <AttachmentPreviewSurface
            attachments={detail.attachments}
            current={preview}
            onSelect={setPreview}
            onBack={closePreview}
          />
        )}
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
