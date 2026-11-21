// features/task-detail/attachment-preview —— 附件 markdown 应用内预览的二级面板
//
// 规格对照：
//   design/screens/attachment-preview.md —— 断点（≥768 两栏：左列表 280 + 右正文；<768 上下堆叠，
//     列表 ≤32vh、正文吃剩余）、结构（头部角色徽标 + 标题 + 关闭 → 左列表（当前项高亮）→ 右侧正文/状态）、
//     状态矩阵六态（default / empty / loading / error / oversized / image-placeholder）与各自文案；
//   design/flows.md F2 第 3 步七条分支（404 / 413 / 超时 / 超大 / 空正文 / 渲染异常 → `<pre>` 原文 /
//     内嵌图片 → 显式占位）、第 4 步（返回详情保留滚动，二级层不动详情状态）；
//   design/components.md §ui/Alert（错误条 + 明确动作）、§ui/EmptyState（虚线容器 + 主操作）、
//     骨架（loading 用 4 条独立骨架条，不用转圈白屏，见 PREVIEW_SKELETON_LINES）、
//     §ui/MarkdownViewer（内嵌图片必显示占位）。
//
// 分层：只用 ui/ 组件；不 import Mantine（design/components.md 红线）。
// 取数（超时 / 并发去重 / 超大判定）全在 use-attachment-preview，本文件只按状态渲染。
import { Component, useRef, type ErrorInfo, type ReactNode } from 'react'
import type { AttachmentRef } from '../../api/types'
import { Alert, AttachmentRow, Button, EmptyState, MarkdownViewer } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'
import { useAttachmentPreview, type PreviewState } from './use-attachment-preview'

const ROLE_LABEL: Record<AttachmentRef['role'], string> = {
  input: '输入',
  output: '输出',
}

/** 字节 → 可读大小（oversized 提示「1.2 MB」），不引依赖 */
function formatBytes(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  if (bytes >= 1024) return `${Math.round(bytes / 1024)} KB`
  return `${bytes} B`
}

/**
 * loading 态的骨架条数（issue #89 A6）：ref 是**4 条独立骨架条**等距排列，
 * 没有卡片外壳（旧实现是 1 张 `SkeletonCard lines={5}`，带边框 + 底色，与 ref 不符）。
 * 形态见 task-detail.css 的 `.fx-preview__skeleton` 段（12 高 / 圆角 8 / 间距 12，
 * 对齐 design/mock/styles.css 的 .sk + .sk-line）。
 */
const PREVIEW_SKELETON_LINES = 4

/**
 * 渲染异常兜底（flows F2 第 3 步「渲染异常 → 降级 `<pre>` 原文」）：
 * `MarkdownViewer` 内部是 marked + DOMPurify，若解析/净化抛错，这里把原文当纯文本吐出来，
 * 而不是让二级面板整块白屏。错误边界必须是 class 组件（React 18 无 hook 版本）。
 */
class MarkdownBoundary extends Component<{ source: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // 只记录，不上报：平台无前端埋点（docs/tech §10 由壳层错误边界兜底）
    console.error('[attachment-preview] markdown 渲染失败，降级为原文', error, info.componentStack)
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <div className="fx-preview__raw" data-ui="preview-raw-fallback">
        <Alert tone="warning" title="正文渲染失败，已降级为原文">
          下面是附件的原始 markdown 文本，可以读，但不再有排版。
        </Alert>
        <pre className="fx-preview__raw-text">{this.props.source}</pre>
      </div>
    )
  }
}

export interface AttachmentPreviewProps {
  /** 详情里的全部附件（左列表数据源，切换只换正文） */
  attachments: AttachmentRef[]
  /** 当前预览的附件 */
  current: AttachmentRef
  /** 切换附件（同一面板内换正文，不关面板） */
  onSelect: (ref: AttachmentRef) => void
  /** 返回详情（二级层退出，详情滚动位置由外层恢复） */
  onBack: () => void
}

/** 正文区：六态各一套（loading / error / oversized / empty / default + image-placeholder） */
function PreviewBody({
  state,
  text,
  bytes,
  failure,
  sourceURL,
  onRetry,
  onForceLoad,
  onBack,
}: {
  state: PreviewState
  text: string
  bytes: number
  failure: ReturnType<typeof useAttachmentPreview>['failure']
  sourceURL: string
  onRetry: () => void
  onForceLoad: () => void
  onBack: () => void
}) {
  // 「在新标签打开原文」是**显式兜底动作**（design/screens/attachment-preview.md error 态原文），
  // 与「点附件应用内预览」互斥：只有用户点这个按钮才新开标签。
  const openSource = () => {
    window.open(sourceURL, '_blank', 'noopener,noreferrer')
  }

  if (state === 'loading') {
    // 4 条独立骨架条（issue #89 A6）：无卡片外壳、等距排列，对齐 design/refs 的 loading 态。
    // aria-busy 留在容器上，占位条自身 aria-hidden（读屏不朗读占位内容）。
    return (
      <div className="fx-preview__skeleton" data-ui="preview-loading" aria-busy="true">
        {Array.from({ length: PREVIEW_SKELETON_LINES }, (_, index) => (
          <span key={index} className="fx-preview__skeleton-line" aria-hidden="true" />
        ))}
      </div>
    )
  }

  if (state === 'error') {
    const status = failure?.httpStatus
    const title =
      failure?.kind === 'timeout'
        ? '加载超时（8 秒内没有响应）'
        : status === 404
          ? '附件不存在或已删除（HTTP 404）'
          : status === 413
            ? '附件过大，服务端拒绝返回（HTTP 413）'
            : status !== undefined
              ? `读取附件失败（HTTP ${status}）`
              : '网络异常，没能读取附件'
    // 可重试的失败（超时 / 网络）动作是「重试」；HTTP 错误（404 / 413）动作是兜底链接，
    // 与 design/screens/attachment-preview.md error 态（一句话 + 「在新标签打开原文」）一致。
    const retryable = failure?.kind === 'timeout' || failure?.kind === 'network'
    return (
      <div className="fx-preview__slot" data-ui="preview-error">
        {/* 与看板 / 详情 / 收件箱 / 派单队列共用 shell/ErrorBar（issue #89 B1：单行横幅 + 动作右置） */}
        <ErrorBar
          title={title}
          message={
            retryable
              ? '网络或服务端没有在 8 秒内给出响应，可以重试；也可以在新标签打开原文自行查看。'
              : '应用内没法渲染这份正文，可以用兜底链接在新标签打开原文。'
          }
          retryLabel="重试"
          onRetry={retryable ? onRetry : undefined}
          extraActions={
            <Button variant="default" size="sm" onClick={openSource}>
              在新标签打开原文
            </Button>
          }
        />
      </div>
    )
  }

  if (state === 'oversized') {
    // 动作顺序与 ref 一致：「仍然加载」在前、「在新标签打开原文」在后（两个都是显式动作）
    return (
      <div className="fx-preview__slot" data-ui="preview-oversized">
        <Alert tone="info" title={`正文较大：${formatBytes(bytes)}（超过 512 KB 自动渲染上限）`}>
          <span className="fx-preview__hint">
            自动渲染上限是为了避免大正文卡住页面；需要读的话可以显式放行，或在新标签打开原文。
          </span>
          <span className="fx-preview__actions">
            <Button size="sm" onClick={onForceLoad}>
              仍然加载
            </Button>
            <Button variant="default" size="sm" onClick={openSource}>
              在新标签打开原文
            </Button>
          </span>
        </Alert>
      </div>
    )
  }

  if (state === 'empty') {
    return (
      <div className="fx-preview__slot" data-ui="preview-empty">
        <EmptyState
          title="附件为空"
          description="这个 material 存的是空正文。回到详情确认是否选错了附件。"
          actionLabel="返回详情"
          onAction={onBack}
        />
      </div>
    )
  }

  return (
    <div className="fx-preview__markdown" data-ui="preview-markdown">
      <MarkdownBoundary source={text}>
        <MarkdownViewer source={text} />
      </MarkdownBoundary>
    </div>
  )
}

/**
 * 二级预览面板：左列表（280，≥768）+ 右正文。
 * 面板挂在详情抽屉的正文槽里（`features/task-detail/TaskDetailRoute` 负责层级与返回恢复滚动），
 * 头部（角色徽标 + 标题 + 关闭）由抽屉头部承接，见 `previewSheetTitle`。
 */
export function AttachmentPreview({ attachments, current, onSelect, onBack }: AttachmentPreviewProps) {
  const preview = useAttachmentPreview(current.material_id)
  // 焦点落点：从列表进入正文区时不要把焦点丢在列表行上（可访问性；Esc/关闭由抽屉头部承接）
  const bodyRef = useRef<HTMLDivElement | null>(null)

  return (
    <div
      className="fx-preview"
      data-ui="attachment-preview"
      data-testid="attachment-preview"
      data-state={preview.state}
      data-material-id={current.material_id}
    >
      <div className="fx-preview__list" data-loading={preview.state === 'loading' || undefined}>
        <ul className="fx-preview__items">
          {attachments.map((ref) => {
            const active = ref.material_id === current.material_id
            return (
              <li key={`${ref.role}-${ref.material_id}`} className="fx-preview__item" data-active={active || undefined}>
                <AttachmentRow
                  role={ref.role}
                  title={ref.title || ref.material_id}
                  // 右侧不再重复角色文案（角色已在徽标里）：ref 的列表行只有徽标 + 标题 + 图标
                  loading={active && preview.state === 'loading'}
                  onOpen={() => onSelect(ref)}
                />
              </li>
            )
          })}
        </ul>
      </div>
      <div className="fx-preview__body" ref={bodyRef} tabIndex={-1}>
        <PreviewBody
          state={preview.state}
          text={preview.text}
          bytes={preview.bytes}
          failure={preview.failure}
          sourceURL={preview.sourceURL}
          onRetry={preview.reload}
          onForceLoad={preview.forceLoad}
          onBack={onBack}
        />
      </div>
    </div>
  )
}

/** 抽屉头部标题：角色徽标 + 附件标题（design/screens/attachment-preview.md「结构」） */
export function previewSheetTitle(ref: AttachmentRef): ReactNode {
  return (
    <span className="fx-preview__head" data-ui="preview-head">
      <span className="fx-preview__role" data-role={ref.role}>
        {ROLE_LABEL[ref.role]}
      </span>
      <span className="fx-preview__title">{ref.title || ref.material_id}</span>
    </span>
  )
}
