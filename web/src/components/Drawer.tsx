import { useEffect, type ReactNode } from 'react'

export interface DrawerProps {
  open: boolean
  title: string
  onClose: () => void
  children: ReactNode
}

/**
 * 右侧详情抽屉容器：遮罩、ESC 关闭、关闭按钮。
 * 具体详情内容（含决策交互）由任务-15 填充。
 */
export function Drawer({ open, title, onClose, children }: DrawerProps) {
  useEffect(() => {
    if (!open) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      // 抽屉内打开的决策/确认弹窗优先消费 ESC，避免一次按键同时关闭两层。
      if (document.querySelector('[data-modal]')) return
      onClose()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [open, onClose])

  if (!open) return null

  return (
    <div className="drawer-root" data-drawer>
      <div className="drawer-mask" onClick={onClose} role="presentation" />
      <aside className="drawer" role="dialog" aria-modal="true" aria-label={title}>
        <header className="drawer-header">
          <h2>{title}</h2>
          <button type="button" className="icon-button" onClick={onClose} aria-label="关闭详情">
            ×
          </button>
        </header>
        <div className="drawer-body">{children}</div>
      </aside>
    </div>
  )
}
