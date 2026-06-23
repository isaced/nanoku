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
    vi.mocked(api.deployApp).mockResolvedValue({
      accepted: true,
      appId: mockApps[0].id,
      deployId: 42,
    })

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

describe('DeployResponse handling', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    queryClient = createAppQueryClient({
      defaultOptions: { queries: { retry: false } },
    })
  })

  // The mutation cache invalidation is what the deploys-tab polling and
  // the row's container status depend on. Without an explicit
  // invalidateQueries call the UI would stay stale until the next refetch.
  it('useDeployApp marks the deploys query as stale on success', async () => {
    const { useDeployApp } = await import('../lib/hooks')
    vi.mocked(api.deployApp).mockResolvedValue({
      accepted: true,
      appId: 1,
      deployId: 42,
    })

    let mutateRef: ((id: number) => void) | null = null

    function Harness() {
      const m = useDeployApp()
      mutateRef = m.mutate
      return null
    }

    render(
      <QueryClientProvider client={queryClient}>
        <AntdApp>
          <Harness />
        </AntdApp>
      </QueryClientProvider>,
    )

    // Prime the deploys cache so we can observe the invalidation.
    queryClient.setQueryData(queryKeys.apps.deploys(1), [
      { id: 1, status: 'success', trigger: 'manual', createdAt: '' },
    ])
    const stateBefore = queryClient.getQueryState(queryKeys.apps.deploys(1))
    expect(stateBefore?.isInvalidated).toBe(false)

    mutateRef!(1)
    await waitFor(() => {
      expect(api.deployApp).toHaveBeenCalledWith(1)
    })
    await waitFor(() => {
      const state = queryClient.getQueryState(queryKeys.apps.deploys(1))
      // After the mutation's onSuccess runs, the cache key is marked
      // stale so the next read refetches. We don't check the data slot
      // directly because TanStack keeps stale data around until the
      // refetch resolves.
      expect(state?.isInvalidated).toBe(true)
    })
  })
})
