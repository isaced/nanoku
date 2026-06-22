import { useLocation, useNavigate } from '@tanstack/react-router'
import { Button, Layout, Menu, Tooltip } from 'antd'
import { CircleCheck, CircleDashed, CircleX, RefreshCw } from 'lucide-react'
import type { Status } from '../lib/types'

const navItems = [
  { key: '/dashboard', label: 'Dashboard' },
  { key: '/sites', label: 'Sites' },
  { key: '/apps', label: 'Apps' },
  { key: '/system', label: 'System' },
] as const

type NavKey = (typeof navItems)[number]['key']

export function TopNav({
  status,
  onRefresh,
  loading,
}: {
  status: Status | null
  onRefresh: () => void
  loading: boolean
}) {
  const navigate = useNavigate()
  const location = useLocation()

  return (
    <Layout.Header className="!bg-white border-b border-[var(--border)] flex items-center gap-6">
      <div className="flex items-center gap-2">
        <div className="size-2 rounded-full bg-[var(--accent)]" />
        <span className="mono text-sm tracking-wider text-[var(--fg)]">nanoku</span>
      </div>
      <Menu
        mode="horizontal"
        selectedKeys={[location.pathname as NavKey]}
        onClick={({ key }) => navigate({ to: key as NavKey })}
        items={navItems.map((i) => ({ ...i }))}
        className="flex-1 !min-w-0 !border-0 !bg-transparent"
      />
      <div className="flex items-center gap-3 text-xs">
        <DockerBadge status={status} />
        <CaddyBadge status={status} />
        <Tooltip title="Refresh">
          <Button
            type="text"
            size="small"
            icon={
              <RefreshCw
                size={13}
                className={loading ? 'animate-spin' : ''}
              />
            }
            onClick={onRefresh}
          />
        </Tooltip>
      </div>
    </Layout.Header>
  )
}

export function DockerBadge({ status }: { status: Status | null }) {
  if (!status) return null
  if (status.dockerConnected) {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--success)]">
        <CircleCheck size={12} />
        <span className="mono">docker</span>
      </span>
    )
  }
  return (
    <Tooltip title="Docker daemon unreachable">
      <span className="inline-flex items-center gap-1.5 text-[var(--danger)]">
        <CircleX size={12} />
        <span className="mono">docker</span>
      </span>
    </Tooltip>
  )
}

export function CaddyBadge({ status }: { status: Status | null }) {
  if (!status) return null
  const s = status.caddyStatus
  if (s === 'running') {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--success)]">
        <CircleCheck size={12} />
        <span className="mono">caddy</span>
      </span>
    )
  }
  if (s === 'not_found' || s === 'skipped') {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--fg-muted)]">
        <CircleDashed size={12} />
        <span className="mono">caddy · {s}</span>
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-[var(--danger)]">
      <CircleX size={12} />
      <span className="mono">caddy · {s}</span>
    </span>
  )
}
