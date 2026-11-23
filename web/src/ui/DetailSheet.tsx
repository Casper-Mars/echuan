// ui/DetailSheet —— 详情/预览容器，design/components.md §ui/DetailSheet
//
// 用途：任务详情（detail）/ 附件预览（preview）/ 产品与项目只读详情（readonly：字段键值网格 + 0 请求）。
// 底层 Drawer：桌面右侧（720，由令牌推导）；移动全屏（见 ui.css 媒体块）。
// 结构：头部（标题 + 关闭）→ 可滚动正文 → 底部固定操作条（移动端必固定）。
// 状态：rest / hover（关闭键淡底）/ focus-visible / loading（正文区 Skeleton，头部与底部操作条保持稳定）。
// 可访问性：role="dialog"、Esc 关闭、初始焦点落在头部（Mantine Drawer 提供）；关闭键有 aria-label。
// 让位：`concealed` 时根节点 data-concealed + 关掉 Esc / 焦点陷阱 / 滚动锁，由 ui.css 置 visibility:hidden
// （≥1024 的附件预览交给 ui/ContentModal：画面里不留侧边抽屉，但布局盒与 scrollTop 保留）。
import type { ReactNode } from 'react'
import { Drawer } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { SkeletonCard } from './SkeletonCard'
import { cssProps, cx } from './internal'

export interface DetailSheetProps {
  opened: boolean
  title: ReactNode
  onClose: () => void
  /**
   * detail = 单栏；preview = 附件列表 + 正文两栏（移动折叠为上下）；
   * readonly = 只读详情（产品、项目的字段键值网格 + 0 请求、字段取当前行、无写动作）。
   * 三个变体共用同一容器几何（桌面右侧 720 / 移动全屏），变体只表达正文形态，
   * 见 design/components.md §ui/DetailSheet「变体」。
   */
  variant?: 'detail' | 'preview' | 'readonly'
  /** 关闭键 aria-label，默认「关闭详情」；<1024 的附件预览态传「关闭附件预览」（同一容器、不同出口） */
  closeLabel?: string
  /**
   * 让位：true → 根节点带 `data-concealed`，且 `closeOnEscape` / `trapFocus` / `lockScroll` 全关，
   * 由 ui.css 把整棵抽屉置为 `visibility: hidden`。用于 ≥1024 附件预览由 `ui/ContentModal`
   * 接管时——Esc 与焦点只归浮层，画面里没有右侧 720 抽屉（design/components.md §ui/DetailSheet）。
   */
  concealed?: boolean
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
  closeLabel = '关闭详情',
  concealed = false,
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
      // 让位时抽屉不能吃 Esc / 焦点 / 滚动锁：Mantine 每个 Modal/Drawer 实例各自在 window 上
      // 注册 keydown 与焦点陷阱，两层同时 active 会互抢（方案 §8.3）；滚动锁同开会掐掉浮层内滚轮。
      data-concealed={concealed || undefined}
      closeOnEscape={!concealed}
      trapFocus={!concealed}
      lockScroll={!concealed}
      // 遮罩口径（T6 验收返工）：桌面 720 抽屉要**保留看板上下文**（design/screens/task-detail.md「断点」），
      // 原来的 0.6 + blur 2 把背后看板压成不可读的一片，与 ref（背后列与卡片清晰可辨）相抵。
      // 改成轻暗化：0.35 无模糊——抽屉层次靠 surface.canvas 底色 + shadow.e3 + 左侧描边维持。
      overlayProps={{ backgroundOpacity: 0.35, blur: 0 }}
      closeButtonProps={{ 'aria-label': closeLabel }}
      styles={{
        content: cssProps({
          '--drawer-size': cssVar('ui-layout-sheet'),
          // 详情面板底色（issue #89 D3）：抽屉是 overlay 表面 → --surface-overlay（ref #0f141e）；
          // 原先取 --surface-canvas(#070b14)，比 ref 深一档。颜色仍只来自令牌，不写死色值。
          backgroundColor: cssVar('surface-overlay'),
          boxShadow: cssVar('shadow-e3'),
          borderLeft: `${cssVar('border-width')} solid ${cssVar('border-subtle')}`,
          // T6 修复（D5 同源几何）：Mantine 的 Drawer content 是 block 容器，
          // body 的 flex:1 不生效 → 正文按内容长高，底部操作条被顶到视口外（1440×900 实测 footer bottom 916）。
          // 这里把 content 收成定高 flex 列、body 收成可收缩的滚动槽，操作条才会始终钉在抽屉底部。
          display: 'flex',
          flexDirection: 'column',
          overflow: 'hidden',
        }),
        header: cssProps({
          backgroundColor: cssVar('surface-raised'),
          borderBottom: `${cssVar('border-width')} solid ${cssVar('border-subtle')}`,
          paddingInline: cssVar('space-5'),
          paddingBlock: cssVar('space-4'),
          flex: '0 0 auto',
        }),
        body: cssProps({ padding: 0, flex: '1 1 auto', minHeight: 0, overflow: 'hidden' }),
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
