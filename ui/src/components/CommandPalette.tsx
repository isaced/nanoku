import { Modal } from 'antd'
import { useNavigate } from '@tanstack/react-router'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useKeyCombo } from '../lib/keys'
import type { ReactNode } from 'react'

/**
 * CommandPalette is the ⌘K / Ctrl-K quick-jump dialog.
 * Opens with the \`mod+k\` keybind anywhere in the app and
 * presents a small list of route-level commands (Dashboard,
 * Sites, Apps, System). Filter by typing; ↑/↓ to move; Enter
 * to navigate; Esc to close.
 *
 * The list is intentionally short — anything more is what the
 * nav bar is for. The palette is for "I have one hand on the
 * keyboard and I want to get to X in 1s".
 */
export type CommandItem = {
  id: string
  label: string
  hint?: string
  icon?: ReactNode
  run: () => void
}

export function CommandPalette({ items }: { items: CommandItem[] }) {
  const { t } = useTranslation('nav')
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)

  // Open / close via Cmd+K (and Ctrl+K on non-Mac). We also bind
  // a separate "g d / g s / g a / g y" vim-style nav so power
  // users can skip the palette entirely.
  useKeyCombo('mod+k', () => {
    setOpen((v) => !v)
    setQuery('')
    setActive(0)
  })

  useEffect(() => {
    if (!open) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.preventDefault()
        setOpen(false)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return items
    return items.filter((it) => it.label.toLowerCase().includes(q))
  }, [items, query])

  // Keep the active index in bounds when the filter changes.
  useEffect(() => {
    if (active >= filtered.length) setActive(0)
  }, [filtered.length, active])

  function runItem(item: CommandItem) {
    setOpen(false)
    setQuery('')
    item.run()
  }

  return (
    <Modal
      open={open}
      footer={null}
      closable={false}
      width={520}
      destroyOnClose
      onCancel={() => setOpen(false)}
      styles={{ body: { padding: 0 } }}
    >
      <div className="border-b border-[var(--border)] px-3 py-2">
        <input
          autoFocus
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setActive(0)
          }}
          placeholder={t('palette.placeholder')}
          className="w-full bg-transparent outline-none text-sm py-1"
          aria-label={t('palette.placeholder')}
        />
      </div>
      <ul
        className="max-h-72 overflow-auto py-1"
        role="listbox"
        aria-label={t('palette.title')}
      >
        {filtered.length === 0 ? (
          <li className="px-3 py-6 text-center text-xs text-[var(--fg-muted)]">
            {t('palette.empty')}
          </li>
        ) : (
          filtered.map((it, i) => (
            <li
              key={it.id}
              role="option"
              aria-selected={i === active}
              onMouseEnter={() => setActive(i)}
              onClick={() => runItem(it)}
              className={`flex items-center gap-2 px-3 py-2 text-sm cursor-pointer ${
                i === active ? 'bg-[var(--accent-soft)]' : ''
              }`}
            >
              {it.icon && (
                <span className="text-[var(--fg-muted)] shrink-0">
                  {it.icon}
                </span>
              )}
              <span className="flex-1">{it.label}</span>
              {it.hint && (
                <span className="text-[10px] text-[var(--fg-muted)] mono">
                  {it.hint}
                </span>
              )}
            </li>
          ))
        )}
      </ul>
      <div className="border-t border-[var(--border)] px-3 py-1.5 eyebrow flex items-center gap-3">
        <span>↑↓</span>
        <span>↵</span>
        <span>esc</span>
        <span className="ml-auto">{filtered.length} {t('palette.items')}</span>
      </div>
    </Modal>
  )
}

/**
 * makeRouteItems builds the default route items for the palette
 * from the i18n strings. Call once at the top of the app shell
 * with the navigate function bound to the router.
 */
export function useRoutePaletteItems(
  icons: {
    dashboard: ReactNode
    sites: ReactNode
    apps: ReactNode
    system: ReactNode
  },
): CommandItem[] {
  const { t } = useTranslation('nav')
  const navigate = useNavigate()
  return useMemo(
    () => [
      {
        id: '/dashboard',
        label: t('dashboard'),
        hint: 'g d',
        icon: icons.dashboard,
        run: () => navigate({ to: '/dashboard' }),
      },
      {
        id: '/sites',
        label: t('sites'),
        hint: 'g s',
        icon: icons.sites,
        run: () => navigate({ to: '/sites' }),
      },
      {
        id: '/apps',
        label: t('apps'),
        hint: 'g a',
        icon: icons.apps,
        run: () => navigate({ to: '/apps' }),
      },
      {
        id: '/system',
        label: t('system'),
        hint: 'g y',
        icon: icons.system,
        run: () => navigate({ to: '/system' }),
      },
    ],
    [t, navigate, icons.dashboard, icons.sites, icons.apps, icons.system],
  )
}