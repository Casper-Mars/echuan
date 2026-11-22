import type { Context } from '@deepseek-ai/cordis'
import type { EchuanService } from '../service.js'
import { registerEchuanToolNames } from './index.js'

export function applyEchuanToolProfile(ctx: Context, profile: string, names: readonly string[]): void {
  const service = (ctx as unknown as { echuan?: EchuanService }).echuan
  if (service === undefined) throw new Error('dsh-echuan host service 未就绪；检查 inject/echuan 服务')
  ctx.effect(() => registerEchuanToolNames(ctx, service.options, names), `echuan.tools.${profile}`)
}
