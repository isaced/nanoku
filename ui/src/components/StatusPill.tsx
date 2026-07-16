import type { HTMLAttributes, ReactNode } from 'react'

/**
 * StatusPill is the lightweight status chip used for inline state
 * markers (deploy status, "trigger active", "read-only", "disabled").
 * Heavier table/column status rendering lives in `StatusTag`, which
 * carries a leading icon and full variant → i18n mapping.
 *
 * The visual treatment comes from the `.status-pill` + `.status-pill-*`
 * utility classes in `styles.css` — small, padded, with a colour-mixed
 * border so it reads as "designed" rather than the stock antd `Tag`.
 */
export type StatusPillVariant =
  | 'success'
  | 'warn'
  | 'danger'
  | 'muted'
  | 'accent'

export function StatusPill({
  variant,
  children,
  className,
  ...rest
}: {
  variant: StatusPillVariant
  children: ReactNode
  className?: string
} & Omit<HTMLAttributes<HTMLSpanElement>, 'children' | 'className'>) {
  return (
    <span
      className={`status-pill status-pill-${variant} ${className ?? ''}`}
      {...rest}
    >
      {children}
    </span>
  )
}
