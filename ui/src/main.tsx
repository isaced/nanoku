import ReactDOM from 'react-dom/client'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { QueryClientProvider } from '@tanstack/react-query'
import { ReactQueryDevtools } from '@tanstack/react-query-devtools'
import { routeTree } from './routeTree.gen'
import './i18n'
import { ensureAuth } from './lib/auth'
import { setUnauthorizedHandler } from './lib/api'
import { createAppQueryClient } from './lib/queryClient'

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

const queryClient = createAppQueryClient()

// 401 from any admin API call: kick the user back to login. The fetch wrapper
// and the QueryClient global error handler both flow through `triggerUnauthorized`
// in lib/api, so this is the single source of truth for the redirect.
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
  root.render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <ReactQueryDevtools initialIsOpen={false} />
    </QueryClientProvider>,
  )
}
