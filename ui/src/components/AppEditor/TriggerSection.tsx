import { Alert, Button, Popconfirm } from 'antd'
import { RefreshCw, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { App as AppType } from '../../lib/types'
import { CopyableValue } from '../CodeBlock'
import { TriggerUsage } from '../TriggerUsage'

// TriggerSection renders the trigger configuration for an app. The token
// is generated on demand via the "generate / rotate" action and shown
// inline exactly once (GitHub-OAuth-App style): it lives only in React
// state passed down as `revealedToken` and is gone the moment the editor
// closes or the app is switched — never persisted client-side.
//
// In create mode (`editing === null`) the app doesn't exist yet, so no
// token can be minted here; we just point the operator to the trigger tab
// after the app is created.
export function TriggerSection({
  editing,
  triggerConfigured,
  revealedToken,
  onGenerate,
  onDisable,
}: {
  editing: AppType | null
  triggerConfigured: boolean
  revealedToken: string | null
  onGenerate: () => void
  onDisable: () => void
}) {
  const { t } = useTranslation('apps')
  const url = editing
    ? `${window.location.origin}/api/apps/${editing.name}/trigger`
    : ''

  if (!editing) {
    return (
      <div className="text-xs text-[var(--fg-muted)]">{t('trigger.createHint')}</div>
    )
  }

  return (
    <div className="space-y-3">
      {!triggerConfigured ? (
        <div className="space-y-3">
          <div className="text-xs text-[var(--fg-muted)]">{t('trigger.notEnabledHint')}</div>
          <Button size="small" type="primary" icon={<RefreshCw size={12} />} onClick={onGenerate}>
            {t('trigger.generate')}
          </Button>
        </div>
      ) : (
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
          <div className="flex items-center gap-2">
            <Popconfirm
              title={t('trigger.rotateTitle')}
              description={t('trigger.rotateDescription')}
              okText={t('trigger.rotateButton')}
              onConfirm={onGenerate}
            >
              <Button size="small" icon={<RefreshCw size={12} />}>
                {t('trigger.rotateButton')}
              </Button>
            </Popconfirm>
            <Popconfirm
              title={t('trigger.disableTitle')}
              description={t('trigger.disableDescription')}
              okText={t('trigger.disable')}
              onConfirm={onDisable}
            >
              <Button size="small" danger icon={<Trash2 size={12} />}>
                {t('trigger.disable')}
              </Button>
            </Popconfirm>
          </div>
          <div className="text-[11px] text-[var(--fg-muted)]">{t('trigger.disableHint')}</div>
        </div>
      )}

      {revealedToken && (
        <Alert
          type="warning"
          showIcon
          message={t('trigger.tokenWarning')}
          description={<CopyableValue value={revealedToken} label={t('trigger.tokenLabel')} />}
        />
      )}
    </div>
  )
}
