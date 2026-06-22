import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import {
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd'
import {
  CircleCheck,
  CircleDashed,
  CircleX,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  Trash2,
} from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { clearCredentials, getCredentials } from '../lib/auth'
import type { App as AppType, Site, Status } from '../lib/types'

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
        message.success(`Updated ${values.domain}`)
      } else {
        await api.createSite(payload)
        message.success(`Added ${values.domain}`)
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
        s.enabled ? `Disabled ${s.domain}` : `Enabled ${s.domain}`,
      )
      void reload()
    } catch (err) {
      message.error((err as Error).message)
    }
  }

  function onDelete(s: Site) {
    modal.confirm({
      title: `Delete ${s.domain}?`,
      content: 'The Caddyfile will be regenerated and Caddy reloaded.',
      okText: 'Delete',
      okType: 'danger',
      onOk: async () => {
        try {
          await api.deleteSite(s.id)
          message.success(`Deleted ${s.domain}`)
          void reload()
        } catch (err) {
          message.error((err as Error).message)
        }
      },
    })
  }

  return (
    <div className="flex-1 flex flex-col">
      <Header status={status} onRefresh={reload} loading={loading} />

      <main className="flex-1 px-8 py-8 max-w-6xl w-full mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-medium tracking-tight">Sites</h1>
            <p className="text-sm text-[var(--fg-muted)] mt-1">
              {sites.length === 0
                ? 'No sites configured yet.'
                : `${sites.length} ${sites.length === 1 ? 'site' : 'sites'} · ${status?.enabledSiteCount ?? 0} active`}
            </p>
          </div>
          <Button
            type="primary"
            icon={<Plus size={14} />}
            onClick={openCreate}
          >
            New site
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
                title: 'Domain',
                dataIndex: 'domain',
                render: (d: string, row) => (
                  <div className="flex items-center gap-3">
                    <span className="mono text-sm">{d}</span>
                    {!row.enabled && (
                      <Tag className="!m-0">disabled</Tag>
                    )}
                  </div>
                ),
              },
              {
                title: 'Upstream',
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
                    <Tooltip title={row.enabled ? 'Disable' : 'Enable'}>
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
                    <Tooltip title="Edit">
                      <Button
                        type="text"
                        size="small"
                        icon={<Pencil size={14} />}
                        onClick={() => openEdit(row)}
                      />
                    </Tooltip>
                    <Tooltip title="Delete">
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
              Generated Caddyfile
            </h2>
            <CaddyfilePreview />
          </div>
        )}
      </main>

      <Modal
        title={editing ? 'Edit site' : 'New site'}
        open={editorOpen}
        onOk={onSubmit}
        onCancel={() => setEditorOpen(false)}
        okText={editing ? 'Save' : 'Create'}
        destroyOnClose
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="domain"
            label="Domain"
            rules={[
              { required: true, message: 'Domain is required' },
              {
                pattern: /^[a-z0-9.-]+\.[a-z]{2,}$/i,
                message: 'Must look like a domain (e.g. app.example.com)',
              },
            ]}
          >
            <Input placeholder="app.example.com" autoFocus />
          </Form.Item>
          <Form.Item name="appId" label="App (optional)">
            <Select
              allowClear
              placeholder="Link to a deployed app"
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
                label="Upstream"
                rules={
                  getFieldValue('appId')
                    ? []
                    : [
                        { required: true, message: 'Upstream is required' },
                      ]
                }
                extra={
                  getFieldValue('appId')
                    ? 'Upstream auto-resolved from the app container.'
                    : 'host:port, URL, or docker container name'
                }
              >
                <Input
                  placeholder={
                    getFieldValue('appId')
                      ? '(auto-resolved)'
                      : 'localhost:3000'
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

function Header({
  status,
  onRefresh,
  loading,
}: {
  status: Status | null
  onRefresh: () => void
  loading: boolean
}) {
  return (
    <header className="border-b border-[var(--border)] bg-[var(--bg-elevated)]">
      <div className="px-8 h-14 flex items-center justify-between max-w-6xl mx-auto w-full">
        <div className="flex items-center gap-6">
          <div className="flex items-center gap-2">
            <div className="size-2 rounded-full bg-[var(--accent)]" />
            <span className="mono text-sm tracking-wider">nanoku</span>
          </div>
          <nav className="flex items-center gap-4 text-xs">
            <a
              href="/sites"
              className="text-[var(--fg)]"
            >
              Sites
            </a>
            <a
              href="/apps"
              className="text-[var(--fg-muted)] hover:text-[var(--fg)]"
            >
              Apps
            </a>
            <a
              href="/system"
              className="text-[var(--fg-muted)] hover:text-[var(--fg)]"
            >
              System
            </a>
          </nav>
        </div>
        <div className="flex items-center gap-3 text-xs">
          <DockerBadge status={status} />
          <CaddyBadge status={status} />
          <Tooltip title="Refresh">
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
            />
          </Tooltip>
        </div>
      </div>
    </header>
  )
}

function DockerBadge({ status }: { status: Status | null }) {
  if (!status) return null
  if (status.dockerConnected) {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--success)]">
        <CircleCheck size={12} />
        <span className="mono">docker</span>
      </span>
    )
  }
  return (
    <Tooltip title="Docker daemon unreachable">
      <span className="inline-flex items-center gap-1.5 text-[var(--danger)]">
        <CircleX size={12} />
        <span className="mono">docker</span>
      </span>
    </Tooltip>
  )
}

function CaddyBadge({ status }: { status: Status | null }) {
  if (!status) return null
  const s = status.caddyStatus
  if (s === 'running') {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--success)]">
        <CircleCheck size={12} />
        <span className="mono">caddy</span>
      </span>
    )
  }
  if (s === 'not_found' || s === 'skipped') {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--fg-muted)]">
        <CircleDashed size={12} />
        <span className="mono">caddy · {s}</span>
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-[var(--danger)]">
      <CircleX size={12} />
      <span className="mono">caddy · {s}</span>
    </span>
  )
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="py-16 text-center">
      <div className="inline-flex items-center justify-center size-12 rounded-full border border-[var(--border)] mb-4">
        <Plus size={20} className="text-[var(--fg-muted)]" />
      </div>
      <p className="text-sm text-[var(--fg)] mb-1">No sites yet</p>
      <p className="text-xs text-[var(--fg-muted)] mb-4">
        Add your first domain to start routing traffic through Caddy.
      </p>
      <Button type="primary" onClick={onCreate}>
        Add site
      </Button>
    </div>
  )
}

function CaddyfilePreview() {
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
        if (!cancel) setContent('// failed to load')
      })
      .finally(() => {
        if (!cancel) setLoading(false)
      })
    return () => {
      cancel = true
    }
  }, [])

  return (
    <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-4 overflow-auto max-h-96 text-[var(--fg-muted)]">
      {loading ? 'loading…' : content || '# empty'}
    </pre>
  )
}