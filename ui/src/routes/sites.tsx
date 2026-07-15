import { createFileRoute, redirect } from '@tanstack/react-router'
import { Suspense, useState } from 'react'
import {
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Skeleton,
  Table,
  Tag,
  Tooltip,
} from 'antd'
import {
  Pencil,
  Plus,
  RefreshCw,
  ToggleLeft,
  ToggleRight,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ensureAuth, isAuthenticated } from '../lib/auth'
import { isValidDomain } from '../lib/domain-validation'
import {
  useCreateSite,
  useDeleteSite,
  useSuspenseApps,
  useSuspenseCaddyfile,
  useSuspenseSites,
  useSuspenseStatus,
  useToggleSite,
  useUpdateSite,
} from '../lib/hooks'
import type { Site } from '../lib/types'
import { QueryErrorBoundary } from '../components/QueryErrorBoundary'
import { SiteStatusBadge } from '../components/SiteStatusBadge'
import { RouteError } from '../components/RouteError'
import { RouteFallback } from '../components/RouteFallback'

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
  const { message, modal } = App.useApp()
  const { t } = useTranslation('sites')
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<Site | null>(null)
  const [form] = Form.useForm<{
    domain: string;
    upstream: string;
    appId?: number;
    scheme: 'http' | 'https';
  }>()

  const sitesQuery = useSuspenseSites()
  const statusQuery = useSuspenseStatus()
  const appsQuery = useSuspenseApps()
  const createSite = useCreateSite()
  const updateSite = useUpdateSite()
  const deleteSite = useDeleteSite()
  const toggleSite = useToggleSite()

  const sites = sitesQuery.data
  const status = statusQuery.data
  const apps = appsQuery.data

  const fetching = sitesQuery.isFetching || statusQuery.isFetching

  function openCreate() {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ scheme: 'https' })
    setEditorOpen(true)
  }

  function openEdit(s: Site) {
    setEditing(s)
    form.setFieldsValue({
      domain: s.domain,
      upstream: s.upstream,
      appId: s.appId,
      scheme: s.scheme,
    })
    setEditorOpen(true)
  }

  function onSubmit() {
    void form.validateFields().then((values) => {
      const payload: Parameters<typeof createSite.mutate>[0] = {
        domain: values.domain,
        scheme: values.scheme,
      }
      if (values.appId) {
        payload.appId = values.appId
      } else if (values.upstream) {
        payload.upstream = values.upstream
      }
      const onOk = () => {
        setEditorOpen(false)
      }
      const onErr = (err: Error) => {
        message.error(err.message)
      }
      if (editing) {
        updateSite.mutate(
          { id: editing.id, input: payload },
          { onSuccess: onOk, onError: onErr },
        )
      } else {
        createSite.mutate(payload, {
          onSuccess: (created) => {
            onOk()
            message.success(t('toast.added', { domain: created.domain }))
          },
          onError: onErr,
        })
      }
    })
  }

  function onToggle(s: Site) {
    toggleSite.mutate(s.id, {
      onSuccess: (updated) => {
        message.success(
          s.enabled
            ? t('toast.disabled', { domain: updated.domain })
            : t('toast.enabled', { domain: updated.domain }),
        )
      },
      onError: (err) => {
        message.error(err.message)
      },
    })
  }

  function onDelete(s: Site) {
    modal.confirm({
      title: t('delete.title', { domain: s.domain }),
      content: t('delete.content'),
      okText: t('actions.delete', { ns: 'common' }),
      okType: 'danger',
      onOk: () =>
        new Promise<void>((resolve, reject) => {
          deleteSite.mutate(s.id, {
            onSuccess: () => {
              message.success(t('toast.deleted', { domain: s.domain }))
              resolve()
            },
            onError: (err) => {
              message.error(err.message)
              reject(err)
            },
          })
        }),
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
          <Button
            type="primary"
            icon={<Plus size={14} />}
            onClick={openCreate}
          >
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
                  <div className="flex items-center gap-2">
                    <span className="mono text-sm text-[var(--fg-muted)]">
                      {u}
                    </span>
                    {row.appName && (
                      <Tag className="!m-0 text-[10px]">app · {row.appName}</Tag>
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
                      />
                    </Tooltip>
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
                        onClick={() => onDelete(row)}
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

      <Modal
        title={editing ? t('editor.editTitle') : t('editor.newTitle')}
        open={editorOpen}
        onOk={onSubmit}
        onCancel={() => setEditorOpen(false)}
        okText={editing ? t('editor.save') : t('editor.create')}
        cancelText={t('actions.cancel', { ns: 'common' })}
        destroyOnClose
        confirmLoading={createSite.isPending || updateSite.isPending}
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="domain"
            label={t('editor.domain')}
            rules={[
              { required: true, message: t('editor.domainRequired') },
              {
                validator: (_rule, value) => {
                  if (typeof value !== 'string' || !isValidDomain(value)) {
                    return Promise.reject(new Error(t('editor.domainPattern')))
                  }
                  return Promise.resolve()
                },
              },
            ]}
          >
            <Input placeholder={t('editor.domainPlaceholder')} autoFocus />
          </Form.Item>
          <Form.Item name="appId" label={t('editor.app')}>
            <Select
              allowClear
              placeholder={t('editor.appPlaceholder')}
              options={apps.map((a) => ({
                value: a.id,
                label: `${a.name} · ${a.image} :${a.port}`,
              }))}
            />
          </Form.Item>
          <Form.Item
            noStyle
            shouldUpdate={(prev, curr) => prev.appId !== curr.appId}
          >
            {({ getFieldValue }) => (
              <Form.Item
                name="upstream"
                label={t('editor.upstream')}
                rules={
                  getFieldValue('appId')
                    ? []
                    : [
                        { required: true, message: t('editor.upstreamRequired') },
                      ]
                }
                extra={
                  getFieldValue('appId')
                    ? t('editor.upstreamExtraApp')
                    : t('editor.upstreamExtraFree')
                }
              >
                <Input
                  placeholder={
                    getFieldValue('appId')
                      ? t('editor.upstreamPlaceholderApp')
                      : t('editor.upstreamPlaceholderFree')
                  }
                  disabled={!!getFieldValue('appId')}
                />
              </Form.Item>
            )}
          </Form.Item>
          <Form.Item
            name="scheme"
            label={t('editor.scheme')}
            extra={t('editor.schemeExtra')}
          >
            <Select
              options={[
                { value: 'https', label: t('editor.schemeHttps') },
                { value: 'http', label: t('editor.schemeHttp') },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>
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

function CaddyfilePreview() {
  const { t } = useTranslation('sites')
  return (
    <QueryErrorBoundary
      fallback={(err, reset) => (
        <div className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4 flex items-center justify-between gap-3">
          <span className="text-xs text-[var(--danger)] mono">
            {t('caddyfile.failed')}: {(err as Error).message}
          </span>
          <Button
            size="small"
            icon={<RefreshCw size={12} />}
            onClick={reset}
          >
            {t('actions.retry', { ns: 'common' })}
          </Button>
        </div>
      )}
    >
      <Suspense
        fallback={
          <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-4 overflow-auto max-h-96 text-[var(--fg-muted)]">
            <Skeleton active paragraph={{ rows: 4 }} title={false} />
          </pre>
        }
      >
        <CaddyfileContent />
      </Suspense>
    </QueryErrorBoundary>
  )
}

function CaddyfileContent() {
  const { t } = useTranslation('sites')
  const caddyfile = useSuspenseCaddyfile()
  return (
    <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-4 overflow-auto max-h-96 text-[var(--fg-muted)]">
      {caddyfile.data || t('caddyfile.empty')}
    </pre>
  )
}