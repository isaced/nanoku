import { render, renderHook, act } from '@testing-library/react'
import { createElement } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useLogStream } from './useLogStream'

// Mock EventSource so we can feed it events synchronously from the
// test. The mock implements the subset of EventSource we use:
// addEventListener('open' | 'line' | 'error' | 'log-error', listener),
// close(), and a `readyState` property.
//
// Tests construct one of these via the factory in beforeEach and
// inspect it via `lastInstance`.

type Listener = (ev: { data?: string }) => void

class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: FakeEventSource[] = []
  url: string
  withCredentials = false
  readyState = FakeEventSource.CONNECTING
  closed = false
  private listeners: Record<string, Listener[]> = {}

  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url
    this.withCredentials = init?.withCredentials ?? false
    FakeEventSource.instances.push(this)
  }

  addEventListener(event: string, listener: Listener) {
    if (!this.listeners[event]) this.listeners[event] = []
    this.listeners[event].push(listener)
  }

  removeEventListener() {
    // The hook doesn't call this in the paths we test, so leave it
    // as a no-op. (We add it only to satisfy the EventSource
    // interface contract.)
  }

  close() {
    this.closed = true
    this.readyState = 2 // CLOSED
  }

  // Test helpers — not part of the real EventSource API.
  __fire(event: string, data?: string) {
    for (const l of this.listeners[event] ?? []) {
      l({ data })
    }
  }
}

// flushMicrotasks drains the queueMicrotask work that the hook
// schedules to coalesce `line` events. Tests that push events via
// __fire must await this before asserting on result.current.lines,
// otherwise they'll race the batching flush and see a stale buffer.
// We yield enough to cover queueMicrotask + React's own scheduler +
// a macrotask boundary, so the resulting setState is fully visible
// to the next assertion.
const flushMicrotasks = async () => {
  for (let i = 0; i < 5; i++) {
    await Promise.resolve()
  }
  await new Promise<void>((resolve) => setTimeout(resolve, 0))
  for (let i = 0; i < 5; i++) {
    await Promise.resolve()
  }
}

// fireAndFlush is a one-liner that pushes a line event, then awaits
// the microtask flush that coalesces it into React state, and finally
// wraps the assertion in `act()` so React Testing Library's
// scheduler drain runs. Use this anywhere a test would otherwise
// write
//   act(() => es.__fire('line', ...))
// since batching now defers the setState past the act boundary.
async function fireAndFlush(
  es: FakeEventSource,
  ...events: Array<[string, string]>
) {
  await act(async () => {
    for (const [event, data] of events) {
      es.__fire(event, data)
    }
    await flushMicrotasks()
  })
}

// texts extracts the plain-text array from a LogLine[] for ergonomic
// assertion against the string values callers care about.
const texts = (lines: { text: string }[]) => lines.map((l) => l.text)

beforeEach(() => {
  FakeEventSource.instances.length = 0
  // jsdom's EventSource is a no-op class on this Node version, so
  // we replace the global with our fake.
  ;(globalThis as { EventSource: unknown }).EventSource = FakeEventSource
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useLogStream', () => {
  it('opens an EventSource on mount and exposes lines from `line` events', async () => {
    const { result } = renderHook(() =>
      useLogStream('/api/test/stream', { enabled: true }),
    )
    // The connect effect runs after mount.
    expect(FakeEventSource.instances).toHaveLength(1)
    const es = FakeEventSource.instances[0]
    expect(es.url).toBe('/api/test/stream')
    expect(es.withCredentials).toBe(true)

    // status is `connecting` until `open` fires.
    expect(result.current.status).toBe('connecting')

    await act(async () => {
      es.__fire('open')
      await flushMicrotasks()
    })
    expect(result.current.status).toBe('live')

    await fireAndFlush(es, ['line', 'first line'], ['line', 'second line'])
    expect(texts(result.current.lines)).toEqual(['first line', 'second line'])
  })

  it('seeds with initialLines and appends new events on top', async () => {
    const { result } = renderHook(() =>
      useLogStream('/api/test/stream', {
        initialLines: ['seed-1', 'seed-2'],
      }),
    )
    expect(texts(result.current.lines)).toEqual(['seed-1', 'seed-2'])
    const es = FakeEventSource.instances[0]
    await act(async () => {
      es.__fire('open')
      await flushMicrotasks()
    })
    await fireAndFlush(es, ['line', 'live-1'])
    expect(texts(result.current.lines)).toEqual(['seed-1', 'seed-2', 'live-1'])
  })

  it('does not open a connection when url is null', () => {
    renderHook(() => useLogStream(null))
    expect(FakeEventSource.instances).toHaveLength(0)
  })

  it('does not open a connection when disabled', () => {
    renderHook(() => useLogStream('/api/test/stream', { enabled: false }))
    expect(FakeEventSource.instances).toHaveLength(0)
  })

  it('buffers lines while paused and flushes on resume', async () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    act(() => result.current.setPaused(true))
    // While paused, the listener pushes into the backlog ref (no
    // batching), so we don't need flushMicrotasks here.
    act(() => {
      es.__fire('line', 'paused-1')
      es.__fire('line', 'paused-2')
    })
    // Lines should NOT have advanced while paused.
    expect(texts(result.current.lines)).toEqual([])
    act(() => result.current.setPaused(false))
    // Drain effect runs after render; assert the new state.
    await flushMicrotasks()
    expect(texts(result.current.lines)).toEqual(['paused-1', 'paused-2'])
  })

  it('clear() empties the line buffer and the paused backlog', async () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    await fireAndFlush(es, ['line', 'a'], ['line', 'b'])
    expect(texts(result.current.lines)).toEqual(['a', 'b'])
    act(() => result.current.clear())
    expect(result.current.lines).toEqual([])
  })

  it('captures log-error events into the error state', () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    act(() => es.__fire('log-error', 'something broke'))
    expect(result.current.error).toBe('something broke')
  })

  it('disconnect() closes the EventSource', () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    act(() => result.current.disconnect())
    expect(es.closed).toBe(true)
    expect(result.current.status).toBe('closed')
  })

  it('batches many `line` events into a single setState per microtask tick', async () => {
    // We render a host component that counts how many times the
    // `lines` state actually changes by tracking its length on each
    // render. A naive (unbatched) implementation would re-render N
    // times for N events; the batched one renders at most 2 (one
    // initial, one for the coalesced batch).
    const renderCount = { value: 0 }
    const lastLength = { value: 0 }
    function Probe({ url }: { url: string }) {
      const { lines } = useLogStream(url)
      if (lines.length !== lastLength.value) {
        lastLength.value = lines.length
        renderCount.value++
      }
      return null
    }
    render(createElement(Probe, { url: '/api/test/stream' }))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    // Reset the counter to ignore the initial render + open render.
    renderCount.value = 0
    lastLength.value = 0
    // Push 200 lines synchronously — the hook should coalesce them
    // into a single state update rather than 200.
    await act(async () => {
      for (let i = 0; i < 200; i++) {
        es.__fire('line', `tick-${i}`)
      }
      await flushMicrotasks()
    })
    // Render count is at most 2: one for the post-open render that
    // bumped the counter, and one for the coalesced batch. In
    // practice the open render is swallowed by the counter reset
    // above, so we should see at most 1.
    expect(renderCount.value).toBeLessThanOrEqual(2)
    expect(lastLength.value).toBe(200)
  })

  it('reopens the stream when url changes', () => {
    const { rerender } = renderHook(
      ({ url }: { url: string }) => useLogStream(url),
      { initialProps: { url: '/api/test/a' } },
    )
    expect(FakeEventSource.instances).toHaveLength(1)
    rerender({ url: '/api/test/b' })
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(FakeEventSource.instances[1].url).toBe('/api/test/b')
  })

  it('caps the line buffer at MAX_LINES to bound memory', async () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    // Push 5100 lines; the buffer should keep the last 5000.
    await act(async () => {
      for (let i = 0; i < 5100; i++) {
        es.__fire('line', `line-${i}`)
      }
      await flushMicrotasks()
    })
    expect(result.current.lines.length).toBe(5000)
    expect(result.current.lines[0].text).toBe('line-100')
    expect(result.current.lines[4999].text).toBe('line-5099')
  })

  it('caps the paused backlog at MAX_LINES so a long pause cannot OOM the page', async () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    act(() => result.current.setPaused(true))
    // Backlog path: no batching, push directly.
    for (let i = 0; i < 5100; i++) {
      es.__fire('line', `p-${i}`)
    }
    act(() => result.current.setPaused(false))
    await flushMicrotasks()
    expect(result.current.lines.length).toBe(5000)
    expect(result.current.lines[0].text).toBe('p-100')
    expect(result.current.lines[4999].text).toBe('p-5099')
  })

  it('clear() also drops the paused backlog so a paused-and-cleared resume starts empty', async () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    act(() => result.current.setPaused(true))
    act(() => {
      es.__fire('line', 'a')
      es.__fire('line', 'b')
    })
    act(() => result.current.clear())
    act(() => result.current.setPaused(false))
    await flushMicrotasks()
    expect(result.current.lines).toEqual([])
  })

  it('toggles status to "reconnecting" on a transient EventSource error', () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    expect(result.current.status).toBe('live')
    // Simulate an error while the EventSource is still alive (readyState
    // is CONNECTING = 0 by default; in that case the browser will
    // auto-reconnect, which we surface as 'reconnecting').
    act(() => es.__fire('error'))
    expect(result.current.status).toBe('reconnecting')
  })

  it('toggles status to "error" when the EventSource reports a permanent failure', () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    // readyState 2 = CLOSED — the EventSource gave up.
    es.readyState = 2
    act(() => es.__fire('error'))
    expect(result.current.status).toBe('error')
  })

  it('flipping enabled false closes the open EventSource', () => {
    const { rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) =>
        useLogStream('/api/test/stream', { enabled }),
      { initialProps: { enabled: true } },
    )
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    rerender({ enabled: false })
    expect(es.closed).toBe(true)
  })

  it('flipping enabled back to true opens a fresh EventSource', () => {
    const { rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) =>
        useLogStream('/api/test/stream', { enabled }),
      { initialProps: { enabled: false } },
    )
    expect(FakeEventSource.instances).toHaveLength(0)
    rerender({ enabled: true })
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  // The server sends `event: end` once the deploy log stream is
  // definitively done (deploy reached terminal status, all history
  // replayed, no more lines coming). Without this handler the
  // browser's EventSource auto-reconnects on close and the operator
  // sees the same history replay forever — the deploy log "loops"
  // every 2 seconds. The handler must close the EventSource and
  // surface `closed` status so the UI can show a definitive end.
  it('closes the EventSource on `end` event to stop the replay loop', () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    expect(result.current.status).toBe('live')
    act(() => es.__fire('end', 'nanoku deploy log stream end'))
    expect(es.closed).toBe(true)
    expect(result.current.status).toBe('closed')
  })

  // `line-replace` events (data = "key\tmsg") drive in-place progress:
  // a compose download tick with the same layer key updates the
  // existing row's text instead of appending a new row, so a 50 MB
  // layer download shows one updating line rather than dozens of
  // scrolling ones. The first occurrence of a key appends (there's
  // nothing to replace yet); subsequent ones update in place.
  it('updates a keyed row in place on `line-replace` events', async () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    // First occurrence of key "abc" -> appends a row carrying the key.
    await fireAndFlush(es, ['line-replace', 'abc\tDownloading 48.5kB [1%]'])
    expect(texts(result.current.lines)).toEqual(['Downloading 48.5kB [1%]'])
    expect(result.current.lines[0].key).toBe('abc')
    // Second tick, same key -> updates the SAME row, no new row.
    await fireAndFlush(es, ['line-replace', 'abc\tDownloading 3.9MB [98%]'])
    expect(result.current.lines.length).toBe(1)
    expect(texts(result.current.lines)).toEqual(['Downloading 3.9MB [98%]'])
    expect(result.current.lines[0].key).toBe('abc')
  })

  it('appends a keyed row when no prior key matches, and replaces only the matching key', async () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    // Two different layer keys -> two rows, each with its own key.
    await fireAndFlush(
      es,
      ['line-replace', 'aaa\tDownloading 1kB [1%]'],
      ['line-replace', 'bbb\tDownloading 2kB [1%]'],
    )
    expect(texts(result.current.lines)).toEqual([
      'Downloading 1kB [1%]',
      'Downloading 2kB [1%]',
    ])
    // Update only "aaa" -> "bbb" row stays put.
    await fireAndFlush(es, ['line-replace', 'aaa\tDownloading 9kB [90%]'])
    expect(texts(result.current.lines)).toEqual([
      'Downloading 9kB [90%]',
      'Downloading 2kB [1%]',
    ])
  })

  it('mixes plain `line` appends with `line-replace` updates preserving order', async () => {
    const { result } = renderHook(() => useLogStream('/api/test/stream'))
    const es = FakeEventSource.instances[0]
    act(() => es.__fire('open'))
    await fireAndFlush(
      es,
      ['line', '-> compose up: project=app'],
      ['line-replace', 'img1\tImage alpine Pulling'],
      ['line-replace', 'img1\tImage alpine Pulled'],
      ['line', '-> done'],
    )
    expect(texts(result.current.lines)).toEqual([
      '-> compose up: project=app',
      'Image alpine Pulled',
      '-> done',
    ])
    // The middle row carried key img1; the two plain lines have none.
    expect(result.current.lines[0].key).toBeUndefined()
    expect(result.current.lines[1].key).toBe('img1')
    expect(result.current.lines[2].key).toBeUndefined()
  })
})
