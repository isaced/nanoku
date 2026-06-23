import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  ensureAuth,
  isAuthenticated,
  markLoggedIn,
  markLoggedOut,
} from './auth'

function mockFetchResponse(
  init: { ok: boolean; status?: number; statusText?: string },
): Response {
  return new Response(null, {
    status: init.status ?? (init.ok ? 200 : 500),
    statusText: init.statusText ?? '',
  })
}

describe('auth state machine', () => {
  beforeEach(() => {
    markLoggedOut()
  })

  it('starts unauthenticated', () => {
    expect(isAuthenticated()).toBe(false)
  })

  it('markLoggedIn flips the flag on', () => {
    markLoggedIn()
    expect(isAuthenticated()).toBe(true)
  })

  it('markLoggedOut flips the flag off', () => {
    markLoggedIn()
    markLoggedOut()
    expect(isAuthenticated()).toBe(false)
  })

  it('ensureAuth returns true and sets the flag when /api/me is ok', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(mockFetchResponse({ ok: true }))
    await expect(ensureAuth()).resolves.toBe(true)
    expect(isAuthenticated()).toBe(true)
    expect(fetchSpy).toHaveBeenCalledWith('/api/me', { credentials: 'include' })
  })

  it('ensureAuth returns false and clears the flag when /api/me is not ok', async () => {
    markLoggedIn()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      mockFetchResponse({ ok: false, status: 401 }),
    )
    await expect(ensureAuth()).resolves.toBe(false)
    expect(isAuthenticated()).toBe(false)
  })

  it('ensureAuth returns false and clears the flag on network error', async () => {
    markLoggedIn()
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('network down'))
    await expect(ensureAuth()).resolves.toBe(false)
    expect(isAuthenticated()).toBe(false)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })
})