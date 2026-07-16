// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { isMac, useKeyCombo } from './keys'

function fireKey(key: string, init: KeyboardEventInit = {}): KeyboardEvent {
  const ev = new KeyboardEvent('keydown', { key, bubbles: true, ...init })
  window.dispatchEvent(ev)
  return ev
}

afterEach(() => {
  vi.useRealTimers()
})

describe('isMac', () => {
  it('detects Mac platforms', () => {
    Object.defineProperty(navigator, 'platform', {
      configurable: true,
      value: 'MacIntel',
    })
    expect(isMac()).toBe(true)
  })
  it('detects non-Mac platforms', () => {
    Object.defineProperty(navigator, 'platform', {
      configurable: true,
      value: 'Win32',
    })
    expect(isMac()).toBe(false)
  })
})

describe('useKeyCombo', () => {
  beforeEach(() => {
    Object.defineProperty(navigator, 'platform', {
      configurable: true,
      value: 'MacIntel',
    })
  })

  it('fires on a single-key combo with mod', () => {
    const handler = vi.fn()
    renderHook(() => useKeyCombo('mod+k', handler))
    act(() => {
      fireKey('k', { metaKey: true })
    })
    expect(handler).toHaveBeenCalledTimes(1)
  })

  it('does not fire without the modifier', () => {
    const handler = vi.fn()
    renderHook(() => useKeyCombo('mod+k', handler))
    act(() => {
      fireKey('k')
    })
    expect(handler).not.toHaveBeenCalled()
  })

  it('uses Ctrl on non-Mac', () => {
    Object.defineProperty(navigator, 'platform', {
      configurable: true,
      value: 'Win32',
    })
    const handler = vi.fn()
    renderHook(() => useKeyCombo('mod+k', handler))
    act(() => {
      fireKey('k', { ctrlKey: true })
    })
    expect(handler).toHaveBeenCalledTimes(1)
  })

  it('fires on multi-key sequence', () => {
    const handler = vi.fn()
    // The first act() runs the render + effect install so the
    // keydown listener is in place before we fire any keys. Without
    // it, fireKey('g') runs against a no-listener window and the
    // sequence state never gets seeded.
    renderHook(() => useKeyCombo('g d', handler))
    act(() => {
      fireKey('g')
    })
    act(() => {
      fireKey('d')
    })
    expect(handler).toHaveBeenCalledTimes(1)
  })

  it('does not fire on a partial match', () => {
    const handler = vi.fn()
    renderHook(() => useKeyCombo('g d', handler))
    act(() => {
      fireKey('g')
    })
    act(() => {
      fireKey('s')
    })
    expect(handler).not.toHaveBeenCalled()
  })

  it('resets the sequence after the timeout', () => {
    vi.useFakeTimers()
    const handler = vi.fn()
    renderHook(() => useKeyCombo('g d', handler))
    act(() => {
      fireKey('g')
    })
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    act(() => {
      fireKey('d')
    })
    expect(handler).not.toHaveBeenCalled()
  })

  it('suppresses key combos while typing in an input', () => {
    const handler = vi.fn()
    renderHook(() => useKeyCombo('g d', handler))
    const input = document.createElement('input')
    document.body.appendChild(input)
    act(() => {
      const ev1 = new KeyboardEvent('keydown', { key: 'g', bubbles: true })
      input.dispatchEvent(ev1)
      const ev2 = new KeyboardEvent('keydown', { key: 'd', bubbles: true })
      input.dispatchEvent(ev2)
    })
    expect(handler).not.toHaveBeenCalled()
    document.body.removeChild(input)
  })

  it('allows Escape even from inputs (so the palette can close)', () => {
    // We register an Escape handler. The hook by default skips
    // typing targets unless the caller opts in. We opt in here.
    const handler = vi.fn()
    renderHook(() =>
      useKeyCombo('Escape', handler, { allowInInputs: true }),
    )
    const input = document.createElement('input')
    document.body.appendChild(input)
    act(() => {
      const ev = new KeyboardEvent('keydown', {
        key: 'Escape',
        bubbles: true,
      })
      input.dispatchEvent(ev)
    })
    expect(handler).toHaveBeenCalledTimes(1)
    document.body.removeChild(input)
  })
})
