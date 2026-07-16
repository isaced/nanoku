import { describe, expect, it } from 'vitest'
import { formatBytes } from './formatBytes'

describe('formatBytes', () => {
  it('renders 0 as "0 B"', () => {
    expect(formatBytes(0)).toBe('0 B')
  })

  it('renders sub-KB values without a decimal', () => {
    expect(formatBytes(512)).toBe('512 B')
  })

  it('renders KB / MB / GB with one decimal', () => {
    expect(formatBytes(1024)).toBe('1.0 KB')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(1024 * 1024)).toBe('1.0 MB')
    expect(formatBytes(2.5 * 1024 * 1024 * 1024)).toBe('2.5 GB')
  })

  it('caps at TB and does not overflow', () => {
    // 2048 TB → TB unit, 2048.0 TB
    expect(formatBytes(2048 * 1024 ** 4)).toBe('2048.0 TB')
  })
})
