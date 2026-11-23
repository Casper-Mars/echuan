// features/projects/use-project-materials —— 项目资料取数（`GET /v1/projects/materials?id=`）
//
// 口径：
//   * 抽屉打开才拉，关闭不保留：项目 id 变化即重拉，旧请求的回包按请求序号丢弃；
//   * 失败收敛成 error 文案，不抛异常；重试只重拉列表，不清空已展示内容；
//   * 不做缓存：浏览器 HTTP 缓存负责材料正文，列表是廉价查询。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, api } from '../../api/client'
import type { ProjectMaterial } from '../../api/types'

export interface ProjectMaterialsApi {
  materials: ProjectMaterial[]
  loading: boolean
  error: string | null
  reload: () => void
}

export function useProjectMaterials(projectId: string | undefined): ProjectMaterialsApi {
  const [materials, setMaterials] = useState<ProjectMaterial[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const requestId = useRef(0)

  const reload = useCallback(() => setAttempt(value => value + 1), [])

  useEffect(() => {
    if (projectId === undefined) {
      requestId.current += 1
      setMaterials([])
      setLoading(false)
      setError(null)
      return
    }

    const id = ++requestId.current
    setLoading(true)
    setError(null)

    api
      .getProjectMaterials(projectId)
      .then(result => {
        if (id !== requestId.current) return
        setMaterials(result.items)
      })
      .catch(err => {
        if (id !== requestId.current) return
        setError(err instanceof ApiError ? `读取项目资料失败（HTTP ${err.status}）` : '读取项目资料失败')
      })
      .finally(() => {
        if (id !== requestId.current) return
        setLoading(false)
      })

    return () => {
      // 卸载或切项目：让在飞回包失效
      requestId.current += 1
    }
  }, [projectId, attempt])

  return { materials, loading, error, reload }
}
