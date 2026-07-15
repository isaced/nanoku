import {
  App,
  Button,
  Checkbox,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Radio,
  Select,
  Switch,
  Tag,
  Tooltip,
} from 'antd'
import {
  Bell,
  Database,
  Key,
  Plus,
  RefreshCw,
  Trash2,
  Wand2,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  useAppEnv,
  useAppVolumes,
  useCreateApp,
  useImportExposedPorts,
  useReplaceAppEnv,
  useReplaceAppVolumes,
  useRotateTriggerToken,
  useUpdateApp,
} from '../lib/hooks'
import type { App as AppType, AppInput, EnvVar, ExposedPort, VolumeInput } from '../lib/types'
import { CopyableValue } from './CodeBlock'
import { TriggerUsage } from './TriggerUsage'
import { YamlEditor } from './YamlEditor'

export interface AppEditorSaveResult {
  saved: AppType
  previous: AppType | null
  isNew: boolean
}

export function AppEditorModal({
  open,
  editing,
  onClose,
  onSaved,
}: {
  open: boolean
  editing: AppType | null
  onClose: () => void
  onSaved: (result: AppEditorSaveResult) => void
}) {
  const { message } = App.useApp()
  const { t } = useTranslation('apps')
  const [form] = Form.useForm<AppInput>()
  const [envDraft, setEnvDraft] = useState<EnvVar[]>([])
  const [volumeDraft, setVolumeDraft] = useState<VolumeInput[]>([])
  const [exposedPortsDraft, setExposedPortsDraft] = useState<ExposedPort[]>([])

  const createApp = useCreateApp()
  const updateApp = useUpdateApp()
  const replaceEnv = useReplaceAppEnv()
  const replaceVolumes = useReplaceAppVolumes()
  const rotateToken = useRotateTriggerToken()

  const envQuery = useAppEnv(editing?.id ?? null)
  const volumesQuery = useAppVolumes(editing?.id ?? null)

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
    if (!open) return
    if (!editing) {
      setEnvDraft([])
      setVolumeDraft([])
      setExposedPortsDraft([])
      form.resetFields()
      form.setFieldsValue({
        port: 80,
        deployMethod: 'docker',
        deleteVolumesOnRemove: false,
      })
      return
    }
    form.setFieldsValue({
      name: editing.name,
      image: editing.image,
      port: editing.port,
      deployMethod: editing.deployMethod ?? 'docker',
      composePath: editing.composePath ?? '',
      composeContent: editing.composeContent ?? '',
      registryUrl: editing.registryUrl ?? '',
      registryUsername: editing.registryUsername ?? '',
      enableTrigger: editing.triggerConfigured,
      deleteVolumesOnRemove: editing.deleteVolumesOnRemove,
    })
  }, [open, editing, form])

  useEffect(() => {
    if (!editing) {
      setEnvDraft([])
      setVolumeDraft([])
      setExposedPortsDraft([])
      return
    }
    if (envQuery.data) setEnvDraft(envQuery.data)
    if (volumesQuery.data) {
      setVolumeDraft(
        volumesQuery.data.map((row) => ({
          type: row.type,
          source: row.source.startsWith(`nanoku-${editing.name}-vol-`)
            ? ''
            : row.source,
          target: row.target,
          readOnly: row.readOnly,
        })),
      )
    }
    setExposedPortsDraft(editing.exposedPorts ?? [])
  }, [editing, envQuery.data, volumesQuery.data])

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
  // cleanedExposedPorts trims + drops rows with no service name. The
  // server is the final validator (port range, regex, duplicate name
  // detection) but we strip empty rows here so a half-typed "add row"
  // doesn't ship as { name: "", port: 0 } in the PATCH and bounce.
  function cleanedExposedPorts(): ExposedPort[] {
    return exposedPortsDraft
      .map((r) => ({ name: r.name.trim(), port: Number(r.port) }))
      .filter((r) => r.name !== '' && r.port > 0)
  }

  function handleSubmit() {
    void form.validateFields().then(async (values) => {
      const isCompose = values.deployMethod === 'compose'
      const isNew = !editing
      // Inject the in-component exposedPorts draft into the AppInput
      // payload. compose mode sends the cleaned list; docker mode
      // sends an empty array to clear any leftovers from a previous
      // compose deploy (so a docker→compose→docker round trip doesn't
      // leave a stale list in the DB).
      const payload: AppInput = {
        ...values,
        exposedPorts: isCompose ? cleanedExposedPorts() : [],
      }
      const saveMutation = isNew
        ? createApp.mutateAsync(payload)
        : updateApp.mutateAsync({ id: editing!.id, input: payload })
      try {
        const saved = await saveMutation
        await replaceEnv.mutateAsync({ id: saved.id, vars: cleanedEnv() })
        if (!isCompose) {
          await replaceVolumes.mutateAsync({
            id: saved.id,
            vols: cleanedVolumes(),
          })
        }
        onSaved({ saved, previous: editing, isNew })
      } catch (err) {
        message.error((err as Error).message)
      }
    })
  }

  function onRotate() {
    if (!editing) return
    rotateToken.mutate(editing.id, {
      onSuccess: (updated) => {
        if (updated.triggerToken) {
          onSaved({
            saved: updated,
            previous: editing,
            isNew: false,
          })
        }
      },
      onError: (err) => {
        message.error(err.message)
      },
    })
  }

  return (
    <Modal
      title={editing ? t('editor.editTitle') : t('editor.newTitle')}
      open={open}
      onOk={handleSubmit}
      onCancel={onClose}
      okText={editing ? t('editor.save') : t('editor.create')}
      cancelText={t('actions.cancel', { ns: 'common' })}
      destroyOnClose
      width={680}
      confirmLoading={
        createApp.isPending ||
        updateApp.isPending ||
        replaceEnv.isPending ||
        replaceVolumes.isPending
      }
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
                  <YamlEditor
                    rows={12}
                    placeholder={t('editor.composeContentPlaceholder')}
                  />
                </Form.Item>
                <ExposedPortsSection
                  rows={exposedPortsDraft}
                  onChange={setExposedPortsDraft}
                  appId={editing?.id}
                />
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

        <TriggerSection editing={editing} onRotate={onRotate} />

        <EnvVarsSection rows={envDraft} onChange={setEnvDraft} />

        <Form.Item
          noStyle
          shouldUpdate={(prev, curr) => prev.deployMethod !== curr.deployMethod}
        >
          {({ getFieldValue }) =>
            getFieldValue('deployMethod') === 'docker' ? (
              <VolumesSection rows={volumeDraft} onChange={setVolumeDraft} />
            ) : null
          }
        </Form.Item>
      </Form>
    </Modal>
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

// ExposedPortsSection is the compose-mode-only editor that lets the
// user declare which services in the compose stack Caddy should be
// able to reverse-proxy to. Each row maps a service name (must match
// a `services:` key in the YAML) to the container-side port the
// service listens on. The downstream sites editor renders a service
// select from this list.
//
// We use component state (rows) rather than Form.List because the
// field is a nested array of objects — Form.List works fine for it
// but the local-state pattern matches the env/volume editors above
// and keeps the cleanup logic (drop incomplete rows) in one place.
//
// The "Import from compose" button asks the server to parse the
// stored compose YAML and return a scaffolding list. It's only
// available when editing an existing app (the server endpoint
// requires an app id); for a new app, the user has to save the
// compose content first and then re-open the editor to import.
function ExposedPortsSection({
  rows,
  onChange,
  appId,
}: {
  rows: ExposedPort[]
  onChange: (next: ExposedPort[]) => void
  appId?: number
}) {
  const { t } = useTranslation('apps')
  const { message } = App.useApp()
  const [open, setOpen] = useState(rows.length > 0)
  const [touched, setTouched] = useState(false)
  useEffect(() => {
    if (!touched && rows.length > 0) setOpen(true)
  }, [rows.length, touched])
  const importMutation = useImportExposedPorts()
  const count = rows.length
  const canImport = appId != null
  function setRow(i: number, patch: Partial<ExposedPort>) {
    onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }
  function addRow() {
    onChange([...rows, { name: '', port: 80 }])
    setOpen(true)
    setTouched(true)
  }
  function delRow(i: number) {
    onChange(rows.filter((_, idx) => idx !== i))
  }
  // doImport runs the server-side parser and replaces the current
  // draft with the result. The user is expected to review before
  // submitting — the import is scaffolding, not a commit. The
  // message gives a quick "X imported, Y need a port" summary
  // drawn from the same hint the server returns, so the operator
  // knows whether to look at the port column.
  function doImport() {
    if (appId == null) return
    importMutation.mutate(appId, {
      onSuccess: (resp) => {
        onChange(resp.exposedPorts)
        setOpen(true)
        setTouched(true)
        const needPort = resp.exposedPorts.filter((p) => p.port <= 0).length
        if (needPort > 0) {
          message.info(
            t('editor.exposedPortImportNeedsPort', {
              total: resp.exposedPorts.length,
              need: needPort,
            }),
          )
        } else {
          message.success(
            t('editor.exposedPortImportOk', { count: resp.exposedPorts.length }),
          )
        }
      },
      onError: (err) => {
        message.error(err.message)
      },
    })
  }
  return (
    <div className="border border-[var(--border)] rounded-lg p-3 mb-2 bg-[var(--bg-input)]/30">
      <div className="flex items-center gap-2 mb-2">
        <span className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)]">
          {t('editor.exposedPortsTitle')}
        </span>
        {count > 0 && open && <Tag className="!m-0">{count}</Tag>}
        {canImport && (
          <Tooltip title={t('editor.exposedPortImportTooltip')}>
            <Button
              size="small"
              icon={<Wand2 size={13} />}
              onClick={doImport}
              loading={importMutation.isPending}
            >
              {t('editor.exposedPortImport')}
            </Button>
          </Tooltip>
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
          {t('editor.exposedPortsHint')}
        </div>
      ) : (
        <div className="space-y-2">
          {rows.map((row, i) => (
            <div
              key={i}
              className="flex items-center gap-2 border border-[var(--border)] rounded-md p-2 bg-[var(--bg-input)]"
            >
              <Input
                className="!w-44 mono text-xs"
                placeholder={t('editor.exposedPortNamePlaceholder')}
                value={row.name}
                onChange={(e) => setRow(i, { name: e.target.value })}
              />
              <InputNumber
                className="!w-32"
                min={1}
                max={65535}
                placeholder="3000"
                value={row.port}
                onChange={(v) => setRow(i, { port: Number(v) || 0 })}
              />
              <span className="text-[10px] text-[var(--fg-muted)] flex-1">
                {t('editor.exposedPortExtra')}
              </span>
              <Button
                type="text"
                size="small"
                icon={<Trash2 size={13} />}
                onClick={() => delRow(i)}
              />
            </div>
          ))}
          <div className="flex items-center gap-2">
            <Button
              type="dashed"
              size="small"
              icon={<Plus size={13} />}
              onClick={addRow}
            >
              {t('editor.exposedPortAdd')}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
