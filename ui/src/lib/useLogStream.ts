import { useEffect, useMemo, useRef, useState } from 'react'

/**
 * useLogStream subscribes to a Server-Sent Events endpoint and exposes
 * the accumulated lines + connection status to a React tree.
 *
 * The hook intentionally bypasses TanStack Query: the stream is a
 * persistent connection with a long lifetime, not a cached request,
 * and re-renders-on-`lines`-update are what we want (UI scrolls along
 * with the line buffer). Putting it in a Query would force the
 * caller to invalidate on every event, which is the opposite of what
 * we want.
 *
 * Lifecycle:
 *   - On `url` change or first mount, opens a new EventSource.
 *   - On unmount, closes it (the browser will release the connection).
 *   - On EventSource error (server timeout, network blip), the
 *     browser auto-reconnects per the `retry: 2000` directive the
 *     server emits; we also surface `status: 'reconnecting'` so the
 *     UI can show a hint.
 *
 * Pause/resume:
 *   - When `paused` is true, incoming lines accumulate in a
 *     backlog but are NOT appended to `lines` until `paused` flips
 *     back to false (at which point the backlog is flushed). This
 *     mirrors "tail -f" behavior: pausing on the consumer side
 *     doesn't drop anything, it just defers the render.
 */

export type LogStreamStatus = 'connecting' | 'live' | 'reconnecting' | 'closed' | 'error'

export type UseLogStreamOptions = {
  /** When false, the hook closes any open connection and does not
   * open a new one. Useful for tabs that aren't visible. */
  enabled?: boolean
  /** Initial paused state. Flipping to true stops appending to
   * `lines`; flipping to false flushes any backlog. */
  paused?: boolean
  /** Initial line buffer to seed the stream with (e.g. a one-shot
   * GET of the last N lines for instant display before the SSE
   * catches up). When provided, `lines` starts with this content
   * and the first event from the server is appended to it. */
  initialLines?: string[]
}

export type UseLogStreamResult = {
  lines: string[]
  status: LogStreamStatus
  error: string | null
  /** Flip the paused flag. The hook owns the state so the call site
   * doesn't need to mirror it. */
  setPaused: (paused: boolean) => void
  paused: boolean
  /** Wipe the line buffer and pause backlog. */
  clear: () => void
  /** Manually close the stream (e.g. when the parent unmounts
   * the panel ahead of the browser doing it). */
  disconnect: () => void
}

const MAX_LINES = 5000

export function useLogStream(
  url: string | null,
  options: UseLogStreamOptions = {},
): UseLogStreamResult {
  const { enabled = true, paused: initialPaused = false, initialLines } = options
  // initialLines is consumed once on first mount; subsequent updates
  // are ignored (the user has already started tailing).
  const seedRef = useRef<string[] | undefined>(initialLines)
  const [lines, setLines] = useState<string[]>(() => {
    if (seedRef.current && seedRef.current.length > 0) {
      // Honor the seed but cap at MAX_LINES so a stale `tail=9999`
      // doesn't blow up the page.
      return seedRef.current.slice(-MAX_LINES)
    }
    return []
  })
  const [status, setStatus] = useState<LogStreamStatus>('closed')
  const [error, setError] = useState<string | null>(null)
  const [paused, setPaused] = useState<boolean>(initialPaused)
  // Backlog of lines that arrived while paused. Drained on resume.
  const backlogRef = useRef<string[]>([])
  // Refs to the latest values for use inside the EventSource handlers,
  // so we don't have to re-subscribe when paused flips.
  const pausedRef = useRef(paused)
  pausedRef.current = paused
  const esRef = useRef<EventSource | null>(null)
  // Track the URL we last opened for, so a same-URL re-render doesn't
  // churn the connection.
  const openedUrlRef = useRef<string | null>(null)

  // Connect / disconnect on url or enabled changes.
  useEffect(() => {
    if (!enabled || !url) {
      // Disabled: tear down any open stream, mark closed.
      if (esRef.current) {
        esRef.current.close()
        esRef.current = null
      }
      openedUrlRef.current = null
      setStatus('closed')
      return
    }
    if (openedUrlRef.current === url) {
      return
    }
    // New URL — open a fresh stream.
    openedUrlRef.current = url
    setStatus('connecting')
    setError(null)
    // We use credentials: 'include' so the same session cookie used
    // for /api/* requests rides along. The server sets
    // Access-Control-Allow-Credentials implicitly because we don't
    // override Origin in the Caddyfile; the trigger endpoint already
    // runs cross-origin so the SSE endpoint inherits that.
    const ES = (globalThis as { EventSource: typeof EventSource }).EventSource
    const es = new ES(url, { withCredentials: true })
    esRef.current = es
    es.addEventListener('open', () => {
      setStatus('live')
      setError(null)
    })
    // Batch `line` events into one setState per microtask tick.
    // A chatty container (nginx access log, bash loop, etc.) can
    // emit hundreds of lines per second; without batching we'd
    // re-render the entire 5000-line buffer that many times per
    // second, pegging a CPU core. A microtask runs before the
    // browser yields (paint, layout, etc.), so any lines that
    // arrive in the same dispatch loop are coalesced into one
    // React render. We use microtask over rAF for two reasons:
    // (1) rAF waits up to 16ms; if the browser is idle that's
    // wasted latency on a tail; (2) tests can flush microtasks
    // deterministically with `await Promise.resolve()`.
    const pendingRef: { current: string[] } = { current: [] }
    const scheduledRef: { current: boolean } = { current: false }
    const flush = () => {
      scheduledRef.current = false
      if (pendingRef.current.length === 0) return
      const batch = pendingRef.current
      pendingRef.current = []
      setLines((prev) => appendLines(prev, batch))
    }
    es.addEventListener('line', (ev: MessageEvent) => {
      const text = ev.data as string
      if (pausedRef.current) {
        backlogRef.current.push(text)
        // Bound the backlog so a long pause doesn't OOM the page.
        if (backlogRef.current.length > MAX_LINES) {
          backlogRef.current.splice(0, backlogRef.current.length - MAX_LINES)
        }
        return
      }
      pendingRef.current.push(text)
      if (!scheduledRef.current) {
        scheduledRef.current = true
        queueMicrotask(flush)
      }
    })
    es.addEventListener('error', () => {
      // EventSource auto-reconnects after the server's `retry: N`
      // directive. While in that gap we surface "reconnecting" so
      // the UI can dim the indicator; a permanent failure (e.g. 503
      // on first connect) will keep the status as "reconnecting" or
      // "error" depending on readyState.
      setStatus(es.readyState === EventSource.CLOSED ? 'error' : 'reconnecting')
    })
    es.addEventListener('log-error', (ev: MessageEvent) => {
      // Server-sent `event: log-error` from the SSE helper — we
      // treat this as a soft error (the line is informational, the
      // stream may still recover). We deliberately don't use the
      // event name `error` here because EventSource already has a
      // built-in `error` handler above for connection-level issues.
      setError((ev.data as string) || 'log stream error')
    })
    return () => {
      es.close()
      esRef.current = null
      openedUrlRef.current = null
    }
  }, [url, enabled])

  // Flush backlog on resume.
  useEffect(() => {
    if (paused) return
    if (backlogRef.current.length === 0) return
    const drained = backlogRef.current.splice(0)
    setLines((prev) => mergeLines(prev, drained))
  }, [paused])

  const disconnect = () => {
    if (esRef.current) {
      esRef.current.close()
      esRef.current = null
    }
    openedUrlRef.current = null
    setStatus('closed')
  }

  const clear = () => {
    setLines([])
    backlogRef.current = []
  }

  return useMemo(
    () => ({ lines, status, error, paused, setPaused, clear, disconnect }),
    [lines, status, error, paused],
  )
}

function appendLine(prev: string[], text: string): string[] {
  const next = prev.length >= MAX_LINES ? prev.slice(prev.length - MAX_LINES + 1) : prev.slice()
  next.push(text)
  return next
}

// appendLines is the batch variant of appendLine used by the rAF
// flusher. We append all queued lines in a single immutable update
// so React runs one render per animation frame, not one per line.
function appendLines(prev: string[], batch: string[]): string[] {
  if (batch.length === 0) return prev
  const total = prev.length + batch.length
  if (total <= MAX_LINES) {
    // Common case: everything fits. One allocation, no slicing.
    const next = prev.slice()
    for (const l of batch) next.push(l)
    return next
  }
  // Over the cap: keep the last MAX_LINES of the merged buffer.
  // Concat first, then trim from the head with splice so we don't
  // lose lines at the boundary (e.g. cap=5000, prev=0, batch=5100
  // should keep the last 5000 of the batch, not slice(100) of an
  // empty array).
  const merged = prev.concat(batch)
  if (merged.length > MAX_LINES) {
    merged.splice(0, merged.length - MAX_LINES)
  }
  return merged
}

function mergeLines(prev: string[], drained: string[]): string[] {
  if (drained.length === 0) return prev
  const next = prev.slice()
  for (const l of drained) {
    next.push(l)
  }
  // Bound the resulting array.
  if (next.length > MAX_LINES) {
    next.splice(0, next.length - MAX_LINES)
  }
  return next
}
