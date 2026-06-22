import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import {
  App,
  Alert,
  Button,
  Checkbox,
  Collapse,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Radio,
  Space,
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
  RefreshCw,
  Rocket,
  Square,
  Trash2,
} from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { api, ApiError } from '../lib/api'
import { clearCredentials, getCredentials } from '../lib/auth'
import type { App as AppType, AppInput, Deploy, EnvVar, Status } from '../lib/types'
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
  const { t } = useTranslation('apps')
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
        message.success(t('toast.updated', { name: values.name }))
      } else {
        const created = await api.createApp(values)
        message.success(t('toast.added', { name: values.name }))
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

      <Modal
        title={editing ? t('editor.editTitle') : t('editor.newTitle')}
        open={editorOpen}
        onOk={onSubmit}
        onCancel={() => setEditorOpen(false)}
        okText={editing ? t('editor.save') : t('editor.create')}
        cancelText={t('actions.cancel', { ns: 'common' })}
        destroyOnClose
        width={680}
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="name"
            label={t('editor.name')}
            rules={[
              { required: true, message: t('editor.nameRequired') },
              {
                pattern: /^[a-z][a-z0-9-]{0,62}$/,
                message: t('editor.namePattern'),
              },
            ]}
            extra={t('editor.nameExtra')}
          >
            <Input placeholder={t('editor.namePlaceholder')} autoFocus />
          </Form.Item>

          <Form.Item
            name="deployMethod"
            label={t('editor.deployMethod')}
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
                    label={t('editor.composePath')}
                    extra={t('editor.composePathExtra')}
                  >
                    <Input placeholder={t('editor.composePathPlaceholder')} />
                  </Form.Item>
                  <Form.Item
                    name="composeContent"
                    label={t('editor.composeContent')}
                    rules={[
                      {
                        validator: (_, value) => {
                          const pathVal = form.getFieldValue('composePath')
                          if (pathVal && String(pathVal).trim()) return Promise.resolve()
                          if (!value || !String(value).trim()) {
                            return Promise.reject(
                              new Error(t('editor.composeContentRequired')),
                            )
                          }
                          return Promise.resolve()
                        },
                      },
                    ]}
                    extra={t('editor.composeContentExtra')}
                  >
                    <Input.TextArea
                      rows={10}
                      placeholder={t('editor.composeContentPlaceholder')}
                      className="mono text-xs"
                    />
                  </Form.Item>
                </>
              ) : (
                <>
                  <Form.Item
                    name="image"
                    label={t('editor.image')}
                    rules={[{ required: true, message: t('editor.imageRequired') }]}
                    extra={t('editor.imageExtra')}
                  >
                    <Input placeholder={t('editor.imagePlaceholder')} />
                  </Form.Item>
                  <Form.Item
                    name="port"
                    label={t('editor.port')}
                    rules={[
                      { required: true, message: t('editor.portRequired') },
                      { type: 'number', min: 1, max: 65535 },
                    ]}
                    extra={t('editor.portExtra')}
                  >
                    <InputNumber min={1} max={65535} className="w-full" />
                  </Form.Item>
                </>
              )
            }
          </Form.Item>

          <RegistrySection editing={editing} />

          <Form.Item name="branch" label={t('editor.branch')} initialValue="main">
            <Input placeholder={t('editor.branchPlaceholder')} />
          </Form.Item>
          <Form.Item name="repoUrl" label={t('editor.repoUrl')}>
            <Input placeholder={t('editor.repoUrlPlaceholder')} />
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
  const { t } = useTranslation('apps')
  return (
    <Radio.Group
      value={value}
      onChange={(e) => onChange?.(e.target.value)}
      optionType="button"
      buttonStyle="solid"
    >
      <Radio.Button value="docker">{t('deployMethod.docker')}</Radio.Button>
      <Radio.Button value="compose">{t('deployMethod.compose')}</Radio.Button>
    </Radio.Group>
  )
}

function RegistrySection({ editing }: { editing: AppType | null }) {
  const { t } = useTranslation('apps')
  return (
    <div className="border border-[var(--border)] rounded-lg p-3 mb-2 bg-[var(--bg-input)]/30">
      <div className="flex items-center gap-2 mb-2">
        <Key size={13} className="text-[var(--fg-muted)]" />
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          {t('registry.title')}
        </span>
        {editing?.registryConfigured && (
          <Tag color="blue" className="!m-0 ml-auto">
            {t('registry.configured')}
          </Tag>
        )}
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Form.Item
          name="registryUrl"
          label={t('registry.url')}
          extra={t('registry.urlExtra')}
          className="!mb-2"
        >
          <Input placeholder={t('registry.urlPlaceholder')} />
        </Form.Item>
        <Form.Item
          name="registryUsername"
          label={t('registry.username')}
          className="!mb-2"
        >
          <Input placeholder={t('registry.usernamePlaceholder')} />
        </Form.Item>
      </div>
      <Form.Item
        name="registryPassword"
        label={editing?.registryConfigured ? t('registry.passwordNew') : t('registry.password')}
        extra={editing?.registryConfigured ? t('registry.passwordExtra') : undefined}
        className="!mb-0"
      >
        <Input.Password
          placeholder={editing?.registryConfigured ? t('registry.passwordPlaceholderKeep') : t('registry.passwordPlaceholder')}
          autoComplete="off"
        />
      </Form.Item>
      {editing?.registryConfigured && (
        <Form.Item name="clearRegistry" valuePropName="checked" className="!mb-0 mt-2">
          <Checkbox>{t('registry.clear')}</Checkbox>
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
  const { t } = useTranslation('apps')
  return (
    <div className="border border-[var(--border)] rounded-lg p-3 mb-2 bg-[var(--bg-input)]/30">
      <div className="flex items-center gap-2 mb-2">
        <Bell size={13} className="text-[var(--fg-muted)]" />
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          {t('webhook.sectionTitle')}
        </span>
        {editing?.webhookConfigured && (
          <Tag color="green" className="!m-0 ml-auto">
            {t('common:status.active', { ns: 'common' })}
          </Tag>
        )}
      </div>
      <Form.Item
        name="imageRepo"
        label={t('webhook.imageRepo')}
        extra={
          <Trans
            ns="apps"
            i18nKey="webhook.imageRepoExtra"
            values={{ repoTag: '<repo>:<payload-tag>' }}
            components={{ code: <code className="mono" /> }}
          />
        }
        className="!mb-2"
      >
        <Input placeholder={t('webhook.imageRepoPlaceholder')} />
      </Form.Item>
      {editing?.webhookConfigured && (
        <div className="flex items-center justify-between mt-2">
          <span className="text-xs text-[var(--fg-muted)] mono">
            POST {window.location.origin}/api/webhook/{editing.name}
          </span>
          <Popconfirm
            title={t('webhook.rotateTitle')}
            description={t('webhook.rotateDescription')}
            okText={t('webhook.rotateButton')}
            onConfirm={onRotate}
          >
            <Button size="small" icon={<RefreshCw size={12} />}>
              {t('webhook.rotateButton')}
            </Button>
          </Popconfirm>
        </div>
      )}
    </div>
  )
}

function CopyableValue({ value, label }: { value: string; label: string }) {
  const { t } = useTranslation('common')
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
        {t('actions.copy')}
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
  const { t } = useTranslation('apps')
  const yaml = githubActionsWorkflowYaml(appName, url, secret)
  return (
    <Modal
      title={
        <span className="inline-flex items-center gap-2">
          <Bell size={16} />
          {t('webhook.secretModalTitle', { name: appName })}
        </span>
      }
      open
      onOk={onClose}
      onCancel={onClose}
      okText={t('actions.done', { ns: 'common' })}
      cancelButtonProps={{ style: { display: 'none' } }}
      width={680}
    >
      <Alert
        type="warning"
        showIcon
        message={t('webhook.secretWarning')}
        className="!mb-3"
      />
      <div className="space-y-3">
        <div>
          <div className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
            {t('webhook.url')}
          </div>
          <CopyableValue value={url} label={t('webhook.urlLabel')} />
        </div>
        <div>
          <div className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
            {t('webhook.secret')}
          </div>
          <CopyableValue value={secret} label={t('webhook.secretLabel')} />
        </div>
        <div className="text-xs text-[var(--fg-muted)] mt-3 leading-relaxed">
          <Trans
            ns="apps"
            i18nKey="webhook.configGuide"
            components={{ strong: <strong /> }}
          />
          <ul className="list-disc ml-5 mt-1 space-y-0.5">
            <li>{t('webhook.configUrl')}</li>
            <li>
              <Trans
                ns="apps"
                i18nKey="webhook.configContentType"
                components={{ code: <code /> }}
              />
            </li>
            <li>{t('webhook.configSecret')}</li>
            <li>{t('webhook.configEvents')}</li>
          </ul>
          <span className="block mt-1">
            <Trans
              ns="apps"
              i18nKey="webhook.configPost"
              values={{
                payload: '{ "tag": "<sha>", "commit_message": "<msg>" }',
              }}
              components={{ code: <code /> }}
            />
          </span>
        </div>
        <Collapse
          ghost
          items={[
            {
              key: 'yaml',
              label: (
                <span className="text-xs">
                  <Trans
                    ns="apps"
                    i18nKey="webhook.yamlTitle"
                    components={{ code: <code /> }}
                  />
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
                    {t('webhook.yamlCopy')}
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
  const { t } = useTranslation('apps')
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
      message.error(t('detail.duplicateKeys'))
      return
    }
    setSavingEnv(true)
    try {
      await api.replaceAppEnv(appId, cleaned)
      message.success(
        t('detail.saveEnvSuccess', { count: cleaned.length }),
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
                <div className="space-y-3 text-sm">
                  <Field label={t('detail.image')} value={app.image} mono />
                  <Field label={t('detail.internalPort')} value={String(app.port)} mono />
                  <Field label={t('detail.branch')} value={app.branch} mono />
                  {app.repoUrl && <Field label={t('detail.repo')} value={app.repoUrl} mono />}
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
                  {app.webhookConfigured && (
                    <div className="border border-[var(--border)] rounded-md p-3 mt-3 bg-[var(--bg-input)]/40">
                      <div className="flex items-center gap-2 mb-2">
                        <Bell size={13} className="text-[var(--fg-muted)]" />
                        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
                          Webhook
                        </span>
                        <Tag color="green" className="!m-0 ml-auto">
                          {t('detail.webhookActive')}
                        </Tag>
                      </div>
                      <div className="mono text-xs text-[var(--fg-muted)] break-all">
                        POST {window.location.origin}/api/webhook/{app.name}
                      </div>
                      <div className="mono text-xs text-[var(--fg-muted)] mt-1">
                        {t('detail.imageLabel')}: {app.imageRepo}
                      </div>
                      <div className="mt-2">
                        <Popconfirm
                          title={t('webhook.rotateTitle')}
                          description={t('webhook.rotateDescriptionShort')}
                          okText={t('webhook.rotateButton')}
                          onConfirm={async () => {
                            try {
                              const updated = await api.rotateWebhookSecret(app.id)
                              if (updated.webhookSecret) {
                                message.success(
                                  t('webhook.rotatedToast', {
                                    url: `${window.location.origin}/api/webhook/${app.name}`,
                                  }),
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
                            {t('webhook.rotateButton')}
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
              label: t('detail.tabEnv', { count: env.length }),
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
                          placeholder={t('detail.keyPlaceholder')}
                          value={row.key}
                          onChange={(e) => setRow(i, { key: e.target.value })}
                        />
                        <Input
                          className="flex-1 mono text-xs"
                          placeholder={t('detail.valuePlaceholder')}
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
                      {t('detail.addVar')}
                    </Button>
                  </div>
                  <Popconfirm
                    title={t('detail.saveAndRedeployTitle')}
                    description={t('detail.saveAndRedeployDescription')}
                    okText={t('detail.saveEnv')}
                    cancelText={t('actions.cancel', { ns: 'common' })}
                    onConfirm={saveEnv}
                  >
                    <Button
                      type="primary"
                      loading={savingEnv}
                      disabled={envDraft.length === 0}
                    >
                      {t('detail.saveEnv')}
                    </Button>
                  </Popconfirm>
                  <p className="text-xs text-[var(--fg-muted)]">
                    {t('detail.saveEnvHint')}
                  </p>
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
