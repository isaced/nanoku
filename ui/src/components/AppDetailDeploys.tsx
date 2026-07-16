import { useState } from 'react'
import { Button, Tag } from 'antd'
import { ChevronDown, ChevronRight, ScrollText } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Deploy } from '../lib/types'
import { LogViewer } from './LogViewer'

/**
 * AppDetailDeploys is the "Deploys" tab in the app detail drawer.
 * Renders the most-recent deploys with a collapsible per-deploy
 * log panel. The in-flight deploy (if any) auto-opens its log
 * panel on first mount so the operator sees what's running
 * without having to click — Coolify does the same.
 */
export function AppDetailDeploys({
  deploys,
  appId,
}: {
  deploys: Deploy[]
  appId: number
}) {
  const { t } = useTranslation('apps')
  // Local state for which deploy's log panel is open. By default we
  // auto-open the panel for any in-flight deploy so the operator
  // doesn't have to click to see what's happening — Coolify does
  // the same.
  const [openLogs, setOpenLogs] = useState<Set<number>>(() => {
    const inFlight = deploys.find((d) => d.status === 'running')
    return new Set(inFlight ? [inFlight.id] : [])
  })
  if (deploys.length === 0) {
    return (
      <div className="py-8 text-center text-[var(--fg-muted)] text-sm">
        {t('detail.noDeploys')}
      </div>
    )
  }
  return (
    <div className="space-y-2">
      {deploys.map((d) => {
        const isOpen = openLogs.has(d.id)
        return (
          <div
            key={d.id}
            className="border border-[var(--border)] rounded-md bg-[var(--bg-input)] text-sm"
          >
            <div className="p-3">
              <div className="flex items-center justify-between mb-1">
                <span className="mono text-xs">
                  #{d.id} · {d.trigger}
                  {d.commitSha && (
                    <span className="ml-2 text-[var(--fg-muted)]">{d.commitSha.slice(0, 7)}</span>
                  )}
                </span>
                <div className="flex items-center gap-2">
                  <Tag
                    color={
                      d.status === 'success'
                        ? 'green'
                        : d.status === 'failed'
                          ? 'red'
                          : d.status === 'running'
                            ? 'blue'
                            : 'default'
                    }
                  >
                    {d.status}
                  </Tag>
                  <Button
                    size="small"
                    type="text"
                    icon={isOpen ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                    onClick={() => {
                      setOpenLogs((prev) => {
                        const next = new Set(prev)
                        if (next.has(d.id)) {
                          next.delete(d.id)
                        } else {
                          next.add(d.id)
                        }
                        return next
                      })
                    }}
                  >
                    <ScrollText size={12} className="inline-block mr-1" />
                    {t('detail.logs')}
                  </Button>
                </div>
              </div>
              {d.commitMessage && (
                <div className="text-xs mt-1 line-clamp-2">
                  {d.commitMessage.split('\n')[0]}
                </div>
              )}
              {d.containerName && (
                <div className="mono text-xs text-[var(--fg-muted)] mt-1">
                  {t('detail.containerLabel')}: {d.containerName}
                </div>
              )}
              {d.error && (
                <div
                  className="text-xs text-[var(--danger)] mt-1 line-clamp-2"
                  title={d.error}
                >
                  {d.error}
                </div>
              )}
              <div className="text-xs text-[var(--fg-muted)] mt-1">
                {d.startedAt ?? d.createdAt}
                {d.finishedAt ? ` → ${d.finishedAt}` : ''}
              </div>
            </div>
            {isOpen && (
              <div className="px-3 pb-3">
                <LogViewer
                  url={`/api/apps/${appId}/deployments/${d.id}/logs/stream`}
                  heightClass="h-64"
                />
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
