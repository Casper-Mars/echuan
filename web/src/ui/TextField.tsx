// ui/TextField —— 单行输入（新建任务标题、评审摘要等），design/components.md §ui/TextField / TextArea
//
// 状态：rest / hover / active（描边品牌色）/ focus-visible（焦点环）/ disabled / invalid（描边转红 + 错误文案）。
// 可访问性：Mantine 负责 <label for> 绑定与 aria-describedby；移动端字号 ≥15 防 iOS 缩放（见 ui.css）。
import { TextInput } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export interface TextFieldProps {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  /** 错误文案（非空即进入 invalid 状态） */
  error?: string
  description?: string
  required?: boolean
  disabled?: boolean
  /** 等宽字体（task_id / 租约 id 输入） */
  mono?: boolean
  /** 必填标记的读屏文案 */
  withAsterisk?: boolean
  id?: string
  name?: string
  autoFocus?: boolean
  className?: string
  onBlur?: () => void
}

export function TextField({
  label,
  value,
  onChange,
  placeholder,
  error,
  description,
  required,
  disabled,
  mono = false,
  withAsterisk,
  id,
  name,
  autoFocus,
  className,
  onBlur,
}: TextFieldProps) {
  return (
    <TextInput
      className={cx('ui-text-field', mono && 'ui-text-field--mono', className)}
      label={label}
      value={value}
      onChange={(event) => onChange(event.currentTarget.value)}
      placeholder={placeholder}
      error={error}
      description={description}
      required={required}
      withAsterisk={withAsterisk ?? required}
      disabled={disabled}
      id={id}
      name={name}
      autoFocus={autoFocus}
      onBlur={onBlur}
      styles={{ input: cssVars({ '--input-radius': cssVar('radius-8'), '--input-bd': cssVar('border-default') }) }}
    />
  )
}
