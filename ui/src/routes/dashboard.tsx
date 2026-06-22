import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import {
  App,
  Button,
  Empty,
  Progress,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd'
import {
  Activity,
  CircleCheck,
  CircleDashed,
  CircleX,
  Container as ContainerIcon,
  Cpu,
  Globe,
  Layers,
  MemoryStick,
  Pause,
  Play,
  Power,
  RefreshCw,
  Square,
} from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { clearCredentials, getCredentials } from '../lib/auth'
import type { Dashboard, Status } from '../lib/types'
import { TopNav } from '../components/TopNav'

export const Route = createFileRoute('/dashboard')({
  beforeLoad: () => {
    if (!getCredentials()) {
      throw redirect({ to: '/login' })
    }
  },
  component: DashboardPage,
})

const AUTO_REFRESH_MS = 5000

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1,
  )
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

function DashboardPage() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const [data, setData] = useState<Dashboard | null>(null)
  const [status, setStatus] = useState<Status | null>(null)
  const [loading, setLoading] = useState(true)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const timer = useRef<number | null>(null)

  const reload = useCallback(async () => {
    if (!getCredentials()) {
      navigate({ to: '/login' })
      return
    }
    try {
      const [d, st] = await Promise.all([api.dashboard(), api.status()])
      setData(d)
      setStatus(st)
      setLastUpdated(new Date())
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        clearCredentials()
        navigate({ to: '/login' })
      } else {
        message.error((err as Error).message)
      }
    } finally {
      setLoading(false)
    }
  }, [message, navigate])

  useEffect(() => {
    void reload()
  }, [reload])

  useEffect(() => {
    if (timer.current) {
      window.clearInterval(timer.current)
      timer.current = null
    }
    if (autoRefresh) {
      timer.current = window.setInterval(() => {
        void reload()
      }, AUTO_REFRESH_MS)
    }
    return () => {
      if (timer.current) {
        window.clearInterval(timer.current)
        timer.current = null
      }
    }
  }, [autoRefresh, reload])

  const summary = data?.summary
  const apps = data?.apps ?? []
  const sites = data?.sites ?? []
  const stats = data?.stats ?? [] as ContainerStats[]

  return (
    <div className="flex-1 flex flex-col">
      <TopNav status={status} onRefresh={reload} loading={loading} />

      <main className="flex-1 px-8 py-8 max-w-6xl w-full mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-medium tracking-tight">Dashboard</h1>
            <p className="text-sm text-[var(--fg-muted)] mt-1">
              Sites, apps, and live container resource usage.
              {lastUpdated && (
                <span className="ml-2">
                  · updated {lastUpdated.toLocaleTimeString()}
                </span>
              )}
            </p>
          </div>
          <div className="flex items-center gap-3">
            <Tooltip title="Auto-refresh every 5s">
              <div className="flex items-center gap-2 text-xs text-[var(--fg-muted)]">
                {autoRefresh ? <Play size={12} /> : <Pause size={12} />}
                <span>Auto</span>
                <Switch
                  size="small"
                  checked={autoRefresh}
                  onChange={setAutoRefresh}
                />
              </div>
            </Tooltip>
            <Button
              icon={
                <RefreshCw
                  size={13}
                  className={loading ? 'animate-spin' : ''}
                />
              }
              onClick={() => void reload()}
              loading={loading}
            >
              Refresh
            </Button>
          </div>
        </div>

        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
          <StatTile
            icon={<Globe size={14} />}
            label="Sites"
            value={summary?.totalSites ?? 0}
            sub={`${summary?.enabledSites ?? 0} enabled`}
          />
          <StatTile
            icon={<Layers size={14} />}
            label="Apps"
            value={summary?.totalApps ?? 0}
            sub={`${summary?.runningApps ?? 0} running`}
          />
          <StatTile
            icon={<ContainerIcon size={14} />}
            label="Containers"
            value={summary?.containerCount ?? 0}
            sub={`${stats.filter((s) => s.pids > 0).length} active`}
          />
          <StatTile
            icon={<Cpu size={14} />}
            label="Total CPU"
            value={`${(summary?.totalCpuPerc ?? 0).toFixed(1)}%`}
            sub={
              <UsageBar
                value={summary?.totalCpuPerc ?? 0}
                max={100 * Math.max(summary?.containerCount ?? 1, 1)}
                color="cpu"
                compact
              />
            }
          />
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-8">
          <MemCard summary={summary} />
        </div>

        <section className="mb-10">
          <h2 className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-3">
            Apps
          </h2>
          {apps.length === 0 ? (
            <Empty
              description="No apps yet"
              className="!bg-[var(--bg-elevated)] !border !border-[var(--border)] !rounded-lg !py-12"
            />
          ) : (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              {apps.map((a) => (
                <AppCard key={a.id} app={a} />
              ))}
            </div>
          )}
        </section>

        <section className="mb-10">
          <h2 className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-3">
            Sites
          </h2>
          <div className="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--bg-elevated)]">
            <Table
              dataSource={sites}
              rowKey="id"
              pagination={false}
              size="small"
              locale={{ emptyText: 'No sites' }}
              columns={[
                {
                  title: 'Domain',
                  dataIndex: 'domain',
                  render: (d: string, row) => (
                    <div className="flex items-center gap-2">
                      <span className="mono text-sm">{d}</span>
                      {!row.enabled && (
                        <Tag className="!m-0">disabled</Tag>
                      )}
                    </div>
                  ),
                },
                {
                  title: 'App',
                  dataIndex: 'appName',
                  render: (n?: string) =>
                    n ? (
                      <span className="mono text-xs text-[var(--fg-muted)]">
                        {n}
                      </span>
                    ) : (
                      <span className="text-xs text-[var(--fg-muted)]">—</span>
                    ),
                },
                {
                  title: 'Upstream',
                  dataIndex: 'upstream',
                  render: (u: string) => (
                    <span className="mono text-xs text-[var(--fg-muted)]">
                      {u}
                    </span>
                  ),
                },
                {
                  title: '',
                  dataIndex: 'enabled',
                  width: 60,
                  align: 'right',
                  render: (e: boolean) => (
                    <span
                      className={
                        e
                          ? 'text-[var(--success)]'
                          : 'text-[var(--fg-muted)]'
                      }
                    >
                      <Power size={13} />
                    </span>
                  ),
                },
              ]}
            />
          </div>
        </section>

        <section>
          <h2 className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-3">
            All containers
          </h2>
          <div className="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--bg-elevated)]">
            <Table
              dataSource={stats}
              rowKey="name"
              pagination={false}
              size="small"
              locale={{ emptyText: 'No containers running' }}
              columns={[
                {
                  title: 'Name',
                  dataIndex: 'name',
                  render: (n: string) => (
                    <span className="mono text-xs">{n}</span>
                  ),
                },
                {
                  title: 'CPU',
                  dataIndex: 'cpuPerc',
                  width: 180,
                  render: (v: number) => (
                    <UsageBar value={v} max={100} color="cpu" />
                  ),
                },
                {
                  title: 'Memory',
                  dataIndex: 'memUsedBytes',
                  width: 240,
                  render: (_: number, row) => (
                    <UsageBar
                      value={row.memUsedBytes}
                      max={Math.max(row.memLimitBytes, 1)}
                      color="mem"
                      label={`${formatBytes(row.memUsedBytes)} / ${formatBytes(row.memLimitBytes)}`}
                    />
                  ),
                },
                {
                  title: 'PIDs',
                  dataIndex: 'pids',
                  width: 60,
                  align: 'right',
                  render: (p: number) => (
                    <span className="mono text-xs text-[var(--fg-muted)]">
                      {p}
                    </span>
                  ),
                },
                {
                  title: 'Net I/O',
                  width: 160,
                  render: (_: unknown, row) => (
                    <span className="mono text-xs text-[var(--fg-muted)]">
                      {formatBytes(row.netRxBytes)} ↓ · {formatBytes(row.netTxBytes)} ↑
                    </span>
                  ),
                },
              ]}
            />
          </div>
        </section>
      </main>
    </div>
  )
}

function StatTile({
  icon,
  label,
  value,
  sub,
}: {
  icon: React.ReactNode
  label: string
  value: number | string
  sub?: React.ReactNode
}) {
  return (
    <div className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4">
      <div className="flex items-center gap-2 text-[var(--fg-muted)] mb-2">
        {icon}
        <span className="text-[10px] tracking-widest uppercase">{label}</span>
      </div>
      <div className="text-2xl font-medium tracking-tight text-[var(--fg)] mono">
        {value}
      </div>
      {sub && (
        <div className="text-xs text-[var(--fg-muted)] mt-1">{sub}</div>
      )}
    </div>
  )
}

function MemCard({ summary }: { summary: Dashboard['summary'] | undefined }) {
  if (!summary) return null
  const pct = summary.totalMemPerc
  return (
    <div className="md:col-span-3 border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2 text-[var(--fg-muted)]">
          <MemoryStick size={14} />
          <span className="text-[10px] tracking-widest uppercase">
            Total memory across {summary.containerCount} container
            {summary.containerCount === 1 ? '' : 's'}
          </span>
        </div>
        <span className="mono text-sm text-[var(--fg)]">
          {formatBytes(summary.totalMemBytes)} /{' '}
          {formatBytes(summary.totalMemLimitBytes)}
        </span>
      </div>
      <Progress
        percent={Math.min(pct, 100)}
        showInfo={false}
        strokeColor={
          pct > 80 ? 'var(--danger)' : pct > 60 ? 'var(--accent)' : 'var(--success)'
        }
        trailColor="var(--bg-input)"
        size="small"
      />
      <div className="text-xs text-[var(--fg-muted)] mt-1">
        {pct.toFixed(2)}% of host memory limit
      </div>
    </div>
  )
}

function UsageBar({
  value,
  max,
  color,
  label,
  compact,
}: {
  value: number
  max: number
  color: 'cpu' | 'mem'
  label?: string
  compact?: boolean
}) {
  const pct = max > 0 ? (value / max) * 100 : 0
  const display = label ?? (color === 'cpu' ? `${value.toFixed(1)}%` : formatBytes(value))
  const barColor =
    color === 'cpu'
      ? pct > 80
        ? 'var(--danger)'
        : pct > 50
          ? 'var(--accent)'
          : 'var(--success)'
      : 'var(--accent)'
  return (
    <div className="flex items-center gap-2">
      <div
        className={`flex-1 ${compact ? 'h-1.5' : 'h-2'} rounded-full bg-[var(--bg-input)] overflow-hidden`}
      >
        <div
          className="h-full rounded-full transition-all"
          style={{ width: `${Math.min(pct, 100)}%`, background: barColor }}
        />
      </div>
      <span className="mono text-xs text-[var(--fg-muted)] whitespace-nowrap">
        {display}
      </span>
    </div>
  )
}

function AppCard({
  app,
}: {
  app: Dashboard['apps'][number]
}) {
  const c = app.container
  const stats = app.stats
  const status = c?.status ?? 'not deployed'
  return (
    <div className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4">
      <div className="flex items-start justify-between mb-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="mono text-sm font-medium text-[var(--accent)]">
              {app.name}
            </span>
            <StatusPill status={status} />
          </div>
          <div className="mono text-xs text-[var(--fg-muted)] mt-0.5 truncate">
            {app.image}
          </div>
        </div>
        <Tooltip title="Live stats">
          <Activity size={14} className="text-[var(--fg-muted)] mt-1" />
        </Tooltip>
      </div>

      {app.siteDomains.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 mb-3">
          {app.siteDomains.map((d) => (
            <Tag key={d} className="!m-0 mono text-[10px]">
              {d}
            </Tag>
          ))}
        </div>
      )}

      {!stats ? (
        <div className="text-xs text-[var(--fg-muted)] py-3">
          No stats — container not running
        </div>
      ) : (
        <div className="space-y-2.5">
          <div>
            <div className="flex items-center justify-between text-[10px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
              <span className="inline-flex items-center gap-1">
                <Cpu size={10} /> CPU
              </span>
              <span className="mono">{stats.cpuPerc.toFixed(1)}%</span>
            </div>
            <UsageBar
              value={stats.cpuPerc}
              max={100}
              color="cpu"
              compact
            />
          </div>
          <div>
            <div className="flex items-center justify-between text-[10px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
              <span className="inline-flex items-center gap-1">
                <MemoryStick size={10} /> MEM
              </span>
              <span className="mono">
                {formatBytes(stats.memUsedBytes)} /{' '}
                {formatBytes(stats.memLimitBytes)}
              </span>
            </div>
            <UsageBar
              value={stats.memUsedBytes}
              max={Math.max(stats.memLimitBytes, 1)}
              color="mem"
              compact
            />
          </div>
          <div className="flex items-center gap-4 text-[11px] text-[var(--fg-muted)] mono pt-1">
            <span title="Process count">
              pids: <span className="text-[var(--fg)]">{stats.pids}</span>
            </span>
            <span title="Network I/O">
              net: {formatBytes(stats.netRxBytes)} ↓ ·{' '}
              {formatBytes(stats.netTxBytes)} ↑
            </span>
            <span title="Block I/O">
              disk: {formatBytes(stats.blockReadBytes)} r ·{' '}
              {formatBytes(stats.blockWriteBytes)} w
            </span>
          </div>
        </div>
      )}
    </div>
  )
}

function StatusPill({ status }: { status: string }) {
  if (status === 'running') {
    return (
      <Tag color="green" className="!m-0">
        <span className="inline-flex items-center gap-1">
          <CircleCheck size={10} /> running
        </span>
      </Tag>
    )
  }
  if (status === 'exited' || status === 'created' || status === 'not deployed') {
    return (
      <Tag className="!m-0">
        <span className="inline-flex items-center gap-1">
          <CircleDashed size={10} /> {status}
        </span>
      </Tag>
    )
  }
  return (
    <Tag color="red" className="!m-0">
      <span className="inline-flex items-center gap-1">
        <CircleX size={10} /> {status}
      </span>
    </Tag>
  )
}
