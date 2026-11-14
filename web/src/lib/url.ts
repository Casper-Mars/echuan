/** 外部地址的链接判定：只有能安全放进 `<a href>` 的值才返回字符串。 */

const LINK_PROTOCOLS = ['http:', 'https:']

/**
 * 把登记值收敛成可放进 `href` 的仓库地址。
 *
 * - 空白 / `null` / `undefined` / 无法解析 / 非 http(s) → `null`，调用方按纯文本渲染；
 *   后端不校验 `git_url` 形态，`javascript:`、`data:` 等直塞 `href` 会构成 XSS 面；
 * - 通过校验时返回 **trim 后的原字符串**：不返回 `URL.href`（否则 `https://github.com`
 *   会被补成带尾斜杠，与登记值不一致），也不做 `git@…` → `https` 转换；
 * - 任何异常一律 `null`：`web/src` 没有 ErrorBoundary，渲染期抛出会打掉整个抽屉。
 *   为此整个函数体都在 `try` 内，且先收敛入参类型——`as any` 之类绕过类型的调用方
 *   可能传数字 / 对象 / Symbol，直接 `raw.trim()` 会抛 `TypeError`。
 */
export function repoHref(raw: string | null | undefined): string | null {
  try {
    if (typeof raw !== 'string') return null
    const value = raw.trim()
    if (!value) return null
    // `URL.protocol` 已小写，大小写 scheme（HTTPS://…）也能识别；返回值不做大小写改写。
    if (!LINK_PROTOCOLS.includes(new URL(value).protocol)) return null
    return value
  } catch {
    return null
  }
}
