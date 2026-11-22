import type { Context } from '@deepseek-ai/cordis'
import { READ_TOOL_NAMES } from '../tool-names.js'
import { applyEchuanToolProfile } from './profile.js'

export const name = 'echuan-tools-readonly'
export const inject = ['tools', 'echuan']
export function apply(ctx: Context): void {
  applyEchuanToolProfile(ctx, 'readonly', READ_TOOL_NAMES)
}
