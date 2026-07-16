import { Button, Tooltip } from 'antd'
import { Pause, Play, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useLogStream } from '../lib/useLogStream'
import { LogScroller } from './LogScroller'

/**
 * LogViewer renders a tail -f style panel driven by useLogStream.
 * It composes two single-responsibility pieces:
 *
 *   - useLogStream  — opens/closes the SSE connection, buffers lines
 *                     while paused, exposes the live line list
 *   - LogScroller   — autoscroll + jump-to-bottom behavior, isolated
 *                     so its logic can be unit-tested without SSE
 *
 * Used by:
 *   - AppDetailDrawer's Logs tab (per-container logs)
 *   - system.tsx's two LogPanels (Caddy + nanoku self)
 *   - DeploysTab's inline deploy log panel
 */
export function LogViewer({
  url,
  initialLines,
  emptyHint,
  wordWrap = true,
  heightClass = 'h-96',
}: {
  url: string | null
  initialLines?: string[]
  emptyHint?: React.ReactNode
  wordWrap?: boolean
  heightClass?: string
}) {
  const { t } = useTranslation('logs')
  const { lines, status, error, paused, setPaused, clear } = useLogStream(url, {
    initialLines,
  })
  const hasLines = lines.length > 0
  const disabled = !url
  return (
    <div className="relative">
      <div className="flex items-center gap-2 mb-2">
        <StatusDot status={status} />
        <span className="eyebrow">
          {t(`status.${status}`)}
        </span>
        {error && (
          <span className="text-xs text-[var(--danger)] truncate" title={error}>
            {error}
          </span>
        )}
        <div className="ml-auto flex items-center gap-1">
          <Tooltip title={paused ? t('resume') : t('pause')}>
            <Button
              size="small"
              type="text"
              aria-label={paused ? t('resume') : t('pause')}
              icon={paused ? <Play size={13} /> : <Pause size={13} />}
              onClick={() => setPaused(!paused)}
              disabled={disabled}
            />
          </Tooltip>
          <Tooltip title={t('clear')}>
            <Button
              size="small"
              type="text"
              aria-label={t('clear')}
              icon={<Trash2 size={13} />}
              onClick={clear}
              disabled={disabled || !hasLines}
            />
          </Tooltip>
        </div>
      </div>
      <LogScroller
        lines={lines}
        wordWrap={wordWrap}
        heightClass={heightClass}
        emptyHint={emptyHint ?? t('empty')}
      />
    </div>
  )
}

function StatusDot({ status }: { status: ReturnType<typeof useLogStream>['status'] }) {
  const color =
    status === 'live'
      ? 'bg-[var(--success)]'
      : status === 'reconnecting' || status === 'connecting'
        ? 'bg-[var(--accent)] animate-pulse'
        : status === 'error'
          ? 'bg-[var(--danger)]'
          : 'bg-[var(--fg-muted)]'
  return <span className={`inline-block size-2 rounded-full ${color}`} aria-hidden />
}
