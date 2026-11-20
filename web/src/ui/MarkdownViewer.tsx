// ui/MarkdownViewer —— 任务描述 / 上下文 / 附件正文，design/components.md §ui/MarkdownViewer
//
// 底层：marked + DOMPurify（沿用既有安全渲染链路）+ Mantine TypographyStylesProvider。
// 状态：rest / hover（链接下划线）/ focus-visible（链接焦点环）/ loading（5 行 Skeleton）/
//       invalid：内嵌图片必显示占位 [图片：<alt>（平台不存二进制附件，无法预览）]，禁止静默空白。
// 可访问性：正文 ≥15；表格保留表头；图片占位文本可被读屏读出。
// 验收锚点：根节点恒定带 `data-testid="markdown-viewer"`（T7 AC2 —— 点附件后同页出现该节点，
// 不新开标签/窗口；loading / empty / 正常三态都有，懒加载与骨架态同样可断言）。
import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { useMemo } from 'react'
import { TypographyStylesProvider } from '@mantine/core'
import { SkeletonCard } from './SkeletonCard'
import { cssVar } from '../theme/tokens'
import { cx } from './internal'

marked.setOptions({ gfm: true, breaks: true })

const escapeHtml = (value: string) =>
  value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

/** 平台不存二进制附件：内嵌图片一律渲染成可读占位，而不是静默空白 */
const renderImagePlaceholder = () => {
  const renderer = new marked.Renderer()
  renderer.image = (token) => {
    const alt = (token.text ?? '').trim() || '无描述'
    return `<span class="ui-markdown__image-placeholder" role="note">[图片：${escapeHtml(alt)}（平台不存二进制附件，无法预览）]</span>`
  }
  return renderer
}

export interface MarkdownViewerProps {
  source: string | null | undefined
  /** 正文加载中 */
  loading?: boolean
  /** 空正文文案 */
  emptyLabel?: string
  className?: string
}

export function MarkdownViewer({ source, loading = false, emptyLabel = '暂无内容', className }: MarkdownViewerProps) {
  const html = useMemo(() => {
    const text = (source ?? '').trim()
    if (!text) return ''
    const parsed = marked.parse(text, { async: false, renderer: renderImagePlaceholder() }) as string
    return DOMPurify.sanitize(parsed, { USE_PROFILES: { html: true }, ADD_ATTR: ['target', 'rel'] })
  }, [source])

  if (loading) {
    return (
      <div className={cx('ui-markdown', className)} data-testid="markdown-viewer" aria-busy="true">
        <SkeletonCard lines={5} />
      </div>
    )
  }

  if (!html) {
    return (
      <p className={cx('ui-markdown__empty', className)} data-testid="markdown-viewer">
        {emptyLabel}
      </p>
    )
  }

  return (
    <TypographyStylesProvider
      className={cx('ui-markdown', className)}
      data-testid="markdown-viewer"
      style={{ fontSize: cssVar('text-body-size') }}
    >
      {/* 内容已由 DOMPurify 净化，见本文件头部说明 */}
      <div dangerouslySetInnerHTML={{ __html: html }} />
    </TypographyStylesProvider>
  )
}
