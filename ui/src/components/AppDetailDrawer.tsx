import { App, Button, Drawer, Popconfirm, Tabs, Tag } from 'antd'
import { Bell, Pencil, RefreshCw } from 'lucide-react'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import {
  useApp,
  useAppDeploys,
  useAppEnv,
  useAppLogs,
  useAppVolumes,
  useRotateTriggerToken,
} from '../lib/hooks'
import type { App as AppType } from '../lib/types'

export function AppDetail({
  appId,
  onClose,
  onEditRequested,
}: {
  appId: number
  onClose: () => void
  onEditRequested: (app: AppType) => void
}) {
  const { message } = App.useApp()
  const { t } = useTranslation('apps')

  const appQuery = useApp(appId)
  const envQuery = useAppEnv(appId)
  const volumesQuery = useAppVolumes(appId)
  const deploysQuery = useAppDeploys(appId)
  const logsQuery = useAppLogs(appId, 300, { enabled: false })
  const rotateToken = useRotateTriggerToken()

  useEffect(() => {
    if (appQuery.error) {
      message.error((appQuery.error as Error).message)
    }
  }, [appQuery.error, message])
  useEffect(() => {
    if (envQuery.error) {
      message.error((envQuery.error as Error).message)
    }
  }, [envQuery.error, message])
  useEffect(() => {
    if (volumesQuery.error) {
      message.error((volumesQuery.error as Error).message)
    }
  }, [volumesQuery.error, message])
  useEffect(() => {
    if (deploysQuery.error) {
      message.error((deploysQuery.error as Error).message)
    }
  }, [deploysQuery.error, message])
  useEffect(() => {
    if (logsQuery.error) {
      message.error((logsQuery.error as Error).message)
    }
  }, [logsQuery.error, message])

  const app = appQuery.data ?? null
  const env = envQuery.data ?? []
  const volumes = volumesQuery.data ?? []
  const deploys = deploysQuery.data ?? []
  const logs = logsQuery.data ?? ''

  function loadLogs() {
    if (!app?.container) return
    void logsQuery.refetch()
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
          defaultActiveKey="overview"
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
                          onConfirm={() =>
                            new Promise<void>((resolve, reject) => {
                              rotateToken.mutate(app.id, {
                                onSuccess: () => {
                                  message.success(
                                    t('trigger.rotatedToast', {
                                      url: `${window.location.origin}/api/apps/${app.name}/trigger`,
                                    }),
                                  )
                                  resolve()
                                },
                                onError: (err) => {
                                  message.error(err.message)
                                  reject(err)
                                },
                              })
                            })
                          }
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
              label: t('detail.tabDeploys', { count: deploys.length }),
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
                    loading={logsQuery.isFetching}
                    disabled={!app.container}
                  >
                    {t('detail.loadLogs')}
                  </Button>
                  <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-3 overflow-auto max-h-96 whitespace-pre-wrap break-all text-[var(--fg-muted)]">
                    {!app.container
                      ? t('detail.noContainer')
                      : logs || t('detail.loadLogsHint')}
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
