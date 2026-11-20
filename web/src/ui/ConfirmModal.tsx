// ui/ConfirmModal —— 破坏性动作的二次确认（删除任务、驳回评审），design/components.md §ui/ConfirmModal
//
// 底层用 @mantine/core 自带 Modal（不引 @mantine/modals）。
// 状态：rest / hover（确认按钮加深）/ focus-visible（打开时焦点落在取消键）/ disabled / loading（确认中遮罩不可关闭）。
// 可访问性：role="dialog" + aria-modal，Esc 关闭，焦点陷阱（均由 Mantine Modal 提供）。
import type { ReactNode } from 'react'
import { Modal } from '@mantine/core'
import { cssVar, zIndex } from '../theme/tokens'
import { Button } from './Button'
import { cssProps, cx } from './internal'

export interface ConfirmModalProps {
  opened: boolean
  /** 标题：后果一句话 */
  title: string
  /** 影响面：哪个对象、能不能恢复 */
  impact?: ReactNode
  children?: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  /** 确认按钮变体：destroy 用 danger，普通动作用 primary */
  tone?: 'primary' | 'danger'
  /** 提交中：确认键 loading、遮罩不可关闭 */
  loading?: boolean
  onConfirm: () => void
  onCancel: () => void
  className?: string
}

export function ConfirmModal({
  opened,
  title,
  impact,
  children,
  confirmLabel = '确认',
  cancelLabel = '取消',
  tone = 'danger',
  loading = false,
  onConfirm,
  onCancel,
  className,
}: ConfirmModalProps) {
  return (
    <Modal
      className={cx('ui-confirm-modal', className)}
      opened={opened}
      onClose={onCancel}
      title={title}
      centered
      radius="lg"
      closeOnClickOutside={!loading}
      closeOnEscape={!loading}
      withCloseButton={!loading}
      closeButtonProps={{ 'aria-label': cancelLabel }}
      overlayProps={{ backgroundOpacity: 0.6, blur: 2 }}
      zIndex={zIndex.dialog}
      styles={{
        content: cssProps({
          '--modal-border-radius': cssVar('radius-16'),
          backgroundColor: cssVar('surface-canvas'),
          boxShadow: cssVar('shadow-e3'),
          border: `${cssVar('border-width')} solid ${cssVar('border-subtle')}`,
        }),
        title: cssProps({ fontSize: cssVar('text-h3-size'), fontWeight: cssVar('text-h3-weight') }),
      }}
    >
      <div className="ui-confirm-modal__body">
        {impact ? <div className="ui-confirm-modal__impact">{impact}</div> : null}
        {children}
        <div className="ui-confirm-modal__actions">
          <Button variant="default" size="md" autoFocus={!loading} onClick={onCancel}>
            {cancelLabel}
          </Button>
          <Button
            variant={tone === 'danger' ? 'danger' : 'primary'}
            size="md"
            loading={loading}
            disabled={loading}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
