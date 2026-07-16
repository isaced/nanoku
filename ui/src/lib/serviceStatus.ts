import type { ContainerStatusMeta } from './containerStatus'
import type { StatusVariant } from './containerStatus'

/**
 * ServiceStatus is the contract for non-container service states
 * (docker daemon connectivity, caddy container, nanoku self
 * container). Different value space from ContainerStatus — a caddy
 * "not_found" doesn't make sense for a docker container, and a
 * docker container can be "restarting" in a way caddy can't.
 */
export type ServiceStatus =
  | 'running'
  | 'configured'
  | 'not_found'
  | 'skipped'
  | 'unknown'

const META: Record<ServiceStatus, { variant: StatusVariant; i18nKey: string | null }> = {
  running: { variant: 'success', i18nKey: 'running' },
  configured: { variant: 'configured', i18nKey: 'configured' },
  not_found: { variant: 'muted', i18nKey: 'notFound' },
  skipped: { variant: 'muted', i18nKey: 'skipped' },
  unknown: { variant: 'danger', i18nKey: null },
}

export function serviceStatusMeta(
  status: string | null | undefined,
): ContainerStatusMeta & { key: ServiceStatus } {
  const key: ServiceStatus =
    status === 'running' ||
    status === 'configured' ||
    status === 'not_found' ||
    status === 'skipped'
      ? status
      : 'unknown'
  return { raw: status ?? 'unknown', key, ...META[key] }
}
