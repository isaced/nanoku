import ReactDOM from 'react-dom/client'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { routeTree } from './routeTree.gen'
import './i18n'
import { ensureAuth } from './lib/auth'
import { setUnauthorizedHandler } from './lib/api'

const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
  scrollRestoration: true,
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

// 401 from any admin API call: kick the user back to login.
setUnauthorizedHandler(() => {
  if (router.state.location.pathname !== '/login') {
    void router.navigate({ to: '/login' })
  }
})

// Reconcile the in-memory auth flag with the backend session cookie once at
// boot, so subsequent route guards and the UI shell render correctly without
// an extra round-trip on the first nav.
void ensureAuth()

const rootElement = document.getElementById('app')!

if (!rootElement.innerHTML) {
  const root = ReactDOM.createRoot(rootElement)
  root.render(<RouterProvider router={router} />)
}