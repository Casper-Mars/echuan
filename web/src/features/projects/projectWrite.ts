// features/projects/projectWrite —— 关闭 / 重开项目的错误收敛（屏内私有，不进 ui/ 导出面）
//
// 规格对照：
//   docs/tech/web-ui-102-interaction-fix-v0.1.md
//     §5.1  409 的两种来源必须靠 message 子串区分（两种都是 code=conflict，不能只看 code）；
//     §7.5  D4（选定 A：先不带 force → 409 → 同一弹窗切「取消 N 个任务」→ 以 force=1 重发）；
//     §9.1  前端可见错误条口径：标题 / 说明中文，服务端英文原文只进 detail，detail 形如
//           `HTTP <status> · <message>`（断网等拿不到响应的情况单列，不编造状态码）。
//   design/screens/projects.md「交互」、design/flows.md F5。
//
// **与方案文本的一处实测偏差（以实测为准，登记在此）**：方案 §7.5 把 409 的 N 解析写成
// `/unfinished:\s*(\d+)\s+unfinished/`，但后端真实 message 是
// `v3: project still has unfinished tasks: 3 unfinished`
// ——`internal/store/v3/project.go:128` 用 `fmt.Errorf("%w: %d unfinished", ErrProjectNotEmpty, active)`
// 包装 `models.go:23` 的 `v3: project still has unfinished tasks`，
// 数字在 `unfinished` **后面**（`tasks: 3 unfinished`），方案那条正则匹配不到。
// 这里改用能匹配真实消息的 `/(\d+)\s+unfinished/`；解析不到时降级为「仍有未完成任务」仍可 force。

import { ApiError } from '../../api/client'
import type { Project } from '../../api/types'

/** 两个写动作：关闭（可能两段） / 重开（一段确认） */
export type ProjectWriteKind = 'close' | 'reopen'

/** 写失败后错误条要的三样东西：标题由 kind 派生，说明是固定中文，detail 只放技术原文 */
export interface ProjectWriteError {
  kind: ProjectWriteKind
  /** 失败的那一行：重试直接按原动作重发，不需要用户重新找行 */
  project: Project
  /** `HTTP <status> · <服务端 message>`；断网等没有响应时为 `未收到 HTTP 响应 · <异常首行>` */
  detail: string
}

/** 方案 §9.1「关闭/重开其它错误」一行的标题 */
export function writeErrorTitle(kind: ProjectWriteKind): string {
  return kind === 'close' ? '关闭项目失败' : '重开项目失败'
}

/** 异常首行：ApiError 的 message 就是服务端 message 原文，其它异常取 Error.message */
export function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/**
 * detail 口径（方案 §9.1）：技术原文，服务端英文原样保留，不翻译、不改写。
 * 有响应 → `HTTP <status> · <message>`；没有响应（断网 / CORS / 连接被拒）→ 不编造状态码。
 */
export function httpDetail(err: unknown): string {
  return err instanceof ApiError ? `HTTP ${err.status} · ${err.message}` : `未收到 HTTP 响应 · ${messageOf(err)}`
}

/**
 * 是不是「项目下还有未完成任务」这一种 409。
 * 靠 message 子串而不是状态码：§5.1 说明两种 409 都是 `conflict`，另一种（文案变更 / 中间层冲突）
 * 不能转 force——猜错语义会把任务静默取消掉。
 */
export function isUnfinishedConflict(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409 && messageOf(err).includes('unfinished')
}

/** 409 message 里的未完成任务数；解析不到 → null（降级文案：「仍有未完成任务」，仍可 force） */
export function unfinishedCountOf(message: string): number | null {
  const matched = /(\d+)\s+unfinished/.exec(message)
  if (matched === null) return null
  const count = Number.parseInt(matched[1], 10)
  return Number.isInteger(count) && count > 0 ? count : null
}

export function toWriteError(kind: ProjectWriteKind, project: Project, err: unknown): ProjectWriteError {
  return { kind, project, detail: httpDetail(err) }
}
