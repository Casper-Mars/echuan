import { useState, type FormEvent } from 'react'
import { createProduct, errorMessage, updateProduct } from '../api/client'
import type { Product } from '../api/types'
import { Modal } from './Modal'

interface ProductFormDialogProps {
  /** 传入表示编辑，缺省表示新建。 */
  product?: Product
  onClose: () => void
  onSaved: () => void
}

const PRODUCT_ID_PATTERN = /^[A-Za-z][A-Za-z0-9_-]{0,31}$/

/**
 * 新建/编辑产品弹窗。新建时 product_id 必填且不可重复；
 * 编辑时 product_id 只读（后端契约也不允许改名）。
 */
export function ProductFormDialog({ product, onClose, onSaved }: ProductFormDialogProps) {
  const editing = product !== undefined
  const [productId, setProductId] = useState(product?.product_id ?? '')
  const [name, setName] = useState(product?.name ?? '')
  const [gitUrl, setGitUrl] = useState(product?.git_url ?? '')
  const [description, setDescription] = useState(product?.description ?? '')
  const [license, setLicense] = useState(product?.license ?? '')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting) return

    const id = productId.trim()
    if (!editing && !PRODUCT_ID_PATTERN.test(id)) {
      setError('产品编号必须以字母开头，只含字母/数字/下划线/连字符，最长 32 位。')
      return
    }
    if (!name.trim()) {
      setError('产品名称不能为空。')
      return
    }
    if (!gitUrl.trim()) {
      setError('代码仓库地址不能为空。')
      return
    }

    setSubmitting(true)
    setError(null)
    try {
      if (editing) {
        await updateProduct(product.product_id, {
          name: name.trim(),
          git_url: gitUrl.trim(),
          description: description.trim(),
          license: license.trim(),
        })
      } else {
        await createProduct({
          product_id: id,
          name: name.trim(),
          git_url: gitUrl.trim(),
          description: description.trim(),
          license: license.trim(),
        })
      }
      onSaved()
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal title={editing ? `编辑产品 ${product.product_id}` : '新建产品'} onClose={onClose} closeDisabled={submitting}>
      <form className="dialog-form" onSubmit={submit}>
        <label className="field">
          <span>产品编号 {!editing && <em className="required">（必填）</em>}</span>
          <input
            value={productId}
            onChange={(event) => setProductId(event.target.value)}
            placeholder="例如 APP"
            disabled={editing || submitting}
            autoFocus={!editing}
          />
        </label>
        <label className="field">
          <span>名称</span>
          <input value={name} onChange={(event) => setName(event.target.value)} disabled={submitting} />
        </label>
        <label className="field">
          <span>代码仓库地址</span>
          <input
            value={gitUrl}
            onChange={(event) => setGitUrl(event.target.value)}
            placeholder="https://github.com/org/repo"
            disabled={submitting}
          />
        </label>
        <label className="field">
          <span>许可证（可选）</span>
          <input
            value={license}
            onChange={(event) => setLicense(event.target.value)}
            placeholder="例如 MIT"
            disabled={submitting}
          />
        </label>
        <label className="field">
          <span>描述（可选）</span>
          <textarea
            rows={3}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            disabled={submitting}
          />
        </label>

        {error && (
          <p className="dialog-error" role="alert">
            {error}
          </p>
        )}

        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={submitting}>
            取消
          </button>
          <button type="submit" className="action action-primary" disabled={submitting}>
            {submitting ? '提交中…' : editing ? '保存' : '创建'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
