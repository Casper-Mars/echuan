/**
 * 详情抽屉等输入区域获得焦点时，轮询不应覆盖用户正在编辑的内容。
 * 这里只做“是否处于输入态”的判定，具体是否跳过由调用方决定。
 */
export function isUserEditing(): boolean {
  if (typeof document === 'undefined') return false
  const active = document.activeElement as HTMLElement | null
  if (!active) return false
  const tag = active.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  return active.isContentEditable === true
}

/** 点击事件是否落在可交互元素内（链接/按钮/输入框），用于避免卡片重复导航。 */
export function isInteractiveTarget(target: EventTarget | null): boolean {
  if (typeof HTMLElement === 'undefined' || !(target instanceof HTMLElement)) return false
  return Boolean(target.closest('a, button, input, textarea, select'))
}
