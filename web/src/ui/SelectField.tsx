// ui/SelectField —— 选择框（看板筛选、评审时选被评审附件），design/components.md §ui/SelectField
//
// 状态：rest / hover / active / focus-visible / disabled / invalid；额外支持 loading（选项加载中禁用）。
// 文案要求：筛选项写中文（「待审核」），不写英文枚举。
import { Select } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export interface SelectOption {
  value: string
  label: string
}

export interface SelectFieldProps {
  label: string
  data: SelectOption[]
  value: string | null
  onChange: (value: string | null) => void
  placeholder?: string
  error?: string
  description?: string
  required?: boolean
  disabled?: boolean
  /** 选项加载中：控件禁用 + 右侧 loading */
  loading?: boolean
  /** 可清空（筛选场景） */
  clearable?: boolean
  /** 可搜索（附件选择场景） */
  searchable?: boolean
  id?: string
  className?: string
}

export function SelectField({
  label,
  data,
  value,
  onChange,
  placeholder,
  error,
  description,
  required,
  disabled,
  loading = false,
  clearable,
  searchable,
  id,
  className,
}: SelectFieldProps) {
  return (
    <Select
      className={cx('ui-select-field', className)}
      label={label}
      data={data}
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      error={error}
      description={description}
      required={required}
      withAsterisk={required}
      disabled={disabled || loading}
      clearable={clearable}
      searchable={searchable}
      id={id}
      styles={{
        input: cssVars({ '--input-radius': cssVar('radius-8'), '--input-bd': cssVar('border-default') }),
      }}
    />
  )
}
