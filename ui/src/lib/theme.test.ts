// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  THEME_STORAGE_KEY,
  applyTheme,
  readTheme,
  toggleTheme,
} from './theme'

// jsdom's localStorage is unavailable in our setup (no
// --localstorage-file), so install a minimal in-memory stub for
// the duration of this suite.
const store = new Map<string, string>()
const localStorageStub = {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
  clear: () => store.clear(),
  key: (i: number) => Array.from(store.keys())[i] ?? null,
  get length() {
    return store.size
  },
}

afterEach(() => {
  document.documentElement.removeAttribute('data-theme')
  store.clear()
})

describe('theme', () => {
  beforeEach(() => {
    // Pin matchMedia to a known default so the "no stored, no attr"
    // branch is deterministic across test environments.
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }),
    })
    vi.stubGlobal('localStorage', localStorageStub)
  })

  it('applies the theme to the document attribute and localStorage', () => {
    applyTheme('dark')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    expect(window.localStorage?.getItem(THEME_STORAGE_KEY)).toBe('dark')
    applyTheme('light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
    expect(window.localStorage?.getItem(THEME_STORAGE_KEY)).toBe('light')
  })

  it('readTheme prefers the data-theme attribute over storage', () => {
    document.documentElement.setAttribute('data-theme', 'dark')
    window.localStorage?.setItem(THEME_STORAGE_KEY, 'light')
    expect(readTheme()).toBe('dark')
  })

  it('readTheme falls back to localStorage when attribute is absent', () => {
    window.localStorage?.setItem(THEME_STORAGE_KEY, 'dark')
    expect(readTheme()).toBe('dark')
  })

  it('readTheme falls back to matchMedia when nothing is stored', () => {
    // Override the beforeEach default to dark
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: (query: string) => ({
        matches: true,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }),
    })
    expect(readTheme()).toBe('dark')
  })

  it('readTheme defaults to light when nothing is set', () => {
    expect(readTheme()).toBe('light')
  })

  it('toggleTheme flips between light and dark', () => {
    applyTheme('light')
    expect(toggleTheme()).toBe('dark')
    expect(readTheme()).toBe('dark')
    expect(toggleTheme()).toBe('light')
    expect(readTheme()).toBe('light')
  })
})
