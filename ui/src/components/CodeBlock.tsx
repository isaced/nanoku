import { Copy } from 'lucide-react'
import { Button, Input } from 'antd'
import { useTranslation } from 'react-i18next'

export function CodeBlock({ value }: { value: string }) {
  const { t } = useTranslation('common')
  return (
    <div className="space-y-1">
      <Button
        size="small"
        icon={<Copy size={12} />}
        onClick={() => {
          void navigator.clipboard.writeText(value)
        }}
        aria-label={t('actions.copy')}
      >
        {t('actions.copy')}
      </Button>
      <pre className="mono text-[11px] leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-3 overflow-auto max-h-48 whitespace-pre text-[var(--fg-muted)]">
        {value}
      </pre>
    </div>
  )
}

export function CopyableValue({ value, label }: { value: string; label: string }) {
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
        aria-label={t('actions.copy')}
      >
        {t('actions.copy')}
      </Button>
    </div>
  )
}
