/**
 * logLine.ts splits the RFC3339Nano timestamp that the Docker engine
 * prepends to every log line (when `Timestamps: true` is set on the
 * ContainerLogs API call) from the line body, so the UI can render it
 * as a separate muted column instead of a long inline prefix.
 *
 * Docker's format is exactly: `<RFC3339Nano> <body>\n`, e.g.
 *   2026-07-16T03:49:56.661898467Z Welcome to Uptime Kuma
 *
 * We match this conservatively: the regex requires the full date, a
 * fractional seconds component (Docker always emits nanosecond
 * precision), and a trailing `Z`. Lines that don't match (deploy log
 * progress, application-emitted timestamps that aren't at column 0,
 * plain text) pass through unchanged with `ts: null`.
 *
 * The timestamp is reformatted to `HH:MM:SS` for display: the date is
 * dropped (every line in a tail shares the same day in practice), and
 * sub-second precision is dropped (rarely useful and visually noisy in
 * a log column). The original full-precision timestamp is not retained
 * because nothing in the UI surfaces it today; if a future feature
 * needs it, the split function can be extended to return the raw value.
 */

export interface SplitTimestampResult {
  /** Reformatted timestamp (`HH:MM:SS`) for display, or null when the
   * line has no Docker engine timestamp prefix. */
  ts: string | null
  /** The line body without the timestamp prefix. When `ts` is null
   * this is the original line unchanged. */
  body: string
}

/** Matches Docker's `Timestamps: true` prefix:
 *   YYYY-MM-DDTHH:MM:SS.nnnnnnnnnZ<single space> */
const DOCKER_TS_RE = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})\.\d+Z /

/** Split a log line into a display timestamp and body. See file doc
 * for the matching rules. The function never throws: malformed input
 * returns `{ ts: null, body: line }`. */
export function splitTimestamp(line: string): SplitTimestampResult {
  const m = DOCKER_TS_RE.exec(line)
  if (!m) {
    return { ts: null, body: line }
  }
  // Groups: 1=year 2=month 3=day 4=hour 5=minute 6=second
  const ts = `${m[4]}:${m[5]}:${m[6]}`
  // body is everything after the timestamp and its trailing space.
  const body = line.slice(m[0].length)
  return { ts, body }
}
