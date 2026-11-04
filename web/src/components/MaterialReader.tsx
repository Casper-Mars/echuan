import { useEffect, useState } from 'react'
import { fetchMaterialText, isAbortError } from '../api/client'
import type { Material } from '../api/types'
import { formatBytes, formatDateTime } from '../lib/format'
import { Markdown } from './Markdown'
import { Modal } from './Modal'
import { ErrorNotice, LoadingState } from './States'

/**
 * 结果资料在线阅读：xlarge 弹窗内按 material_id 拉取正文并渲染 Markdown。
 * 长文滚动交给 .modal-body，正文区域不再设置固定高度，避免双层滚动条。
 */
export function MaterialReader({
  material,
  onClose,
}: {
  material: Material
  onClose: () => void
}) {
  const [body, setBody] = useState<string | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [loading, setLoading] = useState(true)
  const [reloadKey, setReloadKey] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setBody(null)
    setError(null)
    setLoading(true)
    fetchMaterialText(material.material_id, controller.signal)
      .then((text) => {
        setBody(text)
        setLoading(false)
      })
      .catch((err) => {
        if (isAbortError(err)) return
        setError(err)
        setLoading(false)
      })
    return () => controller.abort()
  }, [material.material_id, reloadKey])

  const title = material.title ? `阅读结果资料 · 第 ${material.version} 版` : '阅读结果资料'

  return (
    <Modal title={title} onClose={onClose} xlarge>
      <div className="detail">
        <dl className="meta-grid">
          <div>
            <dt>标题</dt>
            <dd>{material.title || '（无标题）'}</dd>
          </div>
          <div>
            <dt>版本</dt>
            <dd>第 {material.version} 版</dd>
          </div>
          <div>
            <dt>上传人</dt>
            <dd>{material.uploaded_by || '—'}</dd>
          </div>
          <div>
            <dt>创建时间</dt>
            <dd>{formatDateTime(material.create_time)}</dd>
          </div>
          <div>
            <dt>大小</dt>
            <dd>{formatBytes(material.size)}</dd>
          </div>
        </dl>
        <p className="mono muted ellipsis" title={material.sha256}>
          内容校验值：{material.sha256}
        </p>

        {loading && <LoadingState label="正在加载结果资料…" />}
        {!loading && (
          <ErrorNotice
            error={error}
            onRetry={error ? () => setReloadKey((key) => key + 1) : undefined}
          />
        )}
        {!loading && !error && <Markdown source={body} empty="暂无正文" />}
      </div>
    </Modal>
  )
}
