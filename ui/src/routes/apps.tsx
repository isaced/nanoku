import { createFileRoute, redirect } from '@tanstack/react-router'
import { Suspense, useState } from 'react'
import { App, Button, Space, Table, Tag, Tooltip } from 'antd'
import {
  Container as ContainerIcon,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Rocket,
  Square,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ensureAuth, isAuthenticated } from '../lib/auth'
import { containerStatusMeta } from '../lib/containerStatus'
import {
  appLifecycle,
  useAction,
  useDeleteApp,
  useDeployApp,
  useRestartApp,
  useStartApp,
  useStopApp,
  useSuspenseApps,
  useSuspenseStatus,
} from '../lib/hooks'
import type { App as AppType } from '../lib/types'
import { AppDetail } from '../components/AppDetailDrawer'
import { AppEditorModal, type AppEditorSaveResult } from '../components/AppEditor'
import { RouteError } from '../components/RouteError'
import { RouteFallback } from '../components/RouteFallback'
import { StatusTag } from '../components/StatusTag'

export const Route = createFileRoute('/apps')({
  beforeLoad: async () => {
    if (isAuthenticated()) return
    if (!(await ensureAuth())) {
      throw redirect({ to: '/login' })
    }
  },
  component: AppsPage,
  errorComponent: RouteError,
})

function AppsPage() {
  return (
    <Suspense fallback={<RouteFallback variant="page" />}>
      <AppsPageContent />
    </Suspense>
  )
}

function AppsPageContent() {
  const { message, modal } = App.useApp()
  const { t } = useTranslation('apps')
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<AppType | null>(null)
  const [detailAppId, setDetailAppId] = useState<number | null>(null)
  const [detailInitialTab, setDetailInitialTab] = useState<'overview' | 'deploys' | 'logs'>('overview')

  const appsQuery = useSuspenseApps()
  const statusQuery = useSuspenseStatus()
  const deployApp = useDeployApp()
  const startApp = useStartApp()
  const stopApp = useStopApp()
  const restartApp = useRestartApp()
  const deleteApp = useDeleteApp()
  const action = useAction()

  const apps = appsQuery.data
  const status = statusQuery.data

  const fetching = appsQuery.isFetching || statusQuery.isFetching

  const reload = () => {
    void appsQuery.refetch()
    void statusQuery.refetch()
  }

  function openCreate() {
    setEditing(null)
    setEditorOpen(true)
  }

  function openEdit(a: AppType) {
    setEditing(a)
    setEditorOpen(true)
  }

  function handleSaved({ saved, previous, isNew }: AppEditorSaveResult) {
    setEditorOpen(false)
    if (isNew) {
      void action.run(Promise.resolve(saved), {
        success: t('toast.added', { name: saved.name }),
      })
      return
    }
    void action.run(Promise.resolve(saved), {
      success: t('toast.updated', { name: saved.name }),
    })
    const appForRedeploy: AppType = { ...previous!, ...saved }
    modal.confirm({
      title: t('redeployPrompt.title', { name: appForRedeploy.name }),
      content: t('redeployPrompt.content'),
      okText: t('redeployPrompt.ok'),
      cancelText: t('redeployPrompt.cancel'),
      onOk: () => triggerDeploy(appForRedeploy),
    })
  }

  function triggerDeploy(app: AppType) {
    // Note: deployApp.mutate (not mutateAsync) because the success
    // payload is the API's DeployResponse which carries a custom
    // `accepted: false` signal that we surface as a warning toast
    // rather than a generic error.
    deployApp.mutate(app.id, {
      onSuccess: (resp) => {
        if (!resp.accepted) {
          message.warning(
            t('toast.deployBusy', { name: app.name, reason: resp.reason ?? '' }),
          )
          return
        }
        message.info(t('toast.deployStarted', { name: app.name }))
        setDetailAppId(app.id)
        setDetailInitialTab('deploys')
      },
      onError: (err) => {
        if (!(err instanceof Error && err.message === 'Unauthorized')) {
          message.error(err.message)
        }
      },
    })
  }

  function runAction(app: AppType, name: string, fn: () => Promise<unknown>) {
    void action.run(fn(), {
      success: t('toast.' + name, { name: app.name }),
    })
  }

  function confirmDelete(app: AppType) {
    modal.confirm({
      title: t('delete.title', { name: app.name }),
      content: t('delete.content'),
      okText: t('actions.delete', { ns: 'common' }),
      okType: 'danger',
      onOk: () =>
        action.run(deleteApp.mutateAsync(app.id), {
          success: t('toast.deleted', { name: app.name }),
        }).then(() => undefined),
    })
  }

  return (
    <div className="flex-1 flex flex-col">
      <main className="flex-1 px-8 py-8 max-w-6xl w-full mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-medium tracking-tight">{t('title')}</h1>
            <p className="text-sm text-[var(--fg-muted)] mt-1">
              {apps.length === 0
                ? t('subtitleEmpty')
                : t(apps.length === 1 ? 'subtitleOne' : 'subtitleOther', {
                    count: apps.length,
                    running: status.runningAppCount,
                  })}
            </p>
          </div>
          <Button
            type="primary"
            icon={<Plus size={14} />}
            onClick={openCreate}
          >
            {t('newApp')}
          </Button>
        </div>

        <div className="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--bg)]">
        <Table<AppType>
          dataSource={apps}
          rowKey="id"
          loading={fetching}
          pagination={false}
          scroll={{ x: 'max-content' }}
          className="nk-apps-table"
          locale={{ emptyText: <EmptyState onCreate={openCreate} /> }}
          columns={[
            {
              title: t('table.name'),
              dataIndex: 'name',
              render: (n: string, row) => {
                const image =
                  row.image ||
                  (row.deployMethod === 'compose' ? t('table.imageFromCompose') : '-')
                return (
                  <button
                    type="button"
                    className="text-left group"
                    onClick={() => setDetailAppId(row.id)}
                  >
                    <span className="block text-sm font-medium text-[var(--accent)] group-hover:underline">
                      {n}
                    </span>
                    <span className="block mono text-[11px] leading-tight text-[var(--fg-muted)] mt-0.5">
                      {image}
                    </span>
                  </button>
                )
              },
            },
            {
              title: t('table.method'),
              dataIndex: 'deployMethod',
              width: 90,
              render: (m: string) => (
                <Tag
                  color={m === 'compose' ? 'purple' : 'default'}
                  className="!m-0 mono text-[10px]"
                >
                  {m ?? 'docker'}
                </Tag>
              ),
            },
            {
              title: t('table.port'),
              dataIndex: 'port',
              width: 80,
              align: 'right',
              render: (p: number) => (
                <span className="mono text-sm text-[var(--fg-muted)]">{p}</span>
              ),
            },
            {
              title: t('table.status'),
              key: 'status',
              width: 220,
              render: (_: unknown, row) => <StatusCell app={row} />,
            },
            {
              title: '',
              key: 'actions',
              width: 240,
              align: 'right',
              render: (_: unknown, row) => (
                <Space size={4}>
                  {!row.container && (
                    <Tooltip title={t('table.actions.deploy')}>
                      <Button
                        type="text"
                        size="small"
                        loading={deployApp.isPending && deployApp.variables === row.id}
                        icon={<Rocket size={14} />}
                        onClick={() => triggerDeploy(row)}
                      />
                    </Tooltip>
                  )}
                  {/* Lifecycle rules live in appLifecycle() so the
                      list and the detail drawer don't drift. */}
                  {(() => {
                    const lc = appLifecycle(row.container)
                    return (
                      <>
                        {lc.isLive && (
                          <>
                            <Tooltip title={t('table.actions.stop')}>
                              <Button
                                type="text"
                                size="small"
                                loading={
                                  stopApp.isPending && stopApp.variables === row.id
                                }
                                icon={<Square size={14} />}
                                onClick={() =>
                                  runAction(row, 'stopped', () =>
                                    stopApp.mutateAsync(row.id),
                                  )
                                }
                              />
                            </Tooltip>
                            {!lc.isRestarting && (
                              <Tooltip title={t('table.actions.restart')}>
                                <Button
                                  type="text"
                                  size="small"
                                  loading={
                                    restartApp.isPending &&
                                    restartApp.variables === row.id
                                  }
                                  icon={<RefreshCw size={14} />}
                                  onClick={() =>
                                    runAction(row, 'restarted', () =>
                                      restartApp.mutateAsync(row.id),
                                    )
                                  }
                                />
                              </Tooltip>
                            )}
                          </>
                        )}
                        {lc.isStopped && (
                          <Tooltip title={t('table.actions.start')}>
                            <Button
                              type="text"
                              size="small"
                              loading={
                                startApp.isPending && startApp.variables === row.id
                              }
                              icon={<Play size={14} />}
                              onClick={() =>
                                runAction(row, 'started', () =>
                                  startApp.mutateAsync(row.id),
                                )
                              }
                            />
                          </Tooltip>
                        )}
                      </>
                    )
                  })()}
                  {row.container && (
                    <Tooltip title={t('table.actions.redeploy')}>
                      <Button
                        type="text"
                        size="small"
                        loading={deployApp.isPending && deployApp.variables === row.id}
                        icon={<ContainerIcon size={14} />}
                        onClick={() => triggerDeploy(row)}
                      />
                    </Tooltip>
                  )}
                  <Tooltip title={t('table.actions.edit')}>
                    <Button
                      type="text"
                      size="small"
                      icon={<Pencil size={14} />}
                      onClick={() => openEdit(row)}
                    />
                  </Tooltip>
                  <Tooltip title={t('table.actions.delete')}>
                    <Button
                      type="text"
                      size="small"
                      icon={<Trash2 size={14} />}
                      onClick={() => confirmDelete(row)}
                    />
                  </Tooltip>
                </Space>
              ),
              },
            ]}
          />
        </div>
      </main>

      <AppEditorModal
        open={editorOpen}
        editing={editing}
        onClose={() => setEditorOpen(false)}
        onSaved={handleSaved}
      />

      {detailAppId !== null && (
        <AppDetail
          key={`${detailAppId}:${detailInitialTab}`}
          appId={detailAppId}
          initialTab={detailInitialTab}
          onClose={() => {
            setDetailAppId(null)
            setDetailInitialTab('overview')
          }}
          onChanged={() => reload()}
        />
      )}
    </div>
  )
}

function StatusCell({ app }: { app: AppType }) {
  const meta = containerStatusMeta(app.container?.status)
  return (
    <span className="inline-flex items-center gap-1.5">
      <StatusTag {...meta} size="small" />
      {app.container && (
        <span className="mono text-[11px] text-[var(--fg-muted)] max-w-[8rem] truncate">
          {app.container.name}
        </span>
      )}
    </span>
  )
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const { t } = useTranslation('apps')
  return (
    <div className="py-16 text-center">
      <div className="inline-flex items-center justify-center size-12 rounded-full border border-[var(--border)] mb-4">
        <Plus size={20} className="text-[var(--fg-muted)]" />
      </div>
      <p className="text-sm text-[var(--fg)] mb-1">{t('emptyState.title')}</p>
      <p className="text-xs text-[var(--fg-muted)] mb-4">
        {t('emptyState.subtitle')}
      </p>
      <Button type="primary" onClick={onCreate}>
        {t('emptyState.addApp')}
      </Button>
    </div>
  )
}