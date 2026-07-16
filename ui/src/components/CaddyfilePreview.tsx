import { Button, Skeleton } from 'antd'
import { RefreshCw } from 'lucide-react'
import { Suspense } from 'react'
import { useTranslation } from 'react-i18next'
import { useSuspenseCaddyfile } from '../lib/hooks'
import { QueryErrorBoundary } from './QueryErrorBoundary'

/**
 * CaddyfilePreview is the "Generated Caddyfile" panel at the
 * bottom of the sites page. It wraps a Suspense + ErrorBoundary
 * around the GET /api/caddyfile query so the rest of the page
 * keeps rendering even if the Caddyfile fetch fails (e.g. the
 * server has never rendered one). The error path offers a
 * "Retry" button that re-mounts the query via the boundary
 * reset.
 */
export function CaddyfilePreview() {
  const { t } = useTranslation('sites')
  return (
    <QueryErrorBoundary
      fallback={(err, reset) => (
        <div className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4 flex items-center justify-between gap-3">
          <span className="text-xs text-[var(--danger)] mono">
            {t('caddyfile.failed')}: {(err as Error).message}
          </span>
          <Button size="small" icon={<RefreshCw size={12} />} onClick={reset}>
            {t('actions.retry', { ns: 'common' })}
          </Button>
        </div>
      )}
    >
      <Suspense
        fallback={
          <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-4 overflow-auto max-h-96 text-[var(--fg-muted)]">
            <Skeleton active paragraph={{ rows: 4 }} title={false} />
          </pre>
        }
      >
        <CaddyfileContent />
      </Suspense>
    </QueryErrorBoundary>
  )
}

function CaddyfileContent() {
  const { t } = useTranslation('sites')
  const caddyfile = useSuspenseCaddyfile()
  return (
    <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-4 overflow-auto max-h-96 text-[var(--fg-muted)]">
      {caddyfile.data || t('caddyfile.empty')}
    </pre>
  )
}
