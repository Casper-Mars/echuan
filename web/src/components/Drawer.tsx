import { useEffect, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

export function Drawer({
  title,
  onClose,
  children,
  footer,
  eyebrow = 'TASK',
}: {
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  /** 抽屉顶字；默认沿用任务详情抽屉的 `TASK`，其它详情按需覆盖。 */
  eyebrow?: string
}) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  if (typeof document === 'undefined') return null

  return createPortal(
    <div className="drawer-root">
      <div className="drawer-mask" onClick={onClose} role="presentation" />
      <aside className="drawer" role="dialog" aria-modal="true" aria-label={title}>
        <header className="drawer-header">
          <div>
            <p className="drawer-eyebrow">{eyebrow}</p>
            <h2>{title}</h2>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="关闭">×</button>
        </header>
        <div className="drawer-body">{children}</div>
        {footer && <footer className="drawer-footer">{footer}</footer>}
      </aside>
    </div>,
    document.body,
  )
}
