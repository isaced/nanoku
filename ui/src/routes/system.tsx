import { createFileRoute, redirect } from '@tanstack/react-router'
import { Suspense, useState } from 'react'
import {
  Button,
  InputNumber,
  Space,
  Switch,
  Tag,
  Tooltip,
} from 'antd'
import {
  CircleCheck,
  CircleDashed,
  CircleX,
  Container as ContainerIcon,
  Info,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react'
import { Trans, useTranslation } from 'react-i18next'
import { ensureAuth, isAuthenticated } from '../lib/auth'
import {
  useSuspenseStatus,
  useSuspenseSystemStatus,
  useSystemLogs,
} from '../lib/hooks'
import { TopNav } from '../components/TopNav'
import { RouteError } from '../components/RouteError'
import { RouteFallback } from '../components/RouteFallback'

export const Route = createFileRoute('/system')({
  beforeLoad: async () => {
    if (isAuthenticated()) return
    if (!(await ensureAuth())) {
      throw redirect({ to: '/login' })
    }
  },
  component: SystemPage,
  errorComponent: RouteError,
  pendingComponent: () => <RouteFallback variant="page" />,
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
  const caddyLogs = useSystemLogs('caddy', tail, {
    enabled: systemStatusQuery.data.dockerAvailable === true,
  })
  const selfLogs = useSystemLogs('nanoku', tail, {
    enabled: systemStatusQuery.data.nanokuContainerConfigured === true,
  })

  const status = statusQuery.data
  const systemStatus = systemStatusQuery.data

  const fetching =
    statusQuery.isFetching ||
    systemStatusQuery.isFetching ||
    caddyLogs.isFetching ||
    selfLogs.isFetching

  const reload = () => {
    void statusQuery.refetch()
    void systemStatusQuery.refetch()
    void caddyLogs.refetch()
    void selfLogs.refetch()
  }

  return (
    <div className="flex-1 flex flex-col">
      <TopNav status={status} onRefresh={reload} loading={fetching} />

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
          <LogPanel
            icon={<ShieldCheck size={14} />}
            title={t('logPanel.nanokuSelf')}
            containerName={systemStatus.nanokuContainerName}
            status={systemStatus.nanokuContainerConfigured ? 'configured' : undefined}
            loading={selfLogs.isFetching}
            logs={selfLogs.data ?? ''}
            wordWrap={wordWrap}
            onRefresh={() => selfLogs.refetch()}
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
          <LogPanel
            icon={<ContainerIcon size={14} />}
            title={t('logPanel.caddy')}
            containerName={systemStatus.caddyContainer}
            status={status.caddyStatus}
            loading={caddyLogs.isFetching}
            logs={caddyLogs.data ?? ''}
            wordWrap={wordWrap}
            onRefresh={() => caddyLogs.refetch()}
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
      </main>
    </div>
  )
}

function LogPanel({
  icon,
  title,
  containerName,
  status,
  loading,
  logs,
  wordWrap,
  onRefresh,
  emptyHint,
}: {
  icon: React.ReactNode
  title: string
  containerName?: string
  status?: string
  loading: boolean
  logs: string
  wordWrap: boolean
  onRefresh: () => void
  emptyHint?: React.ReactNode
}) {
  const { t } = useTranslation('system')
  return (
    <section className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] overflow-hidden flex flex-col">
      <div className="px-4 py-3 border-b border-[var(--border)] flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 min-w-0">
          <span className="text-[var(--fg-muted)]">{icon}</span>
          <span className="mono text-sm">{title}</span>
          {containerName && (
            <Tag className="!m-0 mono text-[10px]">{containerName}</Tag>
          )}
          {status && <StatusTag status={status} />}
        </div>
        <Tooltip title={t('logPanel.refresh')}>
          <Button
            type="text"
            size="small"
            icon={
              <RefreshCw
                size={13}
                className={loading ? 'animate-spin' : ''}
              />
            }
            onClick={onRefresh}
            loading={loading}
            disabled={!containerName}
          />
        </Tooltip>
      </div>
      <div className="flex-1 min-h-0">
        {emptyHint ? (
          <div className="p-4">{emptyHint}</div>
        ) : (
          <pre
            className={`mono text-xs leading-relaxed bg-[var(--bg-input)] p-3 overflow-auto h-96 text-[var(--fg-muted)] ${
              wordWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'
            }`}
          >
            {logs || t('logPanel.clickRefresh')}
          </pre>
        )}
      </div>
    </section>
  )
}

function StatusTag({ status }: { status: string }) {
  if (status === 'running' || status === 'configured') {
    const color = status === 'running' ? 'green' : 'blue'
    return (
      <Tag color={color} className="!m-0">
        <span className="inline-flex items-center gap-1">
          <CircleCheck size={10} /> {status}
        </span>
      </Tag>
    )
  }
  if (status === 'not_found' || status === 'skipped') {
    return (
      <Tag className="!m-0">
        <span className="inline-flex items-center gap-1">
          <CircleDashed size={10} /> {status}
        </span>
      </Tag>
    )
  }
  return (
    <Tag color="red" className="!m-0">
      <span className="inline-flex items-center gap-1">
        <CircleX size={10} /> {status}
      </span>
    </Tag>
  )
}