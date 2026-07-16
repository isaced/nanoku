import { Form, Modal, Tabs, type TabsProps } from 'antd'
import {
  Bell,
  Database,
  Key,
  Network,
  SlidersHorizontal,
  Variable,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { App as AppType } from '../../lib/types'
import { EnvVarsEditor } from './EnvVarsEditor'
import { ExposedPortsEditor } from './ExposedPortsEditor'
import { GeneralTab } from './GeneralTab'
import { RegistrySection } from './RegistrySection'
import { TabLabel } from './TabLabel'
import { TriggerSection } from './TriggerSection'
import { VolumesEditor } from './VolumesEditor'
import { useAppEditorForm, type AppEditorSaveResult } from './useAppEditorForm'

export type { AppEditorSaveResult }

// AppEditorModal is the orchestration shell for creating/editing an app.
// All stateful behaviour lives in useAppEditorForm; this component only
// wires the form state into a tabbed layout. Each tab uses forceRender so
// its fields stay mounted even when the tab is not active — otherwise
// validateFields would miss fields on unvisited tabs at submit time.
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
  const { t } = useTranslation('apps')
  const {
    form,
    deployMethod,
    envDraft,
    setEnvDraft,
    volumeDraft,
    setVolumeDraft,
    exposedPortsDraft,
    setExposedPortsDraft,
    handleSubmit,
    revealedToken,
    triggerEnabled,
    onGenerate,
    onDisable,
    isPending,
  } = useAppEditorForm({ open, editing, onSaved })

  const envCount = envDraft.length
  const volumeCount = volumeDraft.length
  const exposedCount = exposedPortsDraft.length
  const isCompose = deployMethod === 'compose'

  const tabItems: TabsProps['items'] = [
    {
      key: 'general',
      forceRender: true,
      label: (
        <TabLabel icon={<SlidersHorizontal size={14} />} text={t('editor.tabs.general')} />
      ),
      children: <GeneralTab form={form} />,
    },
    {
      key: 'environment',
      forceRender: true,
      label: (
        <TabLabel
          icon={<Variable size={14} />}
          text={t('editor.tabs.environment')}
          count={envCount}
        />
      ),
      children: (
        <div className="pt-1">
          <p className="text-xs text-[var(--fg-muted)] mb-4">
            {t('editor.tabEnvDesc')}
          </p>
          <EnvVarsEditor rows={envDraft} onChange={setEnvDraft} />
        </div>
      ),
    },
    {
      key: 'storage',
      forceRender: true,
      label: isCompose ? (
        <TabLabel
          icon={<Network size={14} />}
          text={t('editor.tabs.networking')}
          count={exposedCount}
        />
      ) : (
        <TabLabel
          icon={<Database size={14} />}
          text={t('editor.tabs.storage')}
          count={volumeCount}
        />
      ),
      children: isCompose ? (
        <div className="pt-1">
          <p className="text-xs text-[var(--fg-muted)] mb-4">
            {t('editor.tabNetworkDesc')}
          </p>
          <ExposedPortsEditor
            rows={exposedPortsDraft}
            onChange={setExposedPortsDraft}
            appId={editing?.id}
          />
        </div>
      ) : (
        <div className="pt-1">
          <p className="text-xs text-[var(--fg-muted)] mb-4">
            {t('editor.tabStorageDesc')}
          </p>
          <VolumesEditor rows={volumeDraft} onChange={setVolumeDraft} />
        </div>
      ),
    },
    {
      key: 'registry',
      forceRender: true,
      label: (
        <TabLabel
          icon={<Key size={14} />}
          text={t('editor.tabs.registry')}
          tag={editing?.registryConfigured ? t('registry.configured') : undefined}
        />
      ),
      children: (
        <div className="pt-1">
          <p className="text-xs text-[var(--fg-muted)] mb-4">
            {t('editor.tabRegistryDesc')}
          </p>
          <RegistrySection editing={editing} />
        </div>
      ),
    },
    {
      key: 'trigger',
      forceRender: true,
      label: (
        <TabLabel
          icon={<Bell size={14} />}
          text={t('editor.tabs.trigger')}
          tag={editing?.triggerConfigured ? t('trigger.activeTag') : undefined}
        />
      ),
      children: (
        <div className="pt-1">
          <p className="text-xs text-[var(--fg-muted)] mb-4">
            {t('editor.tabTriggerDesc')}
          </p>
          <TriggerSection
            editing={editing}
            triggerConfigured={triggerEnabled}
            revealedToken={revealedToken}
            onGenerate={onGenerate}
            onDisable={onDisable}
          />
        </div>
      ),
    },
  ]

  return (
    <Modal
      title={editing ? t('editor.editTitle') : t('editor.newTitle')}
      open={open}
      onOk={handleSubmit}
      onCancel={onClose}
      okText={editing ? t('editor.save') : t('editor.create')}
      cancelText={t('actions.cancel', { ns: 'common' })}
      destroyOnClose
      width={720}
      confirmLoading={isPending}
    >
      <Form form={form} layout="vertical" preserve={false}>
        <Tabs items={tabItems} />
      </Form>
    </Modal>
  )
}
