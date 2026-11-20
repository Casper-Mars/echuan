// ui/SkeletonCard —— 列表/看板加载占位，design/components.md §ui/SkeletonCard
//
// 状态：单一 loading 形态，支持「静态 / 多行」两种形态：
//   静态（默认）各块静态底色；lines > 1 时叠加多行骨架。
// prefers-reduced-motion: reduce 下不动画（截图与实现一致），见 ui.css 的媒体块。
// 可访问性：外层 aria-busy="true"，占位块 aria-hidden，读屏不朗读占位内容。
import { Skeleton } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cx } from './internal'

export interface SkeletonCardProps {
  /** 骨架行数（1 = 单块标题位；>1 = 多行正文骨架），默认 3 */
  lines?: number
  /** 是否显示块尾的短行（模拟文本段落最后一行） */
  tail?: boolean
  className?: string
}

export function SkeletonCard({ lines = 3, tail = true, className }: SkeletonCardProps) {
  return (
    <div className={cx('ui-skeleton-card', className)} aria-busy="true" aria-live="polite">
      {Array.from({ length: Math.max(1, lines) }, (_, index) => (
        <Skeleton
          key={index}
          className="ui-skeleton"
          height={index === 0 ? cssVar('space-4') : cssVar('space-3')}
          width={tail && index === Math.max(1, lines) - 1 ? '60%' : '100%'}
          radius="sm"
          aria-hidden="true"
        />
      ))}
    </div>
  )
}
