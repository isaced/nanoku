import { createFileRoute, redirect } from '@tanstack/react-router'
import { Suspense, useState } from 'react'
import {
  App,
  Button,
  Table,
  Tag,
  Tooltip,
} from 'antd'
import {
  Pencil,
  Plus,
  ToggleLeft,
  ToggleRight,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ensureAuth, isAuthenticated } from '../lib/auth'
import {
  useAction,
  useCreateSite,
  useDeleteSite,
  useSuspenseApps,
  useSuspenseSites,
  useSuspenseStatus,
  useToggleSite,
  useUpdateSite,
} from '../lib/hooks'
import type { Site } from '../lib/types'
import { CaddyfilePreview } from '../components/CaddyfilePreview'
import { RouteError } from '../components/RouteError'
import { RouteFallback } from '../components/RouteFallback'
import {
  SiteEditorModal,
  type SiteEditorSubmitPayload,
} from '../components/SiteEditorModal'
import { SiteStatusBadge } from '../components/SiteStatusBadge'

export const Route = createFileRoute('/sites')({
  beforeLoad: async () => {
    if (isAuthenticated()) return
    if (!(await ensureAuth())) {
      throw redirect({ to: '/login' })
    }
  },
  component: SitesPage,
  errorComponent: RouteError,
})

function SitesPage() {
  return (
    <Suspense fallback={<RouteFallback variant="page" />}>
      <SitesPageContent />
    </Suspense>
  )
}

function SitesPageContent() {
  const { modal } = App.useApp()
  const { t } = useTranslation('sites')
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<Site | null>(null)

  const sitesQuery = useSuspenseSites()
  const statusQuery = useSuspenseStatus()
  const appsQuery = useSuspenseApps()
  const createSite = useCreateSite()
  const updateSite = useUpdateSite()
  const deleteSite = useDeleteSite()
  const toggleSite = useToggleSite()
  const action = useAction()

  const sites = sitesQuery.data
  const status = statusQuery.data
  const apps = appsQuery.data

  const fetching = sitesQuery.isFetching || statusQuery.isFetching

  function openCreate() {
    setEditing(null)
    setEditorOpen(true)
  }

  function openEdit(s: Site) {
    setEditing(s)
    setEditorOpen(true)
  }

  function onSubmit(payload: SiteEditorSubmitPayload) {
    if (editing) {
      void action.run(
        updateSite.mutateAsync({ id: editing.id, input: payload }),
        { onSuccess: () => setEditorOpen(false) },
      )
    } else {
      void action.run(createSite.mutateAsync(payload), {
        success: t('toast.added', { domain: 'P' }),
        successVars: (created) => ({ domain: created.domain }),
        onSuccess: () => setEditorOpen(false),
      })
    }
  }

  function onToggle(s: Site) {
    void action.run(toggleSite.mutateAsync(s.id), {
      success: s.enabled ? t('toast.disabled', { domain: 'P' }) : t('toast.enabled', { domain: 'P' }),
      successVars: (updated) => ({ domain: updated.domain }),
    })
  }

  function onDelete(s: Site) {
    modal.confirm({
      title: t('delete.title', { domain: s.domain }),
      content: t('delete.content'),
      okText: t('actions.delete', { ns: 'common' }),
      okType: 'danger',
      onOk: () =>
        action.run(deleteSite.mutateAsync(s.id), {
          success: t('toast.deleted', { domain: s.domain }),
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
              {sites.length === 0
                ? t('subtitleEmpty')
                : t(sites.length === 1 ? 'subtitleOne' : 'subtitleOther', {
                    count: sites.length,
                    active: status.enabledSiteCount,
                  })}
            </p>
          </div>
          <Button type="primary" icon={<Plus size={14} />} onClick={openCreate}>
            {t('newSite')}
          </Button>
        </div>

        <div className="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--bg-elevated)]">
          <Table<Site>
            dataSource={sites}
            rowKey="id"
            loading={fetching}
            pagination={false}
            locale={{ emptyText: <EmptyState onCreate={openCreate} /> }}
            columns={[
              {
                title: t('table.domain'),
                dataIndex: 'domain',
                render: (d: string, row) => (
                  <div className="flex items-center gap-3">
                    <span className="mono text-sm">{d}</span>
                    {!row.enabled && (
                      <Tag className="!m-0">{t('common:status.disabled', { ns: 'common' })}</Tag>
                    )}
                  </div>
                ),
              },
              {
                title: t('table.upstream'),
                dataIndex: 'upstream',
                render: (u: string, row) => (
                  <div className="flex flex-col gap-0.5">
                    <span className="mono text-sm text-[var(--fg-muted)]">
                      {u}
                    </span>
                    {row.appName && (
                      <span className="text-[11px] text-[var(--fg-muted)]">
                        {row.appService
                          ? t('table.upstreamFromService', {
                              app: row.appName,
                              service: row.appService,
                            })
                          : t('table.upstreamFromApp', { app: row.appName })}
                      </span>
                    )}
                  </div>
                ),
              },
              {
                title: t('table.scheme'),
                dataIndex: 'scheme',
                width: 90,
                render: (s: Site['scheme']) => (
                  <Tag
                    className={`!m-0 ${
                      s === 'http' ? '!bg-amber-500/10 !text-amber-600' : ''
                    }`}
                  >
                    {s.toUpperCase()}
                  </Tag>
                ),
              },
              {
                title: t('table.status'),
                dataIndex: 'enabled',
                width: 60,
                render: (e: boolean) => <SiteStatusBadge enabled={e} />,
              },
              {
                title: '',
                key: 'actions',
                width: 140,
                align: 'right',
                render: (_: unknown, row) => (
                  <div className="flex items-center justify-end gap-1">
                    <Tooltip
                      title={
                        row.enabled
                          ? t('table.actions.disable')
                          : t('table.actions.enable')
                      }
                    >
                      <Button
                        type="text"
                        size="small"
                        icon={
                          <span className={row.enabled ? 'text-[var(--accent)]' : 'text-[var(--fg-muted)]'}>
                            {row.enabled ? <ToggleRight size={14} /> : <ToggleLeft size={14} />}
                          </span>
                        }
                        onClick={() => onToggle(row)}
                        aria-label={
                          row.enabled
                            ? t('table.actions.disable')
                            : t('table.actions.enable')
                        }
                      />
                    </Tooltip>
                    <Tooltip title={t('table.actions.edit')}>
                      <Button
                        type="text"
                        size="small"
                        icon={<Pencil size={14} />}
                        onClick={() => openEdit(row)}
                        aria-label={t('table.actions.edit')}
                      />
                    </Tooltip>
                    <Tooltip title={t('table.actions.delete')}>
                      <Button
                        type="text"
                        size="small"
                        icon={<Trash2 size={14} />}
                        onClick={() => onDelete(row)}
                        aria-label={t('table.actions.delete')}
                      />
                    </Tooltip>
                  </div>
                ),
              },
            ]}
          />
        </div>

        <div className="mt-8">
          <h2 className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-3">
            {t('generatedCaddyfile')}
          </h2>
          <CaddyfilePreview />
        </div>
      </main>

      <SiteEditorModal
        open={editorOpen}
        editing={editing}
        apps={apps}
        isPending={createSite.isPending || updateSite.isPending}
        onClose={() => setEditorOpen(false)}
        onSubmit={onSubmit}
      />
    </div>
  )
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const { t } = useTranslation('sites')
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
        {t('emptyState.addSite')}
      </Button>
    </div>
  )
}
