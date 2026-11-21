// features/board/CreateTaskSheet —— 看板页头 / 空态的「新建任务」
//
// 为什么这次要带上新建：design/screens/board.md「结构」把「新建任务」写在页头，
// 且 empty 态的下一步动作就是它（design/components.md §ui/EmptyState 反例：没有下一步动作）。
// 只做最小创建（类型 / 标题 / 描述），不搬详情里的上下文与依赖编排（详情属 T6）。
//
// 分层：只用 ui/ 组件与 api/client；不直接 import Mantine 的包（design/components.md 红线，验收靠 grep）。
import { useEffect, useRef, useState } from 'react'
import { api, ApiError } from '../../api/client'
import type { TaskKind } from '../../api/types'
import { KINDS, kindLabel } from '../../lib/labels'
import { Button, DetailSheet, SelectField, TextArea, TextField } from '../../ui'
import type { SelectOption } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'

const KIND_OPTIONS: SelectOption[] = KINDS.map((kind) => ({ value: kind, label: kindLabel(kind) }))

/** 错误条口径（方案 §9.1 表格 + §7.8 E9）：主文案中文，服务端原文只进 detail */
interface SubmitError {
  title: string
  message: string
  detail: string
  /** 409（后端已拦 closed 项目）重试无意义 → 动作是「关闭」；其它失败 → 「重新提交」 */
  action: 'close' | 'retry'
}

// `POST /v1/tasks` 唯一的 409 是 closed 项目（store/v3/task.go:56-58 的 ErrProjectClosed）——
// 所以按状态码分类，不解析英文原文（后端改文案也不会退化成「创建失败 + 重新提交」）。
function toSubmitError(err: unknown): SubmitError {
  if (err instanceof ApiError && err.status === 409) {
    return {
      title: '项目已关闭',
      // 文案口径（方案 §7.8 表格）：标题「项目已关闭」+ 说明「不能新建任务，请先在项目列表重开」，
      // 两处关键信息都在主文案里。表格全文（「该项目已关闭，不能新建任务；如需继续，请先在项目列表重开」）
      // 在 720 抽屉的单行错误条里会把「重开」压成省略号（实测 clientWidth 285 / 内容 286），
      // 而标题已写「项目已关闭」，故说明去掉重复主语与中段，保证关键动作完整可见。
      message: '不能新建任务，请先在项目列表重开',
      // 服务端原文（含 project_id）只在 detail；它会按单行横幅的既有取舍先让位（出省略号），
      // 完整原文仍在 DOM 里，E9 的「原文只进 detail」成立。
      detail: `HTTP ${err.status} · ${err.message}`,
      action: 'close',
    }
  }
  return {
    title: '创建失败',
    message: '任务没有提交成功，上一步填写的内容还在，可以直接重试。',
    detail: err instanceof Error ? err.message : String(err),
    action: 'retry',
  }
}

export interface CreateTaskSheetProps {
  opened: boolean
  projectId: string
  onClose: () => void
  /** 创建成功：调用方负责 refresh('write') 与关闭 */
  onCreated: () => void
}

export function CreateTaskSheet({ opened, projectId, onClose, onCreated }: CreateTaskSheetProps) {
  const [kind, setKind] = useState<TaskKind>('dev')
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [error, setError] = useState<SubmitError | null>(null)
  const [busy, setBusy] = useState(false)
  // 同一 JS tick 内连点两次时，busy 这个 state 还没落地（React 批处理），
  // 只有 ref 是同步可读的——所以「已提交」用 ref 守门，state 只负责 UI 的 loading/禁用。
  const submittingRef = useRef(false)

  // 每次重新打开都是干净表单：残留的标题 / 错误不影响下一次创建。
  useEffect(() => {
    if (!opened) return
    setKind('dev')
    setTitle('')
    setDescription('')
    setError(null)
    setBusy(false)
    submittingRef.current = false
  }, [opened])

  const submit = async () => {
    if (submittingRef.current || title.trim() === '') return
    submittingRef.current = true
    setBusy(true)
    setError(null)
    try {
      await api.createTask({
        project_id: projectId,
        kind,
        title: title.trim(),
        description_md: description,
      })
      onCreated()
    } catch (err) {
      setError(toSubmitError(err))
    } finally {
      submittingRef.current = false
      setBusy(false)
    }
  }

  return (
    <DetailSheet
      opened={opened}
      title="新建任务"
      onClose={busy ? () => undefined : onClose}
      footer={
        <div className="bp-sheet-actions">
          <Button variant="default" size="md" disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button size="md" loading={busy} disabled={title.trim() === ''} onClick={submit}>
            创建
          </Button>
        </div>
      }
    >
      <div className="bp-form">
        <SelectField
          id="bp-create-kind"
          label="类型"
          data={KIND_OPTIONS}
          value={kind}
          onChange={(value) => setKind((value ?? 'dev') as TaskKind)}
        />
        <TextField
          id="bp-create-title"
          label="标题"
          required
          autoFocus
          value={title}
          onChange={setTitle}
          placeholder="一句话说明要做什么"
        />
        <TextArea
          id="bp-create-description"
          label="任务描述（markdown，可选）"
          minRows={6}
          value={description}
          onChange={setDescription}
        />
        {error === null ? null : (
          <ErrorBar
            title={error.title}
            message={error.message}
            detail={error.detail}
            retryLabel="重新提交"
            // 409 时重试必然再撞同一堵墙：不给重试，只留「关闭」（方案 §9.1「动作: 关闭」）
            onRetry={error.action === 'retry' ? submit : undefined}
            retrying={busy}
            extraActions={
              error.action === 'close' ? (
                <Button variant="default" size="sm" onClick={onClose}>
                  关闭
                </Button>
              ) : undefined
            }
          />
        )}
      </div>
    </DetailSheet>
  )
}
