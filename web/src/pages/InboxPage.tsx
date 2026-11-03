import { useMemo } from 'react'
import { getInbox } from '../api/client'
import type { InboxItem } from '../api/types'
import {
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { usePolling } from '../hooks/usePolling'
import { formatDuration } from '../lib/format'

/** 收件箱固定分组：五个门禁按「项目评审 → 工作项决策」排列。 */
interface GroupDef {
  key: string
  label: string
  kind: string
  state: string
}

const GROUPS: GroupDef[] = [
  { key: 'requirement_review', label: '需求评审', kind: 'project', state: 'requirement_review' },
  { key: 'spec_review', label: '方案评审', kind: 'project', state: 'spec_review' },
  { key: 'tasks_review', label: '开发任务评审', kind: 'project', state: 'tasks_review' },
  { key: 'submitted', label: '工作项待验收', kind: 'work_item', state: 'submitted' },
  { key: 'failed', label: '工作项失败待处理', kind: 'work_item', state: 'failed' },
]

const GROUP_INDEX = new Map(GROUPS.map((group) => [`${group.kind}:${group.state}`, group]))

const TYPE_LABELS: Record<string, string> = {
  project: '项目',
  work_item: '工作项',
}

interface InboxGroup extends GroupDef {
  items: InboxItem[]
}

/**
 * 统一收件箱：只呈现需要人处理的事（评审门与工作项验收/失败）。
 *
 * - 5s 轮询 GET /v1/inbox?limit=200，服务端已按等待时长排序；
 * - 按门禁固定分组，空分组不渲染，避免健康状态占用界面；
 * - stale 由服务端按 review_stale_hours 判定，前端只负责标红展示。
 */
export function InboxPage() {
  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(
    (signal) => getInbox(200, signal),
    [],
  )

  const items = data?.items ?? []
  const count = data?.count ?? items.length
  const staleHours = data?.review_stale_hours

  const groups = useMemo<InboxGroup[]>(() => {
    const buckets = new Map<string, InboxItem[]>()
    for (const item of items) {
      const groupKey = `${item.kind}:${item.state}`
      const key = GROUP_INDEX.has(groupKey) ? groupKey : 'other'
      const bucket = buckets.get(key)
      if (bucket) bucket.push(item)
      else buckets.set(key, [item])
    }

    const result: InboxGroup[] = GROUPS.map((group) => ({
      ...group,
      items: buckets.get(`${group.kind}:${group.state}`) ?? [],
    })).filter((group) => group.items.length > 0)

    const other = buckets.get('other')
    if (other && other.length > 0) {
      result.push({ key: 'other', label: '其他待处理', kind: '', state: '', items: other })
    }
    return result
  }, [items])

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>收件箱</h1>
          <p className="muted">
            {data ? `共 ${count} 项待处理` : '只显示需要你处理的事项'}
            {staleHours !== undefined && (
              <span className="inbox-hint">
                {' '}
                · 超过 {staleHours} 小时未处理标记为「已超时」
              </span>
            )}
          </p>
        </div>
        <div className="page-head-actions">
          <RefreshBar
            lastUpdated={lastUpdated}
            refreshing={refreshing}
            error={error}
            onRefresh={refresh}
          />
        </div>
      </header>

      <ErrorNotice error={error} onRetry={refresh} />

      {!data && loading && <LoadingState label="正在加载收件箱…" />}
      {!data && !loading && error && (
        <EmptyState title="收件箱加载失败" hint="页面会自动重试；也可以点击上方「重试」。" />
      )}
      {data && items.length === 0 && (
        <EmptyState
          title="当前没有需要处理的事项"
          hint="需要你决策的评审和工作项验收会自动出现在这里。"
        />
      )}

      {groups.length > 0 && (
        <div className="inbox">
          {groups.map((group) => (
            <section className="inbox-group" key={group.key} aria-label={group.label}>
              <header className="inbox-group-head">
                <span>{group.label}</span>
                <span className="count">{group.items.length}</span>
              </header>
              <ul className="inbox-list">
                {group.items.map((item) => (
                  <li
                    key={`${item.kind}-${item.id}`}
                    className={item.stale ? 'inbox-row is-stale' : 'inbox-row'}
                  >
                    <span className={`badge inbox-type inbox-type-${item.kind}`}>
                      {TYPE_LABELS[item.kind] ?? item.kind}
                    </span>
                    <div className="inbox-row-main">
                      <span className="inbox-title" title={item.title}>
                        {item.title || '（无标题）'}
                      </span>
                      {item.reason && (
                        <span className="inbox-reason" title={item.reason}>
                          原因：{item.reason}
                        </span>
                      )}
                    </div>
                    <span className="code inbox-id" title={item.id}>
                      {item.id}
                    </span>
                    <span className="inbox-age">
                      等待 {formatDuration(Math.max(0, item.age_seconds) * 1000)}
                    </span>
                    {item.stale && <span className="inbox-stale">已超时</span>}
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </section>
  )
}
