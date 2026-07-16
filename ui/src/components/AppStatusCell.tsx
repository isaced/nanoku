import { containerStatusMeta } from '../lib/containerStatus'
import type { App as AppType } from '../lib/types'
import { StatusTag } from './StatusTag'

/**
 * AppStatusCell is the "Status" column on the apps list table.
 * Renders a <StatusTag> for the container's status (using the
 * shared meta table so the visual language matches the detail
 * drawer + dashboard) plus the container name as a mono
 * truncated suffix. Falls through to the same "not deployed"
 * tag when the app has no container yet.
 */
export function AppStatusCell({ app }: { app: AppType }) {
  const meta = containerStatusMeta(app.container?.status)
  return (
    <span className="inline-flex items-center gap-1.5">
      <StatusTag {...meta} size="small" />
      {app.container && (
        <span className="mono text-[11px] text-[var(--fg-muted)] max-w-[8rem] truncate">
          {app.container.name}
        </span>
      )}
    </span>
  )
}
