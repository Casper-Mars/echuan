// features/shell/use-crumb-names —— 顶栏面包屑的名字反查（产品名 / 项目名）
//
// 规格对照：docs/tech/web-ui-102-interaction-fix-v0.1.md §7.7 D6 候选 A、§2.2 P4；
//   design/flows.md F1 分支段：「产品 / <产品名> / <项目名>」——名字由壳层反查
//   （GET /v1/projects?id= + GET /v1/products?id=，会话内按 id 缓存），取不到名字时退回 ID，
//   静默降级、不弹错误条、不阻塞渲染。
//
// 为什么这条链路要发请求（推翻 issue #89 B2「面包屑不为名字新增请求」）：
//   1. B2 的前提是「面包屑只展示 ID」——当时看板不要求回项目列表，product_id 根本不需要；
//   2. #102-4 要求从看板回到**项目列表**，而 `#/products/:productId/projects` 需要 product_id，
//      它既不在 `#/projects/:pid/tasks` 的路由段里，也不在 last-project 记忆里（D6 选项 B 也被否）；
//      在「不改路由形态」的前提下（D6 选项 C 已否）只能反查；
//   3. 代价被这三层兜住：仅 `/projects/*` 一条路径启用、首次 ≤2 次 GET、会话内按 id 缓存、
//      失败静默降级（降级后的形态就是 B2 想要的稳态 `产品 / <project_id>`）。
//   详见 docs/tech/web-ui-102-interaction-fix-v0.1.md §7.7 末段「是否推翻 issue #89 B2」。
//
// 分层：壳层只 import api（`web/src/api/client.ts` 已有 getProject / getProduct），不 import Mantine。
//
// 缓存口径（P4-5 请求预算的实现基础）：
//   - 项目与产品**分别**按 id 缓存，且只缓存成功的取数；失败不写缓存 → 下次进入会重试，
//     避免一次抖动把「只剩 ID」钉死在整个会话里。
//   - 「单飞」：同一 id 的并发请求合并成一次（StrictMode 双挂载 / 快速切路由都不会重复发）。
//   - 不写 localStorage / sessionStorage（D6 选项 B 已否：记忆有两套写入点、会陈旧）。
//     模块级 Map 活在这个 SPA 会话里，页面重载即清空。
//   - 卸载或切路由后丢弃迟到结果（alive 守卫，与 ProjectsPage / BoardPage 的取数口径一致）。

import { useEffect, useState } from 'react'
import { api } from '../../api/client'

/** 面包屑用的名字三件套；'' 一律表示「没取到，调用方退回 ID」 */
export interface CrumbNames {
  /** 项目名 */
  projectName: string
  /** 项目所属产品 ID —— 中间「项目列表」段的目标；'' 时该段不出现 */
  productId: string
  /** 产品名 */
  productName: string
}

/** 尚未取到（首帧 / 非 `/projects/*` 路由）：调用方按 ID 渲染，不发请求 */
const EMPTY_NAMES: CrumbNames = { projectName: '', productId: '', productName: '' }

/** 项目反查的最小结果：名字 + 产品 ID（面包屑只用到这两个字段） */
interface ProjectLite {
  name: string
  productId: string
}

// 会话内缓存（只存成功结果）
const projectCache = new Map<string, ProjectLite>()
const productCache = new Map<string, string>()
// 单飞表（请求中的 promise；失败也会被清理，下一次进入重新发起）
const projectInflight = new Map<string, Promise<ProjectLite | null>>()
const productInflight = new Map<string, Promise<string | null>>()

/** 取项目名 + product_id；失败返回 null（调用方降级为 `产品 / <project_id>`），不抛错也不写缓存 */
function loadProject(projectId: string): Promise<ProjectLite | null> {
  const cached = projectCache.get(projectId)
  if (cached) return Promise.resolve(cached)

  const running = projectInflight.get(projectId)
  if (running) return running

  const task = api
    .getProject(projectId)
    .then<ProjectLite | null>((project) => {
      const lite: ProjectLite = { name: project.name ?? '', productId: project.product_id ?? '' }
      // 两个字段都空说明响应异常，缓存它只会让降级形态钉死 —— 不写
      if (lite.name !== '' || lite.productId !== '') projectCache.set(projectId, lite)
      return lite
    })
    .catch(() => null)
    .finally(() => {
      projectInflight.delete(projectId)
    })

  projectInflight.set(projectId, task)
  return task
}

/** 取产品名；失败返回 null（中间段显示 productId，链接仍可用），不抛错也不写缓存 */
function loadProductName(productId: string): Promise<string | null> {
  const cached = productCache.get(productId)
  if (cached !== undefined) return Promise.resolve(cached)

  const running = productInflight.get(productId)
  if (running) return running

  const task = api
    .getProduct(productId)
    .then<string | null>((product) => {
      const name = product.name ?? ''
      if (name !== '') productCache.set(productId, name)
      return name === '' ? null : name
    })
    .catch(() => null)
    .finally(() => {
      productInflight.delete(productId)
    })

  productInflight.set(productId, task)
  return task
}

/** 同步查缓存：命中就立刻有名字（同一会话再进同一项目 → 首帧即完整、0 请求） */
function cachedNames(projectId: string): CrumbNames {
  const project = projectCache.get(projectId)
  if (!project) return EMPTY_NAMES
  const productName = project.productId === '' ? '' : (productCache.get(project.productId) ?? '')
  return { projectName: project.name, productId: project.productId, productName }
}

/** 完整反查：项目 → 产品；两段各自独立降级 */
async function resolveNames(projectId: string): Promise<CrumbNames> {
  const project = await loadProject(projectId)
  if (!project) return EMPTY_NAMES
  const productName = project.productId === '' ? '' : ((await loadProductName(project.productId)) ?? '')
  return { projectName: project.name, productId: project.productId, productName }
}

/**
 * `#/projects/:pid/*` 的面包屑名字。`projectId` 为 null（其它路由）时返回空态且**不发任何请求**。
 * 渲染只依赖返回值：取数失败时返回值停在空态（ID 兜底），不会抛到壳层。
 */
export function useCrumbNames(projectId: string | null): CrumbNames {
  const [names, setNames] = useState<CrumbNames>(() => (projectId === null ? EMPTY_NAMES : cachedNames(projectId)))

  useEffect(() => {
    if (projectId === null) {
      setNames(EMPTY_NAMES)
      return
    }
    // 缓存命中先落位（0 请求）；未命中再补请求
    setNames(cachedNames(projectId))

    let alive = true
    void resolveNames(projectId).then((next) => {
      if (alive) setNames(next)
    })
    return () => {
      alive = false
    }
  }, [projectId])

  return names
}
