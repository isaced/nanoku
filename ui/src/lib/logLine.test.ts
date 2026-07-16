import { describe, expect, it } from 'vitest'
import { splitTimestamp } from './logLine'

describe('splitTimestamp', () => {
  it('splits a Docker RFC3339Nano timestamp prefix into HH:MM:SS + body', () => {
    const line = '2026-07-16T03:49:56.661898467Z Welcome to Uptime Kuma'
    const { ts, body } = splitTimestamp(line)
    expect(ts).toBe('03:49:56')
    expect(body).toBe('Welcome to Uptime Kuma')
  })

  it('handles a body that itself starts with a timestamp (no double-split)', () => {
    // Uptime Kuma emits its own timestamp in the body; only the Docker
    // prefix should be stripped, the app timestamp is part of the body.
    const line =
      '2026-07-16T03:49:56.781345839Z \x1b[36m2026-07-16T03:49:56Z\x1b[0m [SERVER] INFO: Env: production'
    const { ts, body } = splitTimestamp(line)
    expect(ts).toBe('03:49:56')
    expect(body).toBe(
      '\x1b[36m2026-07-16T03:49:56Z\x1b[0m [SERVER] INFO: Env: production',
    )
  })

  it('returns ts=null for a line with no timestamp prefix', () => {
    const line = 'Image alpine Pulling'
    const { ts, body } = splitTimestamp(line)
    expect(ts).toBeNull()
    expect(body).toBe('Image alpine Pulling')
  })

  it('returns ts=null for deploy log progress lines (no Docker prefix)', () => {
    const line = '2dd7 Downloading 48.5kB [1%]'
    const { ts, body } = splitTimestamp(line)
    expect(ts).toBeNull()
    expect(body).toBe('2dd7 Downloading 48.5kB [1%]')
  })

  it('returns ts=null for a plain seed string like "seed-1"', () => {
    const { ts, body } = splitTimestamp('seed-1')
    expect(ts).toBeNull()
    expect(body).toBe('seed-1')
  })

  it('does not match a timestamp without fractional seconds', () => {
    // Docker always emits nanosecond precision with `Timestamps: true`.
    // A bare RFC3339 timestamp (no fractional component) is likely an
    // app-emitted value, not the Docker prefix - leave it alone.
    const line = '2026-07-16T03:49:56Z something happened'
    const { ts, body } = splitTimestamp(line)
    expect(ts).toBeNull()
    expect(body).toBe(line)
  })

  it('does not match a timestamp without the trailing Z', () => {
    const line = '2026-07-16T03:49:56.661898467 something'
    const { ts } = splitTimestamp(line)
    expect(ts).toBeNull()
  })

  it('does not match when the timestamp is not at the start of the line', () => {
    const line = 'prefix 2026-07-16T03:49:56.661898467Z body'
    const { ts, body } = splitTimestamp(line)
    expect(ts).toBeNull()
    expect(body).toBe(line)
  })

  it('handles an empty body after the timestamp', () => {
    // Docker can emit a timestamp followed by a blank line (the trailing
    // space + newline). body should be empty, not undefined.
    const line = '2026-07-16T03:49:56.661898467Z '
    const { ts, body } = splitTimestamp(line)
    expect(ts).toBe('03:49:56')
    expect(body).toBe('')
  })

  it('handles an empty string input', () => {
    const { ts, body } = splitTimestamp('')
    expect(ts).toBeNull()
    expect(body).toBe('')
  })

  it('formats midnight correctly (00:00:00)', () => {
    const line = '2026-07-16T00:00:00.123456789Z midnight log'
    const { ts, body } = splitTimestamp(line)
    expect(ts).toBe('00:00:00')
    expect(body).toBe('midnight log')
  })

  it('preserves the body exactly when there is no match (identity)', () => {
    const line = 'some random text with numbers 123 and symbols !@#'
    const { ts, body } = splitTimestamp(line)
    expect(ts).toBeNull()
    expect(body).toBe(line)
  })
})
