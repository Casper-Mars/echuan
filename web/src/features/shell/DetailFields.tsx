// features/shell/DetailFields —— 只读详情抽屉的键值字段网格（壳层共用件，不进 ui/ 导出面）
//
// 规格对照：
//   design/components.md §「屏内私有详情抽屉（不属于 ui/ 导出面）」：产品抽屉（6 字段）与项目抽屉
//   （9 字段）共用同一套键值排版，落在 `features/shell/DetailFields.tsx`（与 ErrorBar 同层，
//   `features/*` 允许 import `features/shell`）；它**不是** ui 组件、不从 `web/src/ui/index.ts` 导出，
//   因此不进总览表、不计入 21 个组件（`web/scripts/ui-imports-check.mjs` 的断言集合不变）。
//   docs/tech/web-ui-102-interaction-fix-v0.1.md §7.2（D1 字段口径）/ §7.3（D2 选 A）。
//
// 本文件只做「排版」：
//   * 不收数据、不发请求——字段由调用方按当前列表行派生后传进来（抽屉 0 请求）；
//   * 不决定空值文案（「未设置 / 未填写 / —」是各屏契约的口径，由调用方给出）；
//   * 不写死颜色与间距（样式在 shell.css 的 .fx-detail-fields*，只消费 theme 令牌）。
// 语义：`<dl>` + 逐行 `<div>` 包裹 `<dt>/<dd>`（HTML 允许 dl 内用 div 分组），读屏按「字段名→值」播报。
import type { ReactNode } from 'react'

export interface DetailField {
  /** 字段名（产品：产品编号 / 名称 / 仓库 / 描述 / 证书 / 项目数） */
  label: string
  /** 字段值：调用方按当前行派生，空值时直接传已本地化的文案 */
  value: ReactNode
  /** 等宽字体：编号、仓库地址这类需要逐字符比对的字段 */
  mono?: boolean
  /** 多行正文（描述 / 上下文）：保留换行，超出抽屉高度由抽屉正文区滚动 */
  multiline?: boolean
  /**
   * 单行省略（仓库地址、产品编号/项目编号这类长标识）：`nowrap + hidden + ellipsis`，
   * 不换行、不撑破抽屉，鼠标悬停靠 `title` 看原文。
   * 与 `multiline` 互斥（同时给时以 multiline 为准，正文优先保留换行）。
   */
  truncate?: boolean
}

export interface DetailFieldsProps {
  fields: DetailField[]
  className?: string
}

export function DetailFields({ fields, className }: DetailFieldsProps) {
  return (
    <dl className={className ? `fx-detail-fields ${className}` : 'fx-detail-fields'} data-ui="detail-fields">
      {fields.map((field) => {
        // multiline 优先：正文要保留换行，单行省略对多行正文没有意义
        const truncate = field.truncate === true && field.multiline !== true
        return (
          // key 用字段名：同一抽屉内字段名唯一（6 项 / 9 项都是固定清单）
          <div className="fx-detail-fields__row" key={field.label}>
            <dt className="fx-detail-fields__label">{field.label}</dt>
            <dd
              className="fx-detail-fields__value"
              data-mono={field.mono ? 'on' : undefined}
              data-multiline={field.multiline ? 'on' : undefined}
              data-truncate={truncate ? 'on' : undefined}
              // 单行省略后文本被截断，看不见的部分靠 title 兜住（方案 §7.2「单行省略 + title」）。
              // 只对纯字符串值设 title：ReactNode 可能是节点树，硬塞进属性会变成 "[object Object]"。
              title={truncate && typeof field.value === 'string' ? field.value : undefined}
            >
              {field.value}
            </dd>
          </div>
        )
      })}
    </dl>
  )
}
