// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
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
      dashboard: vi.fn(),
      status: vi.fn(),
    },
  }
})

// ensureAuth is mocked so the /dashboard route guard passes without
// touching the backend; isAuthenticated / markLoggedIn / markLoggedOut
// stay real so the 401 redirect flow can be observed.
vi.mock('../lib/auth', async () => {
  const actual = await vi.importActual<typeof import('../lib/auth')>('../lib/auth')
  return {
    ...actual,
    ensureAuth: async () => true,
  }
})

import { ApiError, api, setUnauthorizedHandler } from '../lib/api'
import { isAuthenticated, markLoggedIn } from '../lib/auth'
import { queryKeys } from '../lib/queryKeys'
import { createAppQueryClient } from '../lib/queryClient'
import type { Dashboard, Status } from '../lib/types'

function Providers({ queryClient }: { queryClient: QueryClient }) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/dashboard'] }),
  })
  return (
    <QueryClientProvider client={queryClient}>
      <AntdApp>
        <RouterProvider router={router} />
      </AntdApp>
    </QueryClientProvider>
  )
}

const mockDashboard: Dashboard = {
  summary: {
    totalSites: 1,
    enabledSites: 1,
    totalApps: 0,
    runningApps: 0,
    totalCpuPerc: 0,
    totalMemBytes: 0,
    totalMemLimitBytes: 0,
    totalMemPerc: 0,
    containerCount: 0,
  },
  sites: [
    { id: 1, domain: 'example.com', upstream: 'app:80', enabled: true },
  ],
  apps: [],
  stats: [],
}

const mockStatus: Status = {
  dockerConnected: true,
  caddyStatus: 'running',
  siteCount: 1,
  enabledSiteCount: 1,
  appCount: 0,
  runningAppCount: 0,
  acmeEmail: '',
}

describe('queryKeys', () => {
  it('uses stable keys for dashboard and status', () => {
    expect(queryKeys.dashboard.all()).toEqual(['dashboard'])
    expect(queryKeys.status.all()).toEqual(['status'])
  })
})

describe('Dashboard route', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    queryClient = createAppQueryClient({
      defaultOptions: { queries: { retry: false } },
    })
  })

  afterEach(() => {
    setUnauthorizedHandler(null)
  })

  it('populates the query cache under the expected keys on success', async () => {
    vi.mocked(api.dashboard).mockResolvedValue(mockDashboard)
    vi.mocked(api.status).mockResolvedValue(mockStatus)

    render(<Providers queryClient={queryClient} />)

    await waitFor(() => {
      expect(queryClient.getQueryData(queryKeys.dashboard.all())).toEqual(
        mockDashboard,
      )
    })
    expect(queryClient.getQueryData(queryKeys.status.all())).toEqual(mockStatus)
    expect(api.dashboard).toHaveBeenCalledTimes(1)
    expect(api.status).toHaveBeenCalledTimes(1)
  })

  it('does not trigger the unauthorized handler on a non-401 error', async () => {
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)
    markLoggedIn()
    vi.mocked(api.dashboard).mockRejectedValue(new ApiError(500, 'boom'))
    vi.mocked(api.status).mockResolvedValue(mockStatus)

    render(<Providers queryClient={queryClient} />)

    await waitFor(() => {
      expect(api.dashboard).toHaveBeenCalled()
    })
    expect(onUnauthorized).not.toHaveBeenCalled()
    expect(isAuthenticated()).toBe(true)
  })

  it('triggers the unauthorized flow on a 401 error from the global query handler', async () => {
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)
    markLoggedIn()
    vi.mocked(api.dashboard).mockRejectedValue(new ApiError(401, 'Unauthorized'))
    vi.mocked(api.status).mockResolvedValue(mockStatus)

    render(<Providers queryClient={queryClient} />)

    await waitFor(() => {
      expect(onUnauthorized).toHaveBeenCalledTimes(1)
    })
    expect(isAuthenticated()).toBe(false)
  })
})
