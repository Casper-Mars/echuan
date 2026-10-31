import { useEffect, useMemo, useState } from 'react'
import {
  fetchMaterialText,
  isAbortError,
  listProjectMaterials,
  materialDownloadUrl,
} from '../api/client'
import type { MaterialChain } from '../api/types'
import { formatDateTime } from '../lib/format'
import { Markdown } from './Markdown'
import { Modal } from './Modal'
import { ErrorNotice, LoadingState } from './States'

/**
 * 需求全文只读预览：版本链下拉 + 正文 + 本版下载。
 * 与 SpecReader 同构；传入 chain 时直接复用（抽屉已拉过），避免重复请求。
 * 当前版以 chain.current_material_id 为准，不假定最新版即当前版。
 */
export function RequirementReader({
  projectId,
  materialId,
  chain,
  onClose,
}: {
  projectId: string
  materialId?: string
  chain?: MaterialChain
  onClose: () => void
}) {
  const [loadedChain, setLoadedChain] = useState<MaterialChain | null>(null)
  const [chainError, setChainError] = useState<unknown>(null)
  const [chainReloadKey, setChainReloadKey] = useState(0)
  const [selected, setSelected] = useState('')
  const [body, setBody] = useState<string | null>(null)
  const [bodyError, setBodyError] = useState<unknown>(null)
  const [bodyLoading, setBodyLoading] = useState(false)
  const [bodyReloadKey, setBodyReloadKey] = useState(0)

  const activeChain = chain ?? loadedChain

  useEffect(() => {
    if (chain) return
    const controller = new AbortController()
    setLoadedChain(null)
    setChainError(null)
    listProjectMaterials(projectId, 1, controller.signal)
      .then((data) => setLoadedChain(data))
      .catch((err) => {
        if (!isAbortError(err)) setChainError(err)
      })
    return () => controller.abort()
  }, [projectId, chain, chainReloadKey])

  // 版本倒序（最新在前），顺序稳定。
  const versions = useMemo(
    () => (activeChain ? [...activeChain.items].sort((a, b) => b.version - a.version) : []),
    [activeChain],
  )

  // 选中优先级：入口指定版本 → 链当前版 → 最新版。用户切换后以本地选择为准。
  const fallbackId = materialId || activeChain?.current_material_id || versions[0]?.material_id || ''
  const activeSelected = versions.some((item) => item.material_id === selected) ? selected : fallbackId
  const current = versions.find((item) => item.material_id === activeSelected)

  useEffect(() => {
    if (!activeSelected) {
      setBody(null)
      setBodyError(null)
      setBodyLoading(false)
      return
    }
    const controller = new AbortController()
    setBody(null)
    setBodyError(null)
    setBodyLoading(true)
    fetchMaterialText(activeSelected, controller.signal)
      .then((text) => {
        setBody(text)
        setBodyLoading(false)
      })
      .catch((err) => {
        if (isAbortError(err)) return
        setBodyError(err)
        setBodyLoading(false)
      })
    return () => controller.abort()
  }, [activeSelected, bodyReloadKey])

  return (
    <Modal title={current ? `需求全文 · 第 ${current.version} 版` : '需求全文'} onClose={onClose} wide>
      <ErrorNotice error={chainError} onRetry={() => setChainReloadKey((key) => key + 1)} />
      {!activeChain && !chainError && <LoadingState label="正在加载需求版本…" />}
      {activeChain && versions.length === 0 && <p className="muted">暂无需求版本</p>}
      {activeChain && versions.length > 0 && (
        <div className="detail">
          {current && (
            <p className="muted">
              第 {current.version} 版 · {current.title || '（无标题）'} · {current.uploaded_by || '—'} ·{' '}
              {formatDateTime(current.create_time)}
            </p>
          )}
          <label className="field"><span>阅读版本</span>
            <select value={activeSelected} onChange={(event) => setSelected(event.target.value)}>
              {versions.map((item) => (
                <option key={item.material_id} value={item.material_id}>
                  第 {item.version} 版{item.material_id === activeChain.current_material_id ? '（当前）' : ''} ·{' '}
                  {item.title}
                </option>
              ))}
            </select>
          </label>
          <div className="detail-actions">
            {current && (
              <a className="action" href={materialDownloadUrl(current.material_id)} download>下载本版需求</a>
            )}
          </div>
          {bodyLoading && <LoadingState label="正在加载需求全文…" />}
          {!bodyLoading && (
            <ErrorNotice
              error={bodyError}
              onRetry={bodyError ? () => setBodyReloadKey((key) => key + 1) : undefined}
            />
          )}
          {!bodyLoading && !bodyError && <Markdown source={body} empty="暂无需求正文" />}
        </div>
      )}
    </Modal>
  )
}
