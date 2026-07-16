import { Progress } from 'antd'
import { MemoryStick } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatBytes } from '../../lib/formatBytes'
import type { Dashboard } from '../../lib/types'

/**
 * MemCard is the memory summary card. Lives in its own row
 * below the four StatTiles because it spans the full width —
 * total memory / limit + a single Progress bar coloured by
 * usage threshold. The thresholds (60% accent, 80% danger)
 * match the UsageBar cpu thresholds so the eye learns one
 * mapping.
 */
export function MemCard({ summary }: { summary: Dashboard['summary'] }) {
  const { t } = useTranslation('dashboard')
  const pct = summary.totalMemPerc
  return (
    <div className="md:col-span-3 border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2 text-[var(--fg-muted)]">
          <MemoryStick size={14} />
          <span className="eyebrow">
            {t('memoryCard.title', { count: summary.containerCount })}
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
        {t('memoryCard.ofHost', { pct: pct.toFixed(2) })}
      </div>
    </div>
  )
}
