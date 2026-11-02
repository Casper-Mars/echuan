/**
 * 需求 / 方案文档上传的共享规则。
 *
 * 上传协议仍是 JSON（requirement_md / body_md），这里只负责前端选文件时的
 * 即时校验和展示。
 */

/** 允许的 Markdown 扩展名（小写比较）。 */
export const MD_EXTENSIONS = ['.md', '.markdown'] as const

/**
 * 单份文档大小上限。
 *
 * 与后端 material_max_bytes 的默认值（20 MiB）保持一致；前端没有配置接口，
 * 因此使用常量，超限时在后端 413 too_large 之前给出即时提示。
 */
export const MATERIAL_MAX_BYTES = 20 * 1024 * 1024

export type FileValidation = { ok: true } | { ok: false; message: string }

/** 扩展名判断不区分大小写（.MD / .Markdown 也接受）。 */
export function isMarkdownFilename(name: string): boolean {
  const lower = name.trim().toLowerCase()
  return MD_EXTENSIONS.some((extension) => lower.endsWith(extension))
}

/** 选择文件时的即时校验：类型与大小。 */
export function validateMarkdownFile(file: File): FileValidation {
  if (!isMarkdownFilename(file.name)) {
    return { ok: false, message: '仅支持 .md / .markdown 文件' }
  }
  if (file.size > MATERIAL_MAX_BYTES) {
    return { ok: false, message: '文件大小超过 20 MiB 上限' }
  }
  return { ok: true }
}

/** 去掉最后一个扩展名：a/b.md → a/b；无扩展名时原样返回。 */
export function stripExtension(name: string): string {
  const trimmed = name.trim()
  const slash = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'))
  const dot = trimmed.lastIndexOf('.')
  return dot > slash + 1 ? trimmed.slice(0, dot) : trimmed
}

/** 人类可读的大小，与列表中的格式保持一致。 */
export function formatBytes(size: number): string {
  if (!Number.isFinite(size) || size < 0) return '—'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

/** 预览正文的前 N 个字符，避免把整个 20 MiB 文件塞进 DOM。 */
export const PREVIEW_CHARS = 4000

export function previewText(body: string): string {
  return body.length > PREVIEW_CHARS ? body.slice(0, PREVIEW_CHARS) : body
}
