import { Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Button, Drawer, Popconfirm, Tabs, Tag } from 'antd'
import {
  Bell,
  Box,
  ChevronDown,
  ChevronRight,
  Clock,
  Container as ContainerIcon,
  Network,
  Play,
  ScrollText,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  useSuspenseApp,
  useSuspenseAppDeploys,
  useSuspenseAppEnv,
  useSuspenseAppVolumes,
  useAppLogs,
  useRollbackApp,
} from '../lib/hooks'
import { queryKeys } from '../lib/queryKeys'
import { useQueryClient } from '@tanstack/react-query'
import type { App as AppType, Deploy, EnvVar, Volume } from '../lib/types'
import { RouteFallback } from './RouteFallback'
import { LogViewer } from './LogViewer'

const DEPLOY_POLL_INTERVAL_MS = 3000

type TabKey = 'overview' | 'deploys' | 'logs'

// Map a Docker container status string to an antd Tag color. Used
// both in the drawer title and the apps list status cell — keeping
// the same palette everywhere so the eye learns the mapping.
function statusColor(status: string): string {
  switch (status) {
    case 'running':
      return 'green'
    case 'restarting':
      return 'blue'
    case 'paused':
      return 'orange'
    case 'dead':
      return 'red'
    case 'exited':
    case 'created':
    default:
      return 'default'
  }
}

export function AppDetail({
  appId,
  initialTab,
  onClose,
  onChanged,
}: {
  appId: number
  initialTab?: TabKey
  onClose: () => void
  onChanged: () => void
}) {
  // The Drawer is rendered OUTSIDE the Suspense boundary so it mounts
  // once and stays in the DOM while the body suspends. Earlier the
  // Suspense fallback was a hand-rolled fake drawer div that appeared,
  // vanished, then got replaced by the real antd Drawer — visually a
  // "white flash → drawer disappears → drawer reappears with content"
  // sequence. Lifting <Drawer> above <Suspense> makes the Drawer mount
  // exactly once: the slide-in plays once, and during data load only
  // the body swaps from <RouteFallback> to the real tabs.
  return (
    <Drawer
      open
      onClose={onClose}
      width={680}
      destroyOnClose
      title={
        <Suspense
          fallback={
            <div className="h-6 w-40 bg-[var(--bg-input)] rounded animate-pulse" />
          }
        >
          <AppDetailTitle appId={appId} />
        </Suspense>
      }
    >
      <Suspense fallback={<RouteFallback variant="drawer" />}>
        <AppDetailContent
          appId={appId}
          initialTab={initialTab}
          onChanged={onChanged}
        />
      </Suspense>
    </Drawer>
  )
}

function AppDetailTitle({
  appId,
}: {
  appId: number
}) {
  const appQuery = useSuspenseApp(appId)
  const app = appQuery.data
  // Action buttons live on the apps list row, not on the drawer
  // header — the drawer is for inspection (logs / deploys / env /
  // volumes / config), not control. Keeping it that way means we
  // never have to reconcile two action surfaces; the list is
  // canonical.
  return (
    <div className="flex items-center gap-3">
      <ContainerIcon size={16} className="text-[var(--fg-muted)]" />
      <span className="mono text-base">{app.name}</span>
      {app.container && (
        <Tag className="!m-0" color={statusColor(app.container.status)}>
          {app.container.status}
        </Tag>
      )}
    </div>
  )
}

function AppDetailContent({
  appId,
  initialTab,
  onChanged,
}: {
  appId: number
  initialTab?: TabKey
  onChanged: () => void
}) {
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

  return (
    <Tabs
      activeKey={activeTab}
      onChange={(k) => setActiveTab(k as TabKey)}
      items={[
        {
          key: 'overview',
          label: t('detail.tabOverview'),
          children: (
            <OverviewTab app={app} env={env} volumes={volumes} />
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
          children: <DeploysTab deploys={deploys} appId={appId} />,
        },
        {
          key: 'logs',
          label: t('detail.tabLogs'),
          children: <LogsTab appId={appId} hasContainer={!!app.container} />,
        },
      ]}
    />
  )
}

function OverviewTab({
  app,
  env,
  volumes,
}: {
  app: AppType
  env: EnvVar[]
  volumes: Volume[]
}) {
  const { t } = useTranslation('apps')
  return (
    <div className="space-y-4 text-sm">
      <div className="space-y-3">
        <Field
          label={t('detail.image')}
          value={app.image}
          mono
          icon={<Box size={14} />}
        />
        <Field
          label={t('detail.internalPort')}
          value={String(app.port)}
          mono
          icon={<Network size={14} />}
        />
        <Field
          label={t('detail.created')}
          value={app.createdAt}
          mono
          icon={<Clock size={14} />}
        />
        {app.container && (
          <>
            <Field
              label={t('detail.container')}
              value={app.container.name}
              mono
              icon={<ContainerIcon size={14} />}
            />
            <Field
              label={t('detail.started')}
              value={app.container.startedAt ?? '—'}
              mono
              icon={<Play size={14} />}
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

function DeploysTab({ deploys, appId }: { deploys: Deploy[]; appId: number }) {
  const { t } = useTranslation('apps')
  const rollback = useRollbackApp()
  // Local state for which deploy's log panel is open. By default we
  // auto-open the panel for any in-flight deploy so the operator
  // doesn't have to click to see what's happening — Coolify does
  // the same.
  const [openLogs, setOpenLogs] = useState<Set<number>>(() => {
    const inFlight = deploys.find((d) => d.status === 'running')
    return new Set(inFlight ? [inFlight.id] : [])
  })
  if (deploys.length === 0) {
    return (
      <div className="py-8 text-center text-[var(--fg-muted)] text-sm">
        {t('detail.noDeploys')}
      </div>
    )
  }
  return (
    <div className="space-y-2">
      {deploys.map((d) => {
        const canRollback = d.status === 'success'
        const isOpen = openLogs.has(d.id)
        return (
          <div
            key={d.id}
            className="border border-[var(--border)] rounded-md bg-[var(--bg-input)] text-sm"
          >
            <div className="p-3">
              <div className="flex items-center justify-between mb-1">
                <span className="mono text-xs">
                  #{d.id} · {d.trigger}
                  {d.commitSha && (
                    <span className="ml-2 text-[var(--fg-muted)]">{d.commitSha.slice(0, 7)}</span>
                  )}
                </span>
                <div className="flex items-center gap-2">
                  <Tag
                    color={
                      d.status === 'success'
                        ? 'green'
                        : d.status === 'failed'
                          ? 'red'
                          : d.status === 'rolled_back'
                            ? 'orange'
                            : d.status === 'running'
                              ? 'blue'
                              : 'default'
                    }
                  >
                    {d.status}
                  </Tag>
                  <Button
                    size="small"
                    type="text"
                    icon={isOpen ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                    onClick={() => {
                      setOpenLogs((prev) => {
                        const next = new Set(prev)
                        if (next.has(d.id)) {
                          next.delete(d.id)
                        } else {
                          next.add(d.id)
                        }
                        return next
                      })
                    }}
                  >
                    <ScrollText size={12} className="inline-block mr-1" />
                    {t('detail.logs')}
                  </Button>
                  {canRollback && (
                    <Popconfirm
                      title={t('detail.rollbackConfirmTitle')}
                      description={t('detail.rollbackConfirmDesc', { id: d.id })}
                      okText={t('detail.rollback')}
                      cancelText={t('common.cancel')}
                      onConfirm={() => rollback.mutate({ appId, deployId: d.id })}
                      okButtonProps={{ danger: true }}
                    >
                      <Button size="small" type="text" loading={rollback.isPending}>
                        {t('detail.rollback')}
                      </Button>
                    </Popconfirm>
                  )}
                </div>
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
                <div
                  className="text-xs text-[var(--danger)] mt-1 line-clamp-2"
                  title={d.error}
                >
                  {d.error}
                </div>
              )}
              <div className="text-xs text-[var(--fg-muted)] mt-1">
                {d.startedAt ?? d.createdAt}
                {d.finishedAt ? ` → ${d.finishedAt}` : ''}
              </div>
            </div>
            {isOpen && (
              <div className="px-3 pb-3">
                <LogViewer
                  url={`/api/apps/${appId}/deployments/${d.id}/logs/stream`}
                  heightClass="h-64"
                />
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function LogsTab({ appId, hasContainer }: { appId: number; hasContainer: boolean }) {
  const { t } = useTranslation('apps')
  const logsQuery = useAppLogs(appId, 300, { enabled: hasContainer })
  // Seed the SSE stream with the last 300 lines from the one-shot
  // GET, so the user sees history the moment the tab opens. While
  // the GET is in flight the stream is already open at "now" and
  // the panel shows a connecting indicator — when the GET resolves
  // the seed lands and live lines append on top of it.
  const initialLines = useMemo(() => {
    if (!logsQuery.data) return undefined
    return logsQuery.data.split('\n')
  }, [logsQuery.data])
  if (!hasContainer) {
    return (
      <div className="mono text-xs text-[var(--fg-muted)] py-6 text-center">
        {t('detail.noContainer')}
      </div>
    )
  }
  return (
    <LogViewer
      url={`/api/apps/${appId}/logs/stream?tail=300`}
      initialLines={initialLines}
    />
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
  icon,
}: {
  label: string
  value: string
  mono?: boolean
  icon?: ReactNode
}) {
  return (
    <div className="flex items-center gap-3 min-h-[22px]">
      {icon && (
        <span className="text-[var(--fg-muted)] shrink-0 inline-flex items-center justify-center w-3.5">
          {icon}
        </span>
      )}
      <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] w-32 shrink-0 leading-none">
        {label}
      </span>
      <span className={`${mono ? 'mono text-xs' : 'text-sm'} leading-none`}>{value}</span>
    </div>
  )
}

function hasRunningDeploy(deploys: Deploy[]): boolean {
  return deploys.length > 0 && deploys[0].status === 'running'
}