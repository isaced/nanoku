import { createFileRoute, redirect } from '@tanstack/react-router'
import { Suspense, useEffect, useMemo, useState } from 'react'
import {
  Button,
  InputNumber,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd'
import {
  Container as ContainerIcon,
  Info,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react'
import { Trans, useTranslation } from 'react-i18next'
import { ensureAuth, isAuthenticated } from '../lib/auth'
import { serviceStatusMeta } from '../lib/serviceStatus'
import {
  useCleanupStatus,
  useSuspenseStatus,
  useSuspenseSystemStatus,
  useSystemLogs,
} from '../lib/hooks'
import { RouteError } from '../components/RouteError'
import { RouteFallback } from '../components/RouteFallback'
import { LogViewer } from '../components/LogViewer'
import { StatusTag } from '../components/StatusTag'

export const Route = createFileRoute('/system')({
  beforeLoad: async () => {
    if (isAuthenticated()) return
    if (!(await ensureAuth())) {
      throw redirect({ to: '/login' })
    }
  },
  component: SystemPage,
  errorComponent: RouteError,
})

const SELF_CONTAINER_ENV = 'NANOKU_SELF_CONTAINER'

function SystemPage() {
  return (
    <Suspense fallback={<RouteFallback variant="page" />}>
      <SystemPageContent />
    </Suspense>
  )
}

function SystemPageContent() {
  const { t } = useTranslation('system')
  const [tail, setTail] = useState<number>(200)
  const [wordWrap, setWordWrap] = useState<boolean>(true)

  const statusQuery = useSuspenseStatus()
  const systemStatusQuery = useSuspenseSystemStatus()
  // Seed both panels with a one-shot GET so the user sees the last
  // `tail` lines the moment the page opens; the SSE stream then
  // keeps appending new lines below. We deliberately do NOT use the
  // streamed data as a source of truth for the initial render — the
  // GET lands before the SSE handshake in practice, but the
  // useLogStream initialLines seed handles either order.
  const caddyLogs = useSystemLogs('caddy', tail, {
    enabled: systemStatusQuery.data.dockerAvailable === true,
  })
  const selfLogs = useSystemLogs('nanoku', tail, {
    enabled: systemStatusQuery.data.nanokuContainerConfigured === true,
  })
  const caddyInitial = useMemo(
    () => (caddyLogs.data ? caddyLogs.data.split('\n') : undefined),
    [caddyLogs.data],
  )
  const selfInitial = useMemo(
    () => (selfLogs.data ? selfLogs.data.split('\n') : undefined),
    [selfLogs.data],
  )
  // When the user changes `tail`, force a fresh GET (which becomes
  // a fresh SSE connection from the new url). We use a key bump
  // trick: bumping a counter is enough to invalidate the seed memo
  // because useSystemLogs is keyed on tail already, so a new tail
  // → new query → new data → new initialLines → fresh stream.
  useEffect(() => {
    void caddyLogs.refetch()
    void selfLogs.refetch()
    // We intentionally re-run on tail change only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tail])

  const status = statusQuery.data
  const systemStatus = systemStatusQuery.data

  return (
    <div className="flex-1 flex flex-col">
      <main className="flex-1 px-8 py-8 max-w-6xl w-full mx-auto w-full">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-medium tracking-tight">{t('title')}</h1>
            <p className="text-sm text-[var(--fg-muted)] mt-1">{t('subtitle')}</p>
          </div>
          <Space>
            <span className="text-xs text-[var(--fg-muted)]">
              {t('wordWrap')}
            </span>
            <Switch
              size="small"
              checked={wordWrap}
              onChange={setWordWrap}
              aria-label={t('wordWrap')}
            />
            <span className="text-xs text-[var(--fg-muted)]">{t('tailLines')}</span>
            <InputNumber
              min={50}
              max={5000}
              step={50}
              value={tail}
              onChange={(v) => v && setTail(v as number)}
              className="!w-24"
            />
            <Button
              icon={<RefreshCw size={13} />}
              onClick={() => {
                void caddyLogs.refetch()
                void selfLogs.refetch()
              }}
              loading={caddyLogs.isFetching || selfLogs.isFetching}
            >
              {t('refreshBoth')}
            </Button>
          </Space>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <LogCard
            icon={<ShieldCheck size={14} />}
            title={t('logPanel.nanokuSelf')}
            containerName={systemStatus.nanokuContainerName}
            status={systemStatus.nanokuContainerConfigured ? 'configured' : undefined}
            wordWrap={wordWrap}
            streamUrl={
              systemStatus.nanokuContainerConfigured
                ? `/api/system/logs/stream?source=nanoku&tail=${tail}`
                : null
            }
            initialLines={selfInitial}
            emptyHint={
              !systemStatus.nanokuContainerConfigured ? (
                <div className="text-xs text-[var(--fg-muted)] space-y-1">
                  <div className="flex items-center gap-1.5">
                    <Info size={12} />
                    <Trans
                      ns="system"
                      i18nKey="nanokuHint.set"
                      components={{
                        codeExample: (
                          <code className="mono text-[var(--fg)]">
                            {`${SELF_CONTAINER_ENV}=<name>`}
                          </code>
                        ),
                      }}
                    />
                  </div>
                  <Trans
                    ns="system"
                    i18nKey="nanokuHint.docker"
                    components={{ mono: <span className="mono text-[var(--fg)]" /> }}
                  />
                </div>
              ) : null
            }
          />
          <LogCard
            icon={<ContainerIcon size={14} />}
            title={t('logPanel.caddy')}
            containerName={systemStatus.caddyContainer}
            status={status.caddyStatus}
            wordWrap={wordWrap}
            streamUrl={
              systemStatus.dockerAvailable
                ? `/api/system/logs/stream?source=caddy&tail=${tail}`
                : null
            }
            initialLines={caddyInitial}
            emptyHint={
              !systemStatus.dockerAvailable ? (
                <div className="text-xs text-[var(--fg-muted)] flex items-center gap-1.5">
                  <Info size={12} />
                  {t('dockerHint')}
                </div>
              ) : null
            }
          />
        </div>

        <CleanupSection />
      </main>
    </div>
  )
}

// CleanupSection surfaces the background Janitor's per-task
// status from GET /api/system/cleanup. Each task shows its
// last-run time, last-run counts (pruned / scanned / bytes), and
// any error from the most recent run. The hook auto-polls every
// 30s, so an operator watching this card sees liveness + health
// without manual refresh.
function CleanupSection() {
  const { t } = useTranslation('system')
  const cleanupQuery = useCleanupStatus()

  const tasks = useMemo(() => {
    const raw = cleanupQuery.data?.tasks ?? {}
    return Object.entries(raw)
      .map(([name, entry]) => ({ name, ...entry }))
      .sort((a, b) => a.name.localeCompare(b.name))
  }, [cleanupQuery.data])

  return (
    <section className="mt-8 border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] overflow-hidden">
      <div className="px-4 py-3 border-b border-[var(--border)] flex items-center justify-between">
        <div className="flex items-center gap-2 min-w-0">
          <ShieldCheck size={14} className="text-[var(--fg-muted)]" />
          <span className="mono text-sm">{t('cleanup.title')}</span>
          <span className="text-xs text-[var(--fg-muted)]">
            {t('cleanup.subtitle')}
          </span>
        </div>
        <Button
          icon={<RefreshCw size={13} />}
          onClick={() => void cleanupQuery.refetch()}
          loading={cleanupQuery.isFetching}
          size="small"
        >
          {t('cleanup.refresh')}
        </Button>
      </div>
      <div className="p-3">
        {tasks.length === 0 ? (
          <div className="text-xs text-[var(--fg-muted)] p-2">
            {t('cleanup.empty')}
          </div>
        ) : (
          <Table<CleanupRow>
            size="small"
            rowKey="name"
            pagination={false}
            dataSource={tasks}
            columns={[
              {
                title: t('cleanup.colTask'),
                dataIndex: 'name',
                key: 'name',
                render: (name: string) => (
                  <span className="mono text-xs">{name}</span>
                ),
              },
              {
                title: t('cleanup.colLastRun'),
                dataIndex: 'lastRun',
                key: 'lastRun',
                render: (v: string) =>
                  v ? <span className="mono text-xs">{formatTs(v)}</span> : '—',
              },
              {
                title: t('cleanup.colNextRun'),
                dataIndex: 'nextRun',
                key: 'nextRun',
                render: (v: string) =>
                  v ? <span className="mono text-xs">{formatTs(v)}</span> : '—',
              },
              {
                title: t('cleanup.colPruned'),
                dataIndex: 'lastResult',
                key: 'pruned',
                render: (r: { pruned: number; scanned: number; bytes: number }) => (
                  <span className="mono text-xs">
                    {r.pruned}
                    <span className="text-[var(--fg-muted)]">
                      {' / '}
                      {r.scanned}
                    </span>
                  </span>
                ),
              },
              {
                title: t('cleanup.colBytes'),
                dataIndex: 'lastResult',
                key: 'bytes',
                render: (r: { bytes: number }) => (
                  <span className="mono text-xs">{formatBytes(r.bytes)}</span>
                ),
              },
              {
                title: t('cleanup.colRuns'),
                dataIndex: 'runCount',
                key: 'runCount',
                render: (n: number, row) => (
                  <span className="mono text-xs">
                    {n}
                    {row.errCount > 0 && (
                      <span className="text-[var(--danger)] ml-1">
                        ({row.errCount} ✗)
                      </span>
                    )}
                  </span>
                ),
              },
              {
                title: t('cleanup.colStatus'),
                key: 'status',
                render: (_: unknown, row) =>
                  row.lastErr ? (
                    <Tooltip title={row.lastErr}>
                      <StatusTag
                        variant="danger"
                        label={t('cleanup.statusError')}
                        size="small"
                      />
                    </Tooltip>
                  ) : row.runCount > 0 ? (
                    <StatusTag
                      variant="success"
                      label={t('cleanup.statusOk')}
                      size="small"
                    />
                  ) : (
                    <StatusTag
                      variant="muted"
                      label={t('cleanup.statusIdle')}
                      size="small"
                    />
                  ),
              },
            ]}
          />
        )}
      </div>
    </section>
  )
}

type CleanupRow = {
  name: string
  lastRun: string
  nextRun: string
  lastResult: { scanned: number; pruned: number; bytes: number }
  lastErr?: string
  lastErrAt?: string
  runCount: number
  errCount: number
};

function formatTs(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  // Locale-aware short timestamp; falls back to ISO on failure.
  return d.toLocaleString();
}

function formatBytes(n: number): string {
  if (!n) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(
    Math.floor(Math.log(n) / Math.log(1024)),
    units.length - 1,
  );
  return `${(n / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function LogCard({
  icon,
  title,
  containerName,
  status,
  wordWrap,
  streamUrl,
  initialLines,
  emptyHint,
}: {
  icon: React.ReactNode
  title: string
  containerName?: string
  status?: string
  wordWrap: boolean
  streamUrl: string | null
  initialLines?: string[]
  emptyHint?: React.ReactNode
}) {
  return (
    <section className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] overflow-hidden flex flex-col">
      <div className="px-4 py-3 border-b border-[var(--border)] flex items-center gap-3">
        <div className="flex items-center gap-2 min-w-0">
          <span className="text-[var(--fg-muted)]">{icon}</span>
          <span className="mono text-sm">{title}</span>
          {containerName && (
            <Tag className="!m-0 mono text-[10px]">{containerName}</Tag>
          )}
          {status && (
            <StatusTag {...serviceStatusMeta(status)} size="small" />
          )}
        </div>
      </div>
      <div className="flex-1 min-h-0 p-3">
        {streamUrl ? (
          <LogViewer
            url={streamUrl}
            initialLines={initialLines}
            wordWrap={wordWrap}
          />
        ) : (
          <div className="p-2">{emptyHint}</div>
        )}
      </div>
    </section>
  )
}

// StatusTag is now imported from `../components/StatusTag` and
// receives its visual contract from `serviceStatusMeta()` above.