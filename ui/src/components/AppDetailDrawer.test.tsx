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
  const React = await import('react')
  void React
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
      rotateTriggerToken: vi.fn(),
    },
  }
})

import { api } from '../lib/api'
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

function Providers({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchInterval: false } },
  })
  return (
    <QueryClientProvider client={qc}>
      <AntdApp>{children}</AntdApp>
    </QueryClientProvider>
  )
}

describe('AppDetailDrawer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.getApp).mockResolvedValue(makeApp())
    vi.mocked(api.listAppEnv).mockResolvedValue(baseEnv)
    vi.mocked(api.listAppVolumes).mockResolvedValue(baseVolumes)
    vi.mocked(api.listAppDeploys).mockResolvedValue([
      makeDeploy({ status: 'success', commitSha: 'abc1234' }),
    ])
    vi.mocked(api.appLogs).mockResolvedValue('// logs')
  })

  it('fetches the deploys list on mount', async () => {
    render(
      <Providers>
        <AppDetail
          appId={1}
          onClose={() => {}}
          onChanged={() => {}}
          onEditRequested={() => {}}
        />
      </Providers>,
    )

    await waitFor(() => {
      expect(api.listAppDeploys).toHaveBeenCalledWith(1)
    })
  })

  it('renders the most recent deploy row in the deploys tab', async () => {
    const { getByText } = render(
      <Providers>
        <AppDetail
          appId={1}
          initialTab="deploys"
          onClose={() => {}}
          onChanged={() => {}}
          onEditRequested={() => {}}
        />
      </Providers>,
    )

    await waitFor(() => {
      expect(getByText('abc1234')).not.toBeNull()
    })
    expect(getByText('success')).not.toBeNull()
  })

  it('renders a failed deploy with its error message', async () => {
    vi.mocked(api.listAppDeploys).mockResolvedValue([
      makeDeploy({ id: 1, status: 'failed', error: 'pull access denied' }),
    ])

    const { getByText } = render(
      <Providers>
        <AppDetail
          appId={1}
          initialTab="deploys"
          onClose={() => {}}
          onChanged={() => {}}
          onEditRequested={() => {}}
        />
      </Providers>,
    )

    await waitFor(() => {
      expect(getByText('pull access denied')).not.toBeNull()
    })
  })
})