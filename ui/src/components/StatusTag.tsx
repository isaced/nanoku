import { Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import {
  STATUS_VARIANT_ICON,
  STATUS_VARIANT_TAG_COLOR,
  type StatusVariant,
} from '../lib/containerStatus'

/**
 * StatusTag is the single rendering contract for "this thing has a
 * state" — used for Docker container statuses (running / exited /
 * created / dead / restarting / paused / not_deployed) AND for
 * service-level statuses (docker daemon / caddy / nanoku self).
 *
 * Two ways to feed it:
 *   - Pre-resolved meta from `containerStatusMeta()` /
 *     `serviceStatusMeta()` — preferred for typed callers
 *   - Inline `variant` + `i18nKey` or `label` for one-off uses
 *
 * Trailing `children` render after the tag with a small gap —
 * used for the container name suffix in the apps list
 * (`<StatusTag …>nanoku-xxx-1</StatusTag>`).
 */
export type StatusTagProps = {
  variant: StatusVariant
  /** Translate `common:status.${i18nKey}` and use that as the label.
   * Wins over `label` when both are set. */
  i18nKey?: string | null
  /** Raw label, used when no i18n key applies. */
  label?: string
  /** Render the leading icon (default true). */
  withIcon?: boolean
  /** Override the antd Tag color (defaults to the variant's color). */
  color?: string
  /** Tighter padding for inline use. */
  size?: 'default' | 'small'
  /** Trailing text (e.g. container name). */
  children?: ReactNode
  className?: string
}

export function StatusTag({
  variant,
  i18nKey,
  label,
  withIcon = true,
  color,
  size = 'default',
  children,
  className,
  ...rest
}: StatusTagProps & { 'data-testid'?: string }) {
  const { t } = useTranslation('common')
  const Icon = STATUS_VARIANT_ICON[variant]
  const resolvedColor = color ?? STATUS_VARIANT_TAG_COLOR[variant]
  const resolvedLabel =
    i18nKey != null ? t(`status.${i18nKey}`) : label ?? ''
  return (
    <Tag
      color={resolvedColor}
      className={`!m-0 ${size === 'small' ? '!text-[10px]' : ''} ${className ?? ''}`}
      {...rest}
    >
      <span className="inline-flex items-center gap-1">
        {withIcon && <Icon size={size === 'small' ? 10 : 12} />}
        {resolvedLabel}
        {children}
      </span>
    </Tag>
  )
}
