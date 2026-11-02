import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { useMemo } from 'react'

marked.setOptions({ gfm: true, breaks: true })

/**
 * 安全渲染 Markdown：marked 解析后用 DOMPurify 净化，再写入 DOM。
 * 任何用户可控正文（需求/方案/报告）都必须走这里，禁止直接 innerHTML。
 */
export function Markdown({
  source,
  empty = '暂无内容',
}: {
  source: string | null | undefined
  empty?: string
}) {
  const html = useMemo(() => {
    const text = (source ?? '').trim()
    if (!text) return ''
    const parsed = marked.parse(text, { async: false }) as string
    return DOMPurify.sanitize(parsed, {
      USE_PROFILES: { html: true },
      ADD_ATTR: ['target', 'rel'],
    })
  }, [source])

  if (!html) return <p className="muted">{empty}</p>
  // eslint-disable-next-line react/no-danger -- 内容已经过 DOMPurify 净化
  return <div className="markdown" dangerouslySetInnerHTML={{ __html: html }} />
}
