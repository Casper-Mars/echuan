// features/board/use-column-cursor —— 移动端「第 N / 7 列」指示器
//
// 口径（design/flows.md F4 第 1 条「顶部第 N / 7 列指示器；滚到底最后一列完整可见」）：
//   * N 是**列序号 1..7**，按 7 列固定枚举数，折叠列照样占号（不是「第 N 个展开列」）；
//   * 滚到最右必须读到 7/7：末端吸附位置可能停在倒数第二列的左边界上（折叠列只有 56 宽，
//     最后一列的起点够不到容器左边缘），所以「末端」要单独判定，不能只靠「谁最贴近左边缘」。
// 为什么不按 scrollLeft / 列宽 算：列宽是 `min(86vw, 320)` 与折叠列 56 的混合，宽度不恒定，
// 用几何量测（getBoundingClientRect）比按尺寸推更稳，且折叠/展开后自动正确。
//
// 滚动监听用 requestAnimationFrame 合并：一帧最多测一次，不随 scroll 事件（每帧可达数十次）重复布局。
import { useCallback, useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

export interface ColumnCursor {
  /** 挂在横向滚动容器上 */
  ref: RefObject<HTMLDivElement>
  /** 当前列的下标（0 起，对应 7 列固定枚举的序号；折叠列同样占号） */
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
    // 末端：一像素容差，避免亚像素把「已到底」判成「没到底」。
    // 到底时指示器必须是最后一列（7/7）——这是「滚到底最后一列完整可见」的可读版本。
    const atEnd = scroller.scrollLeft + scroller.clientWidth >= scroller.scrollWidth - 1
    if (atEnd) {
      setIndex(columns.length - 1)
      return
    }
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
