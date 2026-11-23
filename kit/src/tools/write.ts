/**
 * 工具层共享的选项与身份类型。
 *
 * v3 工具面（`task_*` / `project_*` / `product_*`）只依赖这里两项：
 * - {@link EchuanToolOptions}：注入的客户端与身份；
 * - {@link WorkerIdentity}：`worker_id` 的唯一内存来源（心跳循环）。
 *
 * 历史：v2 的 claim / renew / report 工具已删除，本文件不再包含工具实现。
 */
import type { EchuanClient } from '../client.js'
import type { WorkerInfoDeps } from './worker-info.js'

/**
 * 共享身份：worker_id 的唯一内存来源。
 *
 * 生产实现由 `index.ts` 提供（读心跳循环的 `workerId` 只读属性）；
 * 未注册时返回 `undefined`，此时 `echuan_task_claim` 会在本地拒绝（平台契约要求
 * `worker_id` 必填）。
 */
export interface WorkerIdentity {
  readonly workerId: string | undefined
  /**
   * worker 心跳是否开启（`worker.poll.enable`）。
   *
   * 开启时领卡由心跳循环负责；工具层的 `echuan_task_claim` 会本地拒绝，
   * 避免同一张卡同时被「手动领」与「派单投递」两个持有者处理。
   */
  readonly pollEnabled?: boolean
  /**
   * 报告成功的回调（可选）：由 `index.ts` 接到心跳循环的 `notifyReported`，
   * 用于清除该工作项的 inflight。只在平台 HTTP 成功后调用。
   */
  notifyReported?(workId: string): void
}

export interface EchuanToolOptions {
  /** 平台 HTTP 客户端（TOOL_METHODS 里的方法集合）。 */
  client: EchuanClient
  /** 共享身份（心跳循环）。 */
  identity: WorkerIdentity
  /**
   * `echuan_worker_info` 的只读依赖（可选）。缺省时该工具返回「诊断不可用」，
   * 不影响其余工具；依赖本身也是每次调用惰性读取。
   */
  workerInfo?: WorkerInfoDeps
  /**
   * 不注册的工具名（可选）。只影响本进程注册哪些工具，不改变工具定义与 HTTP 契约；
   * 缺省等价于 `[]`。名称的归一化与校验见 `../tool-names.js`。
   */
  exclude?: readonly string[]
}
