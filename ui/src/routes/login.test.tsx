// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, render, waitFor } from '@testing-library/react'
import {
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRouter,
} from '@tanstack/react-router'
import { App as AntdApp } from 'antd'
import '../i18n'
import { routeTree } from '../routeTree.gen'

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../lib/api')>('../lib/api')
  return {
    ...actual,
    api: {
      login: vi.fn(),
    },
  }
})

vi.mock('../lib/auth', async () => {
  const actual = await vi.importActual<typeof import('../lib/auth')>('../lib/auth')
  return {
    ...actual,
    ensureAuth: async () => false,
  }
})

import { ApiError, api, setUnauthorizedHandler } from '../lib/api'
import { markLoggedOut } from '../lib/auth'
import { createAppQueryClient } from '../lib/queryClient'

describe('Login route', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    setUnauthorizedHandler(null)
    markLoggedOut()
    queryClient = createAppQueryClient({
      defaultOptions: { queries: { retry: false } },
    })
  })

  afterEach(() => {
    setUnauthorizedHandler(null)
    markLoggedOut()
  })

  it('submits credentials and calls api.login', async () => {
    vi.mocked(api.login).mockResolvedValue({
      username: 'admin',
      role: 'admin',
    })
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/login'] }),
    })
    await router.load()
    render(
      <QueryClientProvider client={queryClient}>
        <AntdApp>
          <RouterProvider router={router} />
        </AntdApp>
      </QueryClientProvider>,
    )

    const passwordInput = (await waitFor(() =>
      document.querySelector('input[type="password"]'),
    )) as HTMLInputElement
    expect(passwordInput).toBeTruthy()
    fireEvent.change(passwordInput, { target: { value: 'secret123' } })

    const submit = await waitFor(() => {
      const btn = document.querySelector('button[type="submit"]') as HTMLButtonElement | null
      if (!btn) throw new Error('no submit button')
      return btn
    })
    fireEvent.click(submit)

    await waitFor(() => {
      expect(api.login).toHaveBeenCalledWith('admin', 'secret123')
    })
    await new Promise((r) => setTimeout(r, 50))
  })

  it('still calls api.login on a 429 error', async () => {
    vi.mocked(api.login).mockRejectedValue(new ApiError(429, 'too many'))
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/login'] }),
    })
    await router.load()
    console.log('test2 router state:', router.state.location.pathname)
    render(
      <QueryClientProvider client={queryClient}>
        <AntdApp>
          <RouterProvider router={router} />
        </AntdApp>
      </QueryClientProvider>,
    )

    const passwordInput = (await waitFor(() =>
      document.querySelector('input[type="password"]'),
    )) as HTMLInputElement
    fireEvent.change(passwordInput, { target: { value: 'secret123' } })

    const submit = await waitFor(() => {
      const btn = document.querySelector('button[type="submit"]') as HTMLButtonElement | null
      if (!btn) throw new Error('no submit button')
      return btn
    })
    fireEvent.click(submit)

    await waitFor(() => {
      expect(api.login).toHaveBeenCalled()
    })
  })

  it('hides the global TopNav and Footer for an immersive full-screen layout', async () => {
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/login'] }),
    })
    await router.load()
    render(
      <QueryClientProvider client={queryClient}>
        <AntdApp>
          <RouterProvider router={router} />
        </AntdApp>
      </QueryClientProvider>,
    )

    // Wait for the login form to mount so the route is committed.
    await waitFor(() => {
      expect(document.querySelector('input[type="password"]')).toBeTruthy()
    })

    // TopNav: the nav items carry the route keys as their antd Menu item keys,
    // so when the nav is gone none of those keys exist as <li data-menu-id>.
    const navKeys = ['/dashboard', '/sites', '/apps', '/system']
    for (const key of navKeys) {
      expect(document.querySelector(`[data-menu-id="${key}"]`)).toBeNull()
    }
    // Footer: the version label only renders inside the Footer component.
    expect(document.querySelector('[data-testid="footer-version"]')).toBeNull()
  })
})
