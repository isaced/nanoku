import type { ReactNode } from 'react'

/**
 * StatTile is one of the four big numbers in the dashboard
 * header row (sites / apps / containers / total CPU). Renders
 * a label + value + optional sub-line (e.g. "3 enabled" under
 * the sites count). The sub-line accepts JSX so the same tile
 * shape can host either a plain string or an inline UsageBar.
 */
export function StatTile({
  icon,
  label,
  value,
  sub,
}: {
  icon: ReactNode
  label: string
  value: number | string
  sub?: ReactNode
}) {
  return (
    <div className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4">
      <div className="flex items-center gap-2 text-[var(--fg-muted)] mb-2">
        {icon}
        <span className="eyebrow">{label}</span>
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
