/** 审批弹窗展示的「被审资料」引用。version 为空表示详情尚未取到。 */
export interface ReviewedMaterial {
  materialId: string
  version: number | null
  title?: string | null
}

/**
 * 被审资料一行：material_id + v{version}（有标题则追加）。
 *
 * loading 用于详情请求尚未返回时占位；两者都没有时明确显示「未绑定资料」，
 * 避免审批人误以为在审最新版本。
 */
export function ReviewedMaterialLine({
  material,
  loading = false,
}: {
  material: ReviewedMaterial | null
  loading?: boolean
}) {
  return (
    <div className="dialog-target-meta reviewed-material">
      <span className="muted">被审资料：</span>
      {material?.materialId ? (
        <>
          <span className="mono">{material.materialId}</span>
          {material.version !== null ? (
            <span className="badge">v{material.version}</span>
          ) : (
            <span className="muted">版本读取中…</span>
          )}
          {material.title ? <span className="muted">{material.title}</span> : null}
        </>
      ) : (
        <span className="muted">{loading ? '读取中…' : '未绑定资料'}</span>
      )}
    </div>
  )
}
