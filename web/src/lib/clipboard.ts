/**
 * 复制文本到剪贴板。
 *
 * 优先使用异步 Clipboard API；在非安全上下文（如 `http://` 非 localhost）、
 * 旧浏览器或权限被拒绝时，回落到隐藏 textarea + `document.execCommand('copy')`。
 * 所有异常都在内部消化，调用方只关心返回的布尔值。
 */
export async function copyText(text: string): Promise<boolean> {
  // 空内容没有复制意义，也不应该触发兜底路径。
  if (!text) return false

  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 权限拒绝 / 非安全上下文：继续尝试兜底方案。
    }
  }

  if (typeof document === 'undefined' || !document.body) return false

  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '-1000px'
  area.style.left = '-1000px'
  area.style.opacity = '0'

  try {
    document.body.appendChild(area)
    // iOS 需要先聚焦再 select，否则选区可能不生效。
    area.focus()
    area.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    // 清理失败不应把成功结果变成异常。
    try {
      area.parentNode?.removeChild(area)
    } catch {
      // 忽略清理异常
    }
  }
}
