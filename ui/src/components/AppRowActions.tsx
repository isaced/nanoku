import { Button, Space, Tooltip } from 'antd'
import {
  Container as ContainerIcon,
  Pencil,
  Play,
  RefreshCw,
  Rocket,
  Square,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { appLifecycle, type AppActions } from '../lib/hooks/mutations'
import { useAction } from '../lib/hooks'
import type { App as AppType } from '../lib/types'

/**
 * AppRowActions is the per-row action column in the apps list.
 * The 7 buttons + the appLifecycle() branching previously lived
 * inline in routes/apps.tsx; pulling them out keeps the table
 * definition readable and makes the row action contract
 * (which buttons render for which state) testable in isolation.
 *
 * The component owns the start/stop/restart lifecycle mutations
 * (via `actions`) and uses useAction internally to handle the
 * success/error toasts. The deploy button is the only one the
 * parent owns — the trigger-deploy success path needs to open
 * the detail drawer, which only the parent can do.
 */
export function AppRowActions({
  app,
  actions,
  deployPending,
  onTriggerDeploy,
  onEdit,
  onDelete,
}: {
  app: AppType
  actions: AppActions
  deployPending: boolean
  onTriggerDeploy: (app: AppType) => void
  onEdit: (app: AppType) => void
  onDelete: (app: AppType) => void
}) {
  const { t } = useTranslation('apps')
  const action = useAction()
  const lc = appLifecycle(app.container)

  function runLifecycle(name: 'stopped' | 'started' | 'restarted', mutate: () => Promise<unknown>) {
    void action.run(mutate(), {
      success: t('toast.' + name, { name: app.name }),
    })
  }

  return (
    <Space size={4}>
      {!app.container && (
        <Tooltip title={t('table.actions.deploy')}>
          <Button
            type="text"
            size="small"
            loading={deployPending}
            icon={<Rocket size={14} />}
            onClick={() => onTriggerDeploy(app)}
            aria-label={t('table.actions.deploy')}
          />
        </Tooltip>
      )}
      {lc.isLive && (
        <>
          <Tooltip title={t('table.actions.stop')}>
            <Button
              type="text"
              size="small"
              loading={actions.stop.isPending && actions.stop.variables === app.id}
              icon={<Square size={14} />}
              onClick={() => runLifecycle('stopped', () => actions.stop.mutateAsync(app.id))}
              aria-label={t('table.actions.stop')}
            />
          </Tooltip>
          {!lc.isRestarting && (
            <Tooltip title={t('table.actions.restart')}>
              <Button
                type="text"
                size="small"
                loading={
                  actions.restart.isPending && actions.restart.variables === app.id
                }
                icon={<RefreshCw size={14} />}
                onClick={() => runLifecycle('restarted', () => actions.restart.mutateAsync(app.id))}
                aria-label={t('table.actions.restart')}
              />
            </Tooltip>
          )}
        </>
      )}
      {lc.isStopped && (
        <Tooltip title={t('table.actions.start')}>
          <Button
            type="text"
            size="small"
            loading={
              actions.start.isPending && actions.start.variables === app.id
            }
            icon={<Play size={14} />}
            onClick={() => runLifecycle('started', () => actions.start.mutateAsync(app.id))}
            aria-label={t('table.actions.start')}
          />
        </Tooltip>
      )}
      {app.container && (
        <Tooltip title={t('table.actions.redeploy')}>
          <Button
            type="text"
            size="small"
            loading={deployPending}
            icon={<ContainerIcon size={14} />}
            onClick={() => onTriggerDeploy(app)}
            aria-label={t('table.actions.redeploy')}
          />
        </Tooltip>
      )}
      <Tooltip title={t('table.actions.edit')}>
        <Button
          type="text"
          size="small"
          icon={<Pencil size={14} />}
          onClick={() => onEdit(app)}
          aria-label={t('table.actions.edit')}
        />
      </Tooltip>
      <Tooltip title={t('table.actions.delete')}>
        <Button
          type="text"
          size="small"
          icon={<Trash2 size={14} />}
          onClick={() => onDelete(app)}
          aria-label={t('table.actions.delete')}
        />
      </Tooltip>
    </Space>
  )
}
