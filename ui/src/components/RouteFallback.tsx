import { Skeleton } from 'antd'
import { useTranslation } from 'react-i18next'

export function RouteFallback({ variant = 'page' }: { variant?: 'page' | 'table' | 'drawer' }) {
  const { t } = useTranslation('common')

  if (variant === 'drawer') {
    return (
      <div className="space-y-4 p-1" role="status" aria-label={t('status.loading')}>
        <Skeleton active paragraph={{ rows: 1 }} title={false} />
        <Skeleton active paragraph={{ rows: 6 }} />
      </div>
    )
  }

  if (variant === 'table') {
    return (
      <div
        className="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--bg-elevated)]"
        role="status"
        aria-label={t('status.loading')}
      >
        <div className="px-4 py-3 border-b border-[var(--border)]">
          <Skeleton.Input active size="small" />
        </div>
        <div className="p-4 space-y-3">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} active paragraph={{ rows: 1 }} title={false} />
          ))}
        </div>
      </div>
    )
  }

  return (
    <div
      className="flex-1 flex flex-col px-8 py-8 max-w-6xl w-full mx-auto"
      role="status"
      aria-label={t('status.loading')}
    >
      <div className="flex items-center justify-between mb-6">
        <div className="space-y-2">
          <Skeleton.Input active size="large" />
          <Skeleton.Input active size="small" />
        </div>
        <Skeleton.Button active />
      </div>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
        {Array.from({ length: 4 }).map((_, i) => (
          <div
            key={i}
            className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4"
          >
            <Skeleton active paragraph={{ rows: 1 }} title={{ width: '40%' }} />
          </div>
        ))}
      </div>
      <Skeleton active paragraph={{ rows: 4 }} />
    </div>
  )
}