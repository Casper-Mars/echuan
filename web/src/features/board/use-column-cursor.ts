// features/board/use-column-cursor —— 移动端「第 N / 7 列」指示器
//
// 只看一件事：横向滚动容器里，哪一列最贴近视口起点（含容器内边距）。
// 为什么不按 scrollLeft / 列宽 算：列宽是 `min(86vw, 320)` 与折叠列 56 的混合，
// 宽度不恒定，用几何量测（getBoundingClientRect）比按尺寸推更稳，且折叠/展开后自动正确。
//
// 滚动监听用 requestAnimationFrame 合并：一帧最多测一次，不随 scroll 事件（每帧可达数十次）重复布局。
import { useCallback, useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

export interface ColumnCursor {
  /** 挂在横向滚动容器上 */
  ref: RefObject<HTMLDivElement>
  /** 最贴近视口起点的那一列的下标（0 起） */
  index: number
}

/** 列元素的选择器：features/board 给自己的列加的类名（见 BoardPage 的 className="bp-col"） */
const COLUMN_SELECTOR = '.bp-col'

export function useColumnCursor(count: number): ColumnCursor {
  const ref = useRef<HTMLDivElement | null>(null)
  const [index, setIndex] = useState(0)
  const frameRef = useRef(0)

  const measure = useCallback(() => {
    frameRef.current = 0
    const scroller = ref.current
    if (!scroller) return
    const columns = scroller.querySelectorAll<HTMLElement>(COLUMN_SELECTOR)
    if (columns.length === 0) return
    const origin = scroller.getBoundingClientRect().left
    let best = 0
    let bestDistance = Number.POSITIVE_INFINITY
    columns.forEach((column, i) => {
      const distance = Math.abs(column.getBoundingClientRect().left - origin)
      if (distance < bestDistance) {
        bestDistance = distance
        best = i
      }
    })
    setIndex(best)
  }, [])

  const schedule = useCallback(() => {
    if (frameRef.current !== 0) return
    frameRef.current = requestAnimationFrame(measure)
  }, [measure])

  useEffect(() => {
    const scroller = ref.current
    if (!scroller) return
    // 首帧与列数变化（筛选后列头宽度不变，但折叠态会变）都要重测一次。
    schedule()
    scroller.addEventListener('scroll', schedule, { passive: true })
    window.addEventListener('resize', schedule)
    return () => {
      scroller.removeEventListener('scroll', schedule)
      window.removeEventListener('resize', schedule)
      if (frameRef.current !== 0) cancelAnimationFrame(frameRef.current)
      frameRef.current = 0
    }
  }, [schedule, count])

  return { ref, index }
}
