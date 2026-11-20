// features/task-detail/ReviewPanel —— 评审区（仅 in_review 态）
//
// 规格：design/screens/task-detail.md default 态「通过/驳回 + 自动带入的 output 附件 + 意见」、
// design/flows.md F3 第 2 步、design/components.md §ui/Segmented / §ui/SelectField / §ui/TextField。
//
// 三条硬口径：
//   1. 被评审附件是**下拉**，options 只来自 attachments 的 output 项（由调用方传入），默认选中第一个；
//      这里没有任何形如 material_id 的自由输入框（验收断言：input[name*="material"] 数量为 0）。
//   2. 驳回意见必填：空则拦截并就地标红（TextArea 的 invalid 态），**不发请求**；
//      真正的「提交」按钮在底部操作条上，与滚动区分开（移动端防误触）。
//   3. 提交中：整组控件禁用、按钮 loading（design/components.md §ui/Segmented loading）。
import type { SelectOption } from '../../ui'
import { Segmented, SelectField, TextArea } from '../../ui'
import type { ReviewDecision } from './use-task-detail'

export interface ReviewPanelProps {
  decision: ReviewDecision
  onDecisionChange: (decision: ReviewDecision) => void
  materialOptions: SelectOption[]
  materialId: string | null
  onMaterialChange: (materialId: string | null) => void
  reason: string
  onReasonChange: (reason: string) => void
  reasonError: string
  submitting: boolean
  attachmentsLoaded: boolean
}

export function ReviewPanel({
  decision,
  onDecisionChange,
  materialOptions,
  materialId,
  onMaterialChange,
  reason,
  onReasonChange,
  reasonError,
  submitting,
  attachmentsLoaded,
}: ReviewPanelProps) {
  const noOutput = attachmentsLoaded && materialOptions.length === 0
  return (
    <section className="fx-detail__card fx-detail__review" data-ui="review" aria-label="评审">
      <h3 className="fx-detail__section-title">评审</h3>
      <Segmented
        ariaLabel="评审结论"
        options={[
          { value: 'approve', label: '通过' },
          { value: 'reject', label: '驳回' },
        ]}
        value={decision}
        onChange={(value) => onDecisionChange(value as ReviewDecision)}
        tone={decision === 'reject' ? 'danger' : 'default'}
        disabled={submitting}
      />
      <SelectField
        id="td-review-material"
        label="被评审附件（自动带入 output）"
        required
        data={materialOptions}
        value={materialId}
        onChange={onMaterialChange}
        disabled={submitting || noOutput}
        loading={!attachmentsLoaded}
        placeholder={noOutput ? '该任务没有 output 附件' : '选择被评审的产出物'}
        description={
          noOutput
            ? '评审需要指定被评审的产出物，请先让执行方把 output 附件挂到任务上'
            : '只能从本任务的 output 附件中选择，不需要手写 material_id'
        }
      />
      <TextArea
        id="td-review-reason"
        label="评审意见（驳回必填）"
        value={reason}
        onChange={onReasonChange}
        error={reasonError}
        placeholder="写清缺口与需要改的地方"
        minRows={4}
        disabled={submitting}
      />
    </section>
  )
}
