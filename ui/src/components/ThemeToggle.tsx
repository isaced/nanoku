import { Button, Tooltip } from 'antd'
import { Moon, Sun } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { applyTheme, readTheme, type Theme } from '../lib/theme'

/**
 * ThemeToggle is the light/dark switch in the TopNav. State is
 * derived from the DOM attribute (the same one the CSS reads),
 * not from React state — the CSS does the actual work, so we
 * only need to know "is the user currently in dark mode?" to
 * pick which icon to show.
 *
 * The toggle listens for the \`storage\` event so a change in
 * one tab propagates to the others without a re-render storm.
 */
export function ThemeToggle() {
  const { t } = useTranslation('nav')
  const [theme, setTheme] = useState<Theme>(() => readTheme())

  useEffect(() => {
    // Sync from other tabs. Without this, opening a second tab
    // would show whatever that tab decided on its own boot and
    // not pick up changes from the first tab.
    function onStorage(e: StorageEvent) {
      if (e.key === 'nanoku.theme') {
        const next = readTheme()
        setTheme(next)
      }
    }
    window.addEventListener('storage', onStorage)
    return () => window.removeEventListener('storage', onStorage)
  }, [])

  function onClick() {
    const next: Theme = theme === 'dark' ? 'light' : 'dark'
    applyTheme(next)
    setTheme(next)
  }

  const isDark = theme === 'dark'
  return (
    <Tooltip title={isDark ? t('lightMode') : t('darkMode')}>
      <Button
        type="text"
        size="small"
        onClick={onClick}
        aria-label={isDark ? t('lightMode') : t('darkMode')}
        className="!text-[var(--fg-muted)] hover:!text-[var(--fg)] hover:!bg-[var(--bg-elevated)]"
      >
        {isDark ? <Sun size={15} /> : <Moon size={15} />}
      </Button>
    </Tooltip>
  )
}
