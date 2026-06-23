import { describe, it, expect } from 'vitest'
import { volumeRowInvalid } from './volume-validation'

describe('volumeRowInvalid', () => {
  it('rejects empty target', () => {
    expect(volumeRowInvalid({ type: 'volume', source: 'data', target: '' })).toBe(true)
  })

  it('rejects whitespace-only target', () => {
    expect(
      volumeRowInvalid({ type: 'volume', source: 'data', target: '   ' }),
    ).toBe(true)
  })

  it('rejects bind without source', () => {
    expect(volumeRowInvalid({ type: 'bind', source: '', target: '/etc/app' })).toBe(
      true,
    )
  })

  it('rejects bind with whitespace source', () => {
    expect(
      volumeRowInvalid({ type: 'bind', source: '   ', target: '/etc/app' }),
    ).toBe(true)
  })

  it('accepts named volume with source and target', () => {
    expect(
      volumeRowInvalid({ type: 'volume', source: 'data', target: '/var/lib/data' }),
    ).toBe(false)
  })

  it('accepts auto-volume (empty source, type=volume)', () => {
    expect(volumeRowInvalid({ type: 'volume', source: '', target: '/data' })).toBe(
      false,
    )
  })

  it('accepts bind with source and target', () => {
    expect(
      volumeRowInvalid({ type: 'bind', source: '/host/path', target: '/app/path' }),
    ).toBe(false)
  })

  it('defaults missing type to volume (auto-volume rule applies)', () => {
    expect(volumeRowInvalid({ target: '/data' })).toBe(false)
  })

  it('treats undefined fields as empty strings', () => {
    expect(volumeRowInvalid({})).toBe(true)
  })
})