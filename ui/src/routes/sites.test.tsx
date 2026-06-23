// @vitest-environment jsdom
import { Suspense } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
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
      listSites: vi.fn(),
      listApps: vi.fn(),
      status: vi.fn(),
      caddyfile: vi.fn(),
      createSite: vi.fn(),
      updateSite: vi.fn(),
      deleteSite: vi.fn(),
      toggleSite: vi.fn(),
    },
  }
})

vi.mock('../lib/auth', async () => {
  const actual = await vi.importActual<typeof import('../lib/auth')>('../lib/auth')
  return {
    ...actual,
    ensureAuth: async () => true,
  }
})

import { api } from '../lib/api'
import { queryKeys } from '../lib/queryKeys'
import { createAppQueryClient } from '../lib/queryClient'
import type { Site, Status } from '../lib/types'

function Providers({ queryClient }: { queryClient: QueryClient }) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/sites'] }),
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

const mockStatus: Status = {
  dockerConnected: true,
  caddyStatus: 'running',
  siteCount: 1,
  enabledSiteCount: 1,
  appCount: 0,
  runningAppCount: 0,
  acmeEmail: '',
}

const mockSites: Site[] = [
  {
    id: 1,
    domain: 'example.com',
    upstream: 'app:80',
    enabled: true,
    scheme: 'https',
    createdAt: '',
    updatedAt: '',
  },
]

describe('Sites route', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    queryClient = createAppQueryClient({
      defaultOptions: { queries: { retry: false } },
    })
  })

  it('populates the sites query cache on success', async () => {
    vi.mocked(api.listSites).mockResolvedValue(mockSites)
    vi.mocked(api.listApps).mockResolvedValue([])
    vi.mocked(api.status).mockResolvedValue(mockStatus)
    vi.mocked(api.caddyfile).mockResolvedValue('# caddy')

    render(<Providers queryClient={queryClient} />)

    await waitFor(() => {
      expect(queryClient.getQueryData(queryKeys.sites.all())).toEqual(mockSites)
    })
    expect(api.listSites).toHaveBeenCalledTimes(1)
  })

  it('invalidates sites list after a successful toggle', async () => {
    vi.mocked(api.listSites).mockResolvedValue(mockSites)
    vi.mocked(api.listApps).mockResolvedValue([])
    vi.mocked(api.status).mockResolvedValue(mockStatus)
    vi.mocked(api.caddyfile).mockResolvedValue('')
    vi.mocked(api.toggleSite).mockResolvedValue({
      ...mockSites[0],
      enabled: false,
    })

    render(<Providers queryClient={queryClient} />)
    await waitFor(() => {
      expect(queryClient.getQueryData(queryKeys.sites.all())).toEqual(mockSites)
    })

    queryClient.setQueryData<Site[]>(queryKeys.sites.all(), mockSites)
    queryClient.invalidateQueries({ queryKey: queryKeys.sites.all() })

    await waitFor(() => {
      expect(api.listSites).toHaveBeenCalledTimes(2)
    })
  })
})
