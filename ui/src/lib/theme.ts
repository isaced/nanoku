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

import { useEffect, useState } from 'react'

export type Theme = 'light' | 'dark'

export const THEME_STORAGE_KEY = 'nanoku.theme'
const THEME_ATTR = 'data-theme'
// Custom event broadcast on the document so React components that
// need to re-render (e.g. ConfigProvider picking the right algorithm)
// can subscribe. The data-theme attribute / CSS variables still
// react synchronously — this is only needed for things that can't
// be expressed as CSS rules (notably antd's algorithm prop, which
// has to be passed at the React tree level).
export const THEME_CHANGE_EVENT = 'nanoku:theme-change'

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
 * localStorage, then notifies in-tab subscribers. */
export function applyTheme(theme: Theme): void {
  if (typeof document !== 'undefined') {
    document.documentElement.setAttribute(THEME_ATTR, theme)
    document.dispatchEvent(
      new CustomEvent<Theme>(THEME_CHANGE_EVENT, { detail: theme }),
    )
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

/** useThemeState subscribes to in-tab theme changes. The current
 * value is read on mount; subsequent toggles (from the same tab
 * or another tab via the \`storage\` event) trigger a re-render.
 * Used by ConfigProvider to pick the right antd algorithm. */
export function useThemeState(): Theme {
  // SSR / pre-mount: default to light. The boot script
  // (main.tsx -> applyTheme(readTheme())) updates the DOM
  // before React paints, so the first client render sees
  // the right value.
  if (typeof window === 'undefined') return 'light'
  // Lazy state init: read once on mount, not on every render.
  const [theme, setTheme] = useState<Theme>(() => readTheme())
  useEffect(() => {
    function onChange() {
      setTheme(readTheme())
    }
    document.addEventListener(THEME_CHANGE_EVENT, onChange)
    window.addEventListener('storage', (e) => {
      if (e.key === THEME_STORAGE_KEY) onChange()
    })
    return () => {
      document.removeEventListener(THEME_CHANGE_EVENT, onChange)
    }
  }, [])
  return theme
}
