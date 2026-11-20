// features/board/CreateTaskSheet —— 看板页头 / 空态的「新建任务」
//
// 为什么这次要带上新建：design/screens/board.md「结构」把「新建任务」写在页头，
// 且 empty 态的下一步动作就是它（design/components.md §ui/EmptyState 反例：没有下一步动作）。
// 只做最小创建（类型 / 标题 / 描述），不搬详情里的上下文与依赖编排（详情属 T6）。
//
// 分层：只用 ui/ 组件与 api/client；不直接 import Mantine 的包（design/components.md 红线，验收靠 grep）。
import { useEffect, useState } from 'react'
import { api, ApiError } from '../../api/client'
import type { TaskKind } from '../../api/types'
import { KINDS, kindLabel } from '../../lib/labels'
import { Button, DetailSheet, SelectField, TextArea, TextField } from '../../ui'
import type { SelectOption } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'

const KIND_OPTIONS: SelectOption[] = KINDS.map((kind) => ({ value: kind, label: kindLabel(kind) }))

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
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // 每次重新打开都是干净表单：残留的标题 / 错误不影响下一次创建。
  useEffect(() => {
    if (!opened) return
    setKind('dev')
    setTitle('')
    setDescription('')
    setError('')
    setBusy(false)
  }, [opened])

  const submit = async () => {
    if (busy || title.trim() === '') return
    setBusy(true)
    setError('')
    try {
      await api.createTask({
        project_id: projectId,
        kind,
        title: title.trim(),
        description_md: description,
      })
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError || err instanceof Error ? err.message : String(err))
    } finally {
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
        {error === '' ? null : (
          <ErrorBar
            title="创建失败"
            message="任务没有提交成功，上一步填写的内容还在，可以直接重试。"
            detail={error}
            retryLabel="重新提交"
            onRetry={submit}
            retrying={busy}
          />
        )}
      </div>
    </DetailSheet>
  )
}
