import { createFileRoute, redirect } from '@tanstack/react-router'
import {
  App,
  Alert,
  Button,
  Checkbox,
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
  Database,
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
import { ensureAuth, isAuthenticated } from '../lib/auth'
import type { App as AppType, AppInput, Deploy, EnvVar, Status, Volume, VolumeInput } from '../lib/types'
import { TopNav } from '../components/TopNav'

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
  const [form] = Form.useForm<AppInput>()
  const [envDraft, setEnvDraft] = useState<EnvVar[]>([])
  const [volumeDraft, setVolumeDraft] = useState<VolumeInput[]>([])

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
    setEnvDraft([])
    setVolumeDraft([])
    form.resetFields()
    form.setFieldsValue({ port: 80, deployMethod: 'docker', deleteVolumesOnRemove: false })
    setEditorOpen(true)
  }

  async function openEdit(a: AppType) {
    setEditing(a)
    form.setFieldsValue({
      name: a.name,
      image: a.image,
      port: a.port,
      deployMethod: a.deployMethod ?? 'docker',
      composePath: a.composePath ?? '',
      composeContent: a.composeContent ?? '',
      registryUrl: a.registryUrl ?? '',
      registryUsername: a.registryUsername ?? '',
      enableTrigger: a.triggerConfigured,
      deleteVolumesOnRemove: a.deleteVolumesOnRemove,
    })
    setEditorOpen(true)
    try {
      const [env, vols] = await Promise.all([
        api.listAppEnv(a.id),
        api.listAppVolumes(a.id),
      ])
      setEnvDraft(env)
      setVolumeDraft(
        vols.map((row) => ({
          type: row.type,
          source: row.source.startsWith(`nanoku-${a.name}-vol-`) ? '' : row.source,
          target: row.target,
          readOnly: row.readOnly,
        })),
      )
    } catch (err) {
      message.error((err as Error).message)
    }
  }

  function cleanedEnv(): EnvVar[] {
    return envDraft
      .map((r) => ({ key: r.key.trim(), value: r.value }))
      .filter((r) => r.key !== '')
  }
  function cleanedVolumes(): VolumeInput[] {
    return volumeDraft
      .map((r) => ({
        type: (r.type ?? 'volume') as 'volume' | 'bind',
        source: (r.source ?? '').trim(),
        target: (r.target ?? '').trim(),
        readOnly: !!r.readOnly,
      }))
      .filter((r) => r.target !== '')
  }

  async function onSubmit() {
    const values = await form.validateFields()
    const isCompose = values.deployMethod === 'compose'
    try {
      if (editing) {
        const saved = await api.updateApp(editing.id, values)
        await api.replaceAppEnv(editing.id, cleanedEnv())
        if (!isCompose) {
          await api.replaceAppVolumes(editing.id, cleanedVolumes())
        }
        message.success(t('toast.updated', { name: values.name }))
        setEditorOpen(false)
        void reload()
        const appForRedeploy: AppType = { ...editing, ...saved }
        modal.confirm({
          title: t('redeployPrompt.title', { name: appForRedeploy.name }),
          content: t('redeployPrompt.content'),
          okText: t('redeployPrompt.ok'),
          cancelText: t('redeployPrompt.cancel'),
          onOk: () => runAction(appForRedeploy, 'redeployed', () => api.deployApp(appForRedeploy.id)),
        })
      } else {
        const saved = await api.createApp(values)
        await api.replaceAppEnv(saved.id, cleanedEnv())
        if (!isCompose) {
          await api.replaceAppVolumes(saved.id, cleanedVolumes())
        }
        message.success(t('toast.added', { name: values.name }))
        if (saved.triggerToken) {
          setRevealedToken({
            url: `${window.location.origin}/api/apps/${saved.name}/trigger`,
            token: saved.triggerToken,
            appName: saved.name,
          })
        }
        setEditorOpen(false)
        void reload()
      }
    } catch (err) {
      message.error((err as Error).message)
    }
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

          <TriggerSection
            editing={editing}
            onRotate={() => editing && rotateToken(editing)}
          />

          <EnvVarsSection
            rows={envDraft}
            onChange={setEnvDraft}
          />

          <Form.Item
            noStyle
            shouldUpdate={(prev, curr) => prev.deployMethod !== curr.deployMethod}
          >
            {({ getFieldValue }) =>
              getFieldValue('deployMethod') === 'docker' ? (
                <VolumesSection
                  rows={volumeDraft}
                  onChange={setVolumeDraft}
                />
              ) : null
            }
          </Form.Item>
        </Form>
      </Modal>

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

function EnvVarsSection({
  rows,
  onChange,
}: {
  rows: EnvVar[]
  onChange: (next: EnvVar[]) => void
}) {
  const { t } = useTranslation('apps')
  const [open, setOpen] = useState(rows.length > 0)
  const [touched, setTouched] = useState(false)
  useEffect(() => {
    if (!touched && rows.length > 0) setOpen(true)
  }, [rows.length, touched])
  const count = rows.length
  function setRow(i: number, patch: Partial<EnvVar>) {
    onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }
  function addRow() {
    onChange([...rows, { key: '', value: '' }])
    setOpen(true)
    setTouched(true)
  }
  function delRow(i: number) {
    onChange(rows.filter((_, idx) => idx !== i))
  }
  return (
    <div className="border border-[var(--border)] rounded-lg p-3 mb-2 bg-[var(--bg-input)]/30">
      <div className="flex items-center gap-2 mb-2">
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          {t('editor.envVarsTitle')}
        </span>
        {count > 0 && open && (
          <Tag className="!m-0">{count}</Tag>
        )}
        <Switch
          className="ml-auto"
          checked={open}
          onChange={(v) => {
            setTouched(true)
            setOpen(v)
          }}
          checkedChildren={t('registry.switchOn')}
          unCheckedChildren={t('registry.switchOff')}
        />
      </div>
      {!open ? (
        <div className="text-xs text-[var(--fg-muted)] py-1">
          {count > 0 ? t('editor.envVarsTitle') : t('editor.envVarsHint')}
        </div>
      ) : (
        <div className="space-y-2">
          {rows.map((row, i) => (
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
      )}
    </div>
  )
}

function VolumesSection({
  rows,
  onChange,
}: {
  rows: VolumeInput[]
  onChange: (next: VolumeInput[]) => void
}) {
  const { t } = useTranslation('apps')
  const [open, setOpen] = useState(rows.length > 0)
  const [touched, setTouched] = useState(false)
  useEffect(() => {
    if (!touched && rows.length > 0) setOpen(true)
  }, [rows.length, touched])
  const count = rows.length
  function setRow(i: number, patch: Partial<VolumeInput>) {
    onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }
  function addRow() {
    onChange([...rows, { type: 'volume', target: '' }])
    setOpen(true)
    setTouched(true)
  }
  function delRow(i: number) {
    onChange(rows.filter((_, idx) => idx !== i))
  }
  return (
    <div className="border border-[var(--border)] rounded-lg p-3 mb-2 bg-[var(--bg-input)]/30">
      <div className="flex items-center gap-2 mb-2">
        <Database size={13} className="text-[var(--fg-muted)]" />
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          {t('editor.volumesTitle')}
        </span>
        {count > 0 && open && (
          <Tag className="!m-0">{count}</Tag>
        )}
        <Switch
          className="ml-auto"
          checked={open}
          onChange={(v) => {
            setTouched(true)
            setOpen(v)
          }}
          checkedChildren={t('registry.switchOn')}
          unCheckedChildren={t('registry.switchOff')}
        />
      </div>
      {!open ? (
        <div className="text-xs text-[var(--fg-muted)] py-1">
          {t('editor.volumesHint')}
        </div>
      ) : (
        <div className="space-y-3">
          <div className="space-y-2">
            {rows.map((row, i) => {
              const isAuto = !row.source && row.type === 'volume'
              return (
                <div
                  key={i}
                  className="flex items-center gap-2 border border-[var(--border)] rounded-md p-2 bg-[var(--bg-input)]"
                >
                  <Select
                    className="!w-32"
                    value={row.type ?? 'volume'}
                    onChange={(v) => setRow(i, { type: v })}
                    options={[
                      { value: 'volume', label: t('volumeEditor.typeVolume') },
                      { value: 'bind', label: t('volumeEditor.typeBind') },
                    ]}
                  />
                  <Input
                    className="!w-56 mono text-xs"
                    placeholder={
                      row.type === 'bind'
                        ? t('volumeEditor.sourceBindPlaceholder')
                        : t('volumeEditor.sourceVolumePlaceholder')
                    }
                    value={row.source ?? ''}
                    onChange={(e) => setRow(i, { source: e.target.value })}
                  />
                  <Input
                    className="flex-1 mono text-xs"
                    placeholder={t('volumeEditor.targetPlaceholder')}
                    value={row.target ?? ''}
                    onChange={(e) => setRow(i, { target: e.target.value })}
                  />
                  <Tooltip title={t('volumeEditor.readOnly')}>
                    <Switch
                      checked={!!row.readOnly}
                      onChange={(v) => setRow(i, { readOnly: v })}
                    />
                  </Tooltip>
                  {isAuto && (
                    <Tooltip title={t('volumeEditor.autoHint')}>
                      <Tag className="!m-0 mono text-[10px]">
                        {t('volumeEditor.autoBadge')}
                      </Tag>
                    </Tooltip>
                  )}
                  <Button
                    type="text"
                    size="small"
                    icon={<Trash2 size={13} />}
                    onClick={() => delRow(i)}
                  />
                </div>
              )
            })}
            <Button
              type="dashed"
              size="small"
              icon={<Plus size={13} />}
              onClick={addRow}
            >
              {t('volumeEditor.addRow')}
            </Button>
          </div>
          <Form.Item
            name="deleteVolumesOnRemove"
            valuePropName="checked"
            extra={t('editor.deleteVolumesOnRemoveExtra')}
            className="!mb-0"
          >
            <Checkbox>{t('editor.deleteVolumesOnRemove')}</Checkbox>
          </Form.Item>
        </div>
      )}
    </div>
  )
}

function RegistrySection({ editing }: { editing: AppType | null }) {
  const { t } = useTranslation('apps')
  // Edit-configured opens by default; new apps and unconfigured edits stay
  // collapsed so the password/username inputs are not in the DOM — that keeps
  // browser autofill and password managers from grabbing them by accident.
  const [open, setOpen] = useState(Boolean(editing?.registryConfigured))
  return (
    <div className="border border-[var(--border)] rounded-lg p-3 mb-2 bg-[var(--bg-input)]/30">
      <div className="flex items-center gap-2 mb-2">
        <Key size={13} className="text-[var(--fg-muted)]" />
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          {t('registry.title')}
        </span>
        {editing?.registryConfigured && open && (
          <Tag color="blue" className="!m-0 ml-auto">
            {t('registry.configured')}
          </Tag>
        )}
        <Switch
          className="ml-auto"
          checked={open}
          onChange={setOpen}
          checkedChildren={t('registry.switchOn')}
          unCheckedChildren={t('registry.switchOff')}
        />
      </div>
      {open ? (
        <>
          <div className="grid grid-cols-2 gap-3">
            <Form.Item
              name="registryUrl"
              label={t('registry.url')}
              extra={t('registry.urlExtra')}
              className="!mb-2"
            >
              <Input
                placeholder={t('registry.urlPlaceholder')}
                autoComplete="off"
              />
            </Form.Item>
            <Form.Item
              name="registryUsername"
              label={t('registry.username')}
              className="!mb-2"
            >
              <Input
                placeholder={t('registry.usernamePlaceholder')}
                autoComplete="off"
              />
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
              autoComplete="new-password"
            />
          </Form.Item>
          {editing?.registryConfigured && (
            <Form.Item name="clearRegistry" valuePropName="checked" className="!mb-0 mt-2">
              <Checkbox>{t('registry.clear')}</Checkbox>
            </Form.Item>
          )}
        </>
      ) : (
        <div className="text-xs text-[var(--fg-muted)] py-1">
          {t('registry.collapsedHint')}
        </div>
      )}
    </div>
  )
}

function TriggerSection({
  editing,
  onRotate,
}: {
  editing: AppType | null
  onRotate: () => void
}) {
  const { t } = useTranslation('apps')
  const url = editing
    ? `${window.location.origin}/api/apps/${editing.name}/trigger`
    : ''
  // The switch is the single source of truth for the form field
  // `enableTrigger`. Mirroring it into local state just drives the
  // collapse / content swap.
  const [open, setOpen] = useState(Boolean(editing?.triggerConfigured))
  return (
    <div className="border border-[var(--border)] rounded-lg p-3 mb-2 bg-[var(--bg-input)]/30">
      <div className="flex items-center gap-2 mb-2">
        <Bell size={13} className="text-[var(--fg-muted)]" />
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          {t('trigger.sectionTitle')}
        </span>
        <Form.Item
          name="enableTrigger"
          valuePropName="checked"
          className="!mb-0 ml-auto"
        >
          <Switch
            checkedChildren={t('registry.switchOn')}
            unCheckedChildren={t('registry.switchOff')}
            onChange={(v) => setOpen(v)}
          />
        </Form.Item>
      </div>
      {!open ? (
        <div className="text-xs text-[var(--fg-muted)] py-1">
          {editing && editing.triggerConfigured
            ? t('trigger.offWhileEnabledHint')
            : t('trigger.collapsedHint')}
        </div>
      ) : editing && editing.triggerConfigured ? (
        <div className="space-y-3">
          <div>
            <div className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
              {t('trigger.url')}
            </div>
            <CopyableValue value={url} label={t('trigger.urlLabel')} />
          </div>
          <details className="text-xs">
            <summary className="cursor-pointer text-[var(--fg-muted)] hover:text-[var(--fg)] select-none">
              {t('trigger.usageTitle')}
            </summary>
            <div className="mt-2">
              <TriggerUsage url={url} appName={editing.name} />
            </div>
          </details>
          <div>
            <Popconfirm
              title={t('trigger.rotateTitle')}
              description={t('trigger.rotateDescription')}
              okText={t('trigger.rotateButton')}
              onConfirm={onRotate}
            >
              <Button size="small" icon={<RefreshCw size={12} />}>
                {t('trigger.rotateButton')}
              </Button>
            </Popconfirm>
          </div>
          <div className="text-[11px] text-[var(--fg-muted)]">
            {t('trigger.disableHint')}
          </div>
        </div>
      ) : editing ? (
        <div className="text-xs text-[var(--fg-muted)] py-1">
          {t('trigger.editNotEnabledHint')}
        </div>
      ) : (
        <div className="text-xs text-[var(--fg-muted)] py-1">
          {t('trigger.enableExtra')}
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

// buildTriggerCurl renders a copy-paste shell snippet that POSTs to the
// trigger URL. `bearer` is the value that goes after `Authorization: ` —
// the caller decides whether to embed a real token or use a
// `$NANOKU_TRIGGER_TOKEN` env var placeholder.
function buildTriggerCurl(url: string, bearer: string) {
  const body = JSON.stringify({ tag: '<sha>', commit_message: '<msg>' })
  return [
    'BODY=' + shellQuote(body),
    'curl -fsS -X POST ' + shellQuote(url) + ' \\',
    '  -H "Authorization: Bearer ' + bearer + '" \\',
    '  -H "Content-Type: application/json" \\',
    '  -d "$BODY"',
  ].join('\n')
}

function shellQuote(s: string): string {
  return "'" + s.replace(/'/g, "'\\''") + "'"
}

// buildWorkflowYaml is the GitHub Actions deploy template.
//
// IMPORTANT: the YAML never embeds the actual token. It references the
// user's repo secret via `${{ secrets.NANOKU_TRIGGER_TOKEN }}` — public
// repos are fine to share this file as-is. The token's first appearance
// to the user is in the modal, where they copy it into the secret.
function buildWorkflowYaml(appName: string, url: string) {
  // GitHub Actions uses ${{ ... }} for expressions. In JS template literals
  // we must escape every literal `$` so it isn't treated as interpolation.
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
    `          TOKEN: ${$}{{ secrets.NANOKU_TRIGGER_TOKEN }}`,
    `          SHA: ${$}{{ github.sha }}`,
    `          MSG: ${$}{{ github.event.head_commit.message }}`,
    '        run: |',
    '          BODY=$(jq -nc --arg t "$SHA" --arg m "$MSG" \'{tag: $t, commit_message: $m}\')',
    '          curl -fsS -X POST "$URL" \\',
    '            -H "Authorization: Bearer $TOKEN" \\',
    '            -H "Content-Type: application/json" \\',
    '            -d "$BODY"',
    '',
  ].join('\n')
}

function CodeBlock({ value }: { value: string }) {
  const { t } = useTranslation('common')
  return (
    <div className="space-y-1">
      <Button
        size="small"
        icon={<Copy size={12} />}
        onClick={() => {
          void navigator.clipboard.writeText(value)
        }}
      >
        {t('actions.copy')}
      </Button>
      <pre className="mono text-[11px] leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-3 overflow-auto max-h-48 whitespace-pre text-[var(--fg-muted)]">
        {value}
      </pre>
    </div>
  )
}

// TriggerUsage renders the curl + workflow YAML examples. `token` is the
// real bearer credential; when omitted, the curl uses
// `$NANOKU_TRIGGER_TOKEN` as a placeholder so the editing view (which
// doesn't have the token) can show the same shape.
function TriggerUsage({
  url,
  appName,
  token,
}: {
  url: string
  appName: string
  token?: string
}) {
  const { t } = useTranslation('apps')
  const bearer = token ?? '$NANOKU_TRIGGER_TOKEN'
  const curl = buildTriggerCurl(url, bearer)
  const yaml = buildWorkflowYaml(appName, url)
  return (
    <div className="space-y-3">
      {!token && (
        <div className="text-[11px] text-[var(--fg-muted)] leading-relaxed">
          {t('trigger.usagePlaceholder', { var: 'NANOKU_TRIGGER_TOKEN' })}
        </div>
      )}
      <div>
        <div className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
          {t('trigger.curlTitle')}
        </div>
        <CodeBlock value={curl} />
      </div>
      <div>
        <div className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
          <Trans
            ns="apps"
            i18nKey="trigger.yamlTitle"
            components={{ code: <code /> }}
          />
        </div>
        <CodeBlock value={yaml} />
      </div>
    </div>
  )
}

function TriggerTokenModal({
  appName,
  url,
  token,
  onClose,
}: {
  appName: string
  url: string
  token: string
  onClose: () => void
}) {
  const { t } = useTranslation('apps')
  return (
    <Modal
      title={
        <span className="inline-flex items-center gap-2">
          <Bell size={16} />
          {t('trigger.tokenModalTitle', { name: appName })}
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
        message={t('trigger.tokenWarning')}
        className="!mb-3"
      />
      <div className="space-y-3">
        <div>
          <div className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
            {t('trigger.url')}
          </div>
          <CopyableValue value={url} label={t('trigger.urlLabel')} />
        </div>
        <div>
          <div className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1">
            {t('trigger.token')}
          </div>
          <CopyableValue value={token} label={t('trigger.tokenLabel')} />
        </div>
        <div className="text-xs text-[var(--fg-muted)] mt-3 leading-relaxed">
          <Trans
            ns="apps"
            i18nKey="trigger.protocolGuide"
            components={{ code: <code />, strong: <strong /> }}
          />
        </div>
        <div className="border-t border-[var(--border)] pt-3">
          <TriggerUsage url={url} appName={appName} token={token} />
        </div>
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
  onEditRequested,
}: {
  appId: number
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
