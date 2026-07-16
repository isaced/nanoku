import { cleanup, render, screen } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import i18n from 'i18next'
import { afterEach, describe, expect, it } from 'vitest'
// The shared `../i18n` module initializes the i18next instance as a
// side effect; using the same instance keeps this test isolated from
// the rest of the suite (re-calling `i18n.init()` races with the
// shared init and breaks translations in unrelated tests).
import '../i18n'
import { StatusTag } from './StatusTag'

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
