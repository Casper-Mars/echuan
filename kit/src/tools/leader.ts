import type { Context } from '@deepseek-ai/cordis'
import { TOOL_NAMES } from '../tool-names.js'
import { applyEchuanToolProfile } from './profile.js'

export const name = 'echuan-tools-leader'
export const inject = ['tools', 'echuan']
export function apply(ctx: Context): void {
  applyEchuanToolProfile(ctx, 'leader', TOOL_NAMES)
}
