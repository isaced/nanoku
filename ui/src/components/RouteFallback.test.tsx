// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ConfigProvider } from 'antd'
import enUS from 'antd/locale/en_US'
import '../i18n'
import { RouteFallback } from './RouteFallback'

function renderWithProviders(ui: React.ReactNode) {
  return render(<ConfigProvider locale={enUS}>{ui}</ConfigProvider>)
}

describe('RouteFallback', () => {
  it('exposes a loading status region for the page variant', () => {
    renderWithProviders(<RouteFallback variant="page" />)
    expect(screen.getAllByRole('status').length).toBeGreaterThan(0)
  })

  it('exposes a loading status region for the table variant', () => {
    renderWithProviders(<RouteFallback variant="table" />)
    expect(screen.getAllByRole('status').length).toBeGreaterThan(0)
  })

  it('exposes a loading status region for the drawer variant', () => {
    renderWithProviders(<RouteFallback variant="drawer" />)
    expect(screen.getAllByRole('status').length).toBeGreaterThan(0)
  })
})