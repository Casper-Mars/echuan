import { useEffect, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

export interface ModalProps {
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  wide?: boolean
  /** 超大弹窗（长文阅读等场景），宽屏约占视口 80%，窄屏回落全宽。 */
  xlarge?: boolean
  /** 提交中禁止关闭，避免请求未完成就丢失上下文。 */
  closeDisabled?: boolean
}

/**
 * 覆盖在抽屉之上的模态弹窗（新建产品、决策、二次确认）。
 * 使用 portal 挂到 body，避免抽屉的定位/动画影响 fixed 布局。
 */
export function Modal({
  title,
  onClose,
  children,
  footer,
  wide = false,
  xlarge = false,
  closeDisabled = false,
}: ModalProps) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !closeDisabled) onClose()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [onClose, closeDisabled])

  if (typeof document === 'undefined') return null

  // xlarge 优先于 wide，二者互不影响其他弹窗的默认尺寸。
  const sizeClass = xlarge ? 'modal modal-xlarge' : wide ? 'modal modal-wide' : 'modal'

  return createPortal(
    <div className="modal-root" data-modal>
      <div
        className="modal-mask"
        onClick={closeDisabled ? undefined : onClose}
        role="presentation"
      />
      <section
        className={sizeClass}
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <header className="modal-header">
          <h2>{title}</h2>
          <button
            type="button"
            className="icon-button"
            onClick={onClose}
            aria-label="关闭弹窗"
            disabled={closeDisabled}
          >
            ×
          </button>
        </header>
        <div className="modal-body">{children}</div>
        {footer && <footer className="modal-footer">{footer}</footer>}
      </section>
    </div>,
    document.body,
  )
}
