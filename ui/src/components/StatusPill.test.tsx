// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { StatusPill } from './StatusPill'

afterEach(() => {
  cleanup()
})

describe('StatusPill', () => {
  it('renders children inside the pill', () => {
    render(<StatusPill variant="success">running</StatusPill>)
    expect(screen.getByText('running')).toBeTruthy()
  })

  it('applies the variant class for each kind', () => {
    const cases: Array<['success' | 'warn' | 'danger' | 'muted' | 'accent', string]> = [
      ['success', 'status-pill-success'],
      ['warn', 'status-pill-warn'],
      ['danger', 'status-pill-danger'],
      ['muted', 'status-pill-muted'],
      ['accent', 'status-pill-accent'],
    ]
    for (const [variant, expected] of cases) {
      const { unmount } = render(
        <StatusPill variant={variant} data-testid={`pill-${variant}`}>
          x
        </StatusPill>,
      )
      const el = screen.getByTestId(`pill-${variant}`)
      expect(el.className).toContain('status-pill')
      expect(el.className).toContain(expected)
      unmount()
    }
  })

  it('merges an extra className when provided', () => {
    render(
      <StatusPill variant="muted" className="ml-2" data-testid="pill-merge">
        off
      </StatusPill>,
    )
    const el = screen.getByTestId('pill-merge')
    expect(el.className).toContain('status-pill')
    expect(el.className).toContain('status-pill-muted')
    expect(el.className).toContain('ml-2')
  })

  it('renders nothing visually besides the chip itself (no svg, no extra DOM)', () => {
    const { container } = render(
      <StatusPill variant="success">ok</StatusPill>,
    )
    // Only the pill span; no antd Tag wrapping it.
    expect(container.querySelector('.ant-tag')).toBeNull()
    expect(container.querySelector('svg')).toBeNull()
    expect(container.querySelectorAll('.status-pill').length).toBe(1)
  })
})
