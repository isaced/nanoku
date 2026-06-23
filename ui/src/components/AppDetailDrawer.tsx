import { useEffect, useRef, useState } from 'react'
import { App, Button, Drawer, Popconfirm, Tabs, Tag } from 'antd'
import { Bell, Pencil, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import {
  useSuspenseApp,
  useSuspenseAppDeploys,
  useSuspenseAppEnv,
  useSuspenseAppVolumes,
  useAppLogs,
} from '../lib/hooks'
import { queryKeys } from '../lib/queryKeys'
import { useQueryClient } from '@tanstack/react-query'
import type { App as AppType, Deploy, EnvVar, Volume } from '../lib/types'

const DEPLOY_POLL_INTERVAL_MS = 3000

type TabKey = 'overview' | 'deploys' | 'logs'

export function AppDetail({
  appId,
  initialTab,
  onClose,
  onChanged,
  onEditRequested,
}: {
  appId: number
  initialTab?: TabKey
  onClose: () => void
  onChanged: () => void
  onEditRequested: (app: AppType) => void
}) {
  return <AppDetailContent appId={appId} initialTab={initialTab} onClose={onClose} onChanged={onChanged} onEditRequested={onEditRequested} />
}

function AppDetailContent({
  appId,
  initialTab,
  onClose,
  onChanged,
  onEditRequested,
}: {
  appId: number
  initialTab?: TabKey
  onClose: () => void
  onChanged: () => void
  onEditRequested: (app: AppType) => void
}) {
  const { message } = App.useApp()
  const { t } = useTranslation('apps')
  const [activeTab, setActiveTab] = useState<TabKey>(initialTab ?? 'overview')
  const queryClient = useQueryClient()

  const appQuery = useSuspenseApp(appId)
  const envQuery = useSuspenseAppEnv(appId)
  const volumesQuery = useSuspenseAppVolumes(appId)
  const deploysQuery = useSuspenseAppDeploys(appId)

  const app = appQuery.data
  const env = envQuery.data
  const volumes = volumesQuery.data
  const deploys = deploysQuery.data

  // Self-managed deploys polling. The Drawer renders inside a modal
  // portal that mounts/unmounts with tab visibility, and we want the
  // polling to keep ticking even when the user isn't actively looking
  // at the Deploys tab — for example to surface a CI-triggered deploy
  // that lands while the user is reading Overview.
  useEffect(() => {
    const id = setInterval(() => {
      void deploysQuery.refetch()
    }, DEPLOY_POLL_INTERVAL_MS)
    return () => clearInterval(id)
  }, [deploysQuery])

  // When a deploy finishes, the App row's container status changes too.
  const inFlight = hasRunningDeploy(deploys)
  const wasInFlight = useRef(false)
  useEffect(() => {
    if (wasInFlight.current && !inFlight) {
      void appQuery.refetch()
      void envQuery.refetch()
      void volumesQuery.refetch()
      void queryClient.invalidateQueries({ queryKey: queryKeys.apps.all() })
      onChanged()
    }
    wasInFlight.current = inFlight
  }, [inFlight, appQuery, envQuery, volumesQuery, queryClient, onChanged])

  // Surface deploy terminal status to the user.
  const lastReported = useRef<{ id: number; status: string } | null>(null)
  useEffect(() => {
    const top = deploys[0]
    if (!top) return
    if (top.status === 'running') return
    const prev = lastReported.current
    if (prev && prev.id === top.id && prev.status === top.status) return
    lastReported.current = { id: top.id, status: top.status }
    if (top.status === 'success') {
      message.success(t('detail.deploySuccess', { name: app.name }))
    } else if (top.status === 'failed') {
      message.error(
        t('detail.deployFailed', {
          name: app.name,
          error: top.error ?? '',
        }),
      )
    }
  }, [deploys, app.name, message, t])

  return (
    <Drawer
      open
      onClose={onClose}
      width={680}
      title={
        <div className="flex items-center gap-3">
          <span className="mono text-base">{app.name}</span>
          {app.container && <Tag className="!m-0">{app.container.status}</Tag>}
          <Button
            size="small"
            type="text"
            icon={<Pencil size={13} />}
            className="!ml-auto"
            onClick={() => onEditRequested(app)}
          >
            {t('detail.editInEditor')}
          </Button>
        </div>
      }
      destroyOnClose
    >
      <Tabs
        activeKey={activeTab}
        onChange={(k) => setActiveTab(k as TabKey)}
        items={[
          {
            key: 'overview',
            label: t('detail.tabOverview'),
            children: (
              <OverviewTab
                app={app}
                env={env}
                volumes={volumes}
                onRotateTrigger={async () => {
                  try {
                    await api.rotateTriggerToken(app.id)
                    message.success(
                      t('trigger.rotatedToast', {
                        url: `${window.location.origin}/api/apps/${app.name}/trigger`,
                      }),
                    )
                    void appQuery.refetch()
                    void queryClient.invalidateQueries({ queryKey: queryKeys.apps.all() })
                    onChanged()
                  } catch (err) {
                    message.error((err as Error).message)
                  }
                }}
              />
            ),
          },
          {
            key: 'deploys',
            label: (
              <span className="inline-flex items-center gap-2">
                {t('detail.tabDeploys', { count: deploys.length })}
                {inFlight && (
                  <span
                    className="inline-block size-1.5 rounded-full bg-[var(--accent)] animate-pulse"
                    aria-label={t('detail.deployInFlight')}
                  />
                )}
              </span>
            ),
            children: <DeploysTab deploys={deploys} />,
          },
          {
            key: 'logs',
            label: t('detail.tabLogs'),
            children: <LogsTab appId={appId} hasContainer={!!app.container} />,
          },
        ]}
      />
    </Drawer>
  )
}

function OverviewTab({
  app,
  env,
  volumes,
  onRotateTrigger,
}: {
  app: AppType
  env: EnvVar[]
  volumes: Volume[]
  onRotateTrigger: () => void
}) {
  const { t } = useTranslation('apps')
  return (
    <div className="space-y-4 text-sm">
      <div className="space-y-3">
        <Field label={t('detail.image')} value={app.image} mono />
        <Field label={t('detail.internalPort')} value={String(app.port)} mono />
        <Field label={t('detail.created')} value={app.createdAt} mono />
        {app.container && (
          <>
            <Field label={t('detail.container')} value={app.container.name} mono />
            <Field
              label={t('detail.started')}
              value={app.container.startedAt ?? '—'}
              mono
            />
          </>
        )}
      </div>

      {app.triggerConfigured && (
        <div className="border border-[var(--border)] rounded-md p-3 bg-[var(--bg-input)]/40">
          <div className="flex items-center gap-2 mb-2">
            <Bell size={13} className="text-[var(--fg-muted)]" />
            <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
              {t('detail.trigger')}
            </span>
            <Tag color="green" className="!m-0 ml-auto">
              {t('detail.triggerActive')}
            </Tag>
          </div>
          <div className="mono text-xs text-[var(--fg-muted)] break-all">
            POST {window.location.origin}/api/apps/{app.name}/trigger
          </div>
          <div className="mt-2">
            <Popconfirm
              title={t('trigger.rotateTitle')}
              description={t('trigger.rotateDescriptionShort')}
              okText={t('trigger.rotateButton')}
              onConfirm={onRotateTrigger}
            >
              <Button size="small" icon={<RefreshCw size={12} />}>
                {t('trigger.rotateButton')}
              </Button>
            </Popconfirm>
          </div>
        </div>
      )}

      <ReadOnlyBlock title={t('detail.envVars')} count={env.length}>
        {env.length === 0 ? (
          <div className="text-xs text-[var(--fg-muted)] py-2">
            {t('detail.noEnvVars')}
          </div>
        ) : (
          <div className="space-y-1">
            {env.map((row, i) => (
              <div
                key={i}
                className="flex items-center gap-2 text-xs mono py-0.5"
              >
                <span className="text-[var(--fg-muted)] w-44 shrink-0 truncate">
                  {row.key}
                </span>
                <span className="text-[var(--fg)] break-all">
                  {row.value}
                </span>
              </div>
            ))}
          </div>
        )}
      </ReadOnlyBlock>

      {app.deployMethod === 'docker' && (
        <ReadOnlyBlock title={t('detail.volumes')} count={volumes.length}>
          {volumes.length === 0 ? (
            <div className="text-xs text-[var(--fg-muted)] py-2">
              {t('detail.noVolumes')}
            </div>
          ) : (
            <div className="space-y-2">
              {volumes.map((v, i) => (
                <div
                  key={i}
                  className="flex items-center gap-2 text-xs mono border border-[var(--border)] rounded-md px-2 py-1 bg-[var(--bg-input)]"
                >
                  <Tag className="!m-0" color={v.type === 'bind' ? 'purple' : 'default'}>
                    {v.type === 'bind' ? t('detail.typeBind') : t('detail.typeVolume')}
                  </Tag>
                  <span className="text-[var(--fg)] break-all flex-1">
                    {v.source || (
                      <span className="text-[var(--fg-muted)]">
                        {t('volumeEditor.autoBadge')}
                      </span>
                    )}
                    <span className="text-[var(--fg-muted)] mx-1">→</span>
                    <span>{v.target}</span>
                  </span>
                  {v.readOnly && (
                    <Tag className="!m-0">{t('detail.readOnly')}</Tag>
                  )}
                </div>
              ))}
              {app.deleteVolumesOnRemove && (
                <div className="text-xs text-[var(--fg-muted)]">
                  {t('detail.deleteVolumesOnRemove')}
                </div>
              )}
            </div>
          )}
        </ReadOnlyBlock>
      )}

      {app.deployMethod === 'compose' && (
        <ReadOnlyBlock title={t('detail.volumes')} count={0}>
          <div className="text-xs text-[var(--fg-muted)] py-2">
            {t('detail.composeModeHint')}
          </div>
        </ReadOnlyBlock>
      )}
    </div>
  )
}

function DeploysTab({ deploys }: { deploys: Deploy[] }) {
  const { t } = useTranslation('apps')
  if (deploys.length === 0) {
    return (
      <div className="py-8 text-center text-[var(--fg-muted)] text-sm">
        {t('detail.noDeploys')}
      </div>
    )
  }
  return (
    <div className="space-y-2">
      {deploys.map((d) => (
        <div
          key={d.id}
          className="border border-[var(--border)] rounded-md p-3 bg-[var(--bg-input)] text-sm"
        >
          <div className="flex items-center justify-between mb-1">
            <span className="mono text-xs">
              #{d.id} · {d.trigger}
              {d.commitSha && (
                <span className="ml-2 text-[var(--fg-muted)]">{d.commitSha.slice(0, 7)}</span>
              )}
            </span>
            <Tag
              color={
                d.status === 'success'
                  ? 'green'
                  : d.status === 'failed'
                    ? 'red'
                    : 'default'
              }
            >
              {d.status}
            </Tag>
          </div>
          {d.commitMessage && (
            <div className="text-xs mt-1 line-clamp-2">
              {d.commitMessage.split('\n')[0]}
            </div>
          )}
          {d.containerName && (
            <div className="mono text-xs text-[var(--fg-muted)] mt-1">
              {t('detail.containerLabel')}: {d.containerName}
            </div>
          )}
          {d.error && (
            <div className="text-xs text-[var(--danger)] mt-1">
              {d.error}
            </div>
          )}
          <div className="text-xs text-[var(--fg-muted)] mt-1">
            {d.startedAt ?? d.createdAt}
            {d.finishedAt ? ` → ${d.finishedAt}` : ''}
          </div>
        </div>
      ))}
    </div>
  )
}

function LogsTab({ appId, hasContainer }: { appId: number; hasContainer: boolean }) {
  const { t } = useTranslation('apps')
  const logsQuery = useAppLogs(appId, 300, { enabled: hasContainer })
  const [loaded, setLoaded] = useState(false)

  const logs = logsQuery.data ?? ''
  const placeholder = !hasContainer ? t('detail.noContainer') : t('detail.loadLogsHint')
  const display = loaded ? logs : placeholder

  return (
    <div className="space-y-2">
      <Button
        size="small"
        icon={<RefreshCw size={13} />}
        onClick={() => {
          setLoaded(true)
          void logsQuery.refetch()
        }}
        loading={logsQuery.isFetching}
        disabled={!hasContainer}
      >
        {t('detail.loadLogs')}
      </Button>
      <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-3 overflow-auto max-h-96 whitespace-pre-wrap break-all text-[var(--fg-muted)]">
        {display}
      </pre>
    </div>
  )
}

function ReadOnlyBlock({
  title,
  count,
  children,
}: {
  title: string
  count: number
  children: React.ReactNode
}) {
  const { t } = useTranslation('apps')
  return (
    <div className="border border-[var(--border)] rounded-md p-3 bg-[var(--bg-input)]/40">
      <div className="flex items-center gap-2 mb-2">
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          {title}
        </span>
        <Tag className="!m-0">{count}</Tag>
        <span className="text-xs text-[var(--fg-muted)] ml-auto">
          {t('detail.editInEditor')}
        </span>
      </div>
      {children}
    </div>
  )
}

function Field({
  label,
  value,
  mono,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="flex items-baseline gap-3">
      <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] w-32 shrink-0">
        {label}
      </span>
      <span className={mono ? 'mono text-xs' : 'text-sm'}>{value}</span>
    </div>
  )
}

function hasRunningDeploy(deploys: Deploy[]): boolean {
  return deploys.length > 0 && deploys[0].status === 'running'
}