// features/products/ProductDetailSheet —— 产品只读详情抽屉（屏内私有组件，不进 ui/ 导出面）
//
// 规格对照：
//   design/components.md §「屏内私有详情抽屉（不属于 ui/ 导出面）」——产品抽屉的页面私有部分落在
//     `features/products/ProductDetailSheet.tsx`（6 字段），键值网格复用壳层 `features/shell/DetailFields.tsx`；
//     容器复用 `ui/DetailSheet` 的 `readonly` 变体；`ui/` 导出面仍是 21 个组件（本文件不从 `ui/index.ts` 出）。
//   design/screens/products.md「交互」——行点击 → 只读详情抽屉（6 字段：产品编号 / 名称 / 仓库 / 描述 / 证书 / 项目数）；
//     抽屉内不做编辑与保存、不放任何动作（关闭键除外）。
//   docs/tech/web-ui-102-interaction-fix-v0.1.md §7.2（D1 字段口径）/ §7.3（D2 选 A）/ §7.8。
//
// 口径：
//   * 只读、0 请求：字段全部从传入的当前行派生，不 copy 行对象、不发 API；
//   * 不持有打开状态：`product` 由页面按 id 从**当前** items 反查后传入，行不存在 → 抽屉自然关闭；
//   * 不写死颜色与间距：键值网格样式在 shell.css 的 .fx-detail-fields*，抽屉几何在 ui/DetailSheet。
import type { Product } from '../../api/types'
import { DetailSheet } from '../../ui'
import { DetailFields } from '../shell/DetailFields'
import type { DetailField } from '../shell/DetailFields'

export interface ProductDetailSheetProps {
  /** 当前行：页面按 product_id 从当前列表数据反查（undefined = 抽屉关闭，不保留快照） */
  product: Product | undefined
  /** 项目数：列表页本地计数（回退接口的 project_count），与表格「项目」列同源 */
  projectCount: number
  onClose: () => void
}

export function ProductDetailSheet({ product, projectCount, onClose }: ProductDetailSheetProps) {
  // opened 由 product 派生而不是另立 prop：两者同时存在会出现「opened=true 但没有行」的中间态，
  // 那时抽屉只能显示上一个产品的残留内容——只读抽屉不允许出现这种幽灵数据。
  return (
    <DetailSheet
      variant="readonly"
      opened={product !== undefined}
      title={product?.name ?? ''}
      onClose={onClose}
    >
      {product ? <DetailFields fields={fieldsOfProduct(product, projectCount)} /> : null}
    </DetailSheet>
  )
}

/**
 * 抽屉字段（design/screens/products.md「交互」6 项 + docs/tech §7.2 字段表）：
 * 产品编号 / 名称 / 仓库 / 描述 / 证书 / 项目数；空值文案按契约逐项给（—/未设置/未填写）。
 * 仓库按方案口径「mono，单行省略 + title」（truncate: true）；描述保持多行换行（multiline: true）。
 *
 * 仓库字段另带 `link: product.git_url`（issue #117 §5 D3 方案 C）：本文件**只声明意图**，
 * 合法 http(s) 的判定与 `<a>` 渲染收口都在 `features/shell/DetailFields.tsx` 一处，
 * 页面不复制第二套判定（方案 §5 D4）；登记值非法 / 为空时 `DetailFields` 回落纯文本，
 * `value` 与空值文案（`未设置`）保持原样。
 */
function fieldsOfProduct(product: Product, projectCount: number): DetailField[] {
  return [
    { label: '产品编号', value: product.product_id, mono: true },
    { label: '名称', value: textOr(product.name, '—') },
    {
      label: '仓库',
      value: textOr(product.git_url, '未设置'),
      mono: true,
      truncate: true,
      link: product.git_url,
    },
    { label: '描述', value: textOr(product.description, '未填写'), multiline: true },
    { label: '证书', value: textOr(product.license, '未设置') },
    { label: '项目数', value: String(projectCount) },
  ]
}

/** 空值判据：undefined / null / 全空白都算「没有值」，由调用方给出本地化文案。 */
function textOr(value: string | null | undefined, fallback: string): string {
  return value !== null && value !== undefined && value.trim() !== '' ? value : fallback
}
