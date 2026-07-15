import { Tooltip } from 'antd'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SystemStatus } from '../lib/types'

// Lucide v1 removed all brand icons; use the official GitHub mark instead.
function GithubMark({ size = 14 }: { size?: number }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.4 3-.405 1.02.005 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
    </svg>
  )
}

const GITHUB_URL = 'https://github.com/isaced/nanoku'
const RELEASES_URL = `${GITHUB_URL}/releases/latest`
const LATEST_RELEASE_API = 'https://api.github.com/repos/isaced/nanoku/releases/latest'
const DEV_COMMIT = 'none'
const UPDATE_CHECK_TTL_MS = 60 * 60 * 1000

type UpdateState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'available'; latest: string }
  | { status: 'uptodate' }
  | { status: 'error' }

type CacheEntry =
  | { kind: 'fresh'; checkedAt: number; result: UpdateState }
  | { kind: 'inflight'; promise: Promise<UpdateState> }

let updateCache: CacheEntry | null = null

export function resetUpdateCache(): void {
  updateCache = null
}

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

async function checkForUpdate(version: string): Promise<UpdateState> {
  if (
    updateCache?.kind === 'fresh' &&
    Date.now() - updateCache.checkedAt < UPDATE_CHECK_TTL_MS
  ) {
    return updateCache.result
  }
  if (updateCache?.kind === 'inflight') {
    return updateCache.promise
  }

  const promise = (async (): Promise<UpdateState> => {
    try {
      const res = await fetch(LATEST_RELEASE_API, {
        headers: { Accept: 'application/vnd.github+json' },
      })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as { tag_name?: string }
      const latest = data.tag_name?.replace(/^v/, '') ?? ''
      if (!latest) throw new Error('empty tag_name')
      return compareSemver(latest, version) > 0
        ? { status: 'available', latest }
        : { status: 'uptodate' }
    } catch {
      return { status: 'error' }
    }
  })()

  updateCache = { kind: 'inflight', promise }
  const result = await promise
  updateCache = { kind: 'fresh', checkedAt: Date.now(), result }
  return result
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
    checkForUpdate(version).then((result) => {
      if (!cancelled) setUpdate(result)
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
            <GithubMark size={14} />
          </a>
        </div>
      </div>
    </footer>
  )
}
