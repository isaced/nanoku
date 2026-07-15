// Vitest setup — polyfills jsdom is missing for the libraries we render in tests.
// `ResizeObserver` and `matchMedia` are used by antd components; without
// them, renders throw and `useQuery` callbacks never run. We also stub
// `EventSource` (jsdom doesn't ship one) so useLogStream can mount in tests
// without throwing — tests that care about the SSE behavior install their
// own FakeEventSource via a per-file beforeEach.

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

if (typeof globalThis.ResizeObserver === 'undefined') {
  (globalThis as { ResizeObserver?: unknown }).ResizeObserver = ResizeObserverStub
}

if (typeof window !== 'undefined' && typeof window.matchMedia === 'undefined') {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
}

if (typeof globalThis.EventSource === 'undefined') {
  // No-op stub. Per-test FakeEventSource installs override this in
  // beforeEach; this default just keeps the mount from throwing.
  class NoopEventSource {
    static readonly CONNECTING = 0
    static readonly OPEN = 1
    static readonly CLOSED = 2
    readonly CONNECTING = 0
    readonly OPEN = 1
    readonly CLOSED = 2
    readyState = 0
    url = ''
    withCredentials = false
    onopen: unknown = null
    onmessage: unknown = null
    onerror: unknown = null
    addEventListener() {}
    removeEventListener() {}
    close() {}
    dispatchEvent(): boolean {
      return true
    }
  }
  ;(globalThis as { EventSource: unknown }).EventSource = NoopEventSource
}
