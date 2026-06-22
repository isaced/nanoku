import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import {
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Table,
  Tag,
  Tooltip,
} from 'antd'
import {
  Pencil,
  Plus,
  Power,
  Trash2,
} from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '../lib/api'
import { clearCredentials, getCredentials } from '../lib/auth'
import type { App as AppType, Site, Status } from '../lib/types'
import { TopNav } from '../components/TopNav'

export const Route = createFileRoute('/sites')({
  beforeLoad: () => {
    if (!getCredentials()) {
      throw redirect({ to: '/login' })
    }
  },
  component: SitesPage,
})

function SitesPage() {
  const navigate = useNavigate()
  const { message, modal } = App.useApp()
  const { t } = useTranslation('sites')
  const [sites, setSites] = useState<Site[]>([])
  const [status, setStatus] = useState<Status | null>(null)
  const [apps, setApps] = useState<AppType[]>([])
  const [loading, setLoading] = useState(true)
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<Site | null>(null)
  const [form] = Form.useForm<{ domain: string; upstream: string; appId?: number }>()

  const reload = useCallback(async () => {
    if (!getCredentials()) {
      navigate({ to: '/login' })
      return
    }
    setLoading(true)
    try {
      const [s, st, a] = await Promise.all([
        api.listSites(),
        api.status(),
        api.listApps().catch(() => []),
      ])
      setSites(s)
      setStatus(st)
      setApps(a)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        clearCredentials()
        navigate({ to: '/login' })
      } else {
        message.error((err as Error).message)
      }
    } finally {
      setLoading(false)
    }
  }, [message, navigate])

  useEffect(() => {
    void reload()
  }, [reload])

  function openCreate() {
    setEditing(null)
    form.resetFields()
    setEditorOpen(true)
  }

  function openEdit(s: Site) {
    setEditing(s)
    form.setFieldsValue({
      domain: s.domain,
      upstream: s.upstream,
      appId: s.appId,
    })
    setEditorOpen(true)
  }

  async function onSubmit() {
    const values = await form.validateFields()
    const payload: Parameters<typeof api.createSite>[0] = {
      domain: values.domain,
    }
    if (values.appId) {
      payload.appId = values.appId
    } else if (values.upstream) {
      payload.upstream = values.upstream
    }
    try {
      if (editing) {
        await api.updateSite(editing.id, payload)
        message.success(t('toast.updated', { domain: values.domain }))
      } else {
        await api.createSite(payload)
        message.success(t('toast.added', { domain: values.domain }))
      }
      setEditorOpen(false)
      void reload()
    } catch (err) {
      message.error((err as Error).message)
    }
  }

  async function onToggle(s: Site) {
    try {
      await api.toggleSite(s.id)
      message.success(
        s.enabled
          ? t('toast.disabled', { domain: s.domain })
          : t('toast.enabled', { domain: s.domain }),
      )
      void reload()
    } catch (err) {
      message.error((err as Error).message)
    }
  }

  function onDelete(s: Site) {
    modal.confirm({
      title: t('delete.title', { domain: s.domain }),
      content: t('delete.content'),
      okText: t('actions.delete', { ns: 'common' }),
      okType: 'danger',
      onOk: async () => {
        try {
          await api.deleteSite(s.id)
          message.success(t('toast.deleted', { domain: s.domain }))
          void reload()
        } catch (err) {
          message.error((err as Error).message)
        }
      },
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
              {sites.length === 0
                ? t('subtitleEmpty')
                : t(sites.length === 1 ? 'subtitleOne' : 'subtitleOther', {
                    count: sites.length,
                    active: status?.enabledSiteCount ?? 0,
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
            loading={loading}
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
                          <Power
                            size={14}
                            className={
                              row.enabled
                                ? 'text-[var(--accent)]'
                                : 'text-[var(--fg-muted)]'
                            }
                          />
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

        {status && (
          <div className="mt-8">
            <h2 className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-3">
              {t('generatedCaddyfile')}
            </h2>
            <CaddyfilePreview />
          </div>
        )}
      </main>

      <Modal
        title={editing ? t('editor.editTitle') : t('editor.newTitle')}
        open={editorOpen}
        onOk={onSubmit}
        onCancel={() => setEditorOpen(false)}
        okText={editing ? t('editor.save') : t('editor.create')}
        cancelText={t('actions.cancel', { ns: 'common' })}
        destroyOnClose
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="domain"
            label={t('editor.domain')}
            rules={[
              { required: true, message: t('editor.domainRequired') },
              {
                pattern: /^[a-z0-9.-]+\.[a-z]{2,}$/i,
                message: t('editor.domainPattern'),
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
  const [content, setContent] = useState<string>('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancel = false
    setLoading(true)
    api
      .caddyfile()
      .then((c) => {
        if (!cancel) setContent(c)
      })
      .catch(() => {
        if (!cancel) setContent(t('caddyfile.failed'))
      })
      .finally(() => {
        if (!cancel) setLoading(false)
      })
    return () => {
      cancel = true
    }
  }, [t])

  return (
    <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-4 overflow-auto max-h-96 text-[var(--fg-muted)]">
      {loading ? t('caddyfile.loading') : content || t('caddyfile.empty')}
    </pre>
  )
}
