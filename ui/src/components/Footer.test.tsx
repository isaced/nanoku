// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { ConfigProvider } from 'antd'
import enUS from 'antd/locale/en_US'
import '../i18n'
import { Footer } from './Footer'
import type { SystemStatus } from '../lib/types'

afterEach(() => {
  cleanup()
})

const GITHUB_URL = 'https://github.com/isaced/nanoku'

function makeSystemStatus(
  overrides: Partial<SystemStatus> = {},
): SystemStatus {
  return {
    nanokuContainerConfigured: false,
    caddyContainer: 'nanoku-caddy',
    dockerAvailable: false,
    version: '0.1.0',
    commit: 'abc1234',
    date: '2026-06-23T00:00:00Z',
    buildType: 'release',
    ...overrides,
  }
}

function renderWithProviders(ui: React.ReactNode) {
  return render(<ConfigProvider locale={enUS}>{ui}</ConfigProvider>)
}

function bodyTextIncludes(text: string): boolean {
  return Array.from(document.body.querySelectorAll('*')).some(
    (el) => el.textContent?.includes(text) ?? false,
  )
}

describe('Footer', () => {
  it('renders copyright and version from system status', () => {
    renderWithProviders(<Footer systemStatus={makeSystemStatus()} />)
    const year = new Date().getFullYear()
    expect(screen.getByText(`© ${year} nanoku`)).toBeTruthy()
    expect(screen.getByText('v0.1.0')).toBeTruthy()
  })

  it('does not crash when system status is null', () => {
    renderWithProviders(<Footer systemStatus={null} />)
    const year = new Date().getFullYear()
    expect(screen.getByText(`© ${year} nanoku`)).toBeTruthy()
    expect(screen.queryByTestId('footer-version')).toBeNull()
  })

  it('disables the check-update button', () => {
    renderWithProviders(<Footer systemStatus={makeSystemStatus()} />)
    const btn = screen.getByTestId('footer-check-update') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
  })

  it('links to the GitHub repo with target=_blank', () => {
    renderWithProviders(<Footer systemStatus={makeSystemStatus()} />)
    const link = screen.getByLabelText('View on GitHub') as HTMLAnchorElement
    expect(link.href).toBe(GITHUB_URL)
    expect(link.target).toBe('_blank')
    expect(link.rel).toContain('noopener')
  })

  it('shows commit + date tooltip on hover for source builds', async () => {
    renderWithProviders(
      <Footer
        systemStatus={makeSystemStatus({
          buildType: 'source',
          commit: 'deadbee',
          date: '2026-06-22T10:00:00Z',
        })}
      />,
    )
    const version = screen.getByTestId('footer-version')
    fireEvent.mouseEnter(version)
    await waitFor(() => {
      expect(bodyTextIncludes('deadbee')).toBe(true)
    })
  })

  it('shows commit + date tooltip on hover for release builds', async () => {
    renderWithProviders(
      <Footer
        systemStatus={makeSystemStatus({
          buildType: 'release',
          commit: 'relcafe',
          date: '2026-06-23T00:00:00Z',
        })}
      />,
    )
    const version = screen.getByTestId('footer-version')
    fireEvent.mouseEnter(version)
    await waitFor(() => {
      expect(bodyTextIncludes('relcafe')).toBe(true)
    })
  })

  it('does not show commit tooltip for dev builds (commit === "none")', () => {
    renderWithProviders(
      <Footer
        systemStatus={makeSystemStatus({
          buildType: 'release',
          commit: 'none',
          date: 'unknown',
        })}
      />,
    )
    // Dev mode renders the version with a small dot, no tooltip wrapper.
    const version = screen.getByTestId('footer-version')
    expect(version.textContent).toContain('v0.1.0')
    expect(version.querySelector('span[aria-label="dev"]')).not.toBeNull()
  })
})
