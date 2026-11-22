import type { EchuanToolOptions } from './tools/write.js'

/** host 插件提供给 preset 工具子路径的共享服务。 */
export interface EchuanService {
  options: EchuanToolOptions
  log: (message: string) => void
  warn: (message: string) => void
}

export const ECHUAN_SERVICE = 'echuan'
