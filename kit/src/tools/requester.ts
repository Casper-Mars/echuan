import type { Context } from '@deepseek-ai/cordis'
import { REQUESTER_TOOL_NAMES } from '../tool-names.js'
import { applyEchuanToolProfile } from './profile.js'

export const name = 'echuan-tools-requester'
export const inject = ['tools', 'echuan']
export function apply(ctx: Context): void {
  applyEchuanToolProfile(ctx, 'requester', REQUESTER_TOOL_NAMES)
}
