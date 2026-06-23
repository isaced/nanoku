import { useTranslation } from 'react-i18next'

function Bar({
  width,
  height = 14,
  className = '',
}: {
  width: string | number
  height?: number
  className?: string
}) {
  return (
    <div
      aria-hidden
      style={{ width: typeof width === 'number' ? `${width}px` : width, height }}
      className={`bg-[var(--bg-elevated)] rounded ${className}`}
    />
  )
}

function PageHeaderSkeleton() {
  return (
    <div className="flex items-center justify-between mb-6">
      <div className="space-y-2">
        <Bar width={180} height={28} />
        <Bar width={320} height={14} />
      </div>
      <Bar width={104} height={32} />
    </div>
  )
}

function StatCardsSkeleton() {
  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
      {Array.from({ length: 4 }).map((_, i) => (
        <div
          key={i}
          className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4 h-[96px] flex flex-col justify-between"
        >
          <Bar width={88} height={12} />
          <div className="space-y-2">
            <Bar width={60} height={22} />
            <Bar width={104} height={10} />
          </div>
        </div>
      ))}
    </div>
  )
}

const TABLE_COLUMNS: { width: string; barWidth: number }[] = [
  { width: '30%', barWidth: 160 },
  { width: '14%', barWidth: 72 },
  { width: '26%', barWidth: 140 },
  { width: '10%', barWidth: 48 },
  { width: '12%', barWidth: 96 },
  { width: '8%', barWidth: 56 },
]

function TableSkeleton({ rows = 6 }: { rows?: number }) {
  return (
    <div className="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--bg-elevated)]">
      <div className="flex items-center px-4 h-10 border-b border-[var(--border)] bg-[var(--bg)]">
        {TABLE_COLUMNS.map((c, i) => (
          <div key={i} style={{ width: c.width }} className="pr-3">
            <Bar width={c.barWidth * 0.6} height={10} />
          </div>
        ))}
      </div>
      <div className="bg-[var(--bg)]">
        {Array.from({ length: rows }).map((_, r) => (
          <div
            key={r}
            className="flex items-center px-4 h-12 border-b border-[var(--border)] last:border-b-0"
          >
            {TABLE_COLUMNS.map((c, i) => (
              <div key={i} style={{ width: c.width }} className="pr-3">
                <Bar
                  width={c.barWidth * (0.55 + ((r + i) % 3) * 0.15)}
                  height={12}
                />
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}

export function RouteFallback({
  variant = 'page',
  rows,
}: {
  variant?: 'page' | 'table' | 'drawer'
  rows?: number
}) {
  const { t } = useTranslation('common')

  if (variant === 'drawer') {
    return (
      <div className="space-y-4 p-1" role="status" aria-label={t('status.loading')}>
        <div className="space-y-2">
          <Bar width="55%" height={20} />
          <Bar width="35%" height={12} />
        </div>
        {Array.from({ length: 5 }).map((_, i) => (
          <Bar key={i} width={i % 2 === 0 ? '92%' : '78%'} height={12} />
        ))}
        <div className="pt-2 space-y-2">
          <Bar width="40%" height={14} />
          <Bar width="88%" height={12} />
          <Bar width="64%" height={12} />
        </div>
      </div>
    )
  }

  if (variant === 'table') {
    return (
      <div role="status" aria-label={t('status.loading')}>
        <TableSkeleton rows={rows ?? 6} />
      </div>
    )
  }

  return (
    <div
      className="flex-1 flex flex-col px-8 py-8 max-w-6xl w-full mx-auto"
      role="status"
      aria-label={t('status.loading')}
    >
      <PageHeaderSkeleton />
      <StatCardsSkeleton />
      <TableSkeleton rows={rows ?? 5} />
    </div>
  )
}
