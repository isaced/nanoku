/**
 * Theme is the single source of truth for the user's chosen light /
 * dark mode. We write the choice to localStorage and to
 * <html data-theme=…> so the CSS variables flip without a
 * re-render. The value is also broadcast to other tabs via the
 * native \`storage\` event so the toggle in one tab updates the
 * navbar in the next.
 *
 * The module is intentionally tiny — no React context, no
 * provider. The CSS does the actual work; the module is just the
 * "what's the current value + how to set it" surface.
 */

export type Theme = 'light' | 'dark'

export const THEME_STORAGE_KEY = 'nanoku.theme'
const THEME_ATTR = 'data-theme'

function getStoredTheme(): Theme | null {
  if (typeof window === 'undefined') return null
  const raw = window.localStorage?.getItem(THEME_STORAGE_KEY)
  if (raw === 'light' || raw === 'dark') return raw
  return null
}

/** readTheme returns the current theme by looking at the DOM
 * attribute (which the CSS reads), falling back to localStorage,
 * falling back to the OS preference, falling back to light. */
export function readTheme(): Theme {
  if (typeof document !== 'undefined') {
    const attr = document.documentElement.getAttribute(THEME_ATTR)
    if (attr === 'light' || attr === 'dark') return attr
  }
  const stored = getStoredTheme()
  if (stored) return stored
  if (typeof window !== 'undefined' && window.matchMedia) {
    if (window.matchMedia('(prefers-color-scheme: dark)').matches) {
      return 'dark'
    }
  }
  return 'light'
}

/** applyTheme writes the theme to the DOM attribute and to
 * localStorage. The CSS variables in styles.css read the
 * attribute and update automatically — no React re-render
 * needed. */
export function applyTheme(theme: Theme): void {
  if (typeof document !== 'undefined') {
    document.documentElement.setAttribute(THEME_ATTR, theme)
  }
  if (typeof window !== 'undefined') {
    window.localStorage?.setItem(THEME_STORAGE_KEY, theme)
  }
}

export function toggleTheme(): Theme {
  const next: Theme = readTheme() === 'dark' ? 'light' : 'dark'
  applyTheme(next)
  return next
}
