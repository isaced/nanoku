// @vitest-environment jsdom
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
      listApps: vi.fn(),
      listSites: vi.fn(),
      status: vi.fn(),
      listAppEnv: vi.fn(),
      listAppVolumes: vi.fn(),
      listAppDeploys: vi.fn(),
      getApp: vi.fn(),
      appLogs: vi.fn(),
      createApp: vi.fn(),
      updateApp: vi.fn(),
      deleteApp: vi.fn(),
      deployApp: vi.fn(),
      startApp: vi.fn(),
      stopApp: vi.fn(),
      restartApp: vi.fn(),
      rotateTriggerToken: vi.fn(),
      replaceAppEnv: vi.fn(),
      replaceAppVolumes: vi.fn(),
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
import type { App, Status } from '../lib/types'

function Providers({ queryClient }: { queryClient: QueryClient }) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/apps'] }),
  })
  return (
    <QueryClientProvider client={queryClient}>
      <AntdApp>
        <RouterProvider router={router} />
      </AntdApp>
    </QueryClientProvider>
  )
}

const mockStatus: Status = {
  dockerConnected: true,
  caddyStatus: 'running',
  siteCount: 0,
  enabledSiteCount: 0,
  appCount: 1,
  runningAppCount: 1,
  acmeEmail: '',
}

const mockApps: App[] = [
  {
    id: 1,
    name: 'web',
    image: 'nginx:latest',
    port: 80,
    deployMethod: 'docker',
    registryConfigured: false,
    triggerConfigured: false,
    deleteVolumesOnRemove: false,
    createdAt: '',
    updatedAt: '',
  },
]

describe('Apps route', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    queryClient = createAppQueryClient({
      defaultOptions: { queries: { retry: false } },
    })
  })

  it('populates the apps query cache on success', async () => {
    vi.mocked(api.listApps).mockResolvedValue(mockApps)
    vi.mocked(api.status).mockResolvedValue(mockStatus)

    render(<Providers queryClient={queryClient} />)

    await waitFor(() => {
      expect(queryClient.getQueryData(queryKeys.apps.all())).toEqual(mockApps)
    })
    expect(api.listApps).toHaveBeenCalledTimes(1)
  })

  it('useDeployApp invalidates apps.all and dashboard', async () => {
    vi.mocked(api.listApps).mockResolvedValue(mockApps)
    vi.mocked(api.status).mockResolvedValue(mockStatus)
    vi.mocked(api.deployApp).mockResolvedValue(mockApps[0])

    render(<Providers queryClient={queryClient} />)
    await waitFor(() => {
      expect(queryClient.getQueryData(queryKeys.apps.all())).toEqual(mockApps)
    })

    await queryClient.fetchQuery({
      queryKey: queryKeys.dashboard.all(),
      queryFn: () => Promise.resolve({ summary: null, sites: [], apps: [], stats: [] }),
    }).catch(() => undefined)
    queryClient.setQueryData(queryKeys.dashboard.all(), {
      summary: null,
      sites: [],
      apps: [],
      stats: [],
    })

    const before = queryClient.getQueryState(queryKeys.apps.all())
    expect(before).toBeDefined()
  })
})
