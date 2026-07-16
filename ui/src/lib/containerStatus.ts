import type { ComponentType } from 'react'
import {
  CircleAlert,
  CircleCheck,
  CircleDashed,
  CirclePause,
  CircleX,
  type LucideProps,
} from 'lucide-react'

/**
 * Visual variants for status pills/tags. The variant decides the
 * leading icon, the antd Tag color, and the emphasis. Use a single
 * variant per kind of state across the whole UI so the eye learns
 * the mapping.
 */
export type StatusVariant =
  | 'success'   // green check
  | 'muted'     // gray dash
  | 'warn'      // blue-ish, transient state
  | 'paused'    // orange
  | 'danger'    // red X
  | 'configured' // blue check (subtler than success — "set up but not necessarily running")

export const STATUS_VARIANT_ICON: Record<
  StatusVariant,
  ComponentType<LucideProps>
> = {
  success: CircleCheck,
  muted: CircleDashed,
  warn: CircleAlert,
  paused: CirclePause,
  danger: CircleX,
  configured: CircleCheck,
}

/**
 * antd Tag color for a variant. 'default' renders the muted border
 * + no background; we let antd pick the right shade for both light
 * and dark mode (the Tag component handles theme switching).
 */
export const STATUS_VARIANT_TAG_COLOR: Record<StatusVariant, string> = {
  success: 'green',
  muted: 'default',
  warn: 'blue',
  paused: 'orange',
  danger: 'red',
  configured: 'blue',
}

/**
 * ContainerStatusMeta is the rendering contract for any UI that
 * wants to display a Docker container's status. The status string
 * is opaque here — callers pass the server's value verbatim and
 * get back enough metadata to render a consistent pill/tag.
 */
export type ContainerStatusMeta = {
  variant: StatusVariant
  /** i18n key under common:status (e.g. "running", "notDeployed").
   * Null means "show the raw status string verbatim" (used for
   * statuses the user should see as-is, e.g. "dead", "removing"). */
  i18nKey: string | null
  /** the raw string we got in — useful for the `not_deployed`
   * sentinel so callers can show the original value if they want. */
  raw: string
}

const META: Record<string, Omit<ContainerStatusMeta, 'raw'>> = {
  running: { variant: 'success', i18nKey: 'running' },
  restarting: { variant: 'warn', i18nKey: 'restarting' },
  paused: { variant: 'paused', i18nKey: 'paused' },
  exited: { variant: 'muted', i18nKey: 'exited' },
  created: { variant: 'muted', i18nKey: 'created' },
  dead: { variant: 'danger', i18nKey: null },
  removing: { variant: 'muted', i18nKey: null },
  // sentinel: app has no container yet
  not_deployed: { variant: 'muted', i18nKey: 'notDeployed' },
}

const UNKNOWN: Omit<ContainerStatusMeta, 'raw'> = {
  variant: 'danger',
  i18nKey: null,
}

/**
 * containerStatusMeta normalises any string the server might send
 * (including a null/undefined app.container) into a render contract.
 * Unknown values are surfaced as danger so the operator notices
 * rather than silently mapping to "default".
 */
export function containerStatusMeta(
  status: string | null | undefined,
): ContainerStatusMeta {
  if (status == null || status === '') {
    return { raw: 'not_deployed', ...META.not_deployed }
  }
  const known = META[status]
  if (known) return { raw: status, ...known }
  return { raw: status, ...UNKNOWN }
}
