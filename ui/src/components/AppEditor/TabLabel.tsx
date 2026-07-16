import type { ReactNode } from 'react'

// TabLabel renders an icon + text with an optional count chip or status
// tag. Keeping it tiny and consistent makes every tab read the same way
// and lets the count/tag surface at-a-glance state without opening the
// tab (e.g. "Environment 3", "Registry · configured").
export function TabLabel({
  icon,
  text,
  count,
  tag,
}: {
  icon: ReactNode
  text: string
  count?: number
  tag?: string
}) {
  return (
    <span className="inline-flex items-center gap-1.5">
      {icon}
      <span>{text}</span>
      {count != null && count > 0 && (
        <span className="inline-flex min-w-[16px] h-4 items-center justify-center rounded-full bg-[var(--accent)]/15 px-1 text-[10px] font-medium text-[var(--accent)]">
          {count}
        </span>
      )}
      {tag && (
        <span className="inline-flex items-center rounded-full bg-[var(--accent)]/10 px-1.5 py-[1px] text-[10px] text-[var(--accent)]">
          {tag}
        </span>
      )}
    </span>
  )
}
