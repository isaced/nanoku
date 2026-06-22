import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import {
  App,
  Alert,
  Button,
  Checkbox,
  Collapse,
  Divider,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Radio,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd'
import {
  Bell,
  CircleCheck,
  CircleDashed,
  CircleX,
  Container as ContainerIcon,
  Copy,
  Key,
  Pencil,
  Play,
  Plus,
  Power,
  RefreshCw,
  Rocket,
  Square,
  Trash2,
} from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { clearCredentials, getCredentials } from '../lib/auth'
import type { App as AppType, Container, Deploy, EnvVar, Status } from '../lib/types'
import { TopNav } from '../components/TopNav'

export const Route = createFileRoute('/apps')({
  beforeLoad: () => {
    if (!getCredentials()) {
      throw redirect({ to: '/login' })
    }
  },
  component: AppsPage,
})

function AppsPage() {
  const navigate = useNavigate()
  const { message, modal } = App.useApp()
  const [apps, setApps] = useState<AppType[]>([])
  const [status, setStatus] = useState<Status | null>(null)
  const [loading, setLoading] = useState(true)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<AppType | null>(null)
  const [detailAppId, setDetailAppId] = useState<number | null>(null)
  const [revealedSecret, setRevealedSecret] = useState<{ url: string; secret: string; appName: string } | null>(null)
  const [form] = Form.useForm<AppInput>()

  const reload = useCallback(async () => {
    if (!getCredentials()) {
      navigate({ to: '/login' })
      return
    }
    setLoading(true)
    try {
      const [a, st] = await Promise.all([api.listApps(), api.status()])
      setApps(a)
      setStatus(st)
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
    form.setFieldsValue({ branch: 'main', port: 80, deployMethod: 'docker' })
    setEditorOpen(true)
  }

  function openEdit(a: AppType) {
    setEditing(a)
    form.setFieldsValue({
      name: a.name,
      image: a.image,
      port: a.port,
      repoUrl: a.repoUrl ?? '',
      branch: a.branch,
      deployMethod: a.deployMethod ?? 'docker',
      composePath: a.composePath ?? '',
      composeContent: a.composeContent ?? '',
      registryUrl: a.registryUrl ?? '',
      registryUsername: a.registryUsername ?? '',
      imageRepo: a.imageRepo ?? '',
    })
    setEditorOpen(true)
  }

  async function onSubmit() {
    const values = await form.validateFields()
    try {
      if (editing) {
        await api.updateApp(editing.id, values)
        message.success(`Updated ${values.name}`)
      } else {
        const created = await api.createApp(values)
        message.success(`Added ${values.name}`)
        if (created.webhookSecret && created.imageRepo) {
          setRevealedSecret({
            url: `${window.location.origin}/api/webhook/${created.name}`,
            secret: created.webhookSecret,
            appName: created.name,
          })
        }
      }
      setEditorOpen(false)
      void reload()
    } catch (err) {
      message.error((err as Error).message)
    }
  }

  async function rotateSecret(a: AppType) {
    try {
      const updated = await api.rotateWebhookSecret(a.id)
      if (updated.webhookSecret) {
        setRevealedSecret({
          url: `${window.location.origin}/api/webhook/${a.name}`,
          secret: updated.webhookSecret,
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
      message.success(`${name} ${app.name}`)
      void reload()
    } catch (err) {
      message.error((err as Error).message)
    } finally {
      setBusyId(null)
    }
  }

  function confirmDelete(app: AppType) {
    modal.confirm({
      title: `Delete ${app.name}?`,
      content: 'The app and its current Docker container will be removed.',
      okText: 'Delete',
      okType: 'danger',
      onOk: () => runAction(app, 'Deleted', () => api.deleteApp(app.id)),
    })
  }

  return (
    <div className="flex-1 flex flex-col">
      <TopNav status={status} onRefresh={reload} loading={loading} />

      <main className="flex-1 px-8 py-8 max-w-6xl w-full mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-medium tracking-tight">Apps</h1>
            <p className="text-sm text-[var(--fg-muted)] mt-1">
              {apps.length === 0
                ? 'No apps configured yet.'
                : `${apps.length} ${apps.length === 1 ? 'app' : 'apps'} · ${status?.runningAppCount ?? 0} running`}
            </p>
          </div>
          <Button
            type="primary"
            icon={<Plus size={14} />}
            onClick={openCreate}
          >
            New app
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
                title: 'Name',
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
                title: 'Method',
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
                title: 'Image',
                dataIndex: 'image',
                render: (i: string, row) => (
                  <span className="mono text-xs text-[var(--fg-muted)]">
                    {i || (row.deployMethod === 'compose' ? '(from compose)' : '—')}
                  </span>
                ),
              },
              {
                title: 'Port',
                dataIndex: 'port',
                width: 80,
                align: 'right',
                render: (p: number) => (
                  <span className="mono text-sm text-[var(--fg-muted)]">{p}</span>
                ),
              },
              {
                title: 'Status',
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
                      <Tooltip title="Deploy">
                        <Button
                          type="text"
                          size="small"
                          loading={busyId === row.id}
                          icon={<Rocket size={14} />}
                          onClick={() =>
                            runAction(row, 'Deployed', () => api.deployApp(row.id))
                          }
                        />
                      </Tooltip>
                    )}
                    {row.container?.status === 'running' && (
                      <>
                        <Tooltip title="Stop">
                          <Button
                            type="text"
                            size="small"
                            loading={busyId === row.id}
                            icon={<Square size={14} />}
                            onClick={() =>
                              runAction(row, 'Stopped', () => api.stopApp(row.id))
                            }
                          />
                        </Tooltip>
                        <Tooltip title="Restart">
                          <Button
                            type="text"
                            size="small"
                            loading={busyId === row.id}
                            icon={<RefreshCw size={14} />}
                            onClick={() =>
                              runAction(row, 'Restarted', () =>
                                api.restartApp(row.id),
                              )
                            }
                          />
                        </Tooltip>
                      </>
                    )}
                    {row.container && row.container.status !== 'running' && (
                      <Tooltip title="Start">
                        <Button
                          type="text"
                          size="small"
                          loading={busyId === row.id}
                          icon={<Play size={14} />}
                          onClick={() =>
                            runAction(row, 'Started', () => api.startApp(row.id))
                          }
                        />
                      </Tooltip>
                    )}
                    {row.container && (
                      <Tooltip title="Redeploy">
                        <Button
                          type="text"
                          size="small"
                          loading={busyId === row.id}
                          icon={<ContainerIcon size={14} />}
                          onClick={() =>
                            runAction(row, 'Redeployed', () =>
                              api.deployApp(row.id),
                            )
                          }
                        />
                      </Tooltip>
                    )}
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

      <Modal
        title={editing ? 'Edit app' : 'New app'}
        open={editorOpen}
        onOk={onSubmit}
        onCancel={() => setEditorOpen(false)}
        okText={editing ? 'Save' : 'Create'}
        destroyOnClose
        width={680}
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="name"
            label="Name"
            rules={[
              { required: true, message: 'Name is required' },
              {
                pattern: /^[a-z][a-z0-9-]{0,62}$/,
                message:
                  'Must start with a-z and contain only lowercase letters, digits, dashes',
              },
            ]}
            extra="Used as Docker container / compose project prefix."
          >
            <Input placeholder="myapp" autoFocus />
          </Form.Item>

          <Form.Item
            name="deployMethod"
            label="Deploy method"
            rules={[{ required: true }]}
            initialValue="docker"
          >
            <DeployMethodSwitch />
          </Form.Item>

          <Form.Item
            noStyle
            shouldUpdate={(prev, curr) => prev.deployMethod !== curr.deployMethod}
          >
            {({ getFieldValue }) =>
              getFieldValue('deployMethod') === 'compose' ? (
                <>
                  <Form.Item
                    name="composePath"
                    label="Compose file path on host (optional)"
                    extra="If set, overrides the inline content below. Use an absolute path the nanoku process can read."
                  >
                    <Input placeholder="/opt/myapp/docker-compose.yml" />
                  </Form.Item>
                  <Form.Item
                    name="composeContent"
                    label="Compose YAML"
                    rules={[
                      {
                        validator: (_, value) => {
                          const pathVal = form.getFieldValue('composePath')
                          if (pathVal && String(pathVal).trim()) return Promise.resolve()
                          if (!value || !String(value).trim()) {
                            return Promise.reject(
                              new Error('Provide a compose file path or inline YAML'),
                            )
                          }
                          return Promise.resolve()
                        },
                      },
                    ]}
                    extra="Multi-service stacks are supported. Container names will be prefixed with the app name."
                  >
                    <Input.TextArea
                      rows={10}
                      placeholder={'services:\n  web:\n    image: nginx:1.27\n    ports: ["8080:80"]'}
                      className="mono text-xs"
                    />
                  </Form.Item>
                </>
              ) : (
                <>
                  <Form.Item
                    name="image"
                    label="Image"
                    rules={[{ required: true, message: 'Image is required' }]}
                    extra="Docker image, e.g. nginx:1.27"
                  >
                    <Input placeholder="nginxdemos/hello:plain-text" />
                  </Form.Item>
                  <Form.Item
                    name="port"
                    label="Internal port"
                    rules={[
                      { required: true, message: 'Port is required' },
                      { type: 'number', min: 1, max: 65535 },
                    ]}
                    extra="Port the app listens on inside the container."
                  >
                    <InputNumber min={1} max={65535} className="w-full" />
                  </Form.Item>
                </>
              )
            }
          </Form.Item>

          <RegistrySection editing={editing} />

          <Form.Item name="branch" label="Branch" initialValue="main">
            <Input placeholder="main" />
          </Form.Item>
          <Form.Item name="repoUrl" label="Repo URL (optional)">
            <Input placeholder="https://github.com/you/repo" />
          </Form.Item>

          <WebhookSection
            editing={editing}
            onRotate={() => editing && rotateSecret(editing)}
          />
        </Form>
      </Modal>

      {revealedSecret && (
        <WebhookSecretModal
          appName={revealedSecret.appName}
          url={revealedSecret.url}
          secret={revealedSecret.secret}
          onClose={() => setRevealedSecret(null)}
        />
      )}

      {detailAppId !== null && (
        <AppDetail
          appId={detailAppId}
          onClose={() => setDetailAppId(null)}
          onChanged={reload}
        />
      )}
    </div>
  )
}

function DeployMethodSwitch({ value, onChange }: { value?: string; onChange?: (v: string) => void }) {
  return (
    <Radio.Group
      value={value}
      onChange={(e) => onChange?.(e.target.value)}
      optionType="button"
      buttonStyle="solid"
    >
      <Radio.Button value="docker">Docker</Radio.Button>
      <Radio.Button value="compose">Docker Compose</Radio.Button>
    </Radio.Group>
  )
}

function RegistrySection({ editing }: { editing: AppType | null }) {
  return (
    <div className="border border-[var(--border)] rounded-lg p-3 mb-2 bg-[var(--bg-input)]/30">
      <div className="flex items-center gap-2 mb-2">
        <Key size={13} className="text-[var(--fg-muted)]" />
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          Private registry (optional)
        </span>
        {editing?.registryConfigured && (
          <Tag color="blue" className="!m-0 ml-auto">
            configured
          </Tag>
        )}
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Form.Item
          name="registryUrl"
          label="Registry URL"
          extra="Empty = public registry"
          className="!mb-2"
        >
          <Input placeholder="ghcr.io" />
        </Form.Item>
        <Form.Item
          name="registryUsername"
          label="Username"
          className="!mb-2"
        >
          <Input placeholder="user" />
        </Form.Item>
      </div>
      <Form.Item
        name="registryPassword"
        label={editing?.registryConfigured ? 'New password (leave empty to keep)' : 'Password'}
        extra={
          editing?.registryConfigured
            ? 'Stored encrypted at rest is on the roadmap; currently stored in the SQLite DB.'
            : undefined
        }
        className="!mb-0"
      >
        <Input.Password
          placeholder={editing?.registryConfigured ? '•••••• (unchanged)' : 'password / token'}
          autoComplete="off"
        />
      </Form.Item>
      {editing?.registryConfigured && (
        <Form.Item name="clearRegistry" valuePropName="checked" className="!mb-0 mt-2">
          <Checkbox>Clear stored credentials</Checkbox>
        </Form.Item>
      )}
    </div>
  )
}

function WebhookSection({
  editing,
  onRotate,
}: {
  editing: AppType | null
  onRotate: () => void
}) {
  return (
    <div className="border border-[var(--border)] rounded-lg p-3 mb-2 bg-[var(--bg-input)]/30">
      <div className="flex items-center gap-2 mb-2">
        <Bell size={13} className="text-[var(--fg-muted)]" />
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          GitHub webhook deploy (optional)
        </span>
        {editing?.webhookConfigured && (
          <Tag color="green" className="!m-0 ml-auto">
            active
          </Tag>
        )}
      </div>
      <Form.Item
        name="imageRepo"
        label="Image repository"
        extra={
          <span>
            OCI image repository without tag, e.g. <code>ghcr.io/you/myapp</code>.
            On webhook fire, nanoku pulls <code>{'<repo>:<payload-tag>'}</code>.
            Build runs in your GitHub Actions — nanoku only pulls the image.
          </span>
        }
        className="!mb-2"
      >
        <Input placeholder="ghcr.io/you/myapp" />
      </Form.Item>
      {editing?.webhookConfigured && (
        <div className="flex items-center justify-between mt-2">
          <span className="text-xs text-[var(--fg-muted)] mono">
            POST {window.location.origin}/api/webhook/{editing.name}
          </span>
          <Popconfirm
            title="Rotate webhook secret?"
            description="A new random secret will be generated. You'll see it once. Old secret stops working immediately."
            okText="Rotate"
            onConfirm={onRotate}
          >
            <Button size="small" icon={<RefreshCw size={12} />}>
              Rotate secret
            </Button>
          </Popconfirm>
        </div>
      )}
    </div>
  )
}

function CopyableValue({ value, label }: { value: string; label: string }) {
  return (
    <div className="flex items-center gap-2">
      <Input
        readOnly
        value={value}
        aria-label={label}
        className="mono text-xs"
      />
      <Button
        icon={<Copy size={13} />}
        onClick={() => {
          void navigator.clipboard.writeText(value)
        }}
      >
        Copy
      </Button>
    </div>
  )
}

function githubActionsWorkflowYaml(appName: string, url: string, secret: string) {
  // GitHub Actions uses ${{ ... }} for expressions. In JS template literals
  // we must escape every literal `$` so it isn't treated as interpolation.
  // `${{` in the final string should read literally — hence `\${{` here.
  const $ = '$'
  return [
    'name: deploy',
    'on:',
    '  push:',
    '    branches: [main]',
    '',
    'jobs:',
    '  build:',
    '    runs-on: ubuntu-latest',
    '    permissions:',
    '      contents: read',
    '      packages: write',
    '    steps:',
    '      - uses: actions/checkout@v4',
    '      - uses: docker/setup-buildx-action@v3',
    '      - uses: docker/login-action@v3',
    '        with:',
    '          registry: ghcr.io',
    `          username: ${$}{{ github.actor }}`,
    `          password: ${$}{{ secrets.GITHUB_TOKEN }}`,
    '      - uses: docker/build-push-action@v5',
    '        with:',
    '          push: true',
    `          tags: ghcr.io/${$}{{ github.repository_owner }}/${$}{{ github.event.repository.name }}:${$}{{ github.sha }}`,
    '',
    `      - name: Notify Nanoku (${appName})`,
    '        env:',
    `          URL: ${url}`,
    `          SECRET: ${secret}`,
    `          SHA: ${$}{{ github.sha }}`,
    `          MSG: ${$}{{ github.event.head_commit.message }}`,
    '        run: |',
    '          BODY="{\\"tag\\":\\"\${SHA}\\",\\"commit_message\\":\\"\${MSG//\\"/\\\\\\"}\\".\\"}"',
    '          SIG="sha256=$(printf \'%s\' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | awk \'{print $2}\')"',
    '          curl -fsS -X POST "$URL" \\',
    '            -H "Content-Type: application/json" \\',
    '            -H "X-Hub-Signature-256: $SIG" \\',
    '            -d "$BODY"',
    '',
  ].join('\n')
}

function WebhookSecretModal({
  appName,
  url,
  secret,
  onClose,
}: {
  appName: string
  url: string
  secret: string
  onClose: () => void
}) {
  const yaml = githubActionsWorkflowYaml(appName, url, secret)
  return (
    <Modal
      title={
        <span className="inline-flex items-center gap-2">
          <Bell size={16} />
          Webhook configured for {appName}
        </span>
      }
      open
      onOk={onClose}
      onCancel={onClose}
      okText="Done"
      cancelButtonProps={{ style: { display: 'none' } }}
      width={680}
    >
      <Alert
        type="warning"
        showIcon
        message="Copy the secret now. You won't be able to see it again."
        className="!mb-3"
      />
      <div className="space-y-3">
        <div>
          <div className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
            Webhook URL
          </div>
          <CopyableValue value={url} label="Webhook URL" />
        </div>
        <div>
          <div className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
            Secret (HMAC-SHA256 key)
          </div>
          <CopyableValue value={secret} label="Webhook secret" />
        </div>
        <div className="text-xs text-[var(--fg-muted)] mt-3 leading-relaxed">
          Configure in GitHub under <strong>Repo → Settings → Webhooks → Add</strong>:
          <ul className="list-disc ml-5 mt-1 space-y-0.5">
            <li>Payload URL: the URL above</li>
            <li>Content type: <code>application/json</code></li>
            <li>Secret: the secret above</li>
            <li>Events: "Just the push event"</li>
          </ul>
          On push, your GitHub Actions workflow builds and pushes the image,
          then POSTs <code>{'{ "tag": "<sha>", "commit_message": "<msg>" }'}</code> to the URL.
        </div>
        <Collapse
          ghost
          items={[
            {
              key: 'yaml',
              label: (
                <span className="text-xs">
                  <code>.github/workflows/deploy.yml</code> — copy into your app repo
                </span>
              ),
              children: (
                <div className="space-y-2">
                  <Button
                    size="small"
                    icon={<Copy size={12} />}
                    onClick={() => {
                      void navigator.clipboard.writeText(yaml)
                    }}
                  >
                    Copy YAML
                  </Button>
                  <pre className="mono text-[11px] leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-3 overflow-auto max-h-72 whitespace-pre text-[var(--fg-muted)]">
                    {yaml}
                  </pre>
                </div>
              ),
            },
          ]}
        />
      </div>
    </Modal>
  )
}

function StatusCell({ app }: { app: AppType }) {
  if (!app.container) {
    return <Tag className="!m-0">not deployed</Tag>
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
        <span className="mono text-xs">{s} · {app.container.name}</span>
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
  return (
    <div className="py-16 text-center">
      <div className="inline-flex items-center justify-center size-12 rounded-full border border-[var(--border)] mb-4">
        <Plus size={20} className="text-[var(--fg-muted)]" />
      </div>
      <p className="text-sm text-[var(--fg)] mb-1">No apps yet</p>
      <p className="text-xs text-[var(--fg-muted)] mb-4">
        Add your first app to deploy a container.
      </p>
      <Button type="primary" onClick={onCreate}>
        Add app
      </Button>
    </div>
  )
}

function AppDetail({
  appId,
  onClose,
  onChanged,
}: {
  appId: number
  onClose: () => void
  onChanged: () => void
}) {
  const { message } = App.useApp()
  const [app, setApp] = useState<AppType | null>(null)
  const [env, setEnv] = useState<EnvVar[]>([])
  const [deploys, setDeploys] = useState<Deploy[]>([])
  const [logs, setLogs] = useState<string>('')
  const [logsLoading, setLogsLoading] = useState(false)
  const [envDraft, setEnvDraft] = useState<EnvVar[]>([])
  const [savingEnv, setSavingEnv] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const [a, e, d] = await Promise.all([
        api.getApp(appId),
        api.listAppEnv(appId),
        api.listAppDeploys(appId),
      ])
      setApp(a)
      setEnv(e)
      setEnvDraft(e)
      setDeploys(d)
    } catch (err) {
      message.error((err as Error).message)
    }
  }, [appId, message])

  useEffect(() => {
    void refresh()
  }, [refresh])

  async function loadLogs() {
    if (!app?.container) {
      setLogs('// no container')
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

  function setRow(i: number, patch: Partial<EnvVar>) {
    setEnvDraft((rows) =>
      rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)),
    )
  }
  function addRow() {
    setEnvDraft((rows) => [...rows, { key: '', value: '' }])
  }
  function delRow(i: number) {
    setEnvDraft((rows) => rows.filter((_, idx) => idx !== i))
  }

  async function saveEnv() {
    const cleaned = envDraft
      .map((r) => ({ key: r.key.trim(), value: r.value }))
      .filter((r) => r.key !== '')
    const keys = cleaned.map((r) => r.key)
    if (new Set(keys).size !== keys.length) {
      message.error('duplicate keys')
      return
    }
    setSavingEnv(true)
    try {
      await api.replaceAppEnv(appId, cleaned)
      message.success(
        `Saved ${cleaned.length} env var${cleaned.length === 1 ? '' : 's'} — redeploy required`,
      )
      void refresh()
      void onChanged()
    } catch (err) {
      message.error((err as Error).message)
    } finally {
      setSavingEnv(false)
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
        </div>
      ) : 'Loading…'}
      destroyOnClose
    >
      {!app ? (
        <div className="py-12 text-center text-[var(--fg-muted)]">Loading…</div>
      ) : (
        <Tabs
          defaultActiveKey="overview"
          items={[
            {
              key: 'overview',
              label: 'Overview',
              children: (
                <div className="space-y-3 text-sm">
                  <Field label="Image" value={app.image} mono />
                  <Field label="Internal port" value={String(app.port)} mono />
                  <Field label="Branch" value={app.branch} mono />
                  {app.repoUrl && <Field label="Repo" value={app.repoUrl} mono />}
                  <Field label="Created" value={app.createdAt} mono />
                  {app.container && (
                    <>
                      <Field label="Container" value={app.container.name} mono />
                      <Field
                        label="Started"
                        value={app.container.startedAt ?? '—'}
                        mono
                      />
                    </>
                  )}
                  {app.webhookConfigured && (
                    <div className="border border-[var(--border)] rounded-md p-3 mt-3 bg-[var(--bg-input)]/40">
                      <div className="flex items-center gap-2 mb-2">
                        <Bell size={13} className="text-[var(--fg-muted)]" />
                        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
                          Webhook
                        </span>
                        <Tag color="green" className="!m-0 ml-auto">
                          active
                        </Tag>
                      </div>
                      <div className="mono text-xs text-[var(--fg-muted)] break-all">
                        POST {window.location.origin}/api/webhook/{app.name}
                      </div>
                      <div className="mono text-xs text-[var(--fg-muted)] mt-1">
                        image: {app.imageRepo}
                      </div>
                      <div className="mt-2">
                        <Popconfirm
                          title="Rotate webhook secret?"
                          description="A new random secret will be generated. You'll see it once."
                          okText="Rotate"
                          onConfirm={async () => {
                            try {
                              const updated = await api.rotateWebhookSecret(app.id)
                              if (updated.webhookSecret) {
                                message.success(
                                  `Rotated. New URL: ${window.location.origin}/api/webhook/${app.name}`,
                                )
                              }
                              void refresh()
                              void onChanged()
                            } catch (err) {
                              message.error((err as Error).message)
                            }
                          }}
                        >
                          <Button size="small" icon={<RefreshCw size={12} />}>
                            Rotate secret
                          </Button>
                        </Popconfirm>
                      </div>
                    </div>
                  )}
                </div>
              ),
            },
            {
              key: 'env',
              label: `Env vars (${env.length})`,
              children: (
                <div className="space-y-3">
                  <div className="space-y-2">
                    {envDraft.map((row, i) => (
                      <div
                        key={i}
                        className="flex items-center gap-2 border border-[var(--border)] rounded-md p-2 bg-[var(--bg-input)]"
                      >
                        <Input
                          className="!w-40 mono text-xs"
                          placeholder="KEY"
                          value={row.key}
                          onChange={(e) => setRow(i, { key: e.target.value })}
                        />
                        <Input
                          className="flex-1 mono text-xs"
                          placeholder="value"
                          value={row.value}
                          onChange={(e) => setRow(i, { value: e.target.value })}
                        />
                        <Button
                          type="text"
                          size="small"
                          icon={<Trash2 size={13} />}
                          onClick={() => delRow(i)}
                        />
                      </div>
                    ))}
                    <Button
                      type="dashed"
                      size="small"
                      icon={<Plus size={13} />}
                      onClick={addRow}
                    >
                      Add var
                    </Button>
                  </div>
                  <Popconfirm
                    title="Save and redeploy?"
                    description="Existing container keeps running until you redeploy."
                    okText="Save only"
                    cancelText="Cancel"
                    onConfirm={saveEnv}
                  >
                    <Button
                      type="primary"
                      loading={savingEnv}
                      disabled={envDraft.length === 0}
                    >
                      Save env vars
                    </Button>
                  </Popconfirm>
                  <p className="text-xs text-[var(--fg-muted)]">
                    Changes apply on next deploy. Click the rocket on the app row.
                  </p>
                </div>
              ),
            },
            {
              key: 'deploys',
              label: `Deploys (${deploys.length})`,
              children: deploys.length === 0 ? (
                <div className="py-8 text-center text-[var(--fg-muted)] text-sm">
                  No deploys yet.
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
                          container: {d.containerName}
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
              label: 'Logs',
              children: (
                <div className="space-y-2">
                  <Button
                    size="small"
                    icon={<RefreshCw size={13} />}
                    onClick={loadLogs}
                    loading={logsLoading}
                  >
                    Load logs
                  </Button>
                  <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-3 overflow-auto max-h-96 whitespace-pre-wrap break-all text-[var(--fg-muted)]">
                    {logs || '// click Load logs'}
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