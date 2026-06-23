import { Tooltip } from 'antd'
import { CircleCheck, CircleDashed } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function SiteStatusBadge({ enabled }: { enabled: boolean }) {
  const { t } = useTranslation('common')
  return (
    <Tooltip
      title={
        enabled
          ? t('common.enabled')
          : t('status.disabled')
      }
    >
      <span
        className={`inline-flex ${
          enabled
            ? 'text-[var(--success)]'
            : 'text-[var(--fg-muted)]'
        }`}
      >
        {enabled ? <CircleCheck size={13} /> : <CircleDashed size={13} />}
      </span>
    </Tooltip>
  )
}
