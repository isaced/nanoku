import { useCallback, useEffect, useRef, useState } from 'react'
import { App, Button, Drawer, Popconfirm, Tabs, Tag } from 'antd'
import { Bell, Pencil, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { App as AppType, Deploy, EnvVar, Volume } from '../lib/types'

// How often to refresh the deploys list while a deploy is in flight.
// 1.5s keeps the UI feeling live without hammering the server for a
// multi-second pull.
const DEPLOY_POLL_INTERVAL_MS = 1500

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
  const { message } = App.useApp()
  const { t } = useTranslation('apps')
  const [app, setApp] = useState<AppType | null>(null)
  const [env, setEnv] = useState<EnvVar[]>([])
  const [deploys, setDeploys] = useState<Deploy[]>([])
  const [logs, setLogs] = useState<string>('')
  const [logsLoading, setLogsLoading] = useState(false)
  const [volumes, setVolumes] = useState<Volume[]>([])
  const [activeTab, setActiveTab] = useState<TabKey>(initialTab ?? 'overview')

  // refetch counter used to drive the deploys query on a timer while a
  // deploy is in flight. The TanStack query interval option polls
  // unconditionally; we want to stop polling as soon as the row reaches a
  // terminal status.
  const [pollTick, setPollTick] = useState(0)

  const refresh = useCallback(async () => {
    try {
      const [a, e, d, v] = await Promise.all([
        api.getApp(appId),
        api.listAppEnv(appId),
        api.listAppDeploys(appId),
        api.listAppVolumes(appId),
      ])
      setApp(a)
      setEnv(e)
      setDeploys(d)
      setVolumes(v)
    } catch (err) {
      message.error((err as Error).message)
    }
  }, [appId, message])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // Drive a poll while the most recent deploy is in flight. This is the
  // contract from the backend: a successful deploy returns 202 immediately
  // with a deployId; the goroutine then writes success/failed to the DB.
  // The UI needs to surface that transition without a manual reload.
  const inFlight = hasRunningDeploy(deploys)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  useEffect(() => {
    if (intervalRef.current) {
      clearInterval(intervalRef.current)
      intervalRef.current = null
    }
    if (!inFlight) return
    intervalRef.current = setInterval(() => {
      setPollTick((n) => n + 1)
    }, DEPLOY_POLL_INTERVAL_MS)
    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current)
        intervalRef.current = null
      }
    }
  }, [inFlight])

  // The poll tick triggers a deploys-list refetch that also picks up the
  // container row update. We don't want to refetch the whole app/env on
  // every tick (env is immutable during deploy); the deploys query is
  // enough to drive the spinner → success transition.
  useEffect(() => {
    if (pollTick === 0) return
    void api
      .listAppDeploys(appId)
      .then(setDeploys)
      .catch((err: Error) => message.error(err.message))
  }, [pollTick, appId, message])

  // Once the in-flight deploy resolves, do a full refresh so the App
  // row reflects the new container (appsQuery was invalidated by the
  // mutation, but our local drawer state is stale until we pull).
  const wasInFlight = useRef(false)
  useEffect(() => {
    if (wasInFlight.current && !inFlight) {
      void refresh()
    }
    wasInFlight.current = inFlight
  }, [inFlight, refresh])

  // Surface deploy terminal status to the user. Only fire when the
  // transition is observed by the poll, not when refresh() runs from a
  // user action (manual reload). The poll tick is the trigger.
  const lastReportedDeployId = useRef<number | null>(null)
  useEffect(() => {
    if (pollTick === 0) return
    const top = deploys[0]
    if (!top || top.status === 'running') return
    if (lastReportedDeployId.current === top.id) return
    lastReportedDeployId.current = top.id
    if (top.status === 'success') {
      message.success(t('detail.deploySuccess', { name: app?.name ?? '' }))
    } else if (top.status === 'failed') {
      message.error(
        t('detail.deployFailed', {
          name: app?.name ?? '',
          error: top.error ?? '',
        }),
      )
    }
  }, [pollTick, deploys, app?.name, message, t])

  async function loadLogs() {
    if (!app?.container) {
      setLogs(t('detail.noContainer'))
      return
    }
    setLogsLoading(true)
    try {
      const out = await api.appLogs(appId, 300)
      setLogs(out)
    } catch (err) {
      message.error((err as Error).message)
    } finally {
      setLogsLoading(false)
    }
  }

  return (
    <Drawer
      open
      onClose={onClose}
      width={680}
      title={app ? (
        <div className="flex items-center gap-3">
          <span className="mono text-base">{app.name}</span>
          {app.container && <Tag className="!m-0">{app.container.status}</Tag>}
          <Button
            size="small"
            type="text"
            icon={<Pencil size={13} />}
            className="!ml-auto"
            onClick={() => app && onEditRequested(app)}
          >
            {t('detail.editInEditor')}
          </Button>
        </div>
      ) : t('common:status.loading', { ns: 'common' })}
      destroyOnClose
    >
      {!app ? (
        <div className="py-12 text-center text-[var(--fg-muted)]">
          {t('common:status.loading', { ns: 'common' })}
        </div>
      ) : (
        <Tabs
          activeKey={activeTab}
          onChange={(k) => setActiveTab(k as TabKey)}
          items={[
            {
              key: 'overview',
              label: t('detail.tabOverview'),
              children: (
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
                          onConfirm={async () => {
                            try {
                              await api.rotateTriggerToken(app.id)
                              message.success(
                                t('trigger.rotatedToast', {
                                  url: `${window.location.origin}/api/apps/${app.name}/trigger`,
                                }),
                              )
                              void refresh()
                              void onChanged()
                            } catch (err) {
                              message.error((err as Error).message)
                            }
                          }}
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
              children: deploys.length === 0 ? (
                <div className="py-8 text-center text-[var(--fg-muted)] text-sm">
                  {t('detail.noDeploys')}
                </div>
              ) : (
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
              ),
            },
            {
              key: 'logs',
              label: t('detail.tabLogs'),
              children: (
                <div className="space-y-2">
                  <Button
                    size="small"
                    icon={<RefreshCw size={13} />}
                    onClick={loadLogs}
                    loading={logsLoading}
                  >
                    {t('detail.loadLogs')}
                  </Button>
                  <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-3 overflow-auto max-h-96 whitespace-pre-wrap break-all text-[var(--fg-muted)]">
                    {logs || t('detail.loadLogsHint')}
                  </pre>
                </div>
              ),
            },
          ]}
        />
      )}
    </Drawer>
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