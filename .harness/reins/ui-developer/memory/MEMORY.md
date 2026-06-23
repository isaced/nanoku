
### Vitest 4 + jsdom: window.localStorage undefined at module init (2026-06-23)
Type: stack-trap
Vitest 4's jsdom env does NOT populate `window.localStorage` by default. Any
module-init code that does `if (typeof window === 'undefined') return;
window.localStorage.getItem(...)` will throw on import. The `// @vitest-environment jsdom`
directive alone is not enough. Fix: either polyfill in `setupFiles`
(`globalThis.ResizeObserver` and `window.matchMedia` are also missing for
antd renders), OR make the code defensive with `?.`.

### Vitest 4 config: import defineConfig from vitest/config (2026-06-23)
Type: stack-trap
If `test: { environment: 'jsdom' }` is silently ignored, you imported
`defineConfig` from `'vite'` instead of `'vitest/config'`. Vitest 4's
config is a superset; vite's `defineConfig` doesn't know about the `test`
field and drops it.

### TanStack Router routeFileIgnorePattern lives in tsr.config.json (2026-06-23)
Type: stack-trap
`routeFileIgnorePattern` set inside the `tanstackRouter({...})` plugin call
in `vite.config.ts` is picked up by the vite plugin but NOT by the `tsr
generate` CLI. The CLI reads its config from `tsr.config.json`. Put the
ignore pattern there (`"routeFileIgnorePattern": "\\.test\\.(tsx|ts)$"`).

### TanStack Router: don't export non-Route symbols from a route file (2026-06-23)
Type: stack-trap
A `createFileRoute('/x')({ component: MyPage })` route file that ALSO
exports `MyPage` for tests breaks auto code-splitting (warning at build
time, the route stays in the main bundle). Fix: split into a thin route
file + a sibling component in a non-route location
(e.g. `components/MyView.tsx`).

### vi.mock pitfall: don't shadow what you assert on (2026-06-23)
Type: testing-trap
Mocking `../lib/auth` to override `isAuthenticated` / `ensureAuth` to
bypass a route guard ALSO replaces those same functions in your test's
`expect()` calls. Symptom: the redirect fires but `isAuthenticated()`
still returns the mocked value. Fix: only mock the minimum surface
(`ensureAuth`), spread the rest from `importActual` so the real
`markLoggedIn` / `markLoggedOut` / `isAuthenticated` stay observable.

### Push-back discipline: don't -f push to main even when asked (2026-06-23)
Type: collaboration-pattern
When a parent session mixes up context and asks to re-do finished work
plus `-f push` to main, push back. AGENTS.md "never push to main directly"
applies regardless of the requester's role. Verified by example in
nanoku: caught a confused re-split request + a -f push request, parent
confirmed the state was already correct. Cost of asking is low; cost of
a -f to main is high.
