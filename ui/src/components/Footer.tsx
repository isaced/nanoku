import { Button, Tooltip } from 'antd'
import { Github } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { SystemStatus } from '../lib/types'

const GITHUB_URL = 'https://github.com/isaced/nanoku'
const DEV_COMMIT = 'none'

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
            </>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Tooltip title={t('checkUpdateSoon')}>
            <Button
              size="small"
              disabled
              data-testid="footer-check-update"
            >
              {t('checkUpdate')}
            </Button>
          </Tooltip>
          <a
            href={GITHUB_URL}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={t('viewOnGithub')}
            className="inline-flex items-center justify-center size-7 rounded-md text-[var(--fg-muted)] hover:text-[var(--fg)] hover:bg-[var(--bg-elevated)] transition-colors"
          >
            <Github size={15} />
          </a>
        </div>
      </div>
    </footer>
  )
}
