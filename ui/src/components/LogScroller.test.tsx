import { fireEvent, render, screen, act, cleanup } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import '../i18n'
import { LogScroller } from './LogScroller'
import type { LogLine } from '../lib/useLogStream'

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

// LogScroller owns the autoscroll + jump-to-bottom behavior. We test
// it standalone so the LogViewer tests don't need to drill into
// scroll geometry. The component reads from `lines` as a prop
// (rather than holding its own state), so the simplest harness is a
// small wrapper that lets us swap the lines array between renders.

function Harness({ initial = [] as LogLine[] }: { initial?: LogLine[] }) {
  // We don't need real interactivity for the scroll geometry; a
  // minimal holder is enough.
  return <LogScroller lines={initial} />
}

describe('LogScroller', () => {
  it('renders the default empty placeholder when no lines are given', () => {
    render(<Harness />)
    expect(screen.getByText(/waiting for logs|等待日志/)).toBeTruthy()
  })

  it('renders the provided emptyHint instead of the default', () => {
    render(<LogScroller lines={[]} emptyHint="hint!" />)
    expect(screen.getByText('hint!')).toBeTruthy()
  })

  it('renders provided placeholder when no lines are given', () => {
    render(<LogScroller lines={[]} placeholder="p!" />)
    expect(screen.getByText('p!')).toBeTruthy()
  })

  it('hides the empty placeholder once lines are present', () => {
    render(<Harness initial={[{ text: 'one' }]} />)
    expect(screen.queryByText(/waiting for logs|等待日志/)).toBeNull()
    expect(screen.getByText('one')).toBeTruthy()
  })

  it('applies word-wrap class by default and the no-wrap class when wordWrap=false', () => {
    const { container, rerender } = render(<LogScroller lines={[{ text: 'x' }]} />)
    const scroller = container.querySelector('[data-testid="log-scroller"]') as HTMLDivElement
    expect(scroller.className).toMatch(/whitespace-pre-wrap/)
    rerender(<LogScroller lines={[{ text: 'x' }]} wordWrap={false} />)
    const scroller2 = container.querySelector('[data-testid="log-scroller"]') as HTMLDivElement
    expect(scroller2.className).toMatch(/whitespace-pre\b/)
    expect(scroller2.className).not.toMatch(/whitespace-pre-wrap/)
  })

  it('does not show a jump-to-bottom button when the user is at the bottom', () => {
    render(<Harness initial={[{ text: 'a' }, { text: 'b' }]} />)
    expect(screen.queryByTestId('log-jump-to-bottom')).toBeNull()
  })

  it('shows a jump-to-bottom button when the user scrolls away from the bottom', async () => {
    render(<Harness initial={[{ text: 'a' }, { text: 'b' }, { text: 'c' }]} />)
    const scroller = screen.getByTestId('log-scroller') as HTMLDivElement
    await act(async () => {
      Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 1000 })
      Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
      scroller.scrollTop = 100
      fireEvent.scroll(scroller)
      await new Promise((r) => setTimeout(r, 0))
    })
    expect(screen.getByTestId('log-jump-to-bottom')).toBeTruthy()
  })

  it('clicking jump-to-bottom scrolls the scroller to the end and hides the button', async () => {
    render(<Harness initial={[{ text: 'a' }, { text: 'b' }, { text: 'c' }]} />)
    const scroller = screen.getByTestId('log-scroller') as HTMLDivElement
    await act(async () => {
      Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 1000 })
      Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
      scroller.scrollTop = 100
      fireEvent.scroll(scroller)
      await new Promise((r) => setTimeout(r, 0))
    })
    const jump = screen.getByTestId('log-jump-to-bottom')
    await act(async () => {
      fireEvent.click(jump)
      await new Promise((r) => setTimeout(r, 0))
    })
    expect(scroller.scrollTop).toBe(1000)
    expect(screen.queryByTestId('log-jump-to-bottom')).toBeNull()
  })

  it('auto-scrolls to the bottom on new lines while stuck to the bottom', async () => {
    function Live({ lines }: { lines: LogLine[] }) {
      return <LogScroller lines={lines} />
    }
    const { rerender } = render(<Live lines={[{ text: 'a' }]} />)
    const scroller = screen.getByTestId('log-scroller') as HTMLDivElement
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
    scroller.scrollTop = 1000
    await act(async () => {
      rerender(<Live lines={[{ text: 'a' }, { text: 'b' }, { text: 'c' }]} />)
      await new Promise((r) => requestAnimationFrame(() => r(null)))
    })
    expect(scroller.scrollTop).toBe(1000)
  })

  it('updates a keyed row in place without growing the list when its text changes', () => {
    // A keyed progress row (e.g. a compose download tick) should
    // reuse the same DOM node: re-rendering with a new text for the
    // same key must NOT add a new row, only update the existing one.
    const { rerender } = render(
      <LogScroller
        lines={[
          { text: 'Image alpine Pulling' },
          { text: '2dd7 Downloading 48.5kB [1%]', key: '2dd7' },
        ]}
      />,
    )
    expect(screen.getByText('2dd7 Downloading 48.5kB [1%]')).toBeTruthy()
    rerender(
      <LogScroller
        lines={[
          { text: 'Image alpine Pulling' },
          { text: '2dd7 Downloading 3.9MB [98%]', key: '2dd7' },
        ]}
      />,
    )
    // The updated text is present, the old one is gone, and no extra
    // row was added.
    expect(screen.getByText('2dd7 Downloading 3.9MB [98%]')).toBeTruthy()
    expect(screen.queryByText('2dd7 Downloading 48.5kB [1%]')).toBeNull()
    expect(screen.queryByText('Image alpine Pulling')).toBeTruthy()
  })

  // ---- ANSI color + timestamp rendering (added with the ansi.ts /
  // logLine.ts log-rendering improvements) ----

  it('renders ANSI-colored text without showing escape-sequence garbage', () => {
    const ESC = '\x1b'
    render(<LogScroller lines={[{ text: `${ESC}[32mOK${ESC}[0m done` }]} />)
    // The colored segment renders as a styled span with its text intact.
    expect(screen.getByText('OK')).toBeTruthy()
    // The plain trailing text is present (use a substring match since it
    // shares a parent <span> with the colored segment).
    expect(screen.getByText(/done/)).toBeTruthy()
    // No escape artifacts should be visible anywhere.
    expect(screen.queryByText(/\[32m/)).toBeNull()
    expect(screen.queryByText(/\[0m/)).toBeNull()
  })

  it('renders a Docker timestamp prefix as a separate muted column', () => {
    render(
      <LogScroller
        lines={[{ text: '2026-07-16T03:49:56.661898467Z hello world' }]}
      />,
    )
    // The HH:MM:SS timestamp column is rendered as its own text node.
    expect(screen.getByText('03:49:56')).toBeTruthy()
    // The body is still present and queryable.
    expect(screen.getByText('hello world')).toBeTruthy()
    // The full RFC3339Nano prefix should NOT appear as a single text
    // node (it was split into the timestamp column + body).
    expect(
      screen.queryByText('2026-07-16T03:49:56.661898467Z hello world'),
    ).toBeNull()
  })

  it('renders the Uptime Kuma bug-report line with split timestamp and ANSI colors', () => {
    const ESC = '\x1b'
    // Real-world shape from the bug report: a plain Docker engine
    // timestamp prefix (added by ContainerLogs with Timestamps: true),
    // followed by the app's own ANSI-colored timestamp + [SERVER] tag +
    // INFO: label. The Docker prefix must be split into the timestamp
    // column; the app's colored content must render without escape garbage.
    const line = `2026-07-16T03:49:56.781345839Z ${ESC}[36m2026-07-16T03:49:56Z${ESC}[0m [${ESC}[32mSERVER${ESC}[0m] ${ESC}[36mINFO:${ESC}[0m Env: production`
    render(<LogScroller lines={[{ text: line }]} />)
    // Docker timestamp column (from the plain prefix).
    expect(screen.getByText('03:49:56')).toBeTruthy()
    // The app's own timestamp is part of the body, rendered as colored text.
    expect(screen.getByText('2026-07-16T03:49:56Z')).toBeTruthy()
    expect(screen.getByText('SERVER')).toBeTruthy()
    expect(screen.getByText('INFO:')).toBeTruthy()
    // No escape artifacts anywhere.
    expect(screen.queryByText(/\[36m/)).toBeNull()
    expect(screen.queryByText(/\[32m/)).toBeNull()
  })

  it('does not render a timestamp column for lines without a Docker prefix', () => {
    render(<LogScroller lines={[{ text: 'plain deploy log line' }]} />)
    expect(screen.getByText('plain deploy log line')).toBeTruthy()
    // No timestamp column element should be present.
    expect(screen.queryByText(/^\d{2}:\d{2}:\d{2}$/)).toBeNull()
  })
})
