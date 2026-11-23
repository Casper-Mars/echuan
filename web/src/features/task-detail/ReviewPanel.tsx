// features/task-detail/ReviewPanel —— 评审区（仅 in_review 态）
//
// 规格：design/screens/task-detail.md「评审区口径」——只有「评审」标题 + 驳回原因框
// （`ui/TextArea`，label「驳回原因（驳回必填）」），**不放结论开关、不放附件选择控件**；
// 结论由底部两个命令按钮承载，被评审附件由系统写入当前正文输出（不可手选）。
//
// 三条硬口径：
//   1. 评审区不再有任何 select / 下拉：被评审附件由 `useTaskDetail` 的 `reviewTargetId` 决定，
//      页面层不可指定（验收断言：`[data-ui="review"] select` 与 `input[name*="material"]` 数量均为 0）。
//   2. 驳回原因必填：空则拦截并就地标红（TextArea 的 invalid 态），**不发请求**；
//      真正的「提交」按钮在底部操作条上，与滚动区分开（移动端防误触）。
//   3. 提示行最多一行、互斥，由状态选一条（技术方案 v0.5 §5.3）：未收口 → 读取中；收口但请求失败 →
//      记录为「未指定附件」；收口且无正文输出 → 同上出口；正常 → 无提示。
import { TextArea } from '../../ui'

export interface ReviewPanelProps {
  reason: string
  onReasonChange: (reason: string) => void
  reasonError: string
  submitting: boolean
  /** 附件请求是否已收口（成功或失败） */
  attachmentsLoaded: boolean
  /** 收口但请求失败：提示降级到「未指定附件」，不阻塞提交 */
  attachmentsFailed: boolean
  /** 本次结论有无当前正文输出可写（`reviewTargetId !== null`） */
  hasReviewTarget: boolean
}

/** 提示行（§5.3 四条互斥状态，最多一行） */
export function reviewHint({
  attachmentsLoaded,
  attachmentsFailed,
  hasReviewTarget,
}: Pick<ReviewPanelProps, 'attachmentsLoaded' | 'attachmentsFailed' | 'hasReviewTarget'>): string | null {
  if (!attachmentsLoaded) return '正在读取本任务的产出物…'
  if (attachmentsFailed) return '产出物读取失败：本次结论会记录为「未指定附件」，可先重试。'
  if (!hasReviewTarget) return '本任务还没有产出物，本次结论会记录为「未指定附件」。'
  return null
}

export function ReviewPanel({
  reason,
  onReasonChange,
  reasonError,
  submitting,
  attachmentsLoaded,
  attachmentsFailed,
  hasReviewTarget,
}: ReviewPanelProps) {
  const hint = reviewHint({ attachmentsLoaded, attachmentsFailed, hasReviewTarget })
  return (
    <section className="fx-detail__card fx-detail__review" data-ui="review" aria-label="评审">
      <h3 className="fx-detail__section-title">评审</h3>
      <TextArea
        id="td-review-reason"
        label="驳回原因（驳回必填）"
        value={reason}
        onChange={onReasonChange}
        error={reasonError}
        placeholder="写清缺口与需要改的地方"
        minRows={4}
        disabled={submitting}
      />
      {hint === null ? null : <p className="fx-detail__section-hint">{hint}</p>}
    </section>
  )
}
