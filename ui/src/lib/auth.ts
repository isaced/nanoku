// Auth state is tracked in-memory only. The cookie is the source of truth —
// `ensureAuth()` reconciles the flag against `/api/me` whenever it might be
// stale (route entry, app boot, explicit check). No credentials are stored
// client-side.
//
// The flag lives on globalThis (rather than a module-level let) so that
// Vite's HMR module replacement doesn't drop it back to false mid-session.
// Without this, saving any file in dev resets the auth flag, the next
// route guard runs isAuthenticated() → false, and the user gets bounced
// to /login even though their session cookie is still valid.

type AuthGlobal = typeof globalThis & { __nanoku_authed__?: boolean }
const g = globalThis as AuthGlobal

export function isAuthenticated(): boolean {
  return g.__nanoku_authed__ === true
}

export function markLoggedIn(): void {
  g.__nanoku_authed__ = true
}

export function markLoggedOut(): void {
  g.__nanoku_authed__ = false
}

// ensureAuth verifies the session cookie against the backend. Resolves true
// if the cookie is valid, false otherwise (network error, 401, etc). The
// result updates the in-memory flag.
export async function ensureAuth(): Promise<boolean> {
  try {
    const res = await fetch('/api/me', { credentials: 'include' })
    if (res.ok) {
      markLoggedIn()
      return true
    }
  } catch {
    // network error: keep previous flag, treat as not authed for routing
  }
  markLoggedOut()
  return false
}