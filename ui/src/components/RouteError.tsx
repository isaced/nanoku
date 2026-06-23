import { Alert, Button } from 'antd'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function RouteError({ error, reset }: { error: unknown; reset?: () => void }) {
  const { t } = useTranslation('common')
  const message =
    error instanceof Error
      ? error.message
      : typeof error === 'string'
        ? error
        : t('error.unknown', { defaultValue: 'Unknown error' })

  return (
    <div className="flex-1 flex items-start justify-center px-8 py-12">
      <Alert
        type="error"
        showIcon
        message={t('error.loadFailed', { defaultValue: 'Failed to load' })}
        description={message}
        action={
          reset && (
            <Button
              size="small"
              icon={<RefreshCw size={12} />}
              onClick={reset}
            >
              {t('actions.retry', { defaultValue: 'Retry' })}
            </Button>
          )
        }
        className="max-w-xl"
      />
    </div>
  )
}