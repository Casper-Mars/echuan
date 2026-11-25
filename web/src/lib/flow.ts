// lib/flow —— 类型（环节）文案与清单：显示名取 DTO 的 kind_label，清单取 GET /v1/flow
//
// 背景（tech-echuan-180-workflow-config-v0.5 §4 web 行 / §7.2）：流程环节表由配置驱动，
// 前端不再持有 `requirement|design|breakdown|dev` 的写死联合与中文映射：
//   * **徽标/详情显示名** = 任务 DTO 的 `kind_label`（后端按 `stage.label`，未配置则回退 name），
//     所以「只配 name 不配 label」的环节显示 name 原值，不会空白；
//   * **类型清单** = `GET /v1/flow` 的 `stages[]`（value=name，显示=label），加环节只需重启后端；
//   * 兜底 = 本地 4 个默认 name→中文映射：只在 `/v1/flow` 不可达、或旧后端窗口缺 `kind_label`
//     时用；默认流程的 name 恒为这 4 个，所以正常部署命中不到它。
import { useCallback, useEffect, useState } from 'react'
import { api } from '../api/client'
import type { Flow, FlowStage, Task } from '../api/types'

/**
 * 默认流程的 4 个环节 name → 中文显示名（§5「GET /v1/flow 失败 → 回退本地 4 个映射」）。
 *
 * 这是**兜底**不是真相：配置里改过的 label、新增的环节都以接口值为准。
 */
export const FALLBACK_KIND_LABEL: Record<string, string> = {
  requirement: '需求',
  design: '方案',
  breakdown: '拆解',
  dev: '开发',
}

/**
 * 任务徽标的显示文案：`kind_label` 优先（后端保证非空 = label 或 name），
 * 缺失/空白时回退默认 4 个映射，再退化到 name 原值——任何一档都不返回空串。
 */
export function kindLabelOf(task: Pick<Task, 'kind'> & Partial<Pick<Task, 'kind_label'>>): string {
  const given = (task.kind_label ?? '').trim()
  if (given !== '') return given
  return FALLBACK_KIND_LABEL[task.kind] ?? task.kind
}

/** 环节下拉的显示名：label 优先（后端已保证「未配置 = name」，这里再兜一次空白） */
export function stageLabel(stage: FlowStage): string {
  const given = (stage.label ?? '').trim()
  return given !== '' ? given : stage.name
}

/** `useFlow` 的返回值；`stages` 为空且 `error` 非空 = 接口不可达 */
export interface FlowState {
  stages: FlowStage[]
  loading: boolean
  /** 非空 = `GET /v1/flow` 失败；界面据此禁用「创建」并提示「无法获取类型清单」 */
  error: string
  reload: () => void
}

// 一次成功的结果在页面间共享：环节表只在后端重启时才变，进/出看板不必重拉。
// 失败**不缓存**：下次挂载（或用户点「重试」）会重新请求。
let cached: Flow | null = null
let inflight: Promise<Flow> | null = null

function loadFlow(): Promise<Flow> {
  if (cached !== null) return Promise.resolve(cached)
  if (inflight === null) {
    inflight = api.getFlow().then(
      (flow) => {
        cached = flow
        inflight = null
        return flow
      },
      (error: unknown) => {
        inflight = null
        throw error
      },
    )
  }
  return inflight
}

export function useFlow(): FlowState {
  const [stages, setStages] = useState<FlowStage[]>(() => cached?.stages ?? [])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(cached === null)
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    let alive = true
    setLoading(true)
    loadFlow().then(
      (flow) => {
        if (!alive) return
        setStages(flow.stages ?? [])
        setError('')
        setLoading(false)
      },
      (cause: unknown) => {
        if (!alive) return
        setStages([])
        setError(cause instanceof Error ? cause.message : String(cause))
        setLoading(false)
      },
    )
    return () => {
      alive = false
    }
  }, [nonce])

  const reload = useCallback(() => {
    cached = null
    setNonce((value) => value + 1)
  }, [])

  return { stages, loading, error, reload }
}
