import { Tag, Tooltip } from 'antd'
import { Activity, Cpu, MemoryStick } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { containerStatusMeta } from '../../lib/containerStatus'
import { formatBytes } from '../../lib/formatBytes'
import type { Dashboard } from '../../lib/types'
import { StatusTag } from '../StatusTag'
import { UsageBar } from './UsageBar'

/**
 * AppCard is one of the two-up cards in the "Apps" section of
 * the dashboard. Renders the app identity + status tag, the
 * linked site domains, and the live container stats (CPU, mem,
 * PIDs, network + block IO). A container without stats renders
 * a one-line hint instead of an empty card.
 */
export function AppCard({ app }: { app: Dashboard['apps'][number] }) {
  const { t } = useTranslation('dashboard')
  const c = app.container
  const stats = app.stats
  return (
    <div className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4">
      <div className="flex items-start justify-between mb-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="mono text-sm font-medium text-[var(--accent)]">
              {app.name}
            </span>
            <StatusTag
              {...containerStatusMeta(c?.status)}
              size="small"
            />
          </div>
          <div className="mono text-xs text-[var(--fg-muted)] mt-0.5 truncate">
            {app.image}
          </div>
        </div>
        <Tooltip title={t('appCard.liveStats')}>
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
          {t('appCard.noStats')}
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
            <span title={t('appCard.processCount')}>
              {t('appCard.processCountLabel')}:{' '}
              <span className="text-[var(--fg)]">{stats.pids}</span>
            </span>
            <span title={t('appCard.networkIo')}>
              {t('appCard.networkLabel')}: {formatBytes(stats.netRxBytes)} ↓ ·{' '}
              {formatBytes(stats.netTxBytes)} ↑
            </span>
            <span title={t('appCard.blockIo')}>
              {t('appCard.diskLabel')}: {formatBytes(stats.blockReadBytes)} r ·{' '}
              {formatBytes(stats.blockWriteBytes)} w
            </span>
          </div>
        </div>
      )}
    </div>
  )
}
