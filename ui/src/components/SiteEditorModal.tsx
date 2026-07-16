import { Form, Input, Modal, Select } from 'antd'
import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { isValidDomain } from '../lib/domain-validation'
import {
  appOptionLabel,
  computeUpstreamForAppUI,
  serviceOptionLabel,
} from '../lib/site-upstream'
import type { App as AppType, Site, SiteInput } from '../lib/types'

/**
 * SiteEditorModal is the create/edit form for sites. The parent
 * owns the actual mutation (`useCreateSite` / `useUpdateSite`);
 * the modal is a pure form that hands a payload back via
 * `onSubmit`. The split lets the modal mount/unmount freely
 * (destroyOnClose) without losing the form state, and keeps
 * sites.tsx focused on the table + list-level toasts.
 */

type SiteFormValues = {
  domain: string
  upstream: string
  appId?: number
  appService?: string
  scheme: 'http' | 'https'
}

export type SiteEditorSubmitPayload = SiteInput & { clearApp?: boolean }

export function SiteEditorModal({
  open,
  editing,
  apps,
  isPending,
  onClose,
  onSubmit,
}: {
  open: boolean
  editing: Site | null
  apps: AppType[]
  isPending: boolean
  onClose: () => void
  onSubmit: (payload: SiteEditorSubmitPayload) => void
}) {
  const { t } = useTranslation('sites')
  const [form] = Form.useForm<SiteFormValues>()

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

  // Re-seed the form when the editor opens with a fresh `editing`
  // (the parent flips `editing` to null → Site when opening, so
  // the effect re-runs and we reset accordingly).
  useEffect(() => {
    if (!open) return
    if (!editing) {
      form.resetFields()
      form.setFieldsValue({ scheme: 'https' })
      return
    }
    form.setFieldsValue({
      domain: editing.domain,
      upstream: editing.upstream,
      appId: editing.appId,
      appService: editing.appService,
      scheme: editing.scheme,
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, editing?.id])

  const appOptions = useMemo(
    () =>
      apps.map((a) => ({
        value: a.id,
        label: appOptionLabel(a),
      })),
    [apps],
  )

  function onOk() {
    void form.validateFields().then((values) => {
      const isLinked = !!values.appId
      const payload: SiteEditorSubmitPayload = {
        domain: values.domain,
        scheme: values.scheme,
      }
      if (isLinked) {
        payload.appId = values.appId
        if (values.appService) payload.appService = values.appService
        // Upstream is computed server-side; we never send it in the
        // linked branch. (Sending a stale value would be ignored by
        // the API but adds noise to the request body.)
      } else if (values.upstream) {
        payload.upstream = values.upstream
      }
      // If the user clears the app linkage on an edit, route through
      // the explicit clearApp flag so the server doesn't have to
      // guess the difference between "no change" and "detach".
      if (editing && editing.appId && !values.appId) {
        payload.clearApp = true
        delete payload.appId
      }
      onSubmit(payload)
    })
  }

  return (
    <Modal
      title={editing ? t('editor.editTitle') : t('editor.newTitle')}
      open={open}
      onOk={onOk}
      onCancel={onClose}
      okText={editing ? t('editor.save') : t('editor.create')}
      cancelText={t('actions.cancel', { ns: 'common' })}
      destroyOnClose
      confirmLoading={isPending}
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
            "Resolved from the compose stack" hint re-renders when
            the user switches service (without that, the hint
            would lag the input). The input value itself is kept
            in sync by the upstream-sync effect at
            the top of the modal — this block just renders it. */}
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
  )
}
