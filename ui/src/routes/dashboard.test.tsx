// @vitest-environment jsdom
import { Suspense } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
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
import type { ContainerStats, Dashboard, Status } from '../lib/types'

function makeStats(name: string, pids: number): ContainerStats {
  return {
    name,
    cpuPerc: 0,
    memUsedBytes: 0,
    memLimitBytes: 1,
    memPerc: 0,
    netRxBytes: 0,
    netTxBytes: 0,
    blockReadBytes: 0,
    blockWriteBytes: 0,
    pids,
  }
}

function Providers({ queryClient }: { queryClient: QueryClient }) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/dashboard'] }),
  })
  return (
    <QueryClientProvider client={queryClient}>
      <AntdApp>
        <Suspense fallback={<div data-testid="suspense-fallback">loading</div>}>
          <RouterProvider router={router} />
        </Suspense>
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
      expect(queryClient.getQueryData(queryKeys.status.all())).toEqual(mockStatus)
    })
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

describe('Dashboard containers table — running-only filter', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    queryClient = createAppQueryClient({
      defaultOptions: { queries: { retry: false } },
    })
  })

  it('hides stopped containers (pids === 0) and shows only running ones', async () => {
    vi.mocked(api.dashboard).mockResolvedValue({
      ...mockDashboard,
      stats: [
        makeStats('c-running-1', 5),
        makeStats('c-stopped', 0),
        makeStats('c-running-2', 3),
      ],
    })
    vi.mocked(api.status).mockResolvedValue(mockStatus)

    render(<Providers queryClient={queryClient} />)

    expect(await screen.findByText('c-running-1')).toBeTruthy()
    expect(screen.getByText('c-running-2')).toBeTruthy()
    expect(screen.queryByText('c-stopped')).toBeNull()
  })

  it('shows all containers when every one has pids > 0', async () => {
    vi.mocked(api.dashboard).mockResolvedValue({
      ...mockDashboard,
      stats: [makeStats('c-a', 2), makeStats('c-b', 1)],
    })
    vi.mocked(api.status).mockResolvedValue(mockStatus)

    render(<Providers queryClient={queryClient} />)

    expect(await screen.findByText('c-a')).toBeTruthy()
    expect(screen.getByText('c-b')).toBeTruthy()
  })

  it('renders no container rows when every container has pids === 0', async () => {
    vi.mocked(api.dashboard).mockResolvedValue({
      ...mockDashboard,
      stats: [makeStats('c-x', 0), makeStats('c-y', 0)],
    })
    vi.mocked(api.status).mockResolvedValue(mockStatus)

    render(<Providers queryClient={queryClient} />)

    await waitFor(() => {
      expect(api.dashboard).toHaveBeenCalled()
    })
    expect(screen.queryByText('c-x')).toBeNull()
    expect(screen.queryByText('c-y')).toBeNull()
  })
})
