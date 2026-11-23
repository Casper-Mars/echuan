// features/task-detail/use-task-detail —— 详情取数、六态派生与评审提交
//
// 规格对照：
//   design/screens/task-detail.md「状态矩阵」六态（default / loading / error / leased / failed / rejected）、
//     「数据来源」三个 GET + POST /v1/tasks/decide；
//   design/flows.md F3 第 2–3 步（output 自动带入 / 驳回必填 / 非 2xx 显示服务端 message + 立即重拉）；
//   docs/tech/web-ui-redo-v0.1.md §8 异常路径（评审并发冲突 → 错误条 + 重拉，界面不转空）。
//
// 三条硬口径（本文件是唯一实现处，页面只渲染）：
//   1. 三个取数独立收口：`GET /v1/tasks?id=` 是详情主体，失败即错误态但**保留上一次数据**；
//      依赖 / 附件任一失败只加一条错误条，已渲染的分区不整块消失（错误态矩阵原文）。
//   2. 被评审附件**不可手选**：由 `reviewTargetId(rawAttachments)` 取当前正文输出写入 `material_id`
//      （技术方案 v0.5 §5.2 R-h、§8.1 第 2 步）；界面不提供任何自由输入口，也不提供下拉。
//   3. 提交非 2xx：错误条用**服务端 message**（api/client 已从 body.message 取），并立即重拉详情。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from '../../api/client'
import type { AttachmentRef, Task } from '../../api/types'
import { currentTaskVersions, reviewTargetId } from '../../lib/current-versions'
import { publishListWrite } from '../../lib/refresh'

/** 六态（design/screens/task-detail.md 状态矩阵） */
export type DetailState = 'default' | 'loading' | 'error' | 'leased' | 'failed' | 'rejected'

export type ReviewDecision = 'approve' | 'reject'

/**
 * `decided_by` 沿用 `VITE_ACTOR`（design/screens/task-detail.md「数据来源」）。
 * 未配置时回退 `web`，与旧详情抽屉的历史行为一致，不新增环境变量。
 */
const ACTOR = (import.meta.env.VITE_ACTOR as string | undefined)?.trim() || 'web'

export interface DetailError {
  /** 服务端 message（api/client 的 ApiError.message 即 body.message），没有时退化成可读短句 */
  message: string
  /** 技术细节：`HTTP <status> · <METHOD> <endpoint>`，错误条弱化展示 */
  meta: string
}

/** 错误 → 错误条两段文案（HTTP status 与端点都保留，便于用户/值班定位） */
export function toDetailError(err: unknown, endpoint: string, method = 'GET'): DetailError {
  const status = err instanceof ApiError ? `HTTP ${err.status} · ` : ''
  const text = err instanceof ApiError || err instanceof Error ? err.message : String(err)
  return { message: text, meta: `${status}${method} ${endpoint}` }
}

/**
 * 六态判定（纯函数，便于按状态矩阵逐条对照）：
 *   loading  无 task 且无错误（首次/换任务时的骨架）
 *   error    有错误（有 task 时是「保留分区 + 错误条」）
 *   failed   status=failed（末尾失败原因 + 重置/删除入口）
 *   rejected 非 failed/done/cancelled 且带 reason（顶部驳回原因条，评审区仍可再提交）
 *   leased   带 lease_id（租约条）
 *   default  其余
 * 优先级：error > failed > rejected > leased > default —— 失败是终态，驳回原因比租约更需要被看见。
 */
export function detailStateOf(task: Task | null, error: DetailError | null): DetailState {
  if (task === null) return error === null ? 'loading' : 'error'
  if (error !== null) return 'error'
  if (task.status === 'failed') return 'failed'
  if (isRejected(task)) return 'rejected'
  if (task.lease_id) return 'leased'
  return 'default'
}

/** 驳回只能发生在「还可能被再次评审」的状态上（done/cancelled 的 reason 不当作驳回条展示） */
export function isRejected(task: Task): boolean {
  if (task.status === 'failed' || task.status === 'done' || task.status === 'cancelled') return false
  return (task.reason ?? '').trim() !== ''
}

export interface TaskDetailApi {
  task: Task | null
  dependencies: string[]
  /** **可见**附件（当前版本投影）：附件区计数与预览左列表同源同一数组（AC1/AC4/AC6） */
  attachments: AttachmentRef[]
  /** 附件请求是否已收口（成功或失败）：结论按钮的禁用与评审区提示行据此判断，不靠「列表非空」猜 */
  attachmentsLoaded: boolean
  /** 附件请求是否失败：收口但失败时提示「产出物读取失败…」（§5.3） */
  attachmentsFailed: boolean
  /** 本次结论要记录的被评审附件（当前正文输出）；null = 本任务没有正文输出，提交时不带 material_id（R-h） */
  reviewTargetId: string | null
  state: DetailState
  loading: boolean
  error: DetailError | null
  reload: () => void
  reviewing: boolean
  /** 上一次点下的结论按钮（只用来支撑「重试提交」；未点过 = null，按钮禁用） */
  lastDecision: ReviewDecision | null
  reason: string
  setReason: (reason: string) => void
  reasonError: string
  submitting: boolean
  submitError: DetailError | null
  /** 提交前置校验：true = 可以弹二次确认（驳回未填意见时返回 false 并就地标红，不发请求） */
  validate: (decision: ReviewDecision) => boolean
  /** 真正提交：true = 成功（调用方负责关闭抽屉回看板） */
  submit: (decision: ReviewDecision) => Promise<boolean>
}

export function useTaskDetail(taskId: string): TaskDetailApi {
  const [task, setTask] = useState<Task | null>(null)
  const [dependencies, setDependencies] = useState<string[]>([])
  // 原始附件（接口顺序原样）；对外只暴露投影后的可见集合，二者同帧同源
  const [rawAttachments, setRawAttachments] = useState<AttachmentRef[]>([])
  const [attachmentsLoaded, setAttachmentsLoaded] = useState(false)
  const [attachmentsFailed, setAttachmentsFailed] = useState(false)
  const [error, setError] = useState<DetailError | null>(null)
  const [loading, setLoading] = useState(true)

  // 评审草稿（不持久化：flows F3 第 5 步「刷新即丢」，避免误提交旧文本）
  const [lastDecision, setLastDecision] = useState<ReviewDecision | null>(null)
  const [reason, setReason] = useState('')
  const [reasonError, setReasonError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<DetailError | null>(null)
  // 提交门闩用 ref：同一 tick 双击时 state 还没落地（React 批处理），只有 ref 能同步读到
  const submittingRef = useRef(false)
  // 换代号：换任务 / 卸载 / 手动重拉都会 +1，旧响应整笔丢弃（避免慢响应覆盖新数据）
  const genRef = useRef(0)

  const load = useCallback(async () => {
    genRef.current += 1
    const gen = genRef.current

    let next: Task
    try {
      next = await api.getTask(taskId)
    } catch (err) {
      if (gen !== genRef.current) return
      // 重拉失败：**不清空**已有的 task/deps/attachments（错误态矩阵「已有分区不整块消失」）
      setError(toDetailError(err, `/v1/tasks?id=${taskId}`))
      setLoading(false)
      return
    }
    if (gen !== genRef.current) return
    setTask(next)
    setError(null)

    // 依赖与附件是分区数据：各自收口，任一失败只补一条错误条，不拖垮整个详情
    const [depsResult, attachmentsResult] = await Promise.allSettled([
      api.dependencies(taskId),
      api.attachments(taskId),
    ])
    if (gen !== genRef.current) return
    if (depsResult.status === 'fulfilled') setDependencies(depsResult.value.items)
    if (attachmentsResult.status === 'fulfilled') setRawAttachments(attachmentsResult.value.items)
    setAttachmentsLoaded(true)
    setAttachmentsFailed(attachmentsResult.status === 'rejected')

    if (depsResult.status === 'rejected') {
      setError(toDetailError(depsResult.reason, `/v1/tasks/dependencies?task_id=${taskId}`))
    } else if (attachmentsResult.status === 'rejected') {
      setError(toDetailError(attachmentsResult.reason, `/v1/tasks/attachments?task_id=${taskId}`))
    } else {
      setError(null)
    }
    setLoading(false)
  }, [taskId])

  // 换任务 = 干净表单：清掉上一条任务的数据与评审草稿，再拉一次
  useEffect(() => {
    setTask(null)
    setDependencies([])
    setRawAttachments([])
    setAttachmentsLoaded(false)
    setAttachmentsFailed(false)
    setError(null)
    setLoading(true)
    setLastDecision(null)
    setReason('')
    setReasonError('')
    setSubmitError(null)
    void load()
    return () => {
      genRef.current += 1
    }
  }, [load])

  const reload = useCallback(() => {
    setLoading(true)
    void load()
  }, [load])

  // 可见集合与评审目标都从同一份原始数组派生（§5.1「一处判定、两处呈现」）：同帧、同源，界面与审计取值不分叉
  const attachments = useMemo<AttachmentRef[]>(() => currentTaskVersions(rawAttachments), [rawAttachments])
  const reviewMaterialId = useMemo<string | null>(() => reviewTargetId(rawAttachments), [rawAttachments])

  // 结论按钮的点击入口（route 的 askDecision 先调它）：驳回未填原因 → 就地标红、不发请求
  const validate = useCallback(
    (next: ReviewDecision): boolean => {
      if (submittingRef.current) return false
      if (next === 'reject' && reason.trim() === '') {
        setReasonError('驳回必须填写意见：写清要改什么，否则对方不知道下一步做什么')
        return false
      }
      setReasonError('')
      // 结论只在「点按钮且校验通过」时记账：只服务「重试提交」，不参与本次入参（入参永远是 reviewTargetId）
      setLastDecision(next)
      return true
    },
    [reason],
  )

  const submit = useCallback(
    async (next: ReviewDecision): Promise<boolean> => {
      if (!task || submittingRef.current) return false
      submittingRef.current = true
      setSubmitting(true)
      setSubmitError(null)
      try {
        const decided = await api.decideTask({
          task_id: task.task_id,
          decision: next,
          // 被评审附件由系统写入当前正文输出（§7.3）：没有正文输出就不带，服务端 material_id 可空
          material_id: reviewMaterialId ?? undefined,
          decided_by: ACTOR,
          reason: reason.trim() === '' ? undefined : reason.trim(),
        })
        // issue #129：写成功后**立刻**广播写后信号（不等下面的详情重拉）——
        // 看板与详情同在 App.tsx 的 BoardWithDetail 挂载点、看板常驻，收到即 refresh('write')，
        // 抽屉关闭时卡片已在目标列，不必等 15s 轮询。
        // 只在 2xx 后广播：非 2xx 走 catch，不发信号，语义与改前一致（错误条 + 立即重拉详情）。
        publishListWrite(decided.project_id || task.project_id)
        await load()
        return true
      } catch (err) {
        // flows F3 第 3 步：非 2xx → 错误条显示服务端 message + 立即重拉详情
        setSubmitError(toDetailError(err, '/v1/tasks/decide', 'POST'))
        await load()
        return false
      } finally {
        submittingRef.current = false
        setSubmitting(false)
      }
    },
    [task, reviewMaterialId, reason, load],
  )

  return {
    task,
    dependencies,
    attachments,
    attachmentsLoaded,
    attachmentsFailed,
    reviewTargetId: reviewMaterialId,
    state: detailStateOf(task, error),
    loading,
    error,
    reload,
    reviewing: task !== null && task.status === 'in_review',
    lastDecision,
    reason,
    setReason,
    reasonError,
    submitting,
    submitError,
    validate,
    submit,
  }
}
