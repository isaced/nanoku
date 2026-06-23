// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ConfigProvider } from 'antd'
import enUS from 'antd/locale/en_US'
import '../i18n'
import { RouteError } from './RouteError'

function renderWithProviders(ui: React.ReactNode) {
  return render(<ConfigProvider locale={enUS}>{ui}</ConfigProvider>)
}

describe('RouteError', () => {
  it('renders the error message from an Error instance', () => {
    renderWithProviders(<RouteError error={new Error('boom')} />)
    expect(screen.getByText('boom')).toBeTruthy()
  })

  it('renders a generic message for non-Error values', () => {
    renderWithProviders(<RouteError error={'oops'} />)
    expect(screen.getByText('oops')).toBeTruthy()
  })

  it('renders the retry button when reset is provided', () => {
    const reset = vi.fn()
    renderWithProviders(<RouteError error={new Error('boom')} reset={reset} />)
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
  })
})