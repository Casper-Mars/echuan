// ui/DetailSheet —— 任务详情与附件预览容器，design/components.md §ui/DetailSheet
//
// 底层 Drawer：桌面右侧（720，由令牌推导）；移动全屏（见 ui.css 媒体块）。
// 结构：头部（标题 + 关闭）→ 可滚动正文 → 底部固定操作条（移动端必固定）。
// 状态：rest / hover（关闭键淡底）/ focus-visible / loading（正文区 Skeleton，头部与底部操作条保持稳定）。
// 可访问性：role="dialog"、Esc 关闭、初始焦点落在头部（Mantine Drawer 提供）；关闭键有 aria-label。
import type { ReactNode } from 'react'
import { Drawer } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { SkeletonCard } from './SkeletonCard'
import { cssProps, cx } from './internal'

export interface DetailSheetProps {
  opened: boolean
  title: ReactNode
  onClose: () => void
  /** detail = 单栏；preview = 附件列表 + 正文两栏（移动折叠为上下） */
  variant?: 'detail' | 'preview'
  /** 正文加载中：正文区 Skeleton，头部与底部操作条保持稳定 */
  loading?: boolean
  /** 底部固定操作条（移动端必固定） */
  footer?: ReactNode
  className?: string
  children?: ReactNode
}

export function DetailSheet({
  opened,
  title,
  onClose,
  variant = 'detail',
  loading = false,
  footer,
  className,
  children,
}: DetailSheetProps) {
  return (
    <Drawer
      className={cx('ui-detail-sheet', `ui-detail-sheet--${variant}`, className)}
      opened={opened}
      onClose={onClose}
      position="right"
      title={title}
      overlayProps={{ backgroundOpacity: 0.6, blur: 2 }}
      closeButtonProps={{ 'aria-label': '关闭详情' }}
      styles={{
        content: cssProps({
          '--drawer-size': cssVar('ui-layout-sheet'),
          backgroundColor: cssVar('surface-canvas'),
          boxShadow: cssVar('shadow-e3'),
          borderLeft: `${cssVar('border-width')} solid ${cssVar('border-subtle')}`,
        }),
        header: cssProps({
          backgroundColor: cssVar('surface-raised'),
          borderBottom: `${cssVar('border-width')} solid ${cssVar('border-subtle')}`,
          paddingInline: cssVar('space-5'),
          paddingBlock: cssVar('space-4'),
        }),
        body: cssProps({ padding: 0, height: '100%' }),
        title: cssProps({ fontSize: cssVar('text-h3-size'), fontWeight: cssVar('text-h3-weight') }),
      }}
    >
      <div className="ui-detail-sheet__layout">
        <div className="ui-detail-sheet__body">
          {loading ? <SkeletonCard lines={5} /> : children}
        </div>
        {footer ? <footer className="ui-detail-sheet__footer">{footer}</footer> : null}
      </div>
    </Drawer>
  )
}
