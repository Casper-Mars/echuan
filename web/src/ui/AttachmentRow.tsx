// ui/AttachmentRow —— 详情与预览里的附件行，design/components.md §ui/AttachmentRow
//
// 结构：角色徽标（输入/输出）→ 标题 → 右侧类型。
// 状态：rest / hover（淡底 + 描边加深）/ active（打开预览）/ focus-visible / disabled（附件不存在时置灰）/ loading（行内小 loading）。
// 可访问性：整行是一个按钮（UnstyledButton），aria-label 含角色与标题，点击区 ≥44。
import { Loader, UnstyledButton } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { AttachmentIcon } from './icons'
import { cssVars, cx } from './internal'

export type AttachmentRole = 'input' | 'output'

export interface AttachmentRowProps {
  role: AttachmentRole
  title: string
  /** 右侧类型（markdown / 大小 / 版本等展示文案） */
  kind?: string
  /** 附件不存在时置灰（同时由页面给错误条） */
  disabled?: boolean
  /** 行内右侧小 loading（正文拉取中） */
  loading?: boolean
  className?: string
  onOpen?: () => void
}

const ROLE_LABEL: Record<AttachmentRole, string> = {
  input: '输入',
  output: '输出',
}

export function AttachmentRow({
  role,
  title,
  kind,
  disabled = false,
  loading = false,
  className,
  onOpen,
}: AttachmentRowProps) {
  return (
    <UnstyledButton
      className={cx('ui-attachment-row', className)}
      aria-label={`${ROLE_LABEL[role]}附件：${title}`}
      disabled={disabled}
      data-disabled={disabled || undefined}
      data-loading={loading || undefined}
      onClick={disabled ? undefined : onOpen}
      style={cssVars({
        '--attachment-row-radius': cssVar('radius-8'),
        '--attachment-row-padding': cssVar('space-3'),
      })}
    >
      <span className="ui-attachment-row__role" data-role={role}>
        {ROLE_LABEL[role]}
      </span>
      <span className="ui-attachment-row__title">{title}</span>
      <span className="ui-attachment-row__meta">
        {loading ? <Loader size={14} aria-label="正在读取附件" /> : <AttachmentIcon size={14} />}
        {kind ? <span>{kind}</span> : null}
      </span>
    </UnstyledButton>
  )
}
