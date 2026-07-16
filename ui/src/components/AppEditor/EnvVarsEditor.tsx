import { Button, Input } from 'antd'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useFieldArray } from '../../lib/hooks/useFieldArray'
import type { EnvVar } from '../../lib/types'

// EnvVarsEditor is an always-open editor (no collapse switch): it lives
// in its own "Environment" tab, so the tab itself is the on/off switch
// for visibility. The row count is surfaced as a badge on the tab label
// instead of a switch.
export function EnvVarsEditor({
  rows,
  onChange,
}: {
  rows: EnvVar[]
  onChange: (next: EnvVar[]) => void
}) {
  const { t } = useTranslation('apps')
  const { set, add, remove } = useFieldArray<EnvVar>(rows, onChange)
  return (
    <div className="space-y-3">
      {rows.length === 0 && (
        <div className="text-xs text-[var(--fg-muted)] py-1">
          {t('editor.envVarsEmpty')}
        </div>
      )}
      <div className="space-y-2">
        {rows.map((row, i) => (
          <div
            key={i}
            className="flex items-center gap-2 border border-[var(--border)] rounded-md p-2 bg-[var(--bg-input)]"
          >
            <Input
              className="!w-40 mono text-xs"
              placeholder={t('detail.keyPlaceholder')}
              value={row.key}
              onChange={(e) => set(i, { key: e.target.value })}
            />
            <Input
              className="flex-1 mono text-xs"
              placeholder={t('detail.valuePlaceholder')}
              value={row.value}
              onChange={(e) => set(i, { value: e.target.value })}
            />
            <Button
              type="text"
              size="small"
              icon={<Trash2 size={13} />}
              onClick={() => remove(i)}
            />
          </div>
        ))}
      </div>
      <Button
        type="dashed"
        size="small"
        icon={<Plus size={13} />}
        onClick={() => add({ key: '', value: '' })}
      >
        {t('detail.addVar')}
      </Button>
    </div>
  )
}
