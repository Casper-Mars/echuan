/**
 * 前端列表定时刷新的公共层：常量、退避、错误文案与全局开关。
 *
 * 本文件只有契约，不接任何页面；使用方（各列表页与自动刷新 hook）严格按下述
 * 导出的名字与语义调用，不要改名字、不要改语义。
 *
 * 全局开关存 localStorage：跨刷新保留，默认开，读写全部静默降级
 * （隐私模式、禁用存储、配额满、脏值都不影响页面可用）。
 */

import { ApiError } from '../api/client'

/** 基础刷新间隔：5 秒。 */
export const REFRESH_BASE_MS = 5000

/** 各列表的基础间隔：收件箱 / 队列 5s，执行节点 / 任务 15s，项目 30s。 */
export const REFRESH_INTERVALS = {
  inbox: REFRESH_BASE_MS,
  queue: REFRESH_BASE_MS,
  workers: REFRESH_BASE_MS * 3,
  tasks: REFRESH_BASE_MS * 3,
  projects: REFRESH_BASE_MS * 6,
}

/** 退避上限：5 分钟。 */
export const BACKOFF_MAX_MS = 300000

/** 项目列表每 N 次轮询补拉一次概要（本卡只定义常量，使用方在项目列表卡）。 */
export const PROJECT_SUMMARY_EVERY = 6

/**
 * 连续失败 failures 次后的下一次间隔：指数退避，封顶 BACKOFF_MAX_MS。
 * failures 为 0 时即 baseMs 本身。
 */
export function nextDelay(baseMs: number, failures: number): number {
  return Math.min(baseMs * 2 ** Math.min(failures, 16), BACKOFF_MAX_MS)
}

/**
 * 错误原因的展示文案。与各页现有 `message()` 写法一致：
 * `ApiError` / `Error` 取 `.message`，其余走 `String(err)`。
 */
export function describeError(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}

const STORAGE_KEY = 'echuan:auto-refresh'
const STORAGE_ON = 'on'
const STORAGE_OFF = 'off'

type Listener = () => void

/** 模块级单例：当前开关值（懒加载，首次读取时落定）。 */
let enabled: boolean | null = null

/** 订阅者集合；值变化时逐个通知。 */
const listeners = new Set<Listener>()

/** 读存储；未设置、脏值、抛异常、无 window 一律按开处理。 */
function readEnabled(): boolean {
  try {
    if (typeof window === 'undefined') return true
    return window.localStorage.getItem(STORAGE_KEY) !== STORAGE_OFF
  } catch {
    return true
  }
}

/** 当前开关值的单例入口。 */
function current(): boolean {
  if (enabled === null) enabled = readEnabled()
  return enabled
}

/** 全局开关当前是否开启（默认开）。 */
export function isAutoRefreshEnabled(): boolean {
  return current()
}

/** 设置全局开关并持久化；值没变不广播。 */
export function setAutoRefreshEnabled(next: boolean): void {
  const prev = current()
  enabled = next
  try {
    if (typeof window !== 'undefined') {
      window.localStorage.setItem(STORAGE_KEY, next ? STORAGE_ON : STORAGE_OFF)
    }
  } catch {
    // 存储不可用：静默降级为仅本次会话生效。
  }
  if (prev === next) return
  // 先快照，避免订阅者在通知过程中增删订阅影响本轮遍历。
  for (const listener of [...listeners]) {
    listener()
  }
}

/** 订阅开关变化；返回取消订阅函数。 */
export function subscribeAutoRefresh(fn: Listener): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

/**
 * 写后补拉信号（issue #129）：写操作（目前是 `POST /v1/tasks/decide`）成功后 publish，
 * 挂载中的列表页订阅后自行调 `refresh('write')` 补拉一次，不必等 15s 轮询。
 *
 * 与上面的开关单例同构：
 * - 按 `project_id` 分桶，只通知同项目的订阅者，不跨项目误刷；
 * - 没有订阅者时 publish 什么也不做（不报错、不排队、不补发）；
 * - 订阅者卸载即退订，桶空了整条删除，避免长期驻留的 Map 泄漏；
 * - 通知时先快照，订阅者在回调里增删订阅不影响本轮遍历。
 *
 * 本函数只发信号，不取数、不碰开关：`useAutoRefresh` 的 `'write'` 语义（在飞则排队、
 * 本轮收口补跑 1 次）负责「不叠发」，全局开关关闭时 `'write'` 也照常放行。
 */
type WriteListener = () => void

/** project_id → 订阅者集合 */
const writeListeners = new Map<string, Set<WriteListener>>()

/** 广播「该项目发生过写入」；无订阅者时静默返回。 */
export function publishListWrite(projectId: string): void {
  if (projectId === '') return
  const bucket = writeListeners.get(projectId)
  if (bucket === undefined) return
  for (const listener of [...bucket]) listener()
}

/** 订阅某项目的写后信号；返回取消订阅函数。 */
export function subscribeListWrite(projectId: string, fn: WriteListener): () => void {
  let bucket = writeListeners.get(projectId)
  if (bucket === undefined) {
    bucket = new Set<WriteListener>()
    writeListeners.set(projectId, bucket)
  }
  bucket.add(fn)
  return () => {
    const current = writeListeners.get(projectId)
    if (current === undefined) return
    current.delete(fn)
    if (current.size === 0) writeListeners.delete(projectId)
  }
}
