import { describe, expect, it } from 'vitest'
import { serviceStatusMeta } from './serviceStatus'

describe('serviceStatusMeta', () => {
  it('maps known service statuses', () => {
    expect(serviceStatusMeta('running').variant).toBe('success')
    expect(serviceStatusMeta('running').i18nKey).toBe('running')
    expect(serviceStatusMeta('configured').variant).toBe('configured')
    expect(serviceStatusMeta('configured').i18nKey).toBe('configured')
    expect(serviceStatusMeta('not_found').variant).toBe('muted')
    expect(serviceStatusMeta('not_found').i18nKey).toBe('notFound')
    expect(serviceStatusMeta('skipped').variant).toBe('muted')
    expect(serviceStatusMeta('skipped').i18nKey).toBe('skipped')
  })

  it('falls back to danger + raw for unknown service status', () => {
    const meta = serviceStatusMeta('restarting')
    expect(meta.variant).toBe('danger')
    expect(meta.i18nKey).toBeNull()
    expect(meta.raw).toBe('restarting')
  })

  it('treats null/undefined as unknown', () => {
    const meta = serviceStatusMeta(null)
    expect(meta.variant).toBe('danger')
    expect(meta.raw).toBe('unknown')
  })
})
