// features/board/use-board-filters —— 看板筛选（kind / status）落在 URL query 上
//
// 为什么进 URL（design/flows.md F1 第 5 步「筛选（kind/status）进 URL query」）：
//   * 可分享：把 `#/projects/echuan-1/tasks?kind=dev&status=in_review` 发给别人，打开就是同一份看板；
//   * 后退可用：每次切换筛选是 push（不是 replace），浏览器后退回到上一组筛选；
//   * 刷新不丢：HashRouter 下 useSearchParams 读写的是 `#` 之后的 query，刷新/深链都能还原。
//
// kind 的合法值来自流程配置（`GET /v1/flow` 的 stages[].name），不再写死 4 个枚举；
// 脏值（手改 URL 写错的环节名）按「不筛选」处理并顺手从 URL 清掉，避免筛出一个不存在的类型。
// 清单还没到手（首拉中或接口不可达）时放行原值：否则深链会在加载窗口里被误清空。
import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { TaskKind, TaskStatus } from '../../api/types'
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

const STATUS_SET = new Set<string>(BOARD_COLUMNS)

function pick<T extends string>(raw: string | null, allowed: Set<string>): T | '' {
  return raw !== null && allowed.has(raw) ? (raw as T) : ''
}

/**
 * @param kinds 当前流程的环节 name 清单（`GET /v1/flow`）；空数组 = 清单未知，按放行处理
 */
export function useBoardFilters(kinds: readonly string[]): BoardFilters {
  const [params, setParams] = useSearchParams()

  // kinds 每次渲染都是新数组，用拼串做依赖，避免无谓重建
  const kindKey = kinds.join('\u0000')
  const kindSet = useMemo(() => new Set(kindKey === '' ? [] : kindKey.split('\u0000')), [kindKey])

  const rawKind = params.get('kind')
  const kind: TaskKind | '' =
    rawKind === null ? '' : kindSet.size === 0 || kindSet.has(rawKind) ? rawKind : ''
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
