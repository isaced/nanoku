import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

/**
 * LogScroller is the autoscroll + jump-to-bottom primitive that
 * LogViewer uses. It is intentionally decoupled from useLogStream so
 * the scroll behavior can be unit-tested in isolation and reused with
 * any line source (the current implementation expects an array of
 * strings; future callers may pass a ReadableStream or a virtualized
 * list).
 *
 * Design notes:
 *   - `stuckToBottom` is a state variable (not a ref+counter hack) so
 *     the jump button render and the scroll effect both see a
 *     consistent value. The "jitter" concern (state changes inside
 *     the post-render scroll effect resetting the scroll) doesn't
 *     apply: we only call setStuckToBottom from `onScroll` (user
 *     input) and `jumpToBottom` (button click) — never from the
 *     effect that responds to `lines`. So state updates here always
 *     correlate with intent, not with new data.
 *   - The post-render scroll uses requestAnimationFrame so the
 *     browser has applied the new content height before we read
 *     scrollHeight (otherwise we'd scroll to the old bottom).
 *   - "Stuck" is defined as "within 8px of the bottom" — chosen to
 *     tolerate sub-pixel rounding and antialiased scrollbar
 *     thimbles that some browsers leave near the bottom.
 */
export function LogScroller({
  lines,
  wordWrap = true,
  heightClass = 'h-96',
  placeholder,
  emptyHint,
}: {
  lines: string[]
  wordWrap?: boolean
  heightClass?: string
  placeholder?: React.ReactNode
  emptyHint?: React.ReactNode
}) {
  const { t } = useTranslation('logs')
  const scrollerRef = useRef<HTMLDivElement | null>(null)
  const [stuckToBottom, setStuckToBottom] = useState(true)

  useEffect(() => {
    const el = scrollerRef.current
    if (!el || !stuckToBottom) return
    const raf = requestAnimationFrame(() => {
      // Re-check stuckToBottom inside the rAF: a user scroll between
      // the effect schedule and the layout pass shouldn't snap back.
      if (stuckToBottom) el.scrollTop = el.scrollHeight
    })
    return () => cancelAnimationFrame(raf)
  }, [lines, stuckToBottom])

  const handleScroll = () => {
    const el = scrollerRef.current
    if (!el) return
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    setStuckToBottom(distance < 8)
  }

  const jumpToBottom = () => {
    const el = scrollerRef.current
    if (!el) return
    el.scrollTop = el.scrollHeight
    setStuckToBottom(true)
  }

  const hasLines = lines.length > 0
  const showJump = !stuckToBottom && hasLines
  const displayPlaceholder = !hasLines ? (placeholder ?? emptyHint ?? t('empty')) : null

  return (
    <>
      <div
        ref={scrollerRef}
        onScroll={handleScroll}
        data-testid="log-scroller"
        className={`mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-3 overflow-auto ${heightClass} text-[var(--fg-muted)] ${
          wordWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'
        }`}
      >
        {displayPlaceholder ? (
          <div className="text-xs">{displayPlaceholder}</div>
        ) : (
          lines.map((line, i) => <div key={i}>{line}</div>)
        )}
      </div>
      {showJump && (
        <button
          type="button"
          onClick={jumpToBottom}
          data-testid="log-jump-to-bottom"
          className="absolute bottom-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-full bg-[var(--accent)] text-white text-xs shadow-md hover:opacity-90"
        >
          {t('jumpToBottom')}
        </button>
      )}
    </>
  )
}
