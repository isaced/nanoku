import { createFileRoute, redirect } from '@tanstack/react-router'
import { App, Button, Space, Table, Tag, Tooltip } from 'antd'
import {
  CircleCheck,
  CircleDashed,
  CircleX,
  Container as ContainerIcon,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Rocket,
  Square,
  Trash2,
} from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '../lib/api'
import { ensureAuth, isAuthenticated } from '../lib/auth'
import type { App as AppType, Status } from '../lib/types'
import { TopNav } from '../components/TopNav'
import { AppDetail } from '../components/AppDetailDrawer'
import { AppEditorModal, type AppEditorSaveResult } from '../components/AppEditorModal'
import { TriggerTokenModal } from '../components/TriggerTokenModal'

export const Route = createFileRoute('/apps')({
  beforeLoad: async () => {
    if (isAuthenticated()) return
    if (!(await ensureAuth())) {
      throw redirect({ to: '/login' })
    }
  },
  component: AppsPage,
})

function AppsPage() {
  const { message, modal } = App.useApp()
  const { t } = useTranslation('apps')
  const [apps, setApps] = useState<AppType[]>([])
  const [status, setStatus] = useState<Status | null>(null)
  const [loading, setLoading] = useState(true)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<AppType | null>(null)
  const [detailAppId, setDetailAppId] = useState<number | null>(null)
  const [revealedToken, setRevealedToken] = useState<{ url: string; token: string; appName: string } | null>(null)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      const [a, st] = await Promise.all([api.listApps(), api.status()])
      setApps(a)
      setStatus(st)
    } catch (err) {
      if (!(err instanceof ApiError && err.status === 401)) {
        message.error((err as Error).message)
      }
    } finally {
      setLoading(false)
    }
  }, [message])

  useEffect(() => {
    void reload()
  }, [reload])

  function openCreate() {
    setEditing(null)
    setEditorOpen(true)
  }

  function openEdit(a: AppType) {
    setEditing(a)
    setEditorOpen(true)
  }

  function handleSaved({ saved, previous, isNew }: AppEditorSaveResult) {
    message.success(t(isNew ? 'toast.added' : 'toast.updated', { name: saved.name }))
    setEditorOpen(false)
    void reload()
    if (isNew) {
      if (saved.triggerToken) {
        setRevealedToken({
          url: `${window.location.origin}/api/apps/${saved.name}/trigger`,
          token: saved.triggerToken,
          appName: saved.name,
        })
      }
      return
    }
    const appForRedeploy: AppType = { ...previous!, ...saved }
    modal.confirm({
      title: t('redeployPrompt.title', { name: appForRedeploy.name }),
      content: t('redeployPrompt.content'),
      okText: t('redeployPrompt.ok'),
      cancelText: t('redeployPrompt.cancel'),
      onOk: () => runAction(appForRedeploy, 'redeployed', () => api.deployApp(appForRedeploy.id)),
    })
  }

  async function rotateToken(a: AppType) {
    try {
      const updated = await api.rotateTriggerToken(a.id)
      if (updated.triggerToken) {
        setRevealedToken({
          url: `${window.location.origin}/api/apps/${a.name}/trigger`,
          token: updated.triggerToken,
          appName: a.name,
        })
      }
    } catch (err) {
      message.error((err as Error).message)
    }
  }

  async function runAction(app: AppType, name: string, fn: () => Promise<unknown>) {
    setBusyId(app.id)
    try {
      await fn()
      message.success(t('toast.' + name, { name: app.name }))
      void reload()
    } catch (err) {
      message.error((err as Error).message)
    } finally {
      setBusyId(null)
    }
  }

  function confirmDelete(app: AppType) {
    modal.confirm({
      title: t('delete.title', { name: app.name }),
      content: t('delete.content'),
      okText: t('actions.delete', { ns: 'common' }),
      okType: 'danger',
      onOk: () => runAction(app, 'deleted', () => api.deleteApp(app.id)),
    })
  }

  return (
    <div className="flex-1 flex flex-col">
      <TopNav status={status} onRefresh={reload} loading={loading} />

      <main className="flex-1 px-8 py-8 max-w-6xl w-full mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-medium tracking-tight">{t('title')}</h1>
            <p className="text-sm text-[var(--fg-muted)] mt-1">
              {apps.length === 0
                ? t('subtitleEmpty')
                : t(apps.length === 1 ? 'subtitleOne' : 'subtitleOther', {
                    count: apps.length,
                    running: status?.runningAppCount ?? 0,
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

        <div className="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--bg-elevated)]">
          <Table<AppType>
            dataSource={apps}
            rowKey="id"
            loading={loading}
            pagination={false}
            locale={{ emptyText: <EmptyState onCreate={openCreate} /> }}
            columns={[
              {
                title: t('table.name'),
                dataIndex: 'name',
                render: (n: string, row) => (
                  <button
                    type="button"
                    className="text-left mono text-sm text-[var(--accent)] hover:underline"
                    onClick={() => setDetailAppId(row.id)}
                  >
                    {n}
                  </button>
                ),
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
                title: t('table.image'),
                dataIndex: 'image',
                render: (i: string, row) => (
                  <span className="mono text-xs text-[var(--fg-muted)]">
                    {i || (row.deployMethod === 'compose' ? t('table.imageFromCompose') : '—')}
                  </span>
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
                width: 200,
                render: (_: unknown, row) => <StatusCell app={row} />,
              },
              {
                title: '',
                key: 'actions',
                width: 280,
                align: 'right',
                render: (_: unknown, row) => (
                  <Space size={4}>
                    {!row.container && (
                      <Tooltip title={t('table.actions.deploy')}>
                        <Button
                          type="text"
                          size="small"
                          loading={busyId === row.id}
                          icon={<Rocket size={14} />}
                          onClick={() =>
                            runAction(row, 'deployed', () => api.deployApp(row.id))
                          }
                        />
                      </Tooltip>
                    )}
                    {row.container?.status === 'running' && (
                      <>
                        <Tooltip title={t('table.actions.stop')}>
                          <Button
                            type="text"
                            size="small"
                            loading={busyId === row.id}
                            icon={<Square size={14} />}
                            onClick={() =>
                              runAction(row, 'stopped', () => api.stopApp(row.id))
                            }
                          />
                        </Tooltip>
                        <Tooltip title={t('table.actions.restart')}>
                          <Button
                            type="text"
                            size="small"
                            loading={busyId === row.id}
                            icon={<RefreshCw size={14} />}
                            onClick={() =>
                              runAction(row, 'restarted', () =>
                                api.restartApp(row.id),
                              )
                            }
                          />
                        </Tooltip>
                      </>
                    )}
                    {row.container && row.container.status !== 'running' && (
                      <Tooltip title={t('table.actions.start')}>
                        <Button
                          type="text"
                          size="small"
                          loading={busyId === row.id}
                          icon={<Play size={14} />}
                          onClick={() =>
                            runAction(row, 'started', () => api.startApp(row.id))
                          }
                        />
                      </Tooltip>
                    )}
                    {row.container && (
                      <Tooltip title={t('table.actions.redeploy')}>
                        <Button
                          type="text"
                          size="small"
                          loading={busyId === row.id}
                          icon={<ContainerIcon size={14} />}
                          onClick={() =>
                            runAction(row, 'redeployed', () =>
                              api.deployApp(row.id),
                            )
                          }
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
        onRotate={() => editing && rotateToken(editing)}
      />

      {revealedToken && (
        <TriggerTokenModal
          appName={revealedToken.appName}
          url={revealedToken.url}
          token={revealedToken.token}
          onClose={() => setRevealedToken(null)}
        />
      )}

      {detailAppId !== null && (
        <AppDetail
          appId={detailAppId}
          onClose={() => setDetailAppId(null)}
          onChanged={reload}
          onEditRequested={(a) => {
            setDetailAppId(null)
            void openEdit(a)
          }}
        />
      )}
    </div>
  )
}

function StatusCell({ app }: { app: AppType }) {
  const { t } = useTranslation('common')
  const translate = (s: string) =>
    s === 'exited'
      ? t('status.exited')
      : s === 'created'
        ? t('status.created')
        : s === 'not deployed'
          ? t('status.notDeployed')
          : s
  if (!app.container) {
    return (
      <Tag className="!m-0">
        <span className="inline-flex items-center gap-1">
          <CircleDashed size={10} /> {t('status.notDeployed')}
        </span>
      </Tag>
    )
  }
  const s = app.container.status
  if (s === 'running') {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--success)]">
        <CircleCheck size={12} />
        <span className="mono text-xs">{app.container.name}</span>
      </span>
    )
  }
  if (s === 'exited' || s === 'created') {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--fg-muted)]">
        <CircleDashed size={12} />
        <span className="mono text-xs">{translate(s)} · {app.container.name}</span>
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-[var(--danger)]">
      <CircleX size={12} />
      <span className="mono text-xs">{s} · {app.container.name}</span>
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
