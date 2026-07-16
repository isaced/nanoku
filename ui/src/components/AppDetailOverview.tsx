import { Tag } from 'antd'
import {
  Bell,
  Box,
  Clock,
  Container as ContainerIcon,
  Network,
  Play,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { App as AppType, EnvVar, Volume } from '../lib/types'
import type { ReactNode } from 'react'

/**
 * AppDetailOverview is the "Overview" tab in the app detail drawer.
 * Renders a read-only view of the app's identity, the optional
 * trigger URL, and the env / volume lists. Editing lives in
 * AppEditorModal — the drawer's job is to *show* the current
 * state, not to let the user mutate it.
 */
export function AppDetailOverview({
  app,
  env,
  volumes,
}: {
  app: AppType
  env: EnvVar[]
  volumes: Volume[]
}) {
  const { t } = useTranslation('apps')
  // Image + internal port only apply to docker-mode apps. Compose
  // apps pull images from the compose YAML and route to per-service
  // ports via exposed_ports — the App-level image/port fields stay
  // empty and would render as confusing blanks, so we hide them.
  const isCompose = app.deployMethod === 'compose'
  return (
    <div className="space-y-4 text-sm">
      <div className="space-y-3">
        {!isCompose && (
          <>
            <Field
              label={t('detail.image')}
              value={app.image}
              mono
              icon={<Box size={14} />}
            />
            <Field
              label={t('detail.internalPort')}
              value={String(app.port)}
              mono
              icon={<Network size={14} />}
            />
          </>
        )}
        <Field
          label={t('detail.created')}
          value={app.createdAt}
          mono
          icon={<Clock size={14} />}
        />
        {app.container && (
          <>
            <Field
              label={t('detail.container')}
              value={app.container.name}
              mono
              icon={<ContainerIcon size={14} />}
            />
            <Field
              label={t('detail.started')}
              value={app.container.startedAt ?? '—'}
              mono
              icon={<Play size={14} />}
            />
          </>
        )}
      </div>

      {app.triggerConfigured && (
        <div className="border border-[var(--border)] rounded-md p-3 bg-[var(--bg-input)]/40">
          <div className="flex items-center gap-2 mb-2">
            <Bell size={13} className="text-[var(--fg-muted)]" />
            <span className="eyebrow">
              {t('detail.trigger')}
            </span>
            <Tag color="green" className="!m-0 ml-auto">
              {t('detail.triggerActive')}
            </Tag>
          </div>
          <div className="mono text-xs text-[var(--fg-muted)] break-all">
            POST {window.location.origin}/api/apps/{app.name}/trigger
          </div>
        </div>
      )}

      <ReadOnlyBlock title={t('detail.envVars')} count={env.length}>
        {env.length === 0 ? (
          <div className="text-xs text-[var(--fg-muted)] py-2">
            {t('detail.noEnvVars')}
          </div>
        ) : (
          <div className="space-y-1">
            {env.map((row, i) => (
              <div
                key={i}
                className="flex items-center gap-2 text-xs mono py-0.5"
              >
                <span className="text-[var(--fg-muted)] w-44 shrink-0 truncate">
                  {row.key}
                </span>
                <span className="text-[var(--fg)] break-all">
                  {row.value}
                </span>
              </div>
            ))}
          </div>
        )}
      </ReadOnlyBlock>

      {app.deployMethod === 'docker' && (
        <ReadOnlyBlock title={t('detail.volumes')} count={volumes.length}>
          {volumes.length === 0 ? (
            <div className="text-xs text-[var(--fg-muted)] py-2">
              {t('detail.noVolumes')}
            </div>
          ) : (
            <div className="space-y-2">
              {volumes.map((v, i) => (
                <div
                  key={i}
                  className="flex items-center gap-2 text-xs mono border border-[var(--border)] rounded-md px-2 py-1 bg-[var(--bg-input)]"
                >
                  <Tag className="!m-0" color={v.type === 'bind' ? 'purple' : 'default'}>
                    {v.type === 'bind' ? t('detail.typeBind') : t('detail.typeVolume')}
                  </Tag>
                  <span className="text-[var(--fg)] break-all flex-1">
                    {v.source || (
                      <span className="text-[var(--fg-muted)]">
                        {t('volumeEditor.autoBadge')}
                      </span>
                    )}
                    <span className="text-[var(--fg-muted)] mx-1">→</span>
                    <span>{v.target}</span>
                  </span>
                  {v.readOnly && (
                    <Tag className="!m-0">{t('detail.readOnly')}</Tag>
                  )}
                </div>
              ))}
              {app.deleteVolumesOnRemove && (
                <div className="text-xs text-[var(--fg-muted)]">
                  {t('detail.deleteVolumesOnRemove')}
                </div>
              )}
            </div>
          )}
        </ReadOnlyBlock>
      )}

      {app.deployMethod === 'compose' && (
        <ReadOnlyBlock title={t('detail.volumes')} count={0}>
          <div className="text-xs text-[var(--fg-muted)] py-2">
            {t('detail.composeModeHint')}
          </div>
        </ReadOnlyBlock>
      )}
    </div>
  )
}

function ReadOnlyBlock({
  title,
  count,
  children,
}: {
  title: string
  count: number
  children: ReactNode
}) {
  const { t } = useTranslation('apps')
  return (
    <div className="border border-[var(--border)] rounded-md p-3 bg-[var(--bg-input)]/40">
      <div className="flex items-center gap-2 mb-2">
        <span className="eyebrow">
          {title}
        </span>
        <Tag className="!m-0">{count}</Tag>
        <span className="text-xs text-[var(--fg-muted)] ml-auto">
          {t('detail.editInEditor')}
        </span>
      </div>
      {children}
    </div>
  )
}

function Field({
  label,
  value,
  mono,
  icon,
}: {
  label: string
  value: string
  mono?: boolean
  icon?: ReactNode
}) {
  return (
    <div className="flex items-center gap-3 min-h-[22px]">
      {icon && (
        <span className="text-[var(--fg-muted)] shrink-0 inline-flex items-center justify-center w-3.5">
          {icon}
        </span>
      )}
      <span className="eyebrow">
        {label}
      </span>
      <span className={`${mono ? 'mono text-xs' : 'text-sm'} leading-none`}>{value}</span>
    </div>
  )
}
