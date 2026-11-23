// features/task-detail/attachment-preview —— 附件 markdown 应用内预览的 surface / 面板 / 正文
//
// 规格对照：
//   design/screens/attachment-preview.md —— 断点（≥1024 整页居中浮层 80vw×80vh，底图是看板 / 项目列表整页，
//     预览期间画面里**没有**侧边抽屉；768–1023 详情内两栏面板（抽屉 720）；<768 上下堆叠，
//     列表 ≤32vh、正文吃剩余）、结构（头部角色徽标 + 标题 + 关闭 `aria-label="关闭附件预览"` →
//     左列表（当前项高亮）→ 右侧正文/状态）、状态矩阵六态与各自文案；
//   design/components.md §ui/ContentModal（几何 / 结构 / 可访问性）、§ui/DetailSheet（closeLabel / concealed）；
//   design/flows.md F2 第 3 步七条分支（404 / 413 / 超时 / 超大 / 空正文 / 渲染异常 → `<pre>` 原文 /
//     内嵌图片 → 显式占位）、第 4 步（Esc / 关闭只退浮层，回详情保留滚动，焦点回触发的附件行）；
//   docs/tech/attachment-preview-overlay-128-v0.2.md §5（组件关系 / 容器选择与断点）。
//
// 分层：只用 ui/ 组件；不 import Mantine（design/components.md 红线）。
// 取数（超时 / 并发去重 / 超大判定）全在 use-attachment-preview，本文件只按状态渲染。
//
// 结构（issue #128 拆分）：
//   useWidePreview   —— 容器判定（breakpoint.md 读令牌，不写字面量 1024）
//   usePreviewSheet  —— 两个入口共用：抽屉头部 title / concealed / closeLabel，避免两处各写一遍后漂移
//   AttachmentPreviewSurface —— 选容器 + 持有取数 hook（切容器不重新取数）
//   PreviewPane      —— 两栏网格（容器无关：浮层正文槽与抽屉正文槽都能铺）
//   PreviewBody      —— 六态正文（逻辑零改动）
//   previewSheetTitle —— 徽标 + 标题节点（浮层头部与 <1024 抽屉头部共用）
import { Component, useCallback, useRef, useSyncExternalStore, type ErrorInfo, type ReactNode } from 'react'
import type { AttachmentRef } from '../../api/types'
import { Alert, AttachmentRow, Button, ContentModal, EmptyState, MarkdownViewer } from '../../ui'
import { breakpoint } from '../../theme/tokens'
import { ErrorBar } from '../shell/ErrorBar'
import { useAttachmentPreview, type PreviewState } from './use-attachment-preview'
// 面板样式随组件走（项目详情的资料区也复用它；CSS 只挂在路由上会让复用方无样式）
import './task-detail.css'

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

/** 容器判定查询串：值取 theme/tokens.ts 的 breakpoint.md（= design/tokens.json），不写字面量 */
const WIDE_QUERY = `(min-width: ${breakpoint.md}px)`

/** ssr / 无 matchMedia 环境（jsdom 默认没有）按窄形态处理，保证首帧不出错 */
function wideMatches(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false
  return window.matchMedia(WIDE_QUERY).matches
}

function subscribeWide(onChange: () => void): () => void {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return () => {}
  const mql = window.matchMedia(WIDE_QUERY)
  mql.addEventListener('change', onChange)
  return () => mql.removeEventListener('change', onChange)
}

/**
 * 是否用「整页居中浮层」容器（≥1024）。与 features/shell/use-layout 同写法（matchMedia + useSyncExternalStore），
 * 窗口跨断点时由 change 事件驱动重渲染，不轮询、不写 resize 监听。
 */
export function useWidePreview(): boolean {
  return useSyncExternalStore(subscribeWide, wideMatches, wideMatches)
}

/**
 * loading 态的骨架条数（issue #89 A6）。
 * 形态（12 高 / 圆角 8 / 间距 12 / 无卡片外壳，替换旧的 `SkeletonCard lines={5}` 卡片壳）
 * 对齐 design/mock/styles.css 的 .sk + .sk-line，见 task-detail.css 的 `.fx-preview__skeleton` 段。
 *
 * 条数口径已裁决（T2 复查 D1）：以设计源为准 —— design/screens/attachment-preview.md 写「正文
 * 5 行 Skeleton」、design/mock/mock.js 的 loading 分支是 `length: 5`、ref PNG 像素扫描也是 5 段
 * （条高 12 / 间距 12），统一按 **5 条** 实现；早先任务卡 AC 的「恰好 4 条」作废。改条数只动这个
 * 常量，形态不变。
 */
const PREVIEW_SKELETON_LINES = 5

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
  /** 返回详情（浮层 / 面板退出，详情滚动位置由调用方的记账恢复） */
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
    // 5 条独立骨架条（issue #89 A6）：无卡片外壳、等距排列，对齐 design/refs 的 loading 态
    // （条高 12 / 间距 12，与 design/screens/attachment-preview.md「正文 5 行 Skeleton」一致）。
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
 * 两栏网格（容器无关）：左列表 280（≥768）+ 右正文。
 * 容器给谁：≥1024 铺在 `ui/ContentModal` 的正文槽里（`data-preview-surface="overlay"` 在浮层内容盒上）；
 * <1024 铺在详情抽屉正文槽里（本节点自带 `data-preview-surface="sheet"`，验收据它区分两种容器）。
 */
function PreviewPane({
  attachments,
  current,
  preview,
  wide,
  onSelect,
  onBack,
}: AttachmentPreviewProps & {
  preview: ReturnType<typeof useAttachmentPreview>
  wide: boolean
}) {
  // 焦点落点：从列表进入正文区时不要把焦点丢在列表行上（可访问性；Esc/关闭由浮层或抽屉头部承接）
  const bodyRef = useRef<HTMLDivElement | null>(null)

  return (
    <div
      className="fx-preview"
      data-ui="attachment-preview"
      data-testid="attachment-preview"
      // 同一时刻只有一个容器：浮层形态的锚点在 ui/ContentModal 的内容盒上，这里不重复挂
      data-preview-surface={wide ? undefined : 'sheet'}
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

/**
 * 记录触发预览的那个附件行，退出时把焦点还给它（方案 §11 R3）。
 *
 * 为什么不能只靠 Mantine 的 useFocusReturn：抽屉在 \`concealed\` 复位时它的焦点陷阱重新 active，
 * 会在 ~10ms 后 \`focusNode\` 头部关闭键，与 useFocusReturn 的 10ms 归还赛跑（实测被抽屉抢走）。
 * 触发行的引用在**点击时**同步取（此时 activeElement 就是被点的行），关闭后 30ms 再显式归还一次，
 * 晚于抽屉陷阱、早于验收断言的 50ms。
 */
export function usePreviewTrigger(): { capture: () => void; restore: () => void } {
  const triggerRef = useRef<HTMLElement | null>(null)
  const capture = useCallback(() => {
    triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
  }, [])
  const restore = useCallback(() => {
    const trigger = triggerRef.current
    if (trigger === null || !trigger.isConnected) return
    window.setTimeout(() => trigger.focus({ preventScroll: true }), 30)
  }, [])
  return { capture, restore }
}

/**
 * 预览 surface：**选容器 + 持有取数 hook**（方案 §5.3）。
 * 取数 hook 挂在 surface 上而不是各自的内容壳上，跨断点只换壳、不重新取数、不出现骨架。
 *   ≥1024 → `ui/ContentModal`（整页居中浮层，portal 到 body；抽屉此时由 concealed 隐去）
 *   <1024  → `PreviewPane`（铺在详情抽屉正文槽里）
 */
export function AttachmentPreviewSurface({ attachments, current, onSelect, onBack }: AttachmentPreviewProps) {
  const wide = useWidePreview()
  const preview = useAttachmentPreview(current.material_id)
  const pane = (
    <PreviewPane
      attachments={attachments}
      current={current}
      preview={preview}
      wide={wide}
      onSelect={onSelect}
      onBack={onBack}
    />
  )

  if (!wide) return pane

  return (
    <ContentModal opened title={previewSheetTitle(current)} onClose={onBack} closeLabel="关闭附件预览">
      {pane}
    </ContentModal>
  )
}

/**
 * 两个入口共用：抽屉头部的 title / concealed / closeLabel（方案 §5.2）。
 * `preview === null` 时是普通详情；预览态把头部换成附件标题，并（≥1024）把抽屉整棵让位给浮层。
 */
export function usePreviewSheet(
  preview: AttachmentRef | null,
  detailTitle: ReactNode,
): { title: ReactNode; concealed: boolean; closeLabel: string } {
  const wide = useWidePreview()
  return {
    title: preview === null ? detailTitle : previewSheetTitle(preview),
    concealed: wide && preview !== null,
    // 同一容器、不同出口：读屏不该把「关预览」听成「关详情」（design/components.md §ui/DetailSheet）
    closeLabel: preview === null ? '关闭详情' : '关闭附件预览',
  }
}

/** 头部标题：角色徽标 + 附件标题（浮层头部与 <1024 抽屉头部共用） */
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
