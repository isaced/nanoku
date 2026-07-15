import { createFileRoute, redirect } from '@tanstack/react-router'
import { Suspense, useEffect, useMemo, useRef, useState } from 'react'
import {
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Skeleton,
  Table,
  Tag,
  Tooltip,
} from 'antd'
import {
  Pencil,
  Plus,
  RefreshCw,
  ToggleLeft,
  ToggleRight,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ensureAuth, isAuthenticated } from '../lib/auth'
import { isValidDomain } from '../lib/domain-validation'
import {
  useCreateSite,
  useDeleteSite,
  useSuspenseApps,
  useSuspenseCaddyfile,
  useSuspenseSites,
  useSuspenseStatus,
  useToggleSite,
  useUpdateSite,
} from '../lib/hooks'
import type { App as AppType, Site } from '../lib/types'
import { QueryErrorBoundary } from '../components/QueryErrorBoundary'
import { SiteStatusBadge } from '../components/SiteStatusBadge'
import { RouteError } from '../components/RouteError'
import { RouteFallback } from '../components/RouteFallback'

export const Route = createFileRoute('/sites')({
  beforeLoad: async () => {
    if (isAuthenticated()) return
    if (!(await ensureAuth())) {
      throw redirect({ to: '/login' })
    }
  },
  component: SitesPage,
  errorComponent: RouteError,
})

function SitesPage() {
  return (
    <Suspense fallback={<RouteFallback variant="page" />}>
      <SitesPageContent />
    </Suspense>
  )
}

// Form shape for the site editor. The upstream field is shown as
// read-only when an app is linked (it gets auto-resolved server-side
// at submit time), and editable when the user picked the "no app ·
// custom upstream" route. appService is the compose-only service
// selector; it's only validated server-side against the linked app's
// exposed_ports, so the type stays loose.
type SiteFormValues = {
  domain: string
  upstream: string
  appId?: number
  appService?: string
  scheme: 'http' | 'https'
}

// appOptionLabel renders the row shown in the App select. Docker
// apps get the legacy `name · image :port` shape; compose apps
// describe themselves by service count so the operator can see at a
// glance whether the stack has 1 service (auto-pick) or N services
// (will need a service pick). The "(no app · use custom upstream)"
// entry is appended as a sentinel with a special id of 0; the form
// maps it to a clearApp / empty upstream.
export function appOptionLabel(a: AppType): string {
  if (a.deployMethod === 'docker') {
    return `${a.name} · docker · ${a.image || '(no image)'} :${a.port}`
  }
  const n = a.exposedPorts?.length ?? 0
  if (n === 0) {
    return `${a.name} · compose · (no exposed ports)`
  }
  if (n === 1) {
    return `${a.name} · compose · ${n} service: ${a.exposedPorts![0].name}:${a.exposedPorts![0].port}`
  }
  return `${a.name} · compose · ${n} services`
}

// serviceOptionLabel formats a single compose service for the
// service select. Mirrors appOptionLabel's terseness.
export function serviceOptionLabel(name: string, port: number): string {
  return `${name} · port ${port}`
}

// computeUpstreamForAppUI mirrors the server's computeUpstreamForApp
// (internal/api/sites.go) so the form can preview the locked upstream
// the moment the operator picks an app. Without it, the disabled
// upstream input stays empty after `appId` changes — the user sees a
// blank field, has no way to verify the value the server will compute
// on submit, and for multi-service compose the server would 400 on
// `appService` until they noticed. The compose branch uses the
// canonical `nanoku-<app>-<service>-1:<port>` shape that compose
// assigns by default; the docker branch needs the app's current
// container name (carried on the App DTO as `container.name`) to
// reconstruct the legacy `<container>:<port>` upstream. Returns ""
// when the inputs are insufficient — callers should render that as
// "not yet determined", not as an error.
export function computeUpstreamForAppUI(
  app: AppType | undefined,
  appService: string | undefined,
): string {
  if (!app) return ''
  if (app.deployMethod === 'compose') {
    if (!appService) return ''
    const ports = app.exposedPorts ?? []
    const ep = ports.find((p) => p.name === appService)
    if (!ep) return ''
    return `nanoku-${app.name}-${appService}-1:${ep.port}`
  }
  // docker mode
  if (!app.container) return ''
  return `${app.container.name}:${app.port}`
}

function SitesPageContent() {
  const { message, modal } = App.useApp()
  const { t } = useTranslation('sites')
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<Site | null>(null)
  const [form] = Form.useForm<SiteFormValues>()

  const sitesQuery = useSuspenseSites()
  const statusQuery = useSuspenseStatus()
  const appsQuery = useSuspenseApps()
  const createSite = useCreateSite()
  const updateSite = useUpdateSite()
  const deleteSite = useDeleteSite()
  const toggleSite = useToggleSite()

  const sites = sitesQuery.data
  const status = statusQuery.data
  const apps = appsQuery.data

  const fetching = sitesQuery.isFetching || statusQuery.isFetching

  // Sync `upstream` (and the auto-picked `appService` for single-service
  // compose) every time the operator picks a new app or service. The
  // upstream field is disabled when linked, so the only way the user
  // can see what value the server will compute on submit is to have
  // us mirror it into the form here. We also auto-pick when a compose
  // app has exactly one service: that case used to be hidden in the
  // UI (the server's auto-pick did the work), but the user wants the
  // select visible for consistency. We still pick the lone service
  // on the user's behalf so the save path stays a one-click action.
  //
  // The "switched apps" branch is detected by comparing the current
  // appId to the last one we synced for. Without that check, changing
  // a service would clobber the user's pick; without skipping when
  // nothing changed, setFieldsValue would loop the effect on every
  // re-render.
  const appIdWatched = Form.useWatch('appId', form)
  const appServiceWatched = Form.useWatch('appService', form)
  const lastSyncedAppIdRef = useRef<number | undefined>(undefined)
  useEffect(() => {
    if (appIdWatched === undefined) {
      // free-upstream mode: no app linked, nothing to derive.
      lastSyncedAppIdRef.current = undefined
      return
    }
    const app = apps.find((a) => a.id === appIdWatched)
    if (!app) return

    // appId changed since last sync: re-derive appService.
    // appId unchanged but appService changed: respect the user's pick.
    let nextService = appServiceWatched
    if (lastSyncedAppIdRef.current !== appIdWatched) {
      if (app.deployMethod === 'compose') {
        const ports = app.exposedPorts ?? []
        // Keep the existing selection only if it's still valid for
        // this app. Otherwise auto-pick (single service) or clear
        // (multi-service, let the user pick).
        if (appServiceWatched && ports.some((p) => p.name === appServiceWatched)) {
          nextService = appServiceWatched
        } else {
          nextService = ports.length === 1 ? ports[0].name : undefined
        }
      } else {
        nextService = undefined
      }
    }

    const upstream = computeUpstreamForAppUI(app, nextService)
    lastSyncedAppIdRef.current = appIdWatched
    // setFieldsValue is a no-op for the form state when nextService
    // and upstream match the current values, so the re-render this
    // effect triggers doesn't loop back into the effect.
    form.setFieldsValue({ appService: nextService, upstream })
  }, [appIdWatched, appServiceWatched, apps, form])

  // Build app options for the editor select. The "no app" sentinel
  // is encoded as id=0, which the API treats identically to the
  // explicit `clearApp: true` flag.
  const appOptions = useMemo(
    () =>
      apps.map((a) => ({
        value: a.id,
        label: appOptionLabel(a),
      })),
    [apps],
  )

  function openCreate() {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ scheme: 'https' })
    setEditorOpen(true)
  }

  function openEdit(s: Site) {
    setEditing(s)
    // The site is "free-upstream" when it has no app_id. We represent
    // that in the form by setting appId to undefined so the upstream
    // field becomes editable.
    form.setFieldsValue({
      domain: s.domain,
      upstream: s.upstream,
      appId: s.appId,
      appService: s.appService,
      scheme: s.scheme,
    })
    setEditorOpen(true)
  }

  function onSubmit() {
    void form.validateFields().then((values) => {
      const isLinked = !!values.appId
      const isFree = !isLinked
      const payload: Parameters<typeof createSite.mutate>[0] = {
        domain: values.domain,
        scheme: values.scheme,
      }
      if (isLinked) {
        payload.appId = values.appId
        if (values.appService) payload.appService = values.appService
        // Upstream is computed server-side; we never send it in the
        // linked branch. (Sending a stale value would be ignored by
        // the API but adds noise to the request body.)
      } else if (isFree) {
        if (values.upstream) payload.upstream = values.upstream
      }
      const onOk = () => {
        setEditorOpen(false)
      }
      const onErr = (err: Error) => {
        message.error(err.message)
      }
      if (editing) {
        // If the user clears the app linkage on an edit, route through
        // the explicit clearApp flag so the server doesn't have to
        // guess the difference between "no change" and "detach".
        if (editing.appId && !values.appId) {
          payload.clearApp = true
          delete payload.appId
        }
        updateSite.mutate(
          { id: editing.id, input: payload },
          { onSuccess: onOk, onError: onErr },
        )
      } else {
        createSite.mutate(payload, {
          onSuccess: (created) => {
            onOk()
            message.success(t('toast.added', { domain: created.domain }))
          },
          onError: onErr,
        })
      }
    })
  }

  function onToggle(s: Site) {
    toggleSite.mutate(s.id, {
      onSuccess: (updated) => {
        message.success(
          s.enabled
            ? t('toast.disabled', { domain: updated.domain })
            : t('toast.enabled', { domain: updated.domain }),
        )
      },
      onError: (err) => {
        message.error(err.message)
      },
    })
  }

  function onDelete(s: Site) {
    modal.confirm({
      title: t('delete.title', { domain: s.domain }),
      content: t('delete.content'),
      okText: t('actions.delete', { ns: 'common' }),
      okType: 'danger',
      onOk: () =>
        new Promise<void>((resolve, reject) => {
          deleteSite.mutate(s.id, {
            onSuccess: () => {
              message.success(t('toast.deleted', { domain: s.domain }))
              resolve()
            },
            onError: (err) => {
              message.error(err.message)
              reject(err)
            },
          })
        }),
    })
  }

  return (
    <div className="flex-1 flex flex-col">
      <main className="flex-1 px-8 py-8 max-w-6xl w-full mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-medium tracking-tight">{t('title')}</h1>
            <p className="text-sm text-[var(--fg-muted)] mt-1">
              {sites.length === 0
                ? t('subtitleEmpty')
                : t(sites.length === 1 ? 'subtitleOne' : 'subtitleOther', {
                    count: sites.length,
                    active: status.enabledSiteCount,
                  })}
            </p>
          </div>
          <Button
            type="primary"
            icon={<Plus size={14} />}
            onClick={openCreate}
          >
            {t('newSite')}
          </Button>
        </div>

        <div className="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--bg-elevated)]">
          <Table<Site>
            dataSource={sites}
            rowKey="id"
            loading={fetching}
            pagination={false}
            locale={{ emptyText: <EmptyState onCreate={openCreate} /> }}
            columns={[
              {
                title: t('table.domain'),
                dataIndex: 'domain',
                render: (d: string, row) => (
                  <div className="flex items-center gap-3">
                    <span className="mono text-sm">{d}</span>
                    {!row.enabled && (
                      <Tag className="!m-0">{t('common:status.disabled', { ns: 'common' })}</Tag>
                    )}
                  </div>
                ),
              },
              {
                title: t('table.upstream'),
                dataIndex: 'upstream',
                render: (u: string, row) => (
                  <div className="flex flex-col gap-0.5">
                    <span className="mono text-sm text-[var(--fg-muted)]">
                      {u}
                    </span>
                    {row.appName && (
                      <span className="text-[11px] text-[var(--fg-muted)]">
                        {row.appService
                          ? t('table.upstreamFromService', {
                              app: row.appName,
                              service: row.appService,
                            })
                          : t('table.upstreamFromApp', { app: row.appName })}
                      </span>
                    )}
                  </div>
                ),
              },
              {
                title: t('table.scheme'),
                dataIndex: 'scheme',
                width: 90,
                render: (s: Site['scheme']) => (
                  <Tag
                    className={`!m-0 ${
                      s === 'http' ? '!bg-amber-500/10 !text-amber-600' : ''
                    }`}
                  >
                    {s.toUpperCase()}
                  </Tag>
                ),
              },
              {
                title: t('table.status'),
                dataIndex: 'enabled',
                width: 60,
                render: (e: boolean) => <SiteStatusBadge enabled={e} />,
              },
              {
                title: '',
                key: 'actions',
                width: 140,
                align: 'right',
                render: (_: unknown, row) => (
                  <div className="flex items-center justify-end gap-1">
                    <Tooltip
                      title={
                        row.enabled
                          ? t('table.actions.disable')
                          : t('table.actions.enable')
                      }
                    >
                      <Button
                        type="text"
                        size="small"
                        icon={
                          <span className={row.enabled ? 'text-[var(--accent)]' : 'text-[var(--fg-muted)]'}>
                            {row.enabled ? <ToggleRight size={14} /> : <ToggleLeft size={14} />}
                          </span>
                        }
                        onClick={() => onToggle(row)}
                      />
                    </Tooltip>
                    <Tooltip title={t('table.actions.edit')}>
                      <Button
                        type="text"
                        size="small"
                        icon={<Pencil size={14} />}
                        onClick={() => openEdit(row)}
                      />
                    </Tooltip>
                    <Tooltip title={t('table.actions.delete')}>
                      <Button
                        type="text"
                        size="small"
                        icon={<Trash2 size={14} />}
                        onClick={() => onDelete(row)}
                      />
                    </Tooltip>
                  </div>
                ),
              },
            ]}
          />
        </div>

        <div className="mt-8">
          <h2 className="text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-3">
            {t('generatedCaddyfile')}
          </h2>
          <CaddyfilePreview />
        </div>
      </main>

      <Modal
        title={editing ? t('editor.editTitle') : t('editor.newTitle')}
        open={editorOpen}
        onOk={onSubmit}
        onCancel={() => setEditorOpen(false)}
        okText={editing ? t('editor.save') : t('editor.create')}
        cancelText={t('actions.cancel', { ns: 'common' })}
        destroyOnClose
        confirmLoading={createSite.isPending || updateSite.isPending}
        width={620}
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="domain"
            label={t('editor.domain')}
            rules={[
              { required: true, message: t('editor.domainRequired') },
              {
                validator: (_rule, value) => {
                  if (typeof value !== 'string' || !isValidDomain(value)) {
                    return Promise.reject(new Error(t('editor.domainPattern')))
                  }
                  return Promise.resolve()
                },
              },
            ]}
          >
            <Input placeholder={t('editor.domainPlaceholder')} autoFocus />
          </Form.Item>

          <Form.Item name="appId" label={t('editor.app')}>
            <Select
              allowClear
              showSearch
              optionFilterProp="label"
              placeholder={t('editor.appPlaceholder')}
              options={appOptions}
            />
          </Form.Item>

          {/* Service select: shown for every compose-mode app, including
              single-service stacks (the previous "hide for <= 1 service"
              shortcut broke the visual feedback that something was
              auto-resolved and the upstream preview that depends on
              appService). The auto-pick for the lone-service case is
              handled by the upstream-sync effect above, so a fresh
              pick of a single-service app lands with that service
              already selected — same one-click UX, but now the user
              sees the choice and the upstream updates in real time.
              For compose apps with no exposed_ports we render a
              disabled message instead of a select, so the operator
              knows why linking won't work and where to fix it. */}
          <Form.Item
            noStyle
            shouldUpdate={(prev, curr) => prev.appId !== curr.appId}
          >
            {({ getFieldValue }) => {
              const appId = getFieldValue('appId') as number | undefined
              const app = apps.find((a) => a.id === appId)
              const isCompose = app?.deployMethod === 'compose'
              if (!isCompose) return null
              const services = app?.exposedPorts ?? []
              if (services.length === 0) {
                return (
                  <Form.Item
                    label={t('editor.service')}
                    extra={t('editor.serviceNoneExtra', { name: app!.name })}
                  >
                    <Input disabled value={t('editor.serviceNone')} />
                  </Form.Item>
                )
              }
              return (
                <Form.Item
                  name="appService"
                  label={t('editor.service')}
                  rules={[
                    { required: true, message: t('editor.serviceRequired') },
                  ]}
                  extra={t('editor.serviceExtra', { name: app!.name })}
                >
                  <Select
                    placeholder={t('editor.servicePlaceholder')}
                    options={services.map((s) => ({
                      value: s.name,
                      label: serviceOptionLabel(s.name, s.port),
                    }))}
                  />
                </Form.Item>
              )
            }}
          </Form.Item>

          {/* Upstream is locked when an app is linked (server derives
              it from app+service) and editable in the free-upstream
              path. The conditional `rules` keeps the validator in
              sync with the field's editability. The shouldUpdate
              predicate watches both `appId` and `appService` so the
              "Resolved as nanoku-<app>-<service>-1:<port>" hint
              re-renders when the user switches service (without
              that, the hint would lag the input). The input value
              itself is kept in sync by the upstream-sync effect at
              the top of the page — this block just renders it. */}
          <Form.Item
            noStyle
            shouldUpdate={(prev, curr) =>
              prev.appId !== curr.appId ||
              prev.appService !== curr.appService
            }
          >
            {({ getFieldValue }) => {
              const appId = getFieldValue('appId') as number | undefined
              const app = apps.find((a) => a.id === appId)
              const isLinked = !!appId
              let extra: string
              if (isLinked) {
                const svc =
                  (getFieldValue('appService') as string | undefined) ||
                  (app?.exposedPorts?.length === 1
                    ? app.exposedPorts![0].name
                    : undefined)
                if (app?.deployMethod === 'compose' && svc) {
                  extra = t('editor.upstreamExtraAppService', {
                    app: app.name,
                    service: svc,
                  })
                } else {
                  extra = t('editor.upstreamExtraApp')
                }
              } else {
                extra = t('editor.upstreamExtraFree')
              }
              return (
                <Form.Item
                  name="upstream"
                  label={t('editor.upstream')}
                  rules={
                    isLinked
                      ? []
                      : [
                          {
                            required: true,
                            message: t('editor.upstreamRequired'),
                          },
                        ]
                  }
                  extra={extra}
                >
                  <Input
                    placeholder={
                      isLinked
                        ? t('editor.upstreamPlaceholderApp')
                        : t('editor.upstreamPlaceholderFree')
                    }
                    disabled={isLinked}
                  />
                </Form.Item>
              )
            }}
          </Form.Item>

          <Form.Item
            name="scheme"
            label={t('editor.scheme')}
            extra={t('editor.schemeExtra')}
          >
            <Select
              options={[
                { value: 'https', label: t('editor.schemeHttps') },
                { value: 'http', label: t('editor.schemeHttp') },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const { t } = useTranslation('sites')
  return (
    <div className="py-16 text-center">
      <div className="inline-flex items-center justify-center size-12 rounded-full border border-[var(--border)] mb-4">
        <Plus size={20} className="text-[var(--fg-muted)]" />
      </div>
      <p className="text-sm text-[var(--fg)] mb-1">{t('emptyState.title')}</p>
      <p className="text-xs text-[var(--fg-muted)] mb-4">
        {t('emptyState.subtitle')}
      </p>
      <Button type="primary" onClick={onCreate}>
        {t('emptyState.addSite')}
      </Button>
    </div>
  )
}

function CaddyfilePreview() {
  const { t } = useTranslation('sites')
  return (
    <QueryErrorBoundary
      fallback={(err, reset) => (
        <div className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] p-4 flex items-center justify-between gap-3">
          <span className="text-xs text-[var(--danger)] mono">
            {t('caddyfile.failed')}: {(err as Error).message}
          </span>
          <Button
            size="small"
            icon={<RefreshCw size={12} />}
            onClick={reset}
          >
            {t('actions.retry', { ns: 'common' })}
          </Button>
        </div>
      )}
    >
      <Suspense
        fallback={
          <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-4 overflow-auto max-h-96 text-[var(--fg-muted)]">
            <Skeleton active paragraph={{ rows: 4 }} title={false} />
          </pre>
        }
      >
        <CaddyfileContent />
      </Suspense>
    </QueryErrorBoundary>
  )
}

function CaddyfileContent() {
  const { t } = useTranslation('sites')
  const caddyfile = useSuspenseCaddyfile()
  return (
    <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] border border-[var(--border)] rounded-lg p-4 overflow-auto max-h-96 text-[var(--fg-muted)]">
      {caddyfile.data || t('caddyfile.empty')}
    </pre>
  )
}