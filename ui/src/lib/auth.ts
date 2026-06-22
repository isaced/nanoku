// Auth state is tracked in-memory only. The cookie is the source of truth —
// `ensureAuth()` reconciles the flag against `/api/me` whenever it might be
// stale (route entry, app boot, explicit check). No credentials are stored
// client-side.

let authed = false;

export function isAuthenticated(): boolean {
  return authed;
}

export function markLoggedIn(): void {
  authed = true;
}

export function markLoggedOut(): void {
  authed = false;
}

// ensureAuth verifies the session cookie against the backend. Resolves true
// if the cookie is valid, false otherwise (network error, 401, etc). The
// result updates the in-memory flag.
export async function ensureAuth(): Promise<boolean> {
  try {
    const res = await fetch('/api/me', { credentials: 'include' });
    if (res.ok) {
      authed = true;
      return true;
    }
  } catch {
    // network error: keep previous flag, treat as not authed for routing
  }
  authed = false;
  return false;
}