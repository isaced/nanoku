import { fireEvent, render, screen, act, cleanup } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import '../i18n'
import { LogViewer } from './LogViewer'

type Listener = (ev: { data?: string }) => void

class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  readyState = 0
  closed = false
  private listeners: Record<string, Listener[]> = {}
  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }
  addEventListener(event: string, listener: Listener) {
    if (!this.listeners[event]) this.listeners[event] = []
    this.listeners[event].push(listener)
  }
  removeEventListener() {}
  close() {
    this.closed = true
    this.readyState = 2
  }
  __fire(event: string, data?: string) {
    for (const l of this.listeners[event] ?? []) {
      l({ data })
    }
  }
}

beforeEach(() => {
  FakeEventSource.instances.length = 0
  ;(globalThis as { EventSource: unknown }).EventSource = FakeEventSource
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

// LogViewer composes useLogStream (SSE state) with LogScroller
// (autoscroll) and a header strip (status dot, pause/clear). These
// tests focus on the composition — the scroller's behavior is
// exhaustively covered in LogScroller.test.tsx.

describe('LogViewer', () => {
  it('renders the connecting state until the stream opens', () => {
    render(<LogViewer url="/api/test/stream" />)
    expect(screen.getByText(/connecting/i)).toBeTruthy()
  })

  it('seeds with initialLines and appends live events on top', async () => {
    render(
      <LogViewer
        url="/api/test/stream"
        initialLines={['seed-1', 'seed-2']}
      />,
    )
    expect(screen.getByText('seed-1')).toBeTruthy()
    expect(screen.getByText('seed-2')).toBeTruthy()
    const es = FakeEventSource.instances[0]
    await act(async () => {
      es.__fire('open')
      es.__fire('line', 'live-1')
      // Yield so the batching microtask flushes before assertion.
      await new Promise((r) => setTimeout(r, 0))
    })
    expect(screen.getByText('live-1')).toBeTruthy()
  })

  it('uses the emptyHint prop when no lines have arrived', () => {
    render(<LogViewer url="/api/test/stream" emptyHint="custom empty" />)
    expect(screen.getByText('custom empty')).toBeTruthy()
  })

  it('toggles the pause icon and label', () => {
    render(<LogViewer url="/api/test/stream" />)
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    // The first button (pause) becomes resume after click.
    const buttons = screen.getAllByRole('button', { name: /pause/i })
    fireEvent.click(buttons[0])
    expect(screen.getAllByRole('button', { name: /resume/i }).length).toBeGreaterThan(0)
  })

  it('disables the controls when there is no url', () => {
    render(<LogViewer url={null} />)
    // No FakeEventSource should have been created.
    expect(FakeEventSource.instances).toHaveLength(0)
    // Both controls are disabled.
    const allButtons = screen.getAllByRole('button')
    for (const b of allButtons) {
      expect((b as HTMLButtonElement).disabled).toBe(true)
    }
  })

  it('disables the clear button when there are no lines', () => {
    render(<LogViewer url="/api/test/stream" />)
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    const clearBtn = screen.getByRole('button', { name: /clear/i })
    expect((clearBtn as HTMLButtonElement).disabled).toBe(true)
  })

  it('captures log-error events into the error state and renders them', () => {
    render(<LogViewer url="/api/test/stream" />)
    const es = FakeEventSource.instances[0]
    act(() => {
      es.__fire('open')
      es.__fire('log-error', 'something broke')
    })
    expect(screen.getByText('something broke')).toBeTruthy()
  })
})
