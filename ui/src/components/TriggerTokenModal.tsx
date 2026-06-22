import { Bell } from 'lucide-react'
import { Alert, Modal } from 'antd'
import { Trans, useTranslation } from 'react-i18next'
import { CopyableValue } from './CodeBlock'
import { TriggerUsage } from './TriggerUsage'

export function TriggerTokenModal({
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
