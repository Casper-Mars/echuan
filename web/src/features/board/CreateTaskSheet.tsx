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
 * 409 的主文案：说明是「给人读的主文案」，**不许被压缩**；技术细节（detail）先让位。
 *
 * 方案 §9.1 E9 行的完整说明是「该项目已关闭，不能新建任务；如需继续，请先在项目列表重开」
 * （含标点 24 字 ≈ 286px），在 720 抽屉的单行错误条（文本列 566px）里放不下：
 * 共用 `features/shell/ErrorBar` 的默认 flex（message 1 1 auto / detail 0 100 auto）按比例分配收缩，
 * 说明会被压掉 0.3~0.9px，`text-overflow: ellipsis` 随即把末字吃掉
 * （旧实现实测 message 内容 208.016px → clientWidth 207.703px，用户只看到「…请先在项目列表…」）。
 *
 * 因此：标题保持 §9.1 的「项目已关闭」，说明压到 16 字 = 两处关键信息
 * （「不能新建任务」+「项目列表重开」）；完整句子放进 <span title>，悬停仍可读到全文。
 * 说明不被压缩靠 board.css 的 `.bp-sheet-error .fx-error-bar__text[data-detail] .fx-error-bar__message
 * { flex: 0 0 auto }` 保证（只对本 sheet 的这条错误条生效，改的是 board 自己的样式文件，
 * 不动 features/shell）：说明盒宽恒等于内容宽，布局零溢出，任何字数都不会再出省略号。
 * 实测（1440×900、720 抽屉、本卡最终验收）：message clientWidth 208 / scrollWidth 208，
 * 盒宽 == 内容宽 208.016px（末字「开」完整、无省略号）；detail clientWidth 285 / scrollWidth 337
 * （出省略号，DOM 保留服务端全文含 pid）；说明所在行 566/566，行本身不溢出。
 */
const CLOSED_MESSAGE = (
  <span title={CLOSED_MESSAGE_FULL}>不能新建任务，请先在项目列表重开</span>
)

// `POST /v1/tasks` 唯一的 409 是 closed 项目（store/v3/task.go:56-58 的 ErrProjectClosed）——
// 所以按状态码分类，不解析英文原文（后端改文案也不会退化成「创建失败 + 重新提交」）。
function toSubmitError(err: unknown): SubmitError {
  if (err instanceof ApiError && err.status === 409) {
    return {
      title: '项目已关闭',
      message: CLOSED_MESSAGE,
      // 服务端原文**逐字保留**（含 `: <pid>`，方案 §9.1 E9 的 detail 口径）：
      // pid 是排障时认「哪个项目被拦」的关键信息，宽度不够时按 ErrorBar 既有取舍由 detail 出省略号，
      // 完整原文始终在 DOM 里。消息与细节的优先级由上面的 CSS 钉死，不靠砍 detail 内容换宽度。
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
            className="bp-sheet-error"
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
