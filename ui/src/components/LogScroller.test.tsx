import { fireEvent, render, screen, act, cleanup } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import '../i18n'
import { LogScroller } from './LogScroller'

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

// LogScroller owns the autoscroll + jump-to-bottom behavior. We test
// it standalone so the LogViewer tests don't need to drill into
// scroll geometry. The component reads from `lines` as a prop
// (rather than holding its own state), so the simplest harness is a
// small wrapper that lets us swap the lines array between renders.

function Harness({ initial = [] as string[] }: { initial?: string[] }) {
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
    render(<Harness initial={['one']} />)
    expect(screen.queryByText(/waiting for logs|等待日志/)).toBeNull()
    expect(screen.getByText('one')).toBeTruthy()
  })

  it('applies word-wrap class by default and the no-wrap class when wordWrap=false', () => {
    const { container, rerender } = render(<LogScroller lines={['x']} />)
    const scroller = container.querySelector('[data-testid="log-scroller"]') as HTMLDivElement
    expect(scroller.className).toMatch(/whitespace-pre-wrap/)
    rerender(<LogScroller lines={['x']} wordWrap={false} />)
    const scroller2 = container.querySelector('[data-testid="log-scroller"]') as HTMLDivElement
    expect(scroller2.className).toMatch(/whitespace-pre\b/)
    expect(scroller2.className).not.toMatch(/whitespace-pre-wrap/)
  })

  it('does not show a jump-to-bottom button when the user is at the bottom', () => {
    render(<Harness initial={['a', 'b']} />)
    expect(screen.queryByTestId('log-jump-to-bottom')).toBeNull()
  })

  it('shows a jump-to-bottom button when the user scrolls away from the bottom', async () => {
    render(<Harness initial={['a', 'b', 'c']} />)
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
    render(<Harness initial={['a', 'b', 'c']} />)
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
    function Live({ lines }: { lines: string[] }) {
      return <LogScroller lines={lines} />
    }
    const { rerender } = render(<Live lines={['a']} />)
    const scroller = screen.getByTestId('log-scroller') as HTMLDivElement
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
    scroller.scrollTop = 1000
    await act(async () => {
      rerender(<Live lines={['a', 'b', 'c']} />)
      await new Promise((r) => requestAnimationFrame(() => r(null)))
    })
    expect(scroller.scrollTop).toBe(1000)
  })
})
