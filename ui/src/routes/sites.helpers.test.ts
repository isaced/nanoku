// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { appOptionLabel, serviceOptionLabel } from './sites'
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
