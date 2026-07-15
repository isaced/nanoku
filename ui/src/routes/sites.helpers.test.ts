// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import {
  appOptionLabel,
  computeUpstreamForAppUI,
  serviceOptionLabel,
} from './sites'
import type { App } from '../lib/types'

// These tests cover the App-select label rendering. The label
// surfaces the app's mode + (for compose) how many services the
// operator will have to pick from — that's the load-bearing piece
// of UX. A regression in the label shape (e.g. dropping the
// service count, or misclassifying docker as compose) would
// silently mislead the user when they pick an app in the site
// editor.
describe('appOptionLabel', () => {
  it('docker apps show image :port', () => {
    const a = makeApp({
      name: 'blog',
      deployMethod: 'docker',
      image: 'nginx:1.27',
      port: 80,
    })
    expect(appOptionLabel(a)).toBe('blog · docker · nginx:1.27 :80')
  })

  it('docker apps with no image fall back to a placeholder', () => {
    const a = makeApp({ name: 'blog', deployMethod: 'docker', port: 80, image: '' })
    expect(appOptionLabel(a)).toContain('(no image)')
  })

  it('compose apps with no exposed_ports show a hint', () => {
    const a = makeApp({
      name: 'kuma',
      deployMethod: 'compose',
      exposedPorts: [],
    })
    expect(appOptionLabel(a)).toBe('kuma · compose · (no exposed ports)')
  })

  it('compose apps with one service are labeled with the service', () => {
    const a = makeApp({
      name: 'kuma',
      deployMethod: 'compose',
      exposedPorts: [{ name: 'uptime-kuma', port: 3001 }],
    })
    expect(appOptionLabel(a)).toBe(
      'kuma · compose · 1 service: uptime-kuma:3001',
    )
  })

  it('compose apps with many services show the count', () => {
    const a = makeApp({
      name: 'stack',
      deployMethod: 'compose',
      exposedPorts: [
        { name: 'web', port: 80 },
        { name: 'api', port: 8080 },
        { name: 'admin', port: 9000 },
      ],
    })
    expect(appOptionLabel(a)).toBe('stack · compose · 3 services')
  })
})

describe('serviceOptionLabel', () => {
  it('formats service name and port', () => {
    expect(serviceOptionLabel('web', 3000)).toBe('web · port 3000')
  })
})

// computeUpstreamForAppUI mirrors the server's computeUpstreamForApp
// so the editor can preview the locked upstream the moment the
// operator picks an app. These tests pin the client-side shape — a
// regression here (e.g. switching the prefix, dropping the port,
// forgetting the docker-mode container branch) would silently leave
// the upstream input blank even though the server's save path is
// unaffected, so the UI lies about what the deploy will route to.
describe('computeUpstreamForAppUI', () => {
  it('returns "" when app is undefined', () => {
    expect(computeUpstreamForAppUI(undefined, undefined)).toBe('')
  })

  it('compose mode: builds nanoku-<app>-<service>-1:<port> from the picked service', () => {
    const a = makeApp({
      name: 'ddd',
      deployMethod: 'compose',
      exposedPorts: [{ name: 'uptime-kuma', port: 3001 }],
    })
    expect(computeUpstreamForAppUI(a, 'uptime-kuma')).toBe(
      'nanoku-ddd-uptime-kuma-1:3001',
    )
  })

  it('compose mode: returns "" when no service was picked', () => {
    const a = makeApp({
      name: 'ddd',
      deployMethod: 'compose',
      exposedPorts: [{ name: 'uptime-kuma', port: 3001 }],
    })
    expect(computeUpstreamForAppUI(a, undefined)).toBe('')
  })

  it('compose mode: returns "" when the service is not in exposed_ports', () => {
    // Server will 400 this on save, but the UI should not pretend
    // the upstream is computable.
    const a = makeApp({
      name: 'ddd',
      deployMethod: 'compose',
      exposedPorts: [{ name: 'uptime-kuma', port: 3001 }],
    })
    expect(computeUpstreamForAppUI(a, 'web')).toBe('')
  })

  it('compose mode: returns "" when the app has no exposed_ports at all', () => {
    const a = makeApp({
      name: 'ddd',
      deployMethod: 'compose',
      exposedPorts: [],
    })
    expect(computeUpstreamForAppUI(a, 'web')).toBe('')
  })

  it('docker mode: builds <current-container>:<port> from the linked container', () => {
    const a = makeApp({
      name: 'blog',
      deployMethod: 'docker',
      port: 8080,
      container: {
        id: 1,
        name: 'nanoku-blog',
        image: 'nginx:1.27',
        status: 'running',
      },
    })
    expect(computeUpstreamForAppUI(a, undefined)).toBe('nanoku-blog:8080')
  })

  it('docker mode: returns "" when the app has no current container', () => {
    // App exists but hasn't been deployed yet; the form should not
    // show a phantom upstream.
    const a = makeApp({ name: 'blog', deployMethod: 'docker', port: 80 })
    expect(computeUpstreamForAppUI(a, undefined)).toBe('')
  })
})

function makeApp(over: Partial<App> = {}): App {
  return {
    id: 1,
    name: 'app',
    image: '',
    port: 0,
    deployMethod: 'docker',
    registryConfigured: false,
    triggerConfigured: false,
    deleteVolumesOnRemove: false,
    createdAt: '',
    updatedAt: '',
    ...over,
  } as App
}
