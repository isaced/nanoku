import { Trans, useTranslation } from 'react-i18next'
import { CodeBlock } from './CodeBlock'
import { buildTriggerCurl, buildWorkflowYaml } from '../lib/trigger-snippets'

// TriggerUsage renders the curl + workflow YAML examples. `token` is the
// real bearer credential; when omitted, the curl uses
// `$NANOKU_TRIGGER_TOKEN` as a placeholder so the editing view (which
// doesn't have the token) can show the same shape.
export function TriggerUsage({
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
        <div className="eyebrow">
          {t('trigger.curlTitle')}
        </div>
        <CodeBlock value={curl} />
      </div>
      <div>
        <div className="eyebrow">
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
