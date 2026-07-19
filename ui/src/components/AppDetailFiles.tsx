import { Suspense, useState } from 'react'
import { Button, Empty, Result, Select, Skeleton, Table, Tag } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  ArrowLeft,
  ArrowUp,
  Download,
  File as FileIcon,
  FileText,
  Folder,
  Link as LinkIcon,
  RefreshCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAppContainerFile, useAppContainerFiles, useAppContainers } from '../lib/hooks'
import { formatBytes } from '../lib/formatBytes'
import type { ContainerFile } from '../lib/types'
import { RouteFallback } from './RouteFallback'
import { CodeBlock } from './CodeBlock'

/**
 * AppDetailFiles is the "Files" tab in the app detail drawer. It
 * shows a directory listing for the selected container, with the
 * usual breadcrumb-style navigation (Up button, click a folder
 * to descend, click a file to view). The tab keeps a per-mount
 * "currently viewed file" state — clicking a file replaces the
 * listing with a read-only text viewer and a Download button;
 * the Back button restores the listing.
 *
 * Compose apps: a container selector sits above the listing and
 * defaults to the first service. Docker apps: a single current
 * container, no selector. The selector is hidden when there's
 * only one container.
 *
 * Path safety: the server enforces absolute + no-".." paths and
 * a per-app container ownership check; the client just trusts
 * the API and uses absolute paths everywhere.
 */
export function AppDetailFiles({
  appId,
  hasContainer,
  deployMethod,
  currentContainerName,
}: {
  appId: number
  hasContainer: boolean
  deployMethod: 'docker' | 'compose'
  currentContainerName?: string
}) {
  return (
    <Suspense fallback={<RouteFallback variant="drawer" />}>
      <AppDetailFilesContent
        appId={appId}
        hasContainer={hasContainer}
        deployMethod={deployMethod}
        currentContainerName={currentContainerName}
      />
    </Suspense>
  )
}

function AppDetailFilesContent({
  appId,
  hasContainer,
  deployMethod,
  currentContainerName,
}: {
  appId: number
  hasContainer: boolean
  deployMethod: 'docker' | 'compose'
  currentContainerName?: string
}) {
  const { t } = useTranslation('apps')
  const isCompose = deployMethod === 'compose'

  // Compose: pull the container list and let the user pick one.
  // We default to the first entry; useAppContainers is enabled
  // only when the app actually has a container to inspect.
  const containersQuery = useAppContainers(appId, { enabled: isCompose && hasContainer })
  const [selectedContainer, setSelectedContainer] = useState<string | undefined>()

  // Resolve the effective container for the queries. Compose
  // apps let the user (or the default of "first") pick; docker
  // apps use the single current_container whose name is passed
  // in via props (the parent AppDetail already has the app
  // detail loaded, so we don't refetch it here).
  const effectiveContainer = isCompose
    ? (selectedContainer ?? containersQuery.data?.[0]?.name)
    : currentContainerName

  // Per-file state: when non-null, the table hides and the
  // viewer for that file shows.
  const [viewingFile, setViewingFile] = useState<{ path: string; name: string } | null>(null)

  if (!hasContainer) {
    return (
      <div className="text-xs text-[var(--fg-muted)] py-6 text-center">
        {t('detail.noContainer')}
      </div>
    )
  }

  const showSelector = isCompose && containersQuery.data && containersQuery.data.length > 1
  const containerForList = effectiveContainer

  return (
    <div className="space-y-3">
      {showSelector && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-[var(--fg-muted)] whitespace-nowrap">
            {t('detail.container')}
          </span>
          <Select
            value={effectiveContainer}
            onChange={setSelectedContainer}
            options={containersQuery.data!.map((c) => ({
              value: c.name,
              label: c.name,
            }))}
            className="!w-64"
            popupMatchSelectWidth={false}
          />
        </div>
      )}

      {viewingFile ? (
        <FileViewer
          appId={appId}
          container={containerForList}
          path={viewingFile.path}
          name={viewingFile.name}
          onBack={() => setViewingFile(null)}
        />
      ) : containerForList ? (
        <FileListing
          appId={appId}
          container={containerForList}
          onOpenFile={(path, name) => setViewingFile({ path, name })}
        />
      ) : (
        <Skeleton active paragraph={{ rows: 4 }} />
      )}
    </div>
  )
}

function FileListing({
  appId,
  container,
  onOpenFile,
}: {
  appId: number
  container: string
  onOpenFile: (path: string, name: string) => void
}) {
  const { t } = useTranslation('apps')
  const [path, setPath] = useState('/')

  const filesQuery = useAppContainerFiles(appId, container, path, { enabled: true })

  function goUp() {
    if (path === '/') return
    const trimmed = path.endsWith('/') && path.length > 1 ? path.slice(0, -1) : path
    const idx = trimmed.lastIndexOf('/')
    setPath(idx <= 0 ? '/' : trimmed.slice(0, idx))
  }

  function refresh() {
    void filesQuery.refetch()
  }

  const columns: ColumnsType<ContainerFile> = [
    {
      title: t('detail.filesName'),
      dataIndex: 'name',
      key: 'name',
      render: (_value, file) => (
        <FileRow
          file={file}
          onOpen={() => {
            const next = joinPath(path, file.name)
            if (file.isDir) {
              setPath(next)
            } else {
              onOpenFile(next, file.name)
            }
          }}
        />
      ),
    },
    {
      title: t('detail.filesSize'),
      dataIndex: 'size',
      key: 'size',
      width: 110,
      align: 'right',
      render: (size: number, file) =>
        file.isDir ? <span className="text-[var(--fg-muted)]">—</span> : formatBytes(size),
    },
    {
      title: t('detail.filesMode'),
      dataIndex: 'mode',
      key: 'mode',
      width: 120,
      render: (mode: string) => (
        <span className="mono text-xs text-[var(--fg-muted)]">{mode}</span>
      ),
    },
    {
      title: t('detail.filesModTime'),
      dataIndex: 'modTime',
      key: 'modTime',
      width: 180,
      render: (modTime: string) =>
        modTime ? (
          <span className="text-xs text-[var(--fg-muted)]">
            {new Date(modTime).toLocaleString()}
          </span>
        ) : (
          <span className="text-[var(--fg-muted)]">—</span>
        ),
    },
  ]

  // Sort: directories first, then by name. The server already
  // gives us a stable per-line ordering; client-side sort keeps
  // the UI deterministic without forcing the server to do the
  // work.
  const sortedEntries = sortEntries(filesQuery.data?.entries ?? [])

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Button
          size="small"
          icon={<ArrowUp size={12} />}
          onClick={goUp}
          disabled={path === '/'}
          aria-label={t('detail.filesUp')}
        >
          {t('detail.filesUp')}
        </Button>
        <code className="mono text-xs text-[var(--fg-muted)] truncate" title={path}>
          {path}
        </code>
        <Button
          size="small"
          type="text"
          icon={<RefreshCw size={12} />}
          onClick={refresh}
          aria-label={t('detail.filesRefresh')}
        />
      </div>

      {filesQuery.error ? (
        <Result
          status="warning"
          title={t('detail.filesLoadError')}
          subTitle={String((filesQuery.error as Error).message ?? '')}
          style={{ padding: 16 }}
        />
      ) : sortedEntries.length === 0 && !filesQuery.isLoading ? (
        <Empty
          description={t('detail.filesEmpty')}
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          style={{ padding: 16 }}
        />
      ) : (
        <Table<ContainerFile>
          rowKey="name"
          size="small"
          columns={columns}
          dataSource={sortedEntries}
          pagination={false}
          loading={filesQuery.isLoading}
        />
      )}
    </div>
  )
}

function FileRow({ file, onOpen }: { file: ContainerFile; onOpen: () => void }) {
  const { t } = useTranslation('apps')
  const icon = file.isLink ? (
    <LinkIcon size={14} className="text-[var(--fg-muted)] shrink-0" />
  ) : file.isDir ? (
    <Folder size={14} className="text-[var(--accent)] shrink-0" />
  ) : (
    <FileIcon size={14} className="text-[var(--fg-muted)] shrink-0" />
  )
  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex items-center gap-2 text-left w-full hover:underline"
    >
      {icon}
      <span className="mono text-xs">{file.name}</span>
      {file.isLink && file.linkTarget && (
        <span className="text-[10px] text-[var(--fg-muted)]">→ {file.linkTarget}</span>
      )}
      {file.isLink && (
        <Tag color="default" className="!m-0 !text-[10px]">
          {t('detail.filesLink')}
        </Tag>
      )}
    </button>
  )
}

function FileViewer({
  appId,
  container,
  path,
  name,
  onBack,
}: {
  appId: number
  container: string | undefined
  path: string
  name: string
  onBack: () => void
}) {
  const { t } = useTranslation('apps')
  const fileQuery = useAppContainerFile(appId, container, path, { enabled: !!container })

  const downloadUrl =
    container != null
      ? `/api/apps/${appId}/containers/${encodeURIComponent(container)}/file?path=${encodeURIComponent(path)}&download=1`
      : ''

  let body: React.ReactNode
  if (!container) {
    body = <Skeleton active paragraph={{ rows: 4 }} />
  } else if (fileQuery.isLoading) {
    body = <Skeleton active paragraph={{ rows: 4 }} />
  } else if (fileQuery.error) {
    const err = fileQuery.error as Error & { status?: number }
    const isTooLarge = err.status === 413
    body = (
      <Result
        status="warning"
        title={isTooLarge ? t('detail.filesTooLarge') : t('detail.filesLoadError')}
        subTitle={isTooLarge ? t('detail.filesTooLargeHint') : err.message}
        extra={
          <Button
            icon={<Download size={12} />}
            href={downloadUrl}
            // The download endpoint sets Content-Disposition;
            // `download` on the <a> is a hint to the browser
            // when the header is missing.
            download
          >
            {t('detail.filesDownload')}
          </Button>
        }
        style={{ padding: 16 }}
      />
    )
  } else if (fileQuery.data) {
    const text = decodeBase64Utf8(fileQuery.data.content)
    body = (
      <div className="space-y-2">
        <div className="flex items-center gap-2 text-xs text-[var(--fg-muted)]">
          <FileText size={12} />
          <span className="mono truncate" title={fileQuery.data.path}>
            {fileQuery.data.path}
          </span>
          <span>·</span>
          <span>{formatBytes(fileQuery.data.size)}</span>
          <span>·</span>
          <span className="mono">{fileQuery.data.mode}</span>
        </div>
        <CodeBlock value={text} />
      </div>
    )
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Button
          size="small"
          icon={<ArrowLeft size={12} />}
          onClick={onBack}
          aria-label={t('detail.filesBack')}
        >
          {t('detail.filesBack')}
        </Button>
        <span className="mono text-xs text-[var(--fg-muted)] truncate" title={name}>
          {name}
        </span>
        {container && (
          <Button
            size="small"
            type="text"
            icon={<Download size={12} />}
            href={downloadUrl}
            download
          >
            {t('detail.filesDownload')}
          </Button>
        )}
      </div>
      {body}
    </div>
  )
}

// joinPath concatenates a parent directory and a child name
// with exactly one '/'. The server is the source of truth for
// path canonicalisation, so we don't normalise "." / ".." here —
// the API rejects those anyway.
function joinPath(parent: string, child: string): string {
  if (parent === '/') return `/${child}`
  return `${parent}/${child}`
}

// sortEntries puts directories ahead of regular files and
// breaks ties alphabetically. The server returns ls's native
// order, which is generally fine but not always alphabetical
// (some coresort settings sort by mtime), so re-sorting here
// makes the UI consistent.
function sortEntries(entries: ContainerFile[]): ContainerFile[] {
  return [...entries].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    return a.name.localeCompare(b.name)
  })
}

// decodeBase64Utf8 turns the server's base64 content into a
// utf-8 string. We use TextDecoder (not atob+decodeURI) so
// non-ASCII paths render correctly. If the bytes aren't valid
// utf-8, the decoder replaces the offending sequences with
// the standard replacement character rather than throwing —
// the worst case is a few weird glyphs in the viewer.
function decodeBase64Utf8(b64: string): string {
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return new TextDecoder('utf-8', { fatal: false }).decode(bytes)
}
