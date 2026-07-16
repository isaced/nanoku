import { formatBytes } from '../../lib/formatBytes'

/**
 * UsageBar is a compact progress bar + numeric label. Two modes:
 *
 *   - color="cpu"   — percentage in/out, thresholds at 50% (accent)
 *     and 80% (danger) drive the bar color
 *   - color="mem"   — bytes in/out, single accent color
 *
 * The width is capped at 100% even if `value > max` — overflow
 * is communicated by the numeric label, not by a bar that
 * runs off the row. `compact` switches between a 6px and 8px
 * bar height (the dense AppCard uses compact, the wider
 * running-containers row uses default).
 */
export function UsageBar({
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
