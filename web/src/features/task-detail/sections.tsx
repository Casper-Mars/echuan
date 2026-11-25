// features/task-detail/sections —— 详情的只读分区（元信息 / 租约条 / 附件 / 描述 / 上下文 / 依赖 / 原因）
//
// 分区顺序（design/screens/task-detail.md「结构」）：
//   元信息 → 租约条（有租约时）→ 评审区（仅 in_review，见 ReviewPanel）→ 附件 → 任务描述 → 任务上下文
//   → 前置依赖 → 驳回/失败原因 → 底部操作条
// `rejected` 态另有顶部「上次驳回」条（状态矩阵原文「顶部驳回原因条（原文 + 时间）」），
// 与末尾的原因分区同源，两处都在，分别对应「结构」与「状态矩阵」两条描述。
//
// 分层：只用 ui/ 组件与 lib/labels；不 import Mantine（design/components.md 红线）。
import type { ReactNode } from 'react'
import type { AttachmentRef, Task } from '../../api/types'
import { kindLabelOf } from '../../lib/flow'
import { formatTime, statusLabel } from '../../lib/labels'
import { AttachmentRow, Chip, KindBadge, MarkdownViewer, SkeletonCard, StatusPill } from '../../ui'

/** 分区头：标题 +（可选）右侧计数/说明，样式与几何全在 task-detail.css */
function SectionHead({ title, hint }: { title: string; hint?: ReactNode }) {
  return (
    <header className="fx-detail__section-head">
      <h3 className="fx-detail__section-title">{title}</h3>
      {hint ? <span className="fx-detail__section-hint">{hint}</span> : null}
    </header>
  )
}

/**
 * 附件 kind 的展示文案。
 *
 * `AttachmentRef.kind` 是**material kind** 枚举（后端 internal/domain/enum 的
 * `requirement|design|plan|report|context|other`），与任务类型（流程环节）是两套词汇：
 * 任务类型的显示名来自流程配置（`kind_label`），素材类别是固定 6 值的平台枚举，
 * 所以这里本地维护完整 6 项映射（不再借用已删除的 `kindLabel`）；未知值原样显示，避免空白。
 */
const MATERIAL_KIND_LABEL: Record<string, string> = {
  requirement: '需求',
  design: '方案',
  plan: '计划',
  report: '报告',
  context: '上下文',
  other: '其他',
}

function attachmentKindLabel(kind: string): string {
  return MATERIAL_KIND_LABEL[kind] ?? kind
}

/** 元信息：状态 · 类型 · 尝试 · 优先级（桌面一行四格，移动 2×2） */
export function MetaGrid({ task }: { task: Task }) {
  return (
    <section className="fx-detail__card fx-detail__meta" data-ui="detail-meta" aria-label="任务元信息">
      <dl className="fx-detail__meta-grid">
        <div className="fx-detail__meta-cell">
          <dt className="fx-detail__meta-label">状态</dt>
          <dd className="fx-detail__meta-value">
            <StatusPill status={task.status} label={statusLabel(task.status)} />
          </dd>
        </div>
        <div className="fx-detail__meta-cell">
          <dt className="fx-detail__meta-label">类型</dt>
          <dd className="fx-detail__meta-value">
            <KindBadge kind={task.kind} label={kindLabelOf(task)} />
          </dd>
        </div>
        <div className="fx-detail__meta-cell">
          <dt className="fx-detail__meta-label">尝试</dt>
          <dd className="fx-detail__meta-value fx-detail__mono">
            {task.attempt}/{task.max_attempts}
          </dd>
        </div>
        <div className="fx-detail__meta-cell">
          <dt className="fx-detail__meta-label">优先级</dt>
          <dd className="fx-detail__meta-value">
            <span className="fx-detail__mono">P{task.priority}</span>
          </dd>
        </div>
      </dl>
    </section>
  )
}

/** 租约条：lease_id + 到期时间 + worker_id（mono 展示，design/screens/task-detail.md leased 态） */
export function LeaseBar({ task }: { task: Task }) {
  if (!task.lease_id) return null
  return (
    <section className="fx-detail__card fx-detail__lease" data-ui="detail-lease" aria-label="租约信息">
      <span className="fx-detail__lease-dot" aria-hidden="true" />
      <span className="fx-detail__lease-label">租约中</span>
      <Chip title={task.lease_id}>{task.lease_id}</Chip>
      <span className="fx-detail__lease-time">
        到期 <span className="fx-detail__mono">{formatTime(task.lease_until)}</span>
      </span>
      {task.worker_id ? <Chip title={task.worker_id}>{task.worker_id}</Chip> : null}
    </section>
  )
}

/** 附件：输入 / 输出，点击进入预览（预览正文由 T7 落地，本卡只做入口） */
export function AttachmentSection({
  attachments,
  onOpen,
}: {
  attachments: AttachmentRef[]
  onOpen: (ref: AttachmentRef) => void
}) {
  return (
    <section className="fx-detail__section" data-ui="detail-attachments">
      <SectionHead title="附件" hint={`${attachments.length} 个 · 可在线读正文`} />
      {attachments.length === 0 ? (
        <p className="fx-detail__empty">暂无附件：评审前请先确认产出物已挂到任务上</p>
      ) : (
        <ul className="fx-detail__attachment-list">
          {attachments.map((ref) => (
            <li key={`${ref.role}-${ref.material_id}`}>
              <AttachmentRow
                role={ref.role}
                title={ref.title || ref.material_id}
                kind={attachmentKindLabel(ref.kind)}
                onOpen={() => onOpen(ref)}
              />
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/** 任务描述 / 任务上下文：markdown 渲染（内嵌图片走 ui/MarkdownViewer 的占位策略） */
export function MarkdownSection({
  ui,
  title,
  source,
  emptyLabel,
}: {
  ui: string
  title: string
  source: string
  emptyLabel: string
}) {
  return (
    <section className="fx-detail__section" data-ui={ui}>
      <SectionHead title={title} />
      <MarkdownViewer source={source} emptyLabel={emptyLabel} />
    </section>
  )
}

/** 前置依赖：只读 mono chips（design/components.md §ui/Chip：40 字符 id 不折成多行） */
export function DependencySection({ dependencies }: { dependencies: string[] }) {
  return (
    <section className="fx-detail__section" data-ui="detail-dependencies">
      <SectionHead title="前置依赖" hint={dependencies.length === 0 ? undefined : `${dependencies.length} 个`} />
      {dependencies.length === 0 ? (
        <p className="fx-detail__empty">无前置依赖：这张卡不等待其他任务</p>
      ) : (
        <ul className="fx-detail__chips">
          {dependencies.map((taskId) => (
            <li key={taskId}>
              <Chip title={taskId}>{taskId}</Chip>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/** rejected 态顶部「上次驳回」条（原文 + 时间），评审区仍可再次提交 */
export function RejectBanner({ task }: { task: Task }) {
  const reason = (task.reason ?? '').trim()
  if (reason === '') return null
  return (
    <section className="fx-detail__banner" data-ui="detail-reject-banner" role="note" aria-label="上次驳回原因">
      <span className="fx-detail__banner-label">上次驳回</span>
      <span className="fx-detail__banner-text">{reason}</span>
      <span className="fx-detail__banner-time fx-detail__mono">{formatTime(task.update_time)}</span>
    </section>
  )
}

/** 末尾「驳回/失败原因」分区：失败态给出「尝试用尽」与原因原文 */
export function ReasonSection({ task }: { task: Task }) {
  const reason = (task.reason ?? '').trim()
  if (reason === '') return null
  const failed = task.status === 'failed'
  return (
    <section className="fx-detail__section fx-detail__reason" data-ui="detail-reason">
      <SectionHead title={failed ? '失败原因' : '驳回原因'} />
      <p className="fx-detail__reason-text">{reason}</p>
      <p className="fx-detail__reason-meta">
        {failed
          ? `尝试 ${task.attempt}/${task.max_attempts} 用尽 · 最后更新 ${formatTime(task.update_time)}`
          : `驳回于 ${formatTime(task.update_time)}`}
      </p>
    </section>
  )
}

/** loading 态骨架：元信息位 + 两个正文块，头部与底部操作条保持稳定（状态矩阵 loading 原文） */
export function SkeletonBody() {
  return (
    <div className="fx-detail__skeleton" data-ui="detail-skeleton" aria-busy="true">
      <SkeletonCard lines={1} />
      <SkeletonCard lines={1} />
      <SkeletonCard lines={5} />
    </div>
  )
}
