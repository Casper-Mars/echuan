// features/board/CreateTaskSheet —— 看板页头 / 空态的「新建任务」
//
// 为什么这次要带上新建：design/screens/board.md「结构」把「新建任务」写在页头，
// 且 empty 态的下一步动作就是它（design/components.md §ui/EmptyState 反例：没有下一步动作）。
// 只做最小创建（类型 / 标题 / 描述），不搬详情里的上下文与依赖编排（详情属 T6）。
//
// 分层：只用 ui/ 组件与 api/client；不直接 import Mantine 的包（design/components.md 红线，验收靠 grep）。
import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { api, ApiError } from '../../api/client'
import type { TaskKind } from '../../api/types'
import { KINDS, kindLabel } from '../../lib/labels'
import { Button, DetailSheet, SelectField, TextArea, TextField } from '../../ui'
import type { SelectOption } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'

const KIND_OPTIONS: SelectOption[] = KINDS.map((kind) => ({ value: kind, label: kindLabel(kind) }))

/** 错误条口径（方案 §9.1 表格 + §6.2 E9）：主文案中文，服务端原文只进 detail */
interface SubmitError {
  title: string
  /** ReactNode：409 分支传带 title 的 <span>，悬停可读完整句子（见 CLOSED_MESSAGE） */
  message: ReactNode
  detail: string
  /** 409（后端已拦 closed 项目）重试无意义 → 动作是「关闭」；其它失败 → 「重新提交」 */
  action: 'close' | 'retry'
}

/** 方案 §9.1「新建任务撞 closed」一行的完整文案（用户悬停可见的兜底全文） */
const CLOSED_MESSAGE_FULL = '该项目已关闭，不能新建任务；如需继续，请先在项目列表重开。'

/**
 * 409 的主文案（**已登记偏差**，非悄悄改口径）。
 *
 * 方案 §9.1 E9 行的完整说明是「该项目已关闭，不能新建任务；如需继续，请先在项目列表重开」
 * （含标点 24 字 ≈ 286px）。但 `features/shell/ErrorBar` 是单行横幅：720 抽屉里文本列只有 566px；
 * 旧实现（说明 16 字 + detail 带 project_id）整行超出 566px，说明被压到 `scrollWidth 208 > clientWidth 207.7`，
 * 末字「开」右沿超出元素右沿 ~1px → `text-overflow: ellipsis` 触发，用户看到「…请先在项目列表…」，
 * 动作词「重开」不可见（Tester 实测）。
 * 因此：标题仍是「项目已关闭」（§9.1 标题不变），说明压到 16 字、只保留两处关键信息
 * （「不能新建任务」+「项目列表重开」）；完整句子放进 <span title>，悬停仍可读到全文。
 * detail 同步让出宽度（去掉尾部 project_id，见 toSubmitError 的 §9.1 偏差说明），
 * 让整行不再超出 566px —— 说明因此**长于**其内容宽度（有富余而不是被压），从根上消除省略号。
 * 实测（1440×900、720 抽屉、本卡返工验收）：message clientWidth 243 / scrollWidth 243（无省略号），
 * 内容宽 208.0px，末字「开」右沿距元素右沿 34.75px；detail clientWidth 250 / scrollWidth 250（同样完整）。
 */
const CLOSED_MESSAGE = (
  <span title={CLOSED_MESSAGE_FULL}>不能新建任务，请先在项目列表重开</span>
)

// `POST /v1/tasks` 唯一的 409 是 closed 项目（store/v3/task.go:56-58 的 ErrProjectClosed）——
// 所以按状态码分类，不解析英文原文（后端改文案也不会退化成「创建失败 + 重新提交」）。
function toSubmitError(err: unknown, projectId: string): SubmitError {
  if (err instanceof ApiError && err.status === 409) {
    // detail 的**已登记偏差**：方案 §9.1 E9 行的 detail 是 `HTTP 409 · v3: project is closed: <pid>`。
    // 单行横幅的文本列只有 566px，说明（最要紧）必须完整可见，而 detail 按 ErrorBar 既有取舍
    // 「信息最可丢」先让位。实测带上尾部的 `: <pid>` 时整行超出 566px，说明被压 0.9px 触发省略号
    // （Tester 实测「重开」被裁）；省掉尾段后整行不满 566px，说明与 detail 都完整可见。
    // 服务端原文其余部分逐字保留（去掉的 pid 就是本 sheet 所属项目，页面上下文里可见）。
    const pidSuffix = `: ${projectId}`
    const serverMessage = err.message.endsWith(pidSuffix) ? err.message.slice(0, -pidSuffix.length) : err.message
    return {
      title: '项目已关闭',
      message: CLOSED_MESSAGE,
      detail: `HTTP ${err.status} · ${serverMessage}`,
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
      setError(toSubmitError(err, projectId))
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
