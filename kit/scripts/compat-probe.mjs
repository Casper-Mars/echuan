/**
 * 运行时兼容探针：在**真的装上 dsh-echuan** 的 dsh profile 上冷启动一次，断言工具与技能生效。
 *
 * `dsh --dump-config` 只证明 patch 能被组合出来，不证明插件 `apply()` 能跑通：
 * 25 个工具注册、1 个技能注册、`@deepseek-ai/dsh-tools` 的惰性 import 都是挂载期
 * 才发生的。本探针补上这一段，是 `scripts/dsh-compat.sh` 的第二道闸门。
 *
 * 用法（由 dsh-compat.sh 调用，必须从 dsh 安装目录运行，裸导入才能解析）：
 *
 *   DSH_HOME=<临时目录> PROBE_PROFILE=compat node compat-probe.mjs
 *
 * 退出码：全 PASS 为 0，任一断言失败或启动失败 / 超时为 1。
 *
 * 本文件同时被 `tests/compat-probe.spec.ts` 导入做静态一致性检查，因此顶层
 * 不得有副作用：只有直接执行（argv[1] === 本文件）时才跑探针主体。
 */
import process from 'node:process'
import { pathToFileURL } from 'node:url'

/**
 * 期望注册的 25 个工具（17 写 + 8 读）。
 *
 * 这份清单**独立声明，不从源码动态读取**：探针的价值就是用一份独立清单去核对运行时，
 * 动态读会退化成「自己核对自己」的假绿。顺序须与 `src/tool-names.ts` 的 `TOOL_NAMES`
 * 逐项一致，由 `tests/compat-probe.spec.ts` 钉死。
 */
export const EXPECTED_TOOLS = [
  'echuan_work_claim',
  'echuan_work_renew',
  'echuan_work_report',
  'echuan_project_create',
  'echuan_project_requirement_write',
  'echuan_project_submit',
  'echuan_spec_create',
  'echuan_spec_revise',
  'echuan_plan_submit',
  'echuan_work_create',
  'echuan_work_update',
  'echuan_work_delete',
  'echuan_project_update',
  'echuan_product_create',
  'echuan_product_update',
  'echuan_product_delete',
  'echuan_project_package',
  'echuan_inbox',
  'echuan_product_list',
  'echuan_project_list',
  'echuan_work_list',
  'echuan_project_get',
  'echuan_work_get',
  'echuan_material_get',
  'echuan_worker_info',
]

/** 期望注册的技能（随插件静态注册）。 */
export const EXPECTED_SKILLS = ['echuan-task-execution']

/** 探针硬超时：冷启动挂死时不能拖住 CI。 */
const DEFAULT_TIMEOUT_MS = 90_000

/** 探针主体。只在直接执行时调用。 */
async function main() {
  const profile = process.env.PROBE_PROFILE ?? 'ci'
  const timeoutMs = Number(process.env.PROBE_TIMEOUT_MS ?? DEFAULT_TIMEOUT_MS)
  const failures = []

  const pass = (message) => console.log(`PASS ${message}`)
  const fail = (message) => {
    failures.push(message)
    console.log(`FAIL ${message}`)
  }

  const timer = setTimeout(() => {
    console.error(`FAIL 冷启动超时（${timeoutMs}ms）：profile "${profile}" 未在限定时间内完成挂载`)
    process.exit(1)
  }, timeoutMs)

  // `@deepseek-ai/dsh` 的 exports 暴露了 `./lib/*`，profile-boot 是官方启动入口。
  const { runProfile } = await import('@deepseek-ai/dsh/lib/profile-boot.js')

  // 启动器要求「带 value 的 env 视图」，直接传 process.env 会在 http-proxy 处崩。
  const environment = new Map(Object.entries(process.env).map(([key, value]) => [key, { value }]))

  let ctx
  let shutdown
  try {
    ;({ ctx, shutdown } = await runProfile({ environment, profile, patchFiles: [], args: [] }))
  } catch (error) {
    clearTimeout(timer)
    const detail = error instanceof Error ? (error.stack ?? error.message) : String(error)
    console.error(`FAIL profile "${profile}" 冷启动失败：${detail}`)
    process.exit(1)
  }

  try {
    const tools = ctx.get('tools')
    if (tools === undefined || typeof tools.get !== 'function') {
      fail('ctx.tools 缺失或不支持 get()：dsh-tools 服务契约可能已变更')
    } else {
      const missing = EXPECTED_TOOLS.filter((name) => tools.get(name) === undefined)
      if (missing.length === 0) pass(`${EXPECTED_TOOLS.length} 个工具全部可见`)
      else fail(`缺少工具：${missing.join('、')}`)
    }

    const skills = ctx.get('skills')
    if (skills === undefined || typeof skills.list !== 'function') {
      fail('ctx.skills 缺失或不支持 list()：dsh-skill 服务契约可能已变更')
    } else {
      const list = await skills.list()
      const names = new Set(list.map((item) => item.name))
      const missing = EXPECTED_SKILLS.filter((name) => !names.has(name))
      if (missing.length === 0) pass(`${EXPECTED_SKILLS.length} 个技能全部可见`)
      else fail(`缺少技能：${missing.join('、')}`)

      const definition = await skills.get(EXPECTED_SKILLS[0])
      if (definition !== undefined && typeof definition.content === 'string' && definition.content.length > 0) {
        pass(`skills.get("${EXPECTED_SKILLS[0]}") 返回正文`)
      } else {
        fail(`skills.get("${EXPECTED_SKILLS[0]}") 未返回正文`)
      }
    }
  } catch (error) {
    const detail = error instanceof Error ? (error.stack ?? error.message) : String(error)
    fail(`探针断言抛错：${detail}`)
  } finally {
    clearTimeout(timer)
    try {
      await shutdown?.shutdown(failures.length === 0 ? 0 : 1)
    } catch {
      // 关停失败不覆盖断言结论
    }
  }

  if (failures.length > 0) {
    console.error(`\n兼容探针失败：${failures.length} 项断言未通过`)
    process.exit(1)
  }
  console.log('\n运行时探针全部通过')
  process.exit(0)
}

const entry = process.argv[1]
if (entry !== undefined && import.meta.url === pathToFileURL(entry).href) {
  await main()
}
