import type { App } from './types'

/**
 * appOptionLabel renders the row shown in the Site editor's
 * "Linked app" select. Docker apps get the legacy
 * `name · image :port` shape; compose apps describe themselves
 * by service count so the operator can see at a glance whether
 * the stack has 1 service (auto-pick) or N services (will need
 * a service pick). The "(no app · use custom upstream)" entry is
 * appended as a sentinel in the editor; this function is only
 * for the per-app rows.
 */
export function appOptionLabel(a: App): string {
  if (a.deployMethod === 'docker') {
    return `${a.name} · docker · ${a.image || '(no image)'} :${a.port}`
  }
  const n = a.exposedPorts?.length ?? 0
  if (n === 0) {
    return `${a.name} · compose · (no exposed ports)`
  }
  if (n === 1) {
    return `${a.name} · compose · ${n} service: ${a.exposedPorts![0].name}:${a.exposedPorts![0].port}`
  }
  return `${a.name} · compose · ${n} services`
}

/**
 * serviceOptionLabel formats a single compose service for the
 * service select. Mirrors appOptionLabel's terseness.
 */
export function serviceOptionLabel(name: string, port: number): string {
  return `${name} · port ${port}`
}

/**
 * computeUpstreamForAppUI mirrors the server's upstreamFor
 * (internal/api/sites.go) so the form can preview the locked
 * upstream the moment the operator picks an app. Without it,
 * the disabled upstream input stays empty after `appId` changes
 * — the user sees a blank field, has no way to verify the value
 * the server will compute on submit, and for multi-service compose
 * the server would 400 on `appService` until they noticed.
 *
 * The compose branch uses the network alias `nanoku-<app>-<service>`
 * (no -1 suffix, no container name dependency) — the alias is the
 * stable routing identity and is what Caddy's reverse_proxy entry
 * will resolve to via Docker network DNS.
 */
export function computeUpstreamForAppUI(
  app: App | undefined,
  appService: string | undefined,
): string {
  if (!app) return ''
  if (app.deployMethod === 'compose') {
    if (!appService) return ''
    const ports = app.exposedPorts ?? []
    const ep = ports.find((p) => p.name === appService)
    if (!ep) return ''
    return `nanoku-${app.name}-${appService}:${ep.port}`
  }
  // docker mode: the alias is the app name; the actual container
  // doesn't appear in the upstream.
  return `nanoku-${app.name}:${app.port}`
}
