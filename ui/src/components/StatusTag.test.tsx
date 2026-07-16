// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import i18n from 'i18next'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import { StatusTag } from './StatusTag'

let i18nReady = false
async function ensureI18n() {
  if (i18nReady) return
  // We intentionally init a dedicated instance instead of importing
  // `../i18n` (which initialises a singleton): a singleton would
  // leak translations from this test into unrelated route tests
  // (the dashboard / sites / apps tests all `import '../i18n'` and
  // share one instance — racing their init from this file made 60+
  // other tests time out).
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'common',
    ns: ['common'],
    resources: {
      en: {
        common: {
          status: {
            running: 'running',
            exited: 'exited',
            notDeployed: 'not deployed',
            configured: 'configured',
            notFound: 'not found',
            restarting: 'restarting',
            paused: 'paused',
          },
        },
      },
    },
    interpolation: { escapeValue: false },
  })
  i18nReady = true
}

beforeAll(async () => {
  await ensureI18n()
})

afterEach(() => {
  cleanup()
})

afterEach(() => {
  cleanup()
})

function renderWithI18n(ui: React.ReactNode) {
  return render(<I18nextProvider i18n={i18n}>{ui}</I18nextProvider>)
}

describe('StatusTag', () => {
  it('renders the translated label when i18nKey is set', () => {
    renderWithI18n(
      <StatusTag variant="success" i18nKey="running" data-testid="tag-running" />,
    )
    expect(screen.getByTestId('tag-running').textContent).toMatch(/running/)
  })

  it('renders raw label when no i18nKey', () => {
    renderWithI18n(
      <StatusTag variant="danger" label="dead" data-testid="tag-dead" />,
    )
    expect(screen.getByTestId('tag-dead').textContent).toContain('dead')
  })

  it('appends children after the tag content', () => {
    renderWithI18n(
      <StatusTag variant="success" i18nKey="running" data-testid="tag-with-name">
        nanoku-app-1
      </StatusTag>,
    )
    expect(screen.getByTestId('tag-with-name').textContent).toMatch(
      /running.*nanoku-app-1/,
    )
  })

  it('hides icon when withIcon=false', () => {
    const { container } = renderWithI18n(
      <StatusTag
        variant="muted"
        i18nKey="notDeployed"
        withIcon={false}
        data-testid="tag-no-icon"
      />,
    )
    expect(container.querySelector('svg')).toBeNull()
  })
})
