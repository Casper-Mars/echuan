// ui/ContentModal —— 内容型整页浮层，design/components.md §ui/ContentModal
//
// 用途：附件预览（≥1024）——底图是看板 / 项目列表**整页**，浮层浮在整页之上居中；
// 预览期间详情抽屉由 `ui/DetailSheet` 的 `concealed` 整棵隐去，画面里没有侧边抽屉。
//
// 底层：@mantine/core 的 `Modal` 复合件（Root / Overlay / Content / Header / Title / CloseButton / Body），
// 不引 @mantine/modals。用复合件而不是单件 Modal 的原因：几何锚点 `data-preview-surface="overlay"`
// 必须挂在**内容盒子**（Mantine 的 `.mantine-Modal-content`，section[role=dialog]）上，
// 单件 Modal 没有把任意 props 传到 content 的入口，验收脚本量到的就会是根节点或内层 div。
//
// 固定几何（不进 props，这是本需求的几何口径，见 docs/tech/attachment-preview-overlay-128-v0.2.md §5.2）：
//   宽 80vw × 高 80vh、水平垂直居中、圆角 radius.16、层级 z.dialog(300)、遮罩 0.35 无模糊；
//   尺寸写在 ui.css 的 `.ui-content-modal__content`，由 classNames 挂到 content 上。
// 状态：rest / hover（关闭键淡底）/ focus-visible（打开时焦点落关闭键，data-autofocus）/ loading（正文自管）。
// 可访问性：role="dialog" + aria-modal（Mantine 提供）、Esc 只关浮层、焦点陷阱只在浮层内循环、
//   关闭后焦点由 Mantine 的 useFocusReturn 归还给触发行。
// 反例：不承载表单与确认（读与写分家：表单走 ui/DetailSheet、确认走 ui/ConfirmModal）。
import type { ReactNode } from 'react'
import { Modal } from '@mantine/core'
import { zIndex } from '../theme/tokens'
import { cx } from './internal'

export interface ContentModalProps {
  opened: boolean
  /** 头部标题（附件预览传 `previewSheetTitle(current)`：角色徽标 + 附件标题） */
  title: ReactNode
  /** Esc 与关闭键都走它 */
  onClose: () => void
  /** 关闭键 aria-label，默认「关闭」；附件预览传「关闭附件预览」 */
  closeLabel?: string
  /** 内容型浮层默认**不**点外关闭（读正文时误点遮罩不该丢上下文） */
  closeOnClickOutside?: boolean
  /** 预留：本期预览不传 */
  footer?: ReactNode
  className?: string
  children?: ReactNode
}

export function ContentModal({
  opened,
  title,
  onClose,
  closeLabel = '关闭',
  closeOnClickOutside = false,
  footer,
  className,
  children,
}: ContentModalProps) {
  return (
    <Modal.Root
      className={cx('ui-content-modal', className)}
      opened={opened}
      onClose={onClose}
      centered
      closeOnClickOutside={closeOnClickOutside}
      zIndex={zIndex.dialog}
      // 几何类只能走 classNames.content：ModalContent 会把 className 同时传给 inner（居中容器），
      // 直接写 className 会把 80vw×80vh 套到 inner 上，浮层就贴不到视口中心。
      classNames={{ content: 'ui-content-modal__content' }}
    >
      {/* 单层遮罩 0.35 / 无模糊：抽屉已隐去，不再出现双层遮罩的合成问题（方案 §1.1） */}
      <Modal.Overlay className="ui-content-modal__overlay" backgroundOpacity={0.35} blur={0} />
      {/* data-preview-surface 是验收锚点：值 overlay 时本节点就是 80vw×80vh 的内容盒 */}
      <Modal.Content data-preview-surface="overlay">
        <Modal.Header className="ui-content-modal__header">
          <Modal.Title className="ui-content-modal__title">{title}</Modal.Title>
          <Modal.CloseButton className="ui-content-modal__close" aria-label={closeLabel} data-autofocus />
        </Modal.Header>
        <Modal.Body className="ui-content-modal__body">{children}</Modal.Body>
        {footer ? <footer className="ui-content-modal__footer">{footer}</footer> : null}
      </Modal.Content>
    </Modal.Root>
  )
}
