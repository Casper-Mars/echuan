// features/task-detail/use-attachment-preview —— 附件正文取数（`GET /v1/materials?id=`）
//
// 规格对照：
//   design/screens/attachment-preview.md「数据来源」/「交互」/「状态矩阵」六态；
//   design/flows.md F2 第 3 步七条分支（404 / 413 / 超时 / 超大 / 空正文 / 渲染异常 / 内嵌图片）；
//   docs/tech/web-ui-redo-v0.1.md §7（`GET /v1/materials` 返回 `text/markdown`、`ETag`、
//     `Cache-Control: immutable`、支持 Range/HEAD）与 §8 异常路径表。
//
// 四条硬口径（本文件是唯一实现处，面板只渲染状态）：
//   1. **8s 超时**：AbortController + 计时器，超时单独成一种错误（文案「加载超时」，可重试）。
//   2. **只做并发去重，不自建缓存**：同一 `material_id` 的在飞请求共用一条 Promise（§7 末条）；
//      浏览器的 `immutable` + `ETag` HTTP 缓存是唯一缓存层，本文件不保存正文。
//   3. **超大不自动渲染**：`Content-Length` > 512 KB 时**不读 body**，直接进 oversized 态，
//      由用户点「仍然加载」显式放行（`force`）；服务端没给 `Content-Length` 时退化为读完后按字节数判。
//   4. **不抛异常**：所有失败都收敛成 `MaterialLoad` 联合类型，面板按 `status` 选文案，绝不白屏。
import { useCallback, useEffect, useRef, useState } from 'react'
import { materialURL } from '../../api/client'

/** 取数超时（flows F2「超时 → 加载超时，重试」，design 未给数值；本条为 card 口径 8s） */
export const PREVIEW_TIMEOUT_MS = 8000

/** 自动渲染上限（design/screens/attachment-preview.md oversized 态「Content-Length > 512 KB」） */
export const PREVIEW_OVERSIZED_BYTES = 512 * 1024

/**
 * 六态（design/screens/attachment-preview.md 状态矩阵）：
 *   loading             正文在飞
 *   error               404 / 413 / 超时 / 网络失败
 *   oversized           Content-Length > 512 KB，等用户放行
 *   empty               正文为空（trim 后无内容）
 *   image-placeholder   正文含内嵌图片：由 MarkdownViewer 渲染显式占位
 *   default             正常渲染
 */
export type PreviewState = 'loading' | 'default' | 'empty' | 'error' | 'oversized' | 'image-placeholder'

/** 失败分类：决定文案与动作按钮（重试 / 在新标签打开原文） */
export type PreviewFailureKind = 'timeout' | 'http' | 'network'

export interface PreviewFailure {
  kind: PreviewFailureKind
  /** 仅 kind=http 时有值（404 / 413 / 其他） */
  httpStatus?: number
}

type MaterialLoad =
  | { status: 'ok'; text: string; bytes: number }
  | { status: 'oversized'; bytes: number }
  | { status: 'failed'; failure: PreviewFailure }

/**
 * 在飞去重表：`material_id` → 同一条 Promise。
 * 只覆盖「同一时刻的重复请求」，不保存结果——落地后立即删除，下一次读正文仍走浏览器 HTTP 缓存。
 * `force` 键独立：用户点「仍然加载」绕过超限判断，不被正在飞的小请求合并。
 */
const inflight = new Map<string, Promise<MaterialLoad>>()

function fail(kind: PreviewFailureKind, httpStatus?: number): MaterialLoad {
  return { status: 'failed', failure: { kind, ...(httpStatus === undefined ? {} : { httpStatus }) } }
}

/** 单次真实请求：超时 / 非 2xx / 超大 / 正常四类出口，绝不 reject */
async function fetchMaterial(materialId: string, force: boolean): Promise<MaterialLoad> {
  const controller = new AbortController()
  // 超时用计时器而不是 `AbortSignal.timeout`（后者在旧 Safari 缺失），AbortController 是同一套语义
  const timer = setTimeout(() => controller.abort(), PREVIEW_TIMEOUT_MS)
  try {
    const response = await fetch(materialURL(materialId), {
      signal: controller.signal,
      headers: { accept: 'text/markdown, text/plain;q=0.9, */*;q=0.8' },
    })
    if (!response.ok) return fail('http', response.status)

    // 先看响应头：能省掉一次 512 KB+ 的下载（§7 的 HEAD/Range 能力让服务端一定给长度口径）
    const declared = Number(response.headers.get('content-length') ?? '')
    const declaredBytes = Number.isFinite(declared) && declared > 0 ? declared : 0
    if (!force && declaredBytes > PREVIEW_OVERSIZED_BYTES) {
      return { status: 'oversized', bytes: declaredBytes }
    }

    const text = await response.text()
    const bytes = declaredBytes > 0 ? declaredBytes : new TextEncoder().encode(text).length
    // 服务端没给 Content-Length（chunked）时退化为读完再判，仍然不自动渲染超限正文
    if (!force && bytes > PREVIEW_OVERSIZED_BYTES) return { status: 'oversized', bytes }
    return { status: 'ok', text, bytes }
  } catch (err) {
    if (controller.signal.aborted) return fail('timeout')
    if (err instanceof Error && err.name === 'AbortError') return fail('timeout')
    return fail('network')
  } finally {
    clearTimeout(timer)
  }
}

/** 并发去重入口：同键复用同一条 Promise，落地即释放（不缓存正文） */
function loadMaterial(materialId: string, force = false): Promise<MaterialLoad> {
  const key = force ? `${materialId}#force` : materialId
  const pending = inflight.get(key)
  if (pending !== undefined) return pending
  const task = fetchMaterial(materialId, force).finally(() => {
    inflight.delete(key)
  })
  inflight.set(key, task)
  return task
}

/** 正文里是否有 markdown 内嵌图片（`![alt](src)`）——有则进 image-placeholder 态做截图对照 */
const IMAGE_SYNTAX = /!\[[^\]]*\]\(/

export interface AttachmentPreviewApi {
  state: PreviewState
  text: string
  bytes: number
  failure: PreviewFailure | null
  /** 重新取一次（error 态的「重试」） */
  reload: () => void
  /** oversized 态的「仍然加载」：跳过超限判断再取一次 */
  forceLoad: () => void
  /** 「在新标签打开原文」的兜底地址（只读；点击由面板负责，不自动打开） */
  sourceURL: string
}

export function useAttachmentPreview(materialId: string): AttachmentPreviewApi {
  const [loading, setLoading] = useState(true)
  const [result, setResult] = useState<MaterialLoad | null>(null)
  // 换附件 / 卸载：旧响应整笔丢弃（同一 material_id 的在飞请求可能被后来者复用）
  const genRef = useRef(0)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    genRef.current += 1
    const gen = genRef.current
    setLoading(true)
    setResult(null)
    void loadMaterial(materialId, false).then((next) => {
      if (gen !== genRef.current) return
      setResult(next)
      setLoading(false)
    })
    return () => {
      genRef.current += 1
    }
  }, [materialId, attempt])

  const reload = useCallback(() => {
    setAttempt((value) => value + 1)
  }, [])

  // 「仍然加载」：绕过 512 KB 判断；同一 id 的 force 请求也走并发去重
  const forceLoad = useCallback(() => {
    genRef.current += 1
    const gen = genRef.current
    setLoading(true)
    setResult(null)
    void loadMaterial(materialId, true).then((next) => {
      if (gen !== genRef.current) return
      setResult(next)
      setLoading(false)
    })
  }, [materialId])

  let state: PreviewState = 'loading'
  let text = ''
  let bytes = 0
  let failure: PreviewFailure | null = null

  if (!loading && result !== null) {
    if (result.status === 'failed') {
      state = 'error'
      failure = result.failure
    } else if (result.status === 'oversized') {
      state = 'oversized'
      bytes = result.bytes
    } else {
      text = result.text
      bytes = result.bytes
      if (text.trim() === '') state = 'empty'
      else if (IMAGE_SYNTAX.test(text)) state = 'image-placeholder'
      else state = 'default'
    }
  }

  return { state, text, bytes, failure, reload, forceLoad, sourceURL: materialURL(materialId) }
}
