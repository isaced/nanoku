import type { CSSProperties, ReactNode } from 'react'

/**
 * ansi.ts is a zero-dependency ANSI SGR (Select Graphic Rendition)
 * parser that turns the escape sequences containers emit for colored
 * log output into React nodes with inline styles.
 *
 * Scope: we handle CSI sequences of the form `\x1b[<params>m` (SGR),
 * which cover the overwhelming majority of container log coloring
 * (pino, winston, logrus, Uptime Kuma's server logger, etc.). Other
 * CSI sequences (cursor movement `\x1b[2K`, erase display `\x1b[2J`,
 * etc.) are stripped entirely so they don't render as visible garbage.
 *
 * The palette is tuned for the project's light theme (see styles.css
 * `:root` variables): standard ANSI colors are mapped to readable
 * hex values that harmonize with `--fg`, `--fg-muted`, `--success`,
 * and `--danger` rather than the raw 16-color VGA values, which would
 * be too saturated on a white background.
 */

const ESC = '\x1b'

/** Map of ANSI SGR foreground color codes to hex values. Codes 30-37
 * are the standard set; 90-97 are the "bright" variants. We collapse
 * bright onto a slightly different shade since the distinction is
 * rarely meaningful in log output. */
const FG_COLORS: Record<number, string> = {
  30: '#4b5563', // black  -> slate (readable on white)
  31: '#dc2626', // red    -> --danger-ish
  32: '#16a34a', // green  -> --success
  33: '#ca8a04', // yellow -> amber
  34: '#2563eb', // blue
  35: '#9333ea', // magenta
  36: '#0891b2', // cyan
  37: '#374151', // white  -> --fg (dark gray, not literal white)
  90: '#6b7280', // bright black   -> --fg-muted
  91: '#ef4444', // bright red
  92: '#22c55e', // bright green
  93: '#eab308', // bright yellow
  94: '#3b82f6', // bright blue
  95: '#a855f7', // bright magenta
  96: '#06b6d4', // bright cyan
  97: '#111827', // bright white   -> darkest fg
}

/** Background color codes (40-47 standard, 100-107 bright). */
const BG_COLORS: Record<number, string> = {
  40: '#374151', 41: '#dc2626', 42: '#16a34a', 43: '#ca8a04',
  44: '#2563eb', 45: '#9333ea', 46: '#0891b2', 47: '#9ca3af',
  100: '#6b7280', 101: '#ef4444', 102: '#22c55e', 103: '#eab308',
  104: '#3b82f6', 105: '#a855f7', 106: '#06b6d4', 107: '#111827',
}

/** Active SGR attributes tracked across a single line. Reset (code 0)
 * returns everything to defaults. */
interface SgrState {
  fg: string | null
  bg: string | null
  bold: boolean
  dim: boolean
  italic: boolean
  underline: boolean
}

function defaultState(): SgrState {
  return { fg: null, bg: null, bold: false, dim: false, italic: false, underline: false }
}

/** Apply a single SGR parameter (or semicolon-separated group) to the
 * state. Code 0 resets everything. */
function applySgr(state: SgrState, code: number): void {
  if (code === 0) {
    Object.assign(state, defaultState())
    return
  }
  if (code === 1) { state.bold = true; return }
  if (code === 2) { state.dim = true; return }
  if (code === 3) { state.italic = true; return }
  if (code === 4) { state.underline = true; return }
  if (code === 22) { state.bold = false; state.dim = false; return }
  if (code === 23) { state.italic = false; return }
  if (code === 24) { state.underline = false; return }
  if (FG_COLORS[code]) { state.fg = FG_COLORS[code]; return }
  if (BG_COLORS[code]) { state.bg = BG_COLORS[code]; return }
  // 39 = default fg, 49 = default bg. Unknown codes are ignored
  // (there are many obscure SGR codes we don't need for logs).
  if (code === 39) { state.fg = null; return }
  if (code === 49) { state.bg = null; return }
}

/** Convert the current SGR state into a CSSProperties object. Returns
 * null when the state is all-defaults, so plain text segments don't
 * get wrapped in a <span> at all. */
function stateToStyle(state: SgrState): CSSProperties | null {
  const style: CSSProperties = {}
  if (state.fg) style.color = state.fg
  if (state.bg) style.backgroundColor = state.bg
  if (state.bold) style.fontWeight = 700
  if (state.dim) style.opacity = 0.6
  if (state.italic) style.fontStyle = 'italic'
  if (state.underline) style.textDecoration = 'underline'
  return Object.keys(style).length > 0 ? style : null
}

/** Regex matching any CSI sequence: ESC [ <params> <intermediate> <final>.
 * We match broadly (any final byte) so non-SGR CSI sequences like cursor
 * movement are stripped, then inspect the params ourselves for SGR ('m'). */
const CSI_RE = /\x1b\[([0-9;]*)[A-Za-z]/

/** Parse a string with embedded ANSI escape sequences into an array of
 * React nodes. Consecutive text runs that share the same SGR state are
 * merged into a single string to minimize React node count.
 *
 * Pure text (no escape sequences) returns a single-element array
 * containing the original string, so callers that never emit ANSI see
 * identical output to rendering the raw string. */
export function parseAnsi(text: string): ReactNode[] {
  if (!text.includes(ESC)) {
    return [text]
  }

  const nodes: ReactNode[] = []
  const state = defaultState()
  let lastIndex = 0
  let pending = ''
  let key = 0

  const flushPending = () => {
    if (pending !== '') {
      const style = stateToStyle(state)
      if (style) {
        nodes.push(
          <span key={key++} style={style}>
            {pending}
          </span>,
        )
      } else {
        nodes.push(pending)
      }
      pending = ''
    }
  }

  let match: RegExpExecArray | null
  // Reset regex state since CSI_RE is a module-level const with 'g'-like
  // behavior via exec in a loop; use a local regex to avoid lastIndex bugs.
  const re = new RegExp(CSI_RE.source, 'g')
  while ((match = re.exec(text)) !== null) {
    // Accumulate plain text before this escape sequence.
    pending += text.slice(lastIndex, match.index)
    lastIndex = re.lastIndex

    const params = match[1]
    const finalByte = match[0][match[0].length - 1]

    if (finalByte === 'm') {
      // SGR: apply each parameter. Empty params (just `\x1b[m`) means
      // a single reset (code 0).
      flushPending()
      if (params === '') {
        applySgr(state, 0)
      } else {
        for (const p of params.split(';')) {
          if (p === '') continue
          applySgr(state, Number(p))
        }
      }
    }
    // Non-SGR CSI sequences: strip silently (cursor movement, erase,
    // scroll, etc.). We've already advanced lastIndex past them.
  }

  // Trailing plain text after the last escape sequence.
  pending += text.slice(lastIndex)
  flushPending()

  return nodes
}
