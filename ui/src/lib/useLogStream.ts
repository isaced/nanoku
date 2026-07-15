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
 *
 * In-place progress (`line-replace` events):
 *   - The deploy stream emits `line-replace` (data = "key\tmsg") for
 *     structured progress (compose download ticks). A line with the
 *     same key updates the existing row in place rather than
 *     appending, so a layer download shows one row that changes
 *     value instead of dozens of scrolling lines. Plain `line` events
 *     always append.
 */

export type LogStreamStatus = 'connecting' | 'live' | 'reconnecting' | 'closed' | 'error'

/** LogLine is one row in the log buffer. `key`, when present, marks the
 * row as replaceable: a later `line-replace` event with the same key
 * overwrites `text` in place (same position) instead of appending. */
export type LogLine = {
  text: string
  key?: string
}

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
  lines: LogLine[]
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

/** One pending event from the SSE stream, awaiting batch flush. */
type PendingEvent =
  | { op: 'append'; text: string }
  | { op: 'replace'; text: string; key: string }

/** Convert raw seed strings into LogLine rows (no keys). */
function seedToLines(seed: string[]): LogLine[] {
  return seed.map((text) => ({ text }))
}

export function useLogStream(
  url: string | null,
  options: UseLogStreamOptions = {},
): UseLogStreamResult {
  const { enabled = true, paused: initialPaused = false, initialLines } = options
  // initialLines is consumed once on first mount; subsequent updates
  // are ignored (the user has already started tailing).
  const seedRef = useRef<string[] | undefined>(initialLines)
  const [lines, setLines] = useState<LogLine[]>(() => {
    if (seedRef.current && seedRef.current.length > 0) {
      // Honor the seed but cap at MAX_LINES so a stale `tail=9999`
      // doesn't blow up the page.
      return seedToLines(seedRef.current.slice(-MAX_LINES))
    }
    return []
  })
  const [status, setStatus] = useState<LogStreamStatus>('closed')
  const [error, setError] = useState<string | null>(null)
  const [paused, setPaused] = useState<boolean>(initialPaused)
  // Backlog of events that arrived while paused. Drained on resume.
  const backlogRef = useRef<PendingEvent[]>([])
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
    // New URL - open a fresh stream.
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
    // Batch `line` / `line-replace` events into one setState per
    // microtask tick. A chatty container (nginx access log, bash
    // loop, etc.) can emit hundreds of lines per second; without
    // batching we'd re-render the entire 5000-line buffer that many
    // times per second, pegging a CPU core. A microtask runs before
    // the browser yields (paint, layout, etc.), so any events that
    // arrive in the same dispatch loop are coalesced into one React
    // render. We use microtask over rAF for two reasons: (1) rAF
    // waits up to 16ms; if the browser is idle that's wasted latency
    // on a tail; (2) tests can flush microtasks deterministically
    // with `await Promise.resolve()`.
    const pendingRef: { current: PendingEvent[] } = { current: [] }
    const scheduledRef: { current: boolean } = { current: false }
    const flush = () => {
      scheduledRef.current = false
      if (pendingRef.current.length === 0) return
      const batch = pendingRef.current
      pendingRef.current = []
      setLines((prev) => applyEvents(prev, batch))
    }
    const enqueue = (ev: PendingEvent) => {
      if (pausedRef.current) {
        backlogRef.current.push(ev)
        // Bound the backlog so a long pause doesn't OOM the page.
        if (backlogRef.current.length > MAX_LINES) {
          backlogRef.current.splice(0, backlogRef.current.length - MAX_LINES)
        }
        return
      }
      pendingRef.current.push(ev)
      if (!scheduledRef.current) {
        scheduledRef.current = true
        queueMicrotask(flush)
      }
    }
    es.addEventListener('line', (ev: MessageEvent) => {
      enqueue({ op: 'append', text: ev.data as string })
    })
    es.addEventListener('line-replace', (ev: MessageEvent) => {
      // data format: "key\tmsg". Split on the FIRST tab so a message
      // containing tabs is preserved. The server guarantees a key is
      // present (it only emits line-replace for keyed lines).
      const data = ev.data as string
      const tabIdx = data.indexOf('\t')
      if (tabIdx < 0) {
        // Malformed (no key) - treat as a plain append so the text
        // isn't lost.
        enqueue({ op: 'append', text: data })
        return
      }
      enqueue({ op: 'replace', key: data.slice(0, tabIdx), text: data.slice(tabIdx + 1) })
    })
    es.addEventListener('error', () => {
      // EventSource auto-reconnects after the server's `retry: N`
      // directive. While in that gap we surface "reconnecting" so the
      // UI can dim the indicator; a permanent failure (e.g. 503 on
      // first connect) will keep the status as "reconnecting" or
      // "error" depending on readyState.
      setStatus(es.readyState === EventSource.CLOSED ? 'error' : 'reconnecting')
    })
    es.addEventListener('log-error', (ev: MessageEvent) => {
      // Server-sent `event: log-error` from the SSE helper - we
      // treat this as a soft error (the line is informational, the
      // stream may still recover). We deliberately don't use the
      // event name `error` here because EventSource already has a
      // built-in `error` handler above for connection-level issues.
      setError((ev.data as string) || 'log stream error')
    })
    es.addEventListener('end', () => {
      // Server-side sentinel that the deploy log stream is done.
      // The deploy reached a terminal status, no more lines are
      // coming, and the server has replayed everything it will ever
      // replay. Without this handler the browser's EventSource
      // auto-reconnects on close (per the `retry: 2000` directive
      // the server emits) and the next connect just replays the
      // same history again - an infinite loop visible to the
      // operator as the same three lines cycling forever.
      //
      // Container log streams (Logs tab, system page) don't send
      // `end`, so this handler is a no-op for them - only the
      // deploy stream opts in by emitting the event before
      // closing.
      es.close()
      setStatus('closed')
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
    setLines((prev) => applyEvents(prev, drained))
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

/** applyEvents applies a batch of append/replace events to the line
 * buffer in one immutable pass, capping the result at MAX_LINES.
 *
 * - `append`: push a new {text} row.
 * - `replace`: find the last row with the same key (scan backwards) and
 *   overwrite its `text` in place; if none exists, fall back to append
 *   (carrying the key so future replaces can find it).
 *
 * Replace never grows the buffer length, so a chatty download-progress
 * stream that only emits replaces for a fixed set of layer keys stays
 * at a constant row count. Only appends (and the first occurrence of a
 * new key) can grow the buffer toward the MAX_LINES cap. */
function applyEvents(prev: LogLine[], batch: PendingEvent[]): LogLine[] {
  if (batch.length === 0) return prev
  // Start from a mutable copy; replace ops mutate in place (no length
  // change), appends push. We rebuild once at the end for the cap.
  const next = prev.slice()
  for (const ev of batch) {
    if (ev.op === 'append') {
      next.push({ text: ev.text })
    } else {
      let found = false
      for (let i = next.length - 1; i >= 0; i--) {
        if (next[i].key === ev.key) {
          next[i] = { ...next[i], text: ev.text }
          found = true
          break
        }
      }
      if (!found) {
        next.push({ text: ev.text, key: ev.key })
      }
    }
  }
  if (next.length > MAX_LINES) {
    next.splice(0, next.length - MAX_LINES)
  }
  return next
}
