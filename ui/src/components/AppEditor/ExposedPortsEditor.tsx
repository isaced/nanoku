import { App, Button, Input, InputNumber, Tooltip } from 'antd'
import { Plus, Trash2, Wand2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useFieldArray } from '../../lib/hooks/useFieldArray'
import { useAction, useImportExposedPorts } from '../../lib/hooks'
import type { ExposedPort } from '../../lib/types'

// ExposedPortsEditor is the compose-mode-only editor that lets the
// user declare which services in the compose stack Caddy should be
// able to reverse-proxy to. Each row maps a service name (must match
// a `services:` key in the YAML) to the container-side port the
// service listens on. The downstream sites editor renders a service
// select from this list.
//
// We use component state (rows) rather than Form.List because the
// field is a nested array of objects — Form.List works fine for it
// but the local-state pattern matches the env/volume editors above
// and keeps the cleanup logic (drop incomplete rows) in one place.
//
// The "Import from compose" button asks the server to parse the
// stored compose YAML and return a scaffolding list. It's only
// available when editing an existing app (the server endpoint
// requires an app id); for a new app, the user has to save the
// compose content first and then re-open the editor to import.
export function ExposedPortsEditor({
  rows,
  onChange,
  appId,
}: {
  rows: ExposedPort[]
  onChange: (next: ExposedPort[]) => void
  appId?: number
}) {
  const { t } = useTranslation('apps')
  const { message } = App.useApp()
  const action = useAction()
  const importMutation = useImportExposedPorts()
  const { set, add, remove } = useFieldArray<ExposedPort>(rows, onChange)
  const count = rows.length
  const canImport = appId != null
  // doImport runs the server-side parser and replaces the current
  // draft with the result. The user is expected to review before
  // submitting — the import is scaffolding, not a commit. The
  // toast gives a quick "X imported, Y need a port" summary so
  // the operator knows whether to look at the port column.
  function doImport() {
    if (appId == null) return
    void action.run(importMutation.mutateAsync(appId), {
      onSuccess: (resp) => {
        onChange(resp.exposedPorts)
        const needPort = resp.exposedPorts.filter((p) => p.port <= 0).length
        if (needPort > 0) {
          message.info(
            t('editor.exposedPortImportNeedsPort', {
              total: resp.exposedPorts.length,
              need: needPort,
            }),
          )
        } else {
          message.success(
            t('editor.exposedPortImportOk', { count: resp.exposedPorts.length }),
          )
        }
      },
    })
  }
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-[var(--fg-muted)]">
          {count > 0
            ? t('editor.exposedPortsCount', { count })
            : t('editor.exposedPortsEmpty')}
        </span>
        {canImport && (
          <Tooltip title={t('editor.exposedPortImportTooltip')}>
            <Button
              size="small"
              icon={<Wand2 size={13} />}
              onClick={doImport}
              loading={importMutation.isPending}
            >
              {t('editor.exposedPortImport')}
            </Button>
          </Tooltip>
        )}
      </div>
      <div className="space-y-2">
        {rows.map((row, i) => (
          <div
            key={i}
            className="flex items-center gap-2 border border-[var(--border)] rounded-md p-2 bg-[var(--bg-input)]"
          >
            <Input
              className="!w-40 mono text-xs"
              placeholder={t('editor.exposedPortNamePlaceholder')}
              value={row.name}
              onChange={(e) => set(i, { name: e.target.value })}
            />
            <InputNumber
              className="!w-28"
              min={1}
              max={65535}
              placeholder="3000"
              value={row.port}
              onChange={(v) => set(i, { port: Number(v) || 0 })}
            />
            <span className="text-[10px] text-[var(--fg-muted)] flex-1">
              {t('editor.exposedPortExtra')}
            </span>
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
        onClick={() => add({ name: '', port: 80 })}
      >
        {t('editor.exposedPortAdd')}
      </Button>
    </div>
  )
}
