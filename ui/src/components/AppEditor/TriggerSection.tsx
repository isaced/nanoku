import { Button, Form, Checkbox, Popconfirm, type FormInstance } from 'antd'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { App as AppType, AppInput } from '../../lib/types'
import { CopyableValue } from '../CodeBlock'
import { TriggerUsage } from '../TriggerUsage'

export function TriggerSection({
  editing,
  onRotate,
  form,
}: {
  editing: AppType | null
  onRotate: () => void
  form: FormInstance<AppInput>
}) {
  const { t } = useTranslation('apps')
  const url = editing
    ? `${window.location.origin}/api/apps/${editing.name}/trigger`
    : ''
  // `enableTrigger` is a real on/off for the feature — enabling generates
  // a token, disabling clears it — so it is NOT just a fold switch. We
  // dropped the Switch in favor of an explicit enable/disable button and
  // keep the field registered via a hidden Form.Item so it still submits.
  // The content below reacts instantly to the toggle via Form.useWatch.
  const enabled = Boolean(Form.useWatch('enableTrigger', form))
  function toggle() {
    form.setFieldValue('enableTrigger', !enabled)
  }
  return (
    <div className="space-y-3">
      <Form.Item name="enableTrigger" valuePropName="checked" hidden>
        <Checkbox />
      </Form.Item>

      <div className="flex items-center justify-between gap-3 rounded-lg border border-[var(--border)] bg-[var(--bg-input)]/30 px-3 py-2">
        <div className="min-w-0">
          <div className="text-sm text-[var(--fg)]">{t('trigger.enableTitle')}</div>
          <div className="text-xs text-[var(--fg-muted)]">{t('trigger.enableHint')}</div>
        </div>
        <Button size="small" type={enabled ? 'default' : 'primary'} onClick={toggle}>
          {enabled ? t('trigger.disable') : t('trigger.enable')}
        </Button>
      </div>

      {enabled ? (
        editing ? (
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
        ) : (
          <div className="text-xs text-[var(--fg-muted)]">{t('trigger.enableExtra')}</div>
        )
      ) : (
        <div className="text-xs text-[var(--fg-muted)]">{t('trigger.enableExtra')}</div>
      )}
    </div>
  )
}
