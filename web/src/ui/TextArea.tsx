// ui/TextArea —— 多行输入（评审意见、驳回原因），design/components.md §ui/TextField / TextArea
//
// 状态与 TextField 一致；invalid 用于「驳回必须填意见」这类校验。
import { Textarea } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export interface TextAreaProps {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  error?: string
  description?: string
  required?: boolean
  disabled?: boolean
  /** 最小行数（评审意见默认 4 行） */
  minRows?: number
  maxRows?: number
  autoSize?: boolean
  id?: string
  name?: string
  className?: string
  onBlur?: () => void
}

export function TextArea({
  label,
  value,
  onChange,
  placeholder,
  error,
  description,
  required,
  disabled,
  minRows = 4,
  maxRows,
  autoSize,
  id,
  name,
  className,
  onBlur,
}: TextAreaProps) {
  return (
    <Textarea
      className={cx('ui-text-area', className)}
      label={label}
      value={value}
      onChange={(event) => onChange(event.currentTarget.value)}
      placeholder={placeholder}
      error={error}
      description={description}
      required={required}
      withAsterisk={required}
      disabled={disabled}
      minRows={minRows}
      maxRows={maxRows}
      autosize={autoSize}
      id={id}
      name={name}
      onBlur={onBlur}
      styles={{ input: cssVars({ '--input-radius': cssVar('radius-8'), '--input-bd': cssVar('border-default') }) }}
    />
  )
}
