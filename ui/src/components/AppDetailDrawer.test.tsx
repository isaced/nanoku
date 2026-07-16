// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { App as AntdApp } from 'antd'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '../i18n'

// Stub the Antd Drawer so the children render synchronously instead of
// going through the lazy portal/transition that jsdom can't drive. The
// hook logic (fetch, poll, state) is what we want to exercise.
vi.mock('antd', async () => {
  const actual = await vi.importActual<typeof import('antd')>('antd')
  return {
    ...actual,
    Drawer: ({ children, title }: { children: React.ReactNode; title: React.ReactNode }) => (
      <div data-testid="drawer">
        <div data-testid="drawer-title">{title}</div>
        {children}
      </div>
    ),
  }
})

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../lib/api')>('../lib/api')
  return {
    ...actual,
    api: {
      getApp: vi.fn(),
      listAppEnv: vi.fn(),
      listAppDeploys: vi.fn(),
      listAppVolumes: vi.fn(),
      appLogs: vi.fn(),
    },
  }
})

import { api } from '../lib/api'
import { queryKeys } from '../lib/queryKeys'
import { AppDetail } from './AppDetailDrawer'
import type { App, Deploy, EnvVar, Volume } from '../lib/types'

function makeApp(overrides: Partial<App> = {}): App {
  return {
    id: 1,
    name: 'web',
    image: 'nginx:1.27',
    port: 80,
    deployMethod: 'docker',
    registryConfigured: false,
    triggerConfigured: false,
    deleteVolumesOnRemove: false,
    createdAt: '2024-01-01T00:00:00Z',
    updatedAt: '2024-01-01T00:00:00Z',
    ...overrides,
  }
}

const baseEnv: EnvVar[] = []
const baseVolumes: Volume[] = []

function makeDeploy(overrides: Partial<Deploy> = {}): Deploy {
  return {
    id: 1,
    trigger: 'manual',
    status: 'success',
    createdAt: '2024-01-01T00:00:00Z',
    ...overrides,
  }
}

function Providers({
  children,
  queryClient,
}: {
  children: React.ReactNode
  queryClient: QueryClient
}) {
  return (
    <QueryClientProvider client={queryClient}>
      <AntdApp>{children}</AntdApp>
    </QueryClientProvider>
  )
}

function primeCache(queryClient: QueryClient, appId: number, deploys: Deploy[]) {
  queryClient.setQueryData(queryKeys.apps.detail(appId), makeApp())
  queryClient.setQueryData(queryKeys.apps.env(appId), baseEnv)
  queryClient.setQueryData(queryKeys.apps.volumes(appId), baseVolumes)
  queryClient.setQueryData(queryKeys.apps.deploys(appId), deploys)
}

describe('AppDetailDrawer', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    // Each test gets a fresh queryClient + fresh mocks so the
    // compose-mode test below doesn't leak its `compose` mock
    // into the docker-mode test (or vice versa).
    vi.mocked(api.getApp).mockReset().mockResolvedValue(makeApp())
    vi.mocked(api.listAppEnv).mockReset().mockResolvedValue(baseEnv)
    vi.mocked(api.listAppVolumes).mockReset().mockResolvedValue(baseVolumes)
    vi.mocked(api.listAppDeploys)
      .mockReset()
      .mockResolvedValue([
        makeDeploy({ status: 'success', commitSha: 'abc1234' }),
      ])
    vi.mocked(api.appLogs).mockReset().mockResolvedValue('// logs')

    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, refetchInterval: false } },
    })
  })

  it('fetches the deploys list on mount when the cache is empty', async () => {
    render(
      <Providers queryClient={queryClient}>
        <AppDetail
          appId={1}
          onClose={() => {}}
          onChanged={() => {}}
        />
      </Providers>,
    )

    await waitFor(() => {
      expect(api.listAppDeploys).toHaveBeenCalledWith(1)
    })
  })

  it('renders the most recent deploy row in the deploys tab', async () => {
    primeCache(queryClient, 1, [
      makeDeploy({ status: 'success', commitSha: 'abc1234' }),
    ])

    const { getByText } = render(
      <Providers queryClient={queryClient}>
        <AppDetail
          appId={1}
          initialTab="deploys"
          onClose={() => {}}
          onChanged={() => {}}
        />
      </Providers>,
    )

    await waitFor(() => {
      expect(getByText('abc1234')).not.toBeNull()
    })
    expect(getByText('success')).not.toBeNull()
  })

  it('renders a failed deploy with its error message', async () => {
    primeCache(queryClient, 1, [
      makeDeploy({ id: 1, status: 'failed', error: 'pull access denied' }),
    ])

    const { getByText } = render(
      <Providers queryClient={queryClient}>
        <AppDetail
          appId={1}
          initialTab="deploys"
          onClose={() => {}}
          onChanged={() => {}}
        />
      </Providers>,
    )

    await waitFor(() => {
      expect(getByText('pull access denied')).not.toBeNull()
    })
  })

  it('shows image + internal port on the overview tab for docker apps', async () => {
    primeCache(queryClient, 1, [])

    const { getByText, queryByText } = render(
      <Providers queryClient={queryClient}>
        <AppDetail appId={1} onClose={() => {}} onChanged={() => {}} />
      </Providers>,
    )

    await waitFor(() => {
      // Field label is i18n'd; we only assert the values show up.
      expect(getByText('nginx:1.27')).not.toBeNull()
      expect(getByText('80')).not.toBeNull()
    })
    // The compose-mode hint stays hidden for docker apps.
    expect(queryByText(/compose/i)).toBeNull()
  })

  it('hides image + internal port on the overview tab for compose apps', async () => {
    // The mock is set up in beforeEach to return the docker-mode
    // default; we override it for this test so the background
    // refetch (staleTime defaults to 0 in this queryClient) doesn't
    // overwrite the cache with a docker app.
    const composeApp = makeApp({ deployMethod: 'compose', image: '', port: 0 })
    vi.mocked(api.getApp).mockResolvedValue(composeApp)
    queryClient.setQueryData(queryKeys.apps.detail(1), composeApp)
    queryClient.setQueryData(queryKeys.apps.env(1), baseEnv)
    queryClient.setQueryData(queryKeys.apps.volumes(1), baseVolumes)
    queryClient.setQueryData(queryKeys.apps.deploys(1), [])

    const { container } = render(
      <Providers queryClient={queryClient}>
        <AppDetail appId={1} onClose={() => {}} onChanged={() => {}} />
      </Providers>,
    )

    // Wait for the suspense to resolve. The createdAt row is
    // always present; use it as a readiness signal so the
    // assertions below are against the rendered tree, not the
    // suspense fallback.
    await waitFor(() => {
      expect(container.textContent).toContain('2024-01-01')
    })

    // For compose apps the image + port fields are omitted. The
    // image/port literal values from a docker app are absent.
    expect(container.textContent).not.toContain('nginx:1.27')
  })
})