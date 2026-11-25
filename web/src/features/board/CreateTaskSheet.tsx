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
import type { FlowStage } from '../../api/types'
import { stageLabel } from '../../lib/flow'
import { Alert, Button, DetailSheet, SelectField, TextArea, TextField } from '../../ui'
import type { SelectOption } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'

/**
 * 默认选中的类型：保持改造前的行为（默认 `dev`/开发），流程里没有 dev 时退回第一个环节。
 * 提交时 value 用的是环节 name（不是 label）——`POST /v1/tasks` 的 kind 唯一对外标识。
 */
function defaultKind(stages: readonly FlowStage[]): string {
  const dev = stages.find((stage) => stage.name === 'dev')
  return dev?.name ?? stages[0]?.name ?? ''
}

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
 * 409 的主文案 = 方案 §9.1 E9 的说明句（原文，带标点）；完整句子另放 <span title> 兜底。
 *
 * **已知可见性缺陷（决策人拍板的登记口径，非本卡引入）**：720 抽屉的单行错误条里这句话会被
 * `text-overflow: ellipsis` 裁掉尾部——预算与实测见 toSubmitError 里 409 分支的注释。
 * 这里不压字数、也不做任何跨层样式覆盖：压到能装下会让句子失去标点且余量只剩 0.6px，
 * 而覆盖会连带影响非 409 分支与窄屏换行（都实测过，见下）。
 */
const CLOSED_MESSAGE = (
  <span title={CLOSED_MESSAGE_FULL}>不能新建任务，请先在项目列表重开</span>
)

// `POST /v1/tasks` 唯一的 409 是 closed 项目（store/v3/task.go:56-58 的 ErrProjectClosed）——
// 所以按状态码分类，不解析英文原文（后端改文案也不会退化成「创建失败 + 重新提交」）。
function toSubmitError(err: unknown): SubmitError {
  if (err instanceof ApiError && err.status === 409) {
    // ── 已登记的口径差：说明在 720 抽屉里会被省略号裁（组件容量限制，不是本卡引入） ─────────────
    // 为什么裁：`features/shell/ErrorBar` 是单行横幅，1440×900 下文本列只有 566px，
    //   566 − 标题「项目已关闭」65.016 − detail 原文（如 `HTTP 409 · v3: project is closed: <pid>` ≈337）
    //   − 两个 gap 8 ≈ **156px 留给说明**；而说明句实测 208.016px，必然超出 52px 左右。
    //   预算随 detail 里 pid 的长度浮动（pid 越长，说明可用的越少），平台真实 id 正落在临界带上。
    // 这是谁的口径：`web/src/features/shell/shell.css:226-243` 已把「容器绝不溢出 → 普通文案可能被压掉
    //   1px 而出现末尾省略号」写死为设计取舍（说明按比例承担亚像素收缩、detail 先让位），
    //   属于共享组件的容量限制；本卡不动 shell，也不在看板里对抗它（试过 flex: 0 0 auto 的页面级覆盖，
    //   连带把非 409 分支与 390 窄屏换行带崩，已撤销）。
    // 量法提醒：**不能用 `scrollWidth == clientWidth` 判「无省略」**（整数四舍五入会显示相等）；
    //   应按 Range 取末字右沿与元素右沿比较——实测末字「开」右沿比元素右沿多出 0.31px，省略号真的会显示。
    // 兜底（用户仍能拿到全文）：① 说明的 <span title> 悬停看 §9.1 全文；
    //   ② detail 的 DOM 里保留服务端原文全文（含 `: <pid>`，视觉上出省略号但可复制/可读屏）；
    //   ③ 抽屉头部有关闭键，动作区只留「关闭」（409 重试必然再撞同一堵墙，不给「重新提交」）。
    return {
      title: '项目已关闭',
      message: CLOSED_MESSAGE,
      // 服务端原文逐字保留（含 `: <pid>`，方案 §9.1 E9 的 detail 口径），不因宽度裁剪内容
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
  /** 类型清单：`GET /v1/flow` 的 stages（value=name，显示=label），由看板页取一次传进来 */
  stages: readonly FlowStage[]
  /** 非空 = 类型清单取不到（接口不可达）：禁用「创建」并给出「无法获取类型清单」提示 */
  flowError: string
  flowLoading: boolean
  onReloadFlow: () => void
  onClose: () => void
  /** 创建成功：调用方负责 refresh('write') 与关闭 */
  onCreated: () => void
}

export function CreateTaskSheet({
  opened,
  projectId,
  stages,
  flowError,
  flowLoading,
  onReloadFlow,
  onClose,
  onCreated,
}: CreateTaskSheetProps) {
  const [kind, setKind] = useState('')
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [error, setError] = useState<SubmitError | null>(null)
  const [busy, setBusy] = useState(false)
  // 同一 JS tick 内连点两次时，busy 这个 state 还没落地（React 批处理），
  // 只有 ref 是同步可读的——所以「已提交」用 ref 守门，state 只负责 UI 的 loading/禁用。
  const submittingRef = useRef(false)

  // 类型下拉的选项：value 用环节 name，显示用 label（两者不能混，§7.3）
  const kindOptions: SelectOption[] = stages.map((stage) => ({ value: stage.name, label: stageLabel(stage) }))
  // 清单取不到时没有任何合法类型可提交：禁用「创建」与服务端请求（不是「猜一个 dev」）
  const noKindList = stages.length === 0
  const kindMissing = flowError !== ''

  // 每次重新打开都是干净表单：残留的标题 / 错误不影响下一次创建。
  useEffect(() => {
    if (!opened) return
    setTitle('')
    setDescription('')
    setError(null)
    setBusy(false)
    submittingRef.current = false
  }, [opened])

  // 默认类型跟随清单：打开时选中 dev（与改造前一致），清单晚到或流程里没有 dev 时落到第一项。
  useEffect(() => {
    if (!opened) return
    setKind((prev) => (prev !== '' && stages.some((stage) => stage.name === prev) ? prev : defaultKind(stages)))
  }, [opened, stages])

  const submit = async () => {
    if (submittingRef.current || title.trim() === '' || kind === '') return
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
          <Button size="md" loading={busy} disabled={title.trim() === '' || noKindList} onClick={submit}>
            创建
          </Button>
        </div>
      }
    >
      <div className="bp-form">
        {kindMissing ? (
          // 类型清单不可达（GET /v1/flow 失败）：保留已有卡片、不白屏，只把「创建」拦下来
          <Alert
            tone="warning"
            title="无法获取类型清单"
            actionLabel="重试"
            onAction={onReloadFlow}
            actionLoading={flowLoading}
          >
            类型来自流程配置（GET /v1/flow），当前取不到，暂时不能新建任务；已有任务不受影响。
          </Alert>
        ) : null}
        <SelectField
          id="bp-create-kind"
          label="类型"
          data={kindOptions}
          value={kind}
          disabled={noKindList}
          placeholder={flowLoading ? '正在获取类型…' : '暂无可用类型'}
          onChange={(value) => setKind(value ?? '')}
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
