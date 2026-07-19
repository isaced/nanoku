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

  it('clicking the Up button navigates to the parent directory', async () => {
    vi.mocked(api.appContainers).mockResolvedValue([])
    // First call (root) returns "sub". Second call (/sub)
    // returns nothing — the user is now at the leaf.
    vi.mocked(api.listAppContainerFiles)
      .mockResolvedValueOnce({
        path: '/',
        entries: [makeFile({ name: 'sub', isDir: true, mode: 'drwxr-xr-x' })],
      })
      .mockResolvedValueOnce({
        path: '/sub',
        entries: [],
      })

    const { getByText, getByLabelText } = render(
      <Providers>
        <AppDetailFiles
          appId={1}
          hasContainer
          deployMethod="docker"
          currentContainerName="nanoku-web"
        />
      </Providers>,
    )

    // Wait for root listing, then descend.
    await waitFor(() => {
      expect(getByText('sub')).not.toBeNull()
    })
    fireEvent.click(getByText('sub'))

    // Confirm we asked for /sub.
    await waitFor(() => {
      const calls = vi.mocked(api.listAppContainerFiles).mock.calls
      expect(calls.some((c) => c[2] === '/sub')).toBe(true)
    })

    // Click Up. Should re-issue the listing for the parent.
    fireEvent.click(getByLabelText(/^up$/i))
    await waitFor(() => {
      const calls = vi.mocked(api.listAppContainerFiles).mock.calls
      // Calls include '/', '/sub', then '/' again.
      const rootCalls = calls.filter((c) => c[2] === '/')
      expect(rootCalls.length).toBeGreaterThanOrEqual(2)
    })
  })

  it('clicking Back returns from the file viewer to the listing', async () => {
    vi.mocked(api.appContainers).mockResolvedValue([])
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/',
      entries: [makeFile({ name: 'note.txt', size: 5 })],
    })
    vi.mocked(api.readAppContainerFile).mockResolvedValue({
      path: '/note.txt',
      size: 5,
      mode: '-rw-r--r--',
      modTime: '2024-01-01T00:00:00Z',
      content: 'aGVsbG8=',
      encoding: 'base64',
    })

    const { getByText, getByLabelText, queryByText, container } = render(
      <Providers>
        <AppDetailFiles
          appId={1}
          hasContainer
          deployMethod="docker"
          currentContainerName="nanoku-web"
        />
      </Providers>,
    )

    // Open the file.
    await waitFor(() => {
      expect(getByText('note.txt')).not.toBeNull()
    })
    fireEvent.click(getByText('note.txt'))

    // Viewer replaces the table — the decoded "hello"
    // shows up in a <pre>.
    await waitFor(() => {
      expect(getByText('hello')).not.toBeNull()
    })

    // Click Back → listing returns, viewer disappears.
    fireEvent.click(getByLabelText(/back/i))

    await waitFor(() => {
      // Listing re-renders the file row.
      expect(getByText('note.txt')).not.toBeNull()
    })
    // "hello" is no longer in the rendered tree.
    expect(queryByText('hello')).toBeNull()
    expect(container.textContent).toContain('/')
  })

  it('clicking the Refresh button re-issues the listing query', async () => {
    vi.mocked(api.appContainers).mockResolvedValue([])
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/',
      entries: [makeFile({ name: 'a.txt' })],
    })

    const { getByLabelText } = render(
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
      expect(api.listAppContainerFiles).toHaveBeenCalledTimes(1)
    })

    fireEvent.click(getByLabelText(/refresh/i))

    await waitFor(() => {
      expect(api.listAppContainerFiles).toHaveBeenCalledTimes(2)
    })
  })

  it('shows a download link with the correct query string in the viewer', async () => {
    vi.mocked(api.appContainers).mockResolvedValue([])
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/',
      entries: [makeFile({ name: 'config.yaml' })],
    })
    vi.mocked(api.readAppContainerFile).mockResolvedValue({
      path: '/config.yaml',
      size: 3,
      mode: '-rw-r--r--',
      modTime: '2024-01-01T00:00:00Z',
      content: 'Zm9v', // "foo"
      encoding: 'base64',
    })

    const { container, getByText } = render(
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
      expect(getByText('config.yaml')).not.toBeNull()
    })
    fireEvent.click(getByText('config.yaml'))

    await waitFor(() => {
      // The Download <a> points at the API with
      // &download=1. We don't click it (jsdom doesn't
      // follow the redirect), just assert the href
      // contains everything we'd expect a browser to
      // see: app id, container name, file endpoint,
      // download=1, and the URL-encoded path.
      const hrefs = Array.from(container.querySelectorAll('a')).map(
        (a) => a.getAttribute('href') ?? '',
      )
      const match = hrefs.find(
        (h) =>
          h.includes('/api/apps/1/containers/') &&
          h.includes('/file') &&
          h.includes('download=1') &&
          h.includes(encodeURIComponent('/config.yaml')),
      )
      expect(match).toBeDefined()
    })
  })

  it('hides the container selector for compose apps with only one service', async () => {
    vi.mocked(api.appContainers).mockResolvedValue(
      makeContainers('nanoku-blog-only'),
    )
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/',
      entries: [],
    })

    const { queryByText } = render(
      <Providers>
        <AppDetailFiles
          appId={1}
          hasContainer
          deployMethod="compose"
          currentContainerName="unused"
        />
      </Providers>,
    )

    // With one service the Select widget shouldn't render
    // (the rule is "show selector only when length > 1").
    // The container name should still appear in the
    // breadcrumb / hidden state, but the visible Select
    // trigger label uses an antd class that we can match
    // on by data-testid absent.
    await waitFor(() => {
      expect(api.appContainers).toHaveBeenCalled()
    })
    // The "container" label tag (the "Container:" caption
    // that prefixes the Select) should not be present.
    expect(queryByText(/^Container:$/)).toBeNull()
  })

  it('renders an em-dash for the Go zero-time mtime sentinel', async () => {
    // Go's time.Time{} marshals to "0001-01-01T00:00:00Z"
    // when the upstream `ls` mtime couldn't be parsed.
    // The table's render function should treat that
    // string as "no value" and show a dash, not the
    // literal year-1 date.
    vi.mocked(api.appContainers).mockResolvedValue([])
    vi.mocked(api.listAppContainerFiles).mockResolvedValue({
      path: '/',
      entries: [
        makeFile({ name: 'unknown-mtime.txt', modTime: '0001-01-01T00:00:00Z' }),
        makeFile({ name: 'real-mtime.txt', modTime: '2024-06-15T12:00:00Z' }),
      ],
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
      expect(container.textContent).toContain('unknown-mtime.txt')
    })
    // The bad-mtime row should NOT contain the literal
    // "1/1/0001" string jsdom would produce from
    // `new Date('0001-01-01T00:00:00Z').toLocaleString()`.
    // We assert it shows the em-dash instead.
    const rows = container.querySelectorAll('tr')
    let foundDash = false
    let foundYearOne = false
    for (const row of Array.from(rows)) {
      const text = row.textContent ?? ''
      if (text.includes('unknown-mtime.txt') && text.includes('—')) {
        foundDash = true
      }
      if (text.includes('0001')) {
        foundYearOne = true
      }
    }
    if (!foundDash) {
      throw new Error('expected em-dash placeholder in unknown-mtime row')
    }
    if (foundYearOne) {
      throw new Error('Go zero-time sentinel leaked into the rendered tree')
    }
  })
})
