// features/board/use-board-filters —— 看板筛选（kind / status）落在 URL query 上
//
// 为什么进 URL（design/flows.md F1 第 5 步「筛选（kind/status）进 URL query」）：
//   * 可分享：把 `#/projects/echuan-1/tasks?kind=dev&status=in_review` 发给别人，打开就是同一份看板；
//   * 后退可用：每次切换筛选是 push（不是 replace），浏览器后退回到上一组筛选；
//   * 刷新不丢：HashRouter 下 useSearchParams 读写的是 `#` 之后的 query，刷新/深链都能还原。
//
// 脏值（手改 URL 写错的枚举）按「不筛选」处理并顺手从 URL 清掉，避免出现「筛选了一个不存在的状态」。
import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { TaskKind, TaskStatus } from '../../api/types'
import { KINDS } from '../../lib/labels'
import { BOARD_COLUMNS } from './collapsed'

export interface BoardFilters {
  /** 空串 = 不筛选（下拉显示「全部类型」） */
  kind: TaskKind | ''
  status: TaskStatus | ''
  /** 任意一项生效 */
  active: boolean
  setKind: (value: string) => void
  setStatus: (value: string) => void
  clear: () => void
}

const KIND_SET = new Set<string>(KINDS)
const STATUS_SET = new Set<string>(BOARD_COLUMNS)

function pick<T extends string>(raw: string | null, allowed: Set<string>): T | '' {
  return raw !== null && allowed.has(raw) ? (raw as T) : ''
}

export function useBoardFilters(): BoardFilters {
  const [params, setParams] = useSearchParams()

  const kind = pick<TaskKind>(params.get('kind'), KIND_SET)
  const status = pick<TaskStatus>(params.get('status'), STATUS_SET)

  const apply = useCallback(
    (patch: Record<string, string>) => {
      const next = new URLSearchParams(params)
      for (const [key, value] of Object.entries(patch)) {
        if (value === '') next.delete(key)
        else next.set(key, value)
      }
      // push（不带 replace）：切换筛选后浏览器后退回到上一组筛选（flows F1「后退可用」）。
      setParams(next)
    },
    [params, setParams],
  )

  const setKind = useCallback((value: string) => apply({ kind: value }), [apply])
  const setStatus = useCallback((value: string) => apply({ status: value }), [apply])
  const clear = useCallback(() => apply({ kind: '', status: '' }), [apply])

  return useMemo(
    () => ({ kind, status, active: kind !== '' || status !== '', setKind, setStatus, clear }),
    [kind, status, setKind, setStatus, clear],
  )
}
