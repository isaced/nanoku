import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { parseAnsi } from './ansi'

/**
 * parseAnsi returns React nodes, so we render them to inspect the
 * output. The helper below renders the nodes and returns the container
 * so tests can assert on text content and element styles.
 */
function renderNodes(nodes: React.ReactNode) {
  return render(<div data-testid="root">{nodes}</div>).container
}

/** Extract the text content of all rendered nodes, concatenating runs. */
function textOf(container: HTMLElement): string {
  return container.querySelector('[data-testid="root"]')?.textContent ?? ''
}

/** Collect (text, color) pairs for every <span> that has a color style,
 * skipping plain text nodes. Used to verify color mapping. */
function coloredSpans(container: HTMLElement): { text: string; color?: string }[] {
  return Array.from(container.querySelectorAll('span')).map((s) => ({
    text: s.textContent ?? '',
    color: (s as HTMLElement).style.color,
  }))
}

const ESC = '\x1b'

describe('parseAnsi', () => {
  it('returns plain text as a single string element (no spans)', () => {
    const container = renderNodes(parseAnsi('hello world'))
    expect(textOf(container)).toBe('hello world')
    // No span wrappers for unstyled text.
    expect(container.querySelectorAll('span')).toHaveLength(0)
  })

  it('returns an empty array element for empty string', () => {
    const container = renderNodes(parseAnsi(''))
    expect(textOf(container)).toBe('')
  })

  it('renders a single colored segment', () => {
    const input = `${ESC}[32mOK${ESC}[0m`
    const container = renderNodes(parseAnsi(input))
    expect(textOf(container)).toBe('OK')
    const spans = coloredSpans(container)
    expect(spans).toHaveLength(1)
    expect(spans[0].text).toBe('OK')
    expect(spans[0].color).toBe('rgb(22, 163, 74)') // #16a34a -> green
  })

  it('renders multiple colored segments with plain text between them', () => {
    const input = `${ESC}[31merr${ESC}[0m plain ${ESC}[36mcyan${ESC}[0m`
    const container = renderNodes(parseAnsi(input))
    expect(textOf(container)).toBe('err plain cyan')
    const spans = coloredSpans(container)
    expect(spans).toHaveLength(2)
    expect(spans[0].text).toBe('err')
    expect(spans[0].color).toBe('rgb(220, 38, 38)') // red
    expect(spans[1].text).toBe('cyan')
    expect(spans[1].color).toBe('rgb(8, 145, 178)') // cyan
  })

  it('handles the Uptime Kuma log line shape from the bug report', () => {
    // Real-world line: timestamp + [SERVER] tag in green + INFO: in cyan
    const input = `${ESC}[36m2026-07-16T03:49:56Z${ESC}[0m [${ESC}[32mSERVER${ESC}[0m] ${ESC}[36mINFO:${ESC}[0m Env: production`
    const container = renderNodes(parseAnsi(input))
    expect(textOf(container)).toBe('2026-07-16T03:49:56Z [SERVER] INFO: Env: production')
    // Three colored segments: timestamp (cyan), SERVER (green), INFO: (cyan)
    const spans = coloredSpans(container)
    expect(spans).toHaveLength(3)
  })

  it('resets all attributes with code 0', () => {
    const input = `${ESC}[1;31mboldred${ESC}[0m plain`
    const container = renderNodes(parseAnsi(input))
    expect(textOf(container)).toBe('boldred plain')
    const spans = coloredSpans(container)
    expect(spans).toHaveLength(1)
    expect(spans[0].text).toBe('boldred')
    // Plain text after reset should not be in a colored span.
  })

  it('applies bold as font-weight 700', () => {
    const input = `${ESC}[1mimportant${ESC}[0m`
    const container = renderNodes(parseAnsi(input))
    const span = container.querySelector('span') as HTMLElement
    expect(span.style.fontWeight).toBe('700')
  })

  it('applies italic and underline', () => {
    const input = `${ESC}[3;4mstyled${ESC}[0m`
    const container = renderNodes(parseAnsi(input))
    const span = container.querySelector('span') as HTMLElement
    expect(span.style.fontStyle).toBe('italic')
    expect(span.style.textDecoration).toBe('underline')
  })

  it('applies dim as opacity 0.6', () => {
    const input = `${ESC}[2mdimmed${ESC}[0m`
    const container = renderNodes(parseAnsi(input))
    const span = container.querySelector('span') as HTMLElement
    expect(span.style.opacity).toBe('0.6')
  })

  it('handles combined params in a single SGR sequence (e.g. 1;32)', () => {
    const input = `${ESC}[1;32mboldgreen${ESC}[0m`
    const container = renderNodes(parseAnsi(input))
    const span = container.querySelector('span') as HTMLElement
    expect(span.style.color).toBe('rgb(22, 163, 74)') // green
    expect(span.style.fontWeight).toBe('700') // bold
  })

  it('strips non-SGR CSI sequences (cursor movement, erase) silently', () => {
    // \x1b[2K = erase line, \x1b[1A = cursor up - should be removed,
    // not rendered as visible text.
    const input = `${ESC}[2Khello${ESC}[1A world`
    const container = renderNodes(parseAnsi(input))
    expect(textOf(container)).toBe('hello world')
    // No visible escape artifacts.
    expect(textOf(container)).not.toContain('[2K')
    expect(textOf(container)).not.toContain('[1A')
  })

  it('treats empty SGR params (\\x1b[m) as reset', () => {
    const input = `${ESC}[31mred${ESC}[m plain`
    const container = renderNodes(parseAnsi(input))
    expect(textOf(container)).toBe('red plain')
    // Only the red segment should be colored; plain after reset is unstyled.
    const spans = coloredSpans(container)
    expect(spans).toHaveLength(1)
    expect(spans[0].text).toBe('red')
  })

  it('supports bright foreground colors (90-97)', () => {
    const input = `${ESC}[91mbrightred${ESC}[0m`
    const container = renderNodes(parseAnsi(input))
    const span = container.querySelector('span') as HTMLElement
    expect(span.style.color).toBe('rgb(239, 68, 68)') // bright red #ef4444
  })

  it('supports background colors (40-47)', () => {
    const input = `${ESC}[44mbluebg${ESC}[0m`
    const container = renderNodes(parseAnsi(input))
    const span = container.querySelector('span') as HTMLElement
    expect(span.style.backgroundColor).toBe('rgb(37, 99, 235)') // #2563eb
  })

  it('ignores unknown SGR codes without crashing', () => {
    // Code 999 is not a standard SGR; should be silently ignored.
    const input = `${ESC}[999mtext${ESC}[0m`
    const container = renderNodes(parseAnsi(input))
    expect(textOf(container)).toBe('text')
    // Unknown code -> no style change -> no span wrapper.
    expect(container.querySelectorAll('span')).toHaveLength(0)
  })

  it('handles default fg/bg reset codes (39, 49)', () => {
    const input = `${ESC}[31mred${ESC}[39mdefault`
    const container = renderNodes(parseAnsi(input))
    expect(textOf(container)).toBe('reddefault')
    // After 39 (default fg), the color is cleared. "red" segment is
    // colored; "default" segment should have no color.
    const spans = coloredSpans(container)
    expect(spans).toHaveLength(1)
    expect(spans[0].text).toBe('red')
  })
})
