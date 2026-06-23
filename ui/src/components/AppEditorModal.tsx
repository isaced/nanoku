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
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  useAppEnv,
  useAppVolumes,
  useCreateApp,
  useReplaceAppEnv,
  useReplaceAppVolumes,
  useRotateTriggerToken,
  useUpdateApp,
} from '../lib/hooks'
import type { App as AppType, AppInput, EnvVar, VolumeInput } from '../lib/types'
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

  function handleSubmit() {
    void form.validateFields().then(async (values) => {
      const isCompose = values.deployMethod === 'compose'
      const isNew = !editing
      const saveMutation = isNew
        ? createApp.mutateAsync(values)
        : updateApp.mutateAsync({ id: editing!.id, input: values })
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
