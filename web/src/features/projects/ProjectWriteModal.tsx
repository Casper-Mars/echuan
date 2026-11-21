// features/projects/ProjectWriteModal —— 项目列表行「关闭 / 重开」的确认弹窗（屏内私有，不进 ui/ 导出面）
//
// 规格对照：
//   design/screens/projects.md「交互」—— 关闭遇到 409（项目下仍有未完成任务）→ 二次确认
//     （`ui/ConfirmModal`：说明「还有 N 个未完成任务，确认关闭会取消它们，且重开不会恢复」），
//     取消 = 不发请求，确认 = 以 `force=true` 重发；重开不复活已取消的任务。
//   design/flows.md F5 关闭/重开项目 —— 409 二次确认标题「关闭项目需要确认」，确认键「关闭并取消 N 个任务」。
//   docs/tech/web-ui-102-interaction-fix-v0.1.md §7.5 D4（选定 A）/ §7.4 D3（入口只在行操作列）/ §9.1。
//
// 口径：
//   * **同一个弹窗切第二态**：两态都渲染同一个 `ui/ConfirmModal`（`opened` 全程为 true），
//     只换标题 / 说明 / 确认键文案，不做「先关再开」——否则会出现一次焦点丢失与遮罩闪动。
//   * 本文件只画，不发请求：请求、`force` 的选择、成功后的 `refresh('write')` 由页面持有
//     （写路径与列表刷新必须在同一处，避免两套状态）。
//   * 破坏性动作的确认键统一 `tone="danger"`（关闭两态）；重开不是破坏性动作 → `tone="primary"`。
//   * 文案里不出现服务端英文原文：原始 message 只进 modal 底部的技术原文行（方案 §9.1 detail 口径）。
import type { Project } from '../../api/types'
import { ConfirmModal } from '../../ui'

/** 关闭的两态：`confirm` = 第一次确认（发不带 force 的请求）；`force` = 409 之后的第二态 */
export type CloseStage =
  | { phase: 'confirm' }
  | {
      phase: 'force'
      /** 409 message 里解析出的未完成任务数；解析不到 → null（降级为「仍有未完成任务」） */
      unfinished: number | null
      /** 服务端 message 原文（`HTTP 409 · …`），只作技术细节，不做主文案 */
      detail: string
    }

/** 弹窗状态（null = 无弹窗）。页面持有它，确认后才发请求 */
export type WriteFlow =
  | { kind: 'close'; project: Project; stage: CloseStage }
  | { kind: 'reopen'; project: Project }

export interface ProjectWriteModalProps {
  flow: WriteFlow | null
  /** 提交中：确认键 loading、遮罩不可关闭（ui/ConfirmModal loading 态） */
  busy: boolean
  onConfirm: () => void
  onCancel: () => void
}

interface ModalText {
  title: string
  impact: string
  confirmLabel: string
  tone: 'primary' | 'danger'
  detail: string | null
}

/** 关闭第一态：此时前端**不知道**有没有未完成任务（不做预检，方案 §7.5 D4），所以不写数量 */
const CLOSE_CONFIRM: ModalText = {
  title: '关闭这个项目？',
  impact: '关闭后项目变为「已关闭」：看板仍可查看，但不能新建任务；随时可以重开。',
  confirmLabel: '关闭项目',
  tone: 'danger',
  detail: null,
}

/** 关闭第二态（409）：N 未知时的降级文案（后端改文案 / 中间层冲突，仍可 force） */
const CLOSE_FORCE_FALLBACK = '该项目还有未完成任务。确认关闭会取消它们，且重开不会恢复。'

const REOPEN_CONFIRM: ModalText = {
  title: '重开这个项目？',
  impact: '重开后项目回到「进行中」，可以继续新建任务；已取消的任务不会恢复。',
  confirmLabel: '重开项目',
  tone: 'primary',
  detail: null,
}

/** flow 为空时的占位：`opened=false`，这些文案不会出现在屏幕上 */
const HIDDEN: ModalText = { ...CLOSE_CONFIRM, title: '', impact: '' }

export function ProjectWriteModal({ flow, busy, onConfirm, onCancel }: ProjectWriteModalProps) {
  const text = textOf(flow)

  return (
    <ConfirmModal
      opened={flow !== null}
      title={text.title}
      impact={text.impact}
      confirmLabel={text.confirmLabel}
      cancelLabel="取消"
      tone={text.tone}
      loading={busy}
      onConfirm={onConfirm}
      onCancel={onCancel}
    >
      {text.detail === null ? null : <p className="pj-confirm__detail">{text.detail}</p>}
    </ConfirmModal>
  )
}

function textOf(flow: WriteFlow | null): ModalText {
  if (flow === null) return HIDDEN
  if (flow.kind === 'reopen') return REOPEN_CONFIRM
  if (flow.stage.phase === 'confirm') return CLOSE_CONFIRM

  const { unfinished, detail } = flow.stage
  return {
    // 标题按 design/flows.md F5 原文：409 二次确认「关闭项目需要确认」
    title: '关闭项目需要确认',
    impact:
      unfinished === null
        ? CLOSE_FORCE_FALLBACK
        : `该项目还有 ${unfinished} 个未完成任务。确认关闭会取消它们，且重开不会恢复。`,
    confirmLabel: unfinished === null ? '关闭并取消任务' : `关闭并取消 ${unfinished} 个任务`,
    tone: 'danger',
    detail,
  }
}
