import { Tooltip } from 'antd'
import { Github } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SystemStatus } from '../lib/types'

const GITHUB_URL = 'https://github.com/isaced/nanoku'
const RELEASES_URL = `${GITHUB_URL}/releases/latest`
const LATEST_RELEASE_API = 'https://api.github.com/repos/isaced/nanoku/releases/latest'
const DEV_COMMIT = 'none'

type UpdateState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'available'; latest: string }
  | { status: 'uptodate' }
  | { status: 'error' }

function compareSemver(a: string, b: string): number {
  const pa = a.replace(/^v/, '').split('.').map((n) => Number.parseInt(n, 10) || 0)
  const pb = b.replace(/^v/, '').split('.').map((n) => Number.parseInt(n, 10) || 0)
  const len = Math.max(pa.length, pb.length)
  for (let i = 0; i < len; i++) {
    const da = pa[i] ?? 0
    const db = pb[i] ?? 0
    if (da !== db) return da - db
  }
  return 0
}

export function Footer({
  systemStatus,
}: {
  systemStatus: SystemStatus | null
}) {
  const { t } = useTranslation('version')
  const year = new Date().getFullYear()
  const version = systemStatus?.version ?? ''
  const commit = systemStatus?.commit ?? ''
  const date = systemStatus?.date ?? ''
  const isDev = !systemStatus || commit === DEV_COMMIT
  const [update, setUpdate] = useState<UpdateState>({ status: 'idle' })

  useEffect(() => {
    if (!systemStatus || isDev) return
    let cancelled = false
    setUpdate({ status: 'loading' })
    fetch(LATEST_RELEASE_API, { headers: { Accept: 'application/vnd.github+json' } })
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((data: { tag_name?: string }) => {
        if (cancelled) return
        const latest = data.tag_name?.replace(/^v/, '') ?? ''
        if (!latest) {
          setUpdate({ status: 'error' })
          return
        }
        setUpdate(
          compareSemver(latest, version) > 0
            ? { status: 'available', latest }
            : { status: 'uptodate' },
        )
      })
      .catch(() => {
        if (!cancelled) setUpdate({ status: 'error' })
      })
    return () => {
      cancelled = true
    }
  }, [systemStatus, isDev, version])

  const tooltipBody =
    !isDev && commit
      ? `${t('tooltip.commit', { commit })}\n${t('tooltip.built', { date })}`
      : ''

  return (
    <footer className="mt-auto w-full border-t border-[var(--border)] bg-white">
      <div className="max-w-6xl w-full mx-auto px-8 py-3 flex items-center justify-between gap-4 text-xs text-[var(--fg-muted)]">
        <div className="flex items-center gap-2 min-w-0">
          <span>{t('copyright', { year })}</span>
          {version && (
            <>
              <span aria-hidden="true">·</span>
              {isDev ? (
                <span
                  className="inline-flex items-center gap-1.5 mono"
                  data-testid="footer-version"
                >
                  {t('version', { version })}
                  <span
                    className="inline-block size-1.5 rounded-full bg-[var(--border-strong)]"
                    aria-label={t('dev')}
                  />
                </span>
              ) : (
                <Tooltip title={tooltipBody}>
                  <span
                    className="mono cursor-default"
                    data-testid="footer-version"
                  >
                    {t('version', { version })}
                  </span>
                </Tooltip>
              )}
              {update.status === 'available' && (
                <Tooltip
                  title={t('updateAvailable', { latest: update.latest })}
                >
                  <a
                    href={RELEASES_URL}
                    target="_blank"
                    rel="noopener noreferrer"
                    data-testid="footer-update-available"
                    className="inline-flex items-center gap-1 text-[var(--success)] hover:underline mono"
                  >
                    <span
                      className="inline-block size-1.5 rounded-full bg-[var(--success)]"
                      aria-hidden="true"
                    />
                    v{update.latest}
                  </a>
                </Tooltip>
              )}
            </>
          )}
        </div>
        <div className="flex items-center gap-2">
          <a
            href={GITHUB_URL}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={t('viewOnGithub')}
            className="inline-flex items-center justify-center size-7 rounded-full border border-[var(--border)] bg-white text-[var(--fg-muted)] hover:text-[var(--fg)] hover:border-[var(--border-strong)] transition-colors"
          >
            <Github size={14} />
          </a>
        </div>
      </div>
    </footer>
  )
}
