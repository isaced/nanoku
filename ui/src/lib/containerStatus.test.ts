import { describe, expect, it } from 'vitest'
import {
  containerStatusMeta,
  STATUS_VARIANT_ICON,
  STATUS_VARIANT_TAG_COLOR,
} from './containerStatus'

describe('containerStatusMeta', () => {
  it('maps known container statuses to their variant + i18n key', () => {
    const cases: Array<[string, 'success' | 'muted' | 'warn' | 'paused' | 'danger', string | null]> = [
      ['running', 'success', 'running'],
      ['restarting', 'warn', 'restarting'],
      ['paused', 'paused', 'paused'],
      ['exited', 'muted', 'exited'],
      ['created', 'muted', 'created'],
      ['dead', 'danger', null],
      ['removing', 'muted', null],
    ]
    for (const [status, variant, i18nKey] of cases) {
      const meta = containerStatusMeta(status)
      expect(meta.variant, `variant for ${status}`).toBe(variant)
      expect(meta.i18nKey, `i18nKey for ${status}`).toBe(i18nKey)
      expect(meta.raw, `raw for ${status}`).toBe(status)
    }
  })

  it('treats null/empty as not_deployed', () => {
    for (const v of [null, undefined, '']) {
      const meta = containerStatusMeta(v as string | null | undefined)
      expect(meta.variant).toBe('muted')
      expect(meta.i18nKey).toBe('notDeployed')
      expect(meta.raw).toBe('not_deployed')
    }
  })

  it('falls back to danger + raw for unknown values', () => {
    const meta = containerStatusMeta('some-future-status')
    expect(meta.variant).toBe('danger')
    expect(meta.i18nKey).toBeNull()
    expect(meta.raw).toBe('some-future-status')
  })
})

describe('status variant tables', () => {
  it('has an icon for every variant', () => {
    for (const v of ['success', 'muted', 'warn', 'paused', 'danger', 'configured'] as const) {
      expect(STATUS_VARIANT_ICON[v], `icon for ${v}`).toBeDefined()
    }
  })

  it('has a tag color for every variant', () => {
    for (const v of ['success', 'muted', 'warn', 'paused', 'danger', 'configured'] as const) {
      expect(STATUS_VARIANT_TAG_COLOR[v], `color for ${v}`).toBeTruthy()
    }
  })
})
