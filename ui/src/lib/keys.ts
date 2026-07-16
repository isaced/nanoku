import { useEffect, useRef } from 'react'

/**
 * useKeyCombo registers a single global keydown handler for the
 * given combo (e.g. \`"mod+k"\`, \`"g d"\`, \`"?"\`).
 *
 * \`mod\` is the platform modifier: ⌘ on macOS, Ctrl elsewhere.
 * Multi-key sequences (the \`g d\` / \`g s\` vim-style nav) are
 * supported via a small state machine: after the first key is
 * pressed, the hook waits up to `sequenceTimeoutMs` for the
 * next key before resetting.
 *
 * The handler is suppressed when the user is typing in an input,
 * textarea, or contenteditable region — Cmd+K should still
 * work, but \`g d\` from inside a comment box should not.
 */

const SEQUENCE_TIMEOUT_MS = 800

export function isMac(): boolean {
  if (typeof navigator === 'undefined') return false
  return /Mac|iPod|iPhone|iPad/.test(navigator.platform)
}

function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  if (target.isContentEditable) return true
  return false
}

type KeyHandler = (e: KeyboardEvent) => void

// The sequence buffer is keyed by combo and lives on globalThis so
// it survives React's double-effect mount/unmount in StrictMode +
// HMR module replacement — both of which would otherwise reset the
// multi-key state mid-combo. We key by the combo string so two
// hooks with different combos each get their own buffer.
type SequenceBuffer = { keys: string[]; timer: number | null }
type KeysGlobal = typeof globalThis & {
  __nanoku_key_buffers__?: Map<string, SequenceBuffer>
}
const g = globalThis as KeysGlobal
function getBuffer(combo: string): SequenceBuffer {
  if (!g.__nanoku_key_buffers__) g.__nanoku_key_buffers__ = new Map()
  let buf = g.__nanoku_key_buffers__.get(combo)
  if (!buf) {
    buf = { keys: [], timer: null }
    g.__nanoku_key_buffers__.set(combo, buf)
  }
  return buf
}

export function useKeyCombo(
  combo: string,
  handler: KeyHandler,
  options: { allowInInputs?: boolean; sequenceTimeoutMs?: number } = {},
): void {
  const { allowInInputs = false, sequenceTimeoutMs = SEQUENCE_TIMEOUT_MS } =
    options
  const handlerRef = useRef<KeyHandler>(handler)
  handlerRef.current = handler

  useEffect(() => {
    const parts = combo
      .toLowerCase()
      .split(/\s+/)
      .filter(Boolean)
    const buf = getBuffer(combo)

    function clearPending() {
      buf.keys = []
      if (buf.timer != null) {
        window.clearTimeout(buf.timer)
        buf.timer = null
      }
    }

    function onKeyDown(e: KeyboardEvent) {
      // Don't swallow browser shortcuts. Esc is always allowed (used
      // to close the command palette); everything else respects the
      // typing-target rule unless the caller explicitly opts in.
      if (!allowInInputs && isTypingTarget(e.target) && e.key !== 'Escape') {
        clearPending()
        return
      }
      const key = e.key.toLowerCase()
      const mod = isMac() ? e.metaKey : e.ctrlKey
      const expectedMod = parts[0]?.startsWith('mod+')
      if (expectedMod) {
        // Single-key combo with modifier.
        const rest = parts[0]!.slice(4)
        if (mod && key === rest) {
          e.preventDefault()
          handlerRef.current(e)
        }
        return
      }
      // Multi-key sequence. Push the new key, then check.
      buf.keys.push(key)
      if (buf.timer != null) window.clearTimeout(buf.timer)
      buf.timer = window.setTimeout(clearPending, sequenceTimeoutMs)
      if (buf.keys.length > parts.length) {
        buf.keys.shift()
      }
      // `matched` is "buf.keys is a prefix of parts" — so a
      // partial match (e.g. 'g' for 'g d') keeps the buffer and
      // a non-prefix (e.g. 'g s' for 'g d') drops it. The previous
      // implementation used parts.every which compared the
      // missing slots to undefined and reset the buffer
      // immediately on a partial match, breaking the sequence.
      const matched = buf.keys.every((k, i) => k === parts[i])
      const isFull = buf.keys.length === parts.length
      if (isFull && matched) {
        e.preventDefault()
        clearPending()
        handlerRef.current(e)
      } else if (!matched) {
        clearPending()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => {
      window.removeEventListener('keydown', onKeyDown)
      clearPending()
    }
  }, [combo, allowInInputs, sequenceTimeoutMs])
}
