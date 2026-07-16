import { Button, Empty, Switch, Table, Tooltip } from 'antd'
import {
  Container as ContainerIcon,
  Cpu,
  Globe,
  Layers,
  Pause,
  Play,
  RefreshCw,
} from 'lucide-react'
import { Suspense, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSuspenseDashboard, useSuspenseStatus } from '../lib/hooks'
import { formatBytes } from '../lib/formatBytes'
import { RouteFallback } from './RouteFallback'
import { SiteStatusBadge } from './SiteStatusBadge'
import { StatusPill } from './StatusPill'
import { AppCard } from './dashboard/AppCard'
import { MemCard } from './dashboard/MemCard'
import { StatTile } from './dashboard/StatTile'
import { UsageBar } from './dashboard/UsageBar'

const AUTO_REFRESH_MS = 5000

export function DashboardPage() {
  return (
    <Suspense fallback={<RouteFallback variant="page" />}>
      <DashboardContent />
    </Suspense>
  )
}

function DashboardContent() {
  const { t } = useTranslation('dashboard')
  const [autoRefresh, setAutoRefresh] = useState(true)

  const dashboardQuery = useSuspenseDashboard()
  const statusQuery = useSuspenseStatus()

  const data = dashboardQuery.data

  useEffect(() => {
    if (!autoRefresh) return
    const id = setInterval(() => {
      void dashboardQuery.refetch()
      void statusQuery.refetch()
    }, AUTO_REFRESH_MS)
    return () => clearInterval(id)
  }, [autoRefresh, dashboardQuery, statusQuery])

  const fetching =
    dashboardQuery.isFetching || statusQuery.isFetching

  const reload = () => {
    void dashboardQuery.refetch()
    void statusQuery.refetch()
  }

  const lastUpdated = dashboardQuery.dataUpdatedAt
    ? new Date(dashboardQuery.dataUpdatedAt)
    : null

  const summary = data.summary
  const apps = data.apps
  const sites = data.sites
  const stats = data.stats
  const runningContainers = stats.filter((s) => s.pids > 0)

  return (
    <div className="flex-1 flex flex-col">
      <main className="flex-1 px-8 py-8 max-w-6xl w-full mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="display-1">{t('title')}</h1>
            <p className="text-sm text-[var(--fg-muted)] mt-1">
              {t('subtitle')}
              {lastUpdated && (
                <span className="ml-2">
                  ·{' '}
                  {t('updatedAt', { time: lastUpdated.toLocaleTimeString() })}
                </span>
              )}
            </p>
          </div>
          <div className="flex items-center gap-3">
            <Tooltip title={t('autoRefresh')}>
              <div className="flex items-center gap-2 text-xs text-[var(--fg-muted)]">
                {autoRefresh ? <Play size={12} /> : <Pause size={12} />}
                <span>{t('auto')}</span>
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
                  className={fetching ? 'animate-spin' : ''}
                />
              }
              onClick={reload}
              loading={fetching}
            >
              {t('refresh')}
            </Button>
          </div>
        </div>

        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
          <StatTile
            icon={<Globe size={14} />}
            label={t('stats.sites')}
            value={summary.totalSites}
            sub={t('stats.enabledSites', { count: summary.enabledSites })}
          />
          <StatTile
            icon={<Layers size={14} />}
            label={t('stats.apps')}
            value={summary.totalApps}
            sub={t('stats.runningApps', { count: summary.runningApps })}
          />
          <StatTile
            icon={<ContainerIcon size={14} />}
            label={t('stats.containers')}
            value={summary.containerCount}
            sub={t('stats.activeContainers', {
              count: stats.filter((s) => s.pids > 0).length,
            })}
          />
          <StatTile
            icon={<Cpu size={14} />}
            label={t('stats.totalCpu')}
            value={`${summary.totalCpuPerc.toFixed(1)}%`}
            sub={
              <UsageBar
                value={summary.totalCpuPerc}
                max={100 * Math.max(summary.containerCount, 1)}
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
          <h2 className="eyebrow">
            {t('sections.apps')}
          </h2>
          {apps.length === 0 ? (
            <Empty
              description={t('empty.noApps')}
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
          <h2 className="eyebrow">
            {t('sections.sites')}
          </h2>
          <div className="surface-card overflow-hidden">
            <Table
              dataSource={sites}
              rowKey="id"
              pagination={false}
              size="small"
              locale={{ emptyText: t('empty.noSites') }}
              columns={[
                {
                  title: t('table.domain'),
                  dataIndex: 'domain',
                  render: (d: string, row) => (
                    <div className="flex items-center gap-2">
                      <span className="mono text-sm">{d}</span>
                      {!row.enabled && (
                        <StatusPill variant="muted">
                          {t('common:status.disabled', { ns: 'common' })}
                        </StatusPill>
                      )}
                    </div>
                  ),
                },
                {
                  title: t('table.app'),
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
                  title: t('table.upstream'),
                  dataIndex: 'upstream',
                  render: (u: string) => (
                    <span className="mono text-xs text-[var(--fg-muted)]">
                      {u}
                    </span>
                  ),
                },
                {
                  title: t('table.status'),
                  dataIndex: 'enabled',
                  width: 60,
                  render: (e: boolean) => <SiteStatusBadge enabled={e} />,
                },
              ]}
            />
          </div>
        </section>

        <section>
          <h2 className="eyebrow">
            {t('sections.runningContainers')}
          </h2>
          <div className="surface-card overflow-hidden">
            <Table
              dataSource={runningContainers}
              rowKey="name"
              pagination={false}
              size="small"
              locale={{ emptyText: t('empty.noContainers') }}
              columns={[
                {
                  title: t('table.name'),
                  dataIndex: 'name',
                  render: (n: string) => (
                    <span className="mono text-xs">{n}</span>
                  ),
                },
                {
                  title: t('table.cpu'),
                  dataIndex: 'cpuPerc',
                  width: 180,
                  render: (v: number) => (
                    <UsageBar value={v} max={100} color="cpu" />
                  ),
                },
                {
                  title: t('table.memory'),
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
                  title: t('table.pids'),
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
                  title: t('table.netIo'),
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
