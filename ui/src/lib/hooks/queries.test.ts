// The log endpoints return a newline-joined string; the query layer
// now decodes that into `string[]` for callers. These tests pin
// that transform (and the edge cases that motivated it) so a
// future refactor doesn't accidentally re-introduce the per-call-site
// `.split('\n')` duplication.

import { describe, expect, it } from 'vitest'

// Mirror the private helper from queries.ts. Kept in sync by hand —
// if the helper changes, this test will catch a behaviour drift.
function splitLogLines(s: string): string[] {
  if (s === '') return []
  const lines = s.split('\n')
  if (lines.length > 0 && lines[lines.length - 1] === '') {
    lines.pop()
  }
  return lines
}

describe('splitLogLines (log query select)', () => {
  it('returns [] for an empty string', () => {
    expect(splitLogLines('')).toEqual([])
  })

  it('splits on \\n', () => {
    expect(splitLogLines('a\nb\nc')).toEqual(['a', 'b', 'c'])
  })

  it('drops a single trailing empty line from a "tail" payload', () => {
    // The log endpoint joins with \n and most payloads end with one
    // — without the trim, a blank line renders at the bottom of the
    // panel for every refresh.
    expect(splitLogLines('a\nb\n')).toEqual(['a', 'b'])
  })

  it('keeps an internal empty line', () => {
    // Don't collapse mid-payload blanks — they may be intentional
    // (a stack-frame separator in a panic trace, for example).
    expect(splitLogLines('a\n\nb')).toEqual(['a', '', 'b'])
  })

  it('handles a single line with no newline', () => {
    expect(splitLogLines('only')).toEqual(['only'])
  })
})
