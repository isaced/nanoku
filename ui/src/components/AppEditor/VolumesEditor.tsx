import { Button, Checkbox, Form, Input, Select, Switch, Tag, Tooltip } from 'antd'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { VolumeInput } from '../../lib/types'

// VolumesEditor mirrors EnvVarsEditor: always-open, lives in the
// "Storage" tab (docker mode only). The deleteVolumesOnRemove checkbox
// rides along as a Form.Item so it submits with the rest of the form.
export function VolumesEditor({
  rows,
  onChange,
}: {
  rows: VolumeInput[]
  onChange: (next: VolumeInput[]) => void
}) {
  const { t } = useTranslation('apps')
  function setRow(i: number, patch: Partial<VolumeInput>) {
    onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }
  function addRow() {
    onChange([...rows, { type: 'volume', target: '' }])
  }
  function delRow(i: number) {
    onChange(rows.filter((_, idx) => idx !== i))
  }
  return (
    <div className="space-y-3">
      {rows.length === 0 && (
        <div className="text-xs text-[var(--fg-muted)] py-1">
          {t('editor.volumesEmpty')}
        </div>
      )}
      <div className="space-y-2">
        {rows.map((row, i) => {
          const isAuto = !row.source && row.type === 'volume'
          return (
            <div
              key={i}
              className="flex items-center gap-2 border border-[var(--border)] rounded-md p-2 bg-[var(--bg-input)]"
            >
              <Select
                className="!w-32"
                value={row.type ?? 'volume'}
                onChange={(v) => setRow(i, { type: v })}
                options={[
                  { value: 'volume', label: t('volumeEditor.typeVolume') },
                  { value: 'bind', label: t('volumeEditor.typeBind') },
                ]}
              />
              <Input
                className="!w-56 mono text-xs"
                placeholder={
                  row.type === 'bind'
                    ? t('volumeEditor.sourceBindPlaceholder')
                    : t('volumeEditor.sourceVolumePlaceholder')
                }
                value={row.source ?? ''}
                onChange={(e) => setRow(i, { source: e.target.value })}
              />
              <Input
                className="flex-1 mono text-xs"
                placeholder={t('volumeEditor.targetPlaceholder')}
                value={row.target ?? ''}
                onChange={(e) => setRow(i, { target: e.target.value })}
              />
              <Tooltip title={t('volumeEditor.readOnly')}>
                <Switch
                  checked={!!row.readOnly}
                  onChange={(v) => setRow(i, { readOnly: v })}
                />
              </Tooltip>
              {isAuto && (
                <Tooltip title={t('volumeEditor.autoHint')}>
                  <Tag className="!m-0 mono text-[10px]">
                    {t('volumeEditor.autoBadge')}
                  </Tag>
                </Tooltip>
              )}
              <Button
                type="text"
                size="small"
                icon={<Trash2 size={13} />}
                onClick={() => delRow(i)}
              />
            </div>
          )
        })}
      </div>
      <Button
        type="dashed"
        size="small"
        icon={<Plus size={13} />}
        onClick={addRow}
      >
        {t('volumeEditor.addRow')}
      </Button>
      <Form.Item
        name="deleteVolumesOnRemove"
        valuePropName="checked"
        extra={t('editor.deleteVolumesOnRemoveExtra')}
        className="!mb-0 mt-1"
      >
        <Checkbox>{t('editor.deleteVolumesOnRemove')}</Checkbox>
      </Form.Item>
    </div>
  )
}
