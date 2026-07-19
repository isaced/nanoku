// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { App as AntdApp } from 'antd'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '../i18n'

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../lib/api')>('../lib/api')
  return {
    ...actual,
    api: {
      appContainers: vi.fn(),
      listAppContainerFiles: vi.fn(),
      readAppContainerFile: vi.fn(),
    },
  }
})

import { api } from '../lib/api'
import { AppDetailFiles } from './AppDetailFiles'
import type { ContainerFile, ContainerFileContent, ContainerInfo } from '../lib/types'

function Providers({ children }: { children: React.ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchInterval: false } },
  })
  return (
    <QueryClientProvider client={queryClient}>
      <AntdApp>{children}</AntdApp>
    </QueryClientProvider>
  )
}

function makeFile(overrides: Partial<ContainerFile> = {}): ContainerFile {
  return {
    name: 'hello.txt',
    size: 12,
    mode: '-rw-r--r--',
    modTime: '2024-01-01T00:00:00Z',
    isDir: false,
    isLink: false,
    ...overrides,
  }
}

function makeContainers(...names: string[]): ContainerInfo[] {
  return names.map((n) => ({ name: n, image: 'nginx:1.27', status: 'running' }))
}

describe('AppDetailFiles', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders a directory listing for the docker app container', async () => {
    vi.mocked(api.appContainers).mockResolvedValue([])
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/',
      entries: [
        makeFile({ name: 'bin', isDir: true, mode: 'drwxr-xr-x', size: 4096 }),
        makeFile({ name: 'hello.txt', size: 12 }),
      ],
    })

    const { getByText, container } = render(
      <Providers>
        <AppDetailFiles
          appId={1}
          hasContainer
          deployMethod="docker"
          currentContainerName="nanoku-web"
        />
      </Providers>,
    )

    await waitFor(() => {
      expect(getByText('hello.txt')).not.toBeNull()
    })
    // Both entries visible.
    expect(getByText('bin')).not.toBeNull()
    // The breadcrumb shows the current path state — we
    // start at "/", so it should be visible somewhere in
    // the rendered tree.
    expect(container.textContent).toContain('/')
  })

  it('clicking a file opens the viewer and decodes the content', async () => {
    vi.mocked(api.appContainers).mockResolvedValue([])
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/',
      entries: [makeFile({ name: 'hello.txt', size: 5 })],
    })
    const content: ContainerFileContent = {
      path: '/hello.txt',
      size: 5,
      mode: '-rw-r--r--',
      modTime: '2024-01-01T00:00:00Z',
      // base64("hello") = "aGVsbG8="
      content: 'aGVsbG8=',
      encoding: 'base64',
    }
    vi.mocked(api.readAppContainerFile).mockResolvedValue(content)

    const { getByText, container } = render(
      <Providers>
        <AppDetailFiles
          appId={1}
          hasContainer
          deployMethod="docker"
          currentContainerName="nanoku-web"
        />
      </Providers>,
    )

    // Wait for the listing to land, then click the file.
    await waitFor(() => {
      expect(getByText('hello.txt')).not.toBeNull()
    })
    fireEvent.click(getByText('hello.txt'))

    // Viewer replaces the table; "Back" appears and the
    // decoded "hello" text shows up inside a <pre>.
    await waitFor(() => {
      expect(getByText('hello')).not.toBeNull()
    })
    expect(container.textContent).toContain('/hello.txt')
  })

  it('clicking a directory re-issues the listing query with the new path', async () => {
    // First call: at root, listing contains "sub". Second call:
    // also respond with the same shape so the second query
    // resolves without erroring.
    vi.mocked(api.appContainers).mockResolvedValue([])
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/',
      entries: [makeFile({ name: 'sub', isDir: true, mode: 'drwxr-xr-x' })],
    })

    const { getByText } = render(
      <Providers>
        <AppDetailFiles
          appId={1}
          hasContainer
          deployMethod="docker"
          currentContainerName="nanoku-web"
        />
      </Providers>,
    )

    await waitFor(() => {
      expect(getByText('sub')).not.toBeNull()
    })
    fireEvent.click(getByText('sub'))

    // After clicking the directory, the next listing call
    // should ask for "/sub" (root + child).
    await waitFor(() => {
      const calls = vi.mocked(api.listAppContainerFiles).mock.calls
      expect(calls.some((c) => c[2] === '/sub')).toBe(true)
    })
  })

  it('renders the container selector for compose apps with multiple services', async () => {
    vi.mocked(api.appContainers).mockResolvedValue(
      makeContainers('nanoku-blog-web', 'nanoku-blog-db'),
    )
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/',
      entries: [],
    })

    const { container } = render(
      <Providers>
        <AppDetailFiles
          appId={1}
          hasContainer
          deployMethod="compose"
          currentContainerName="unused"
        />
      </Providers>,
    )

    // Compose apps use a Select widget; the trigger label
    // is one of the two container names.
    await waitFor(() => {
      expect(container.textContent).toMatch(/nanoku-blog-(web|db)/)
    })
  })

  it('shows an empty-state message when the directory has no entries', async () => {
    vi.mocked(api.appContainers).mockResolvedValue([])
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/empty',
      entries: [],
    })

    const { container } = render(
      <Providers>
        <AppDetailFiles
          appId={1}
          hasContainer
          deployMethod="docker"
          currentContainerName="nanoku-web"
        />
      </Providers>,
    )

    await waitFor(() => {
      // The locale string for the empty state is "Empty directory" (en).
      expect(container.textContent).toMatch(/empty directory/i)
    })
  })

  it('hides the file browser when the app has no container', async () => {
    const { container } = render(
      <Providers>
        <AppDetailFiles
          appId={1}
          hasContainer={false}
          deployMethod="docker"
        />
      </Providers>,
    )
    // The "no container" placeholder shows up; the listing
    // query should never be issued.
    expect(container.textContent).toMatch(/no container/i)
    expect(api.listAppContainerFiles).not.toHaveBeenCalled()
  })
})
