import { App, Form } from 'antd'
import { useEffect, useState } from 'react'
import {
  useAppEnv,
  useAppVolumes,
  useCreateApp,
  useReplaceAppEnv,
  useReplaceAppVolumes,
  useRotateTriggerToken,
  useUpdateApp,
} from '../../lib/hooks'
import type {
  App as AppType,
  AppInput,
  EnvVar,
  ExposedPort,
  VolumeInput,
} from '../../lib/types'

export interface AppEditorSaveResult {
  saved: AppType
  previous: AppType | null
  isNew: boolean
}

// useAppEditorForm owns all of the editor's stateful behaviour so the
// AppEditorModal component can stay a thin layout/orchestration shell:
//   - the antd Form instance + the live-watched deployMethod
//   - the three in-component drafts (env / volumes / exposed ports) that
//     are edited as arrays of objects outside the Form store
//   - hydration effects (reset on new, populate on edit, load env/volume
//     queries) and query-error toasts
//   - submit (create/update app, then replace env, then replace volumes
//     for docker mode) and trigger-token rotation
//   - the aggregate isPending flag for the modal's confirm button
export function useAppEditorForm({
  open,
  editing,
  onSaved,
}: {
  open: boolean
  editing: AppType | null
  onSaved: (result: AppEditorSaveResult) => void
}) {
  const { message } = App.useApp()
  const [form] = Form.useForm<AppInput>()
  const [envDraft, setEnvDraft] = useState<EnvVar[]>([])
  const [volumeDraft, setVolumeDraft] = useState<VolumeInput[]>([])
  const [exposedPortsDraft, setExposedPortsDraft] = useState<ExposedPort[]>([])

  // Reactively track the deploy method so the Storage/Networking tab
  // can swap its label and content as the user flips the radio in the
  // General tab. Form.useWatch reads the live store, so it updates the
  // instant the radio changes — no manual state to keep in sync.
  const deployMethod = Form.useWatch('deployMethod', form) ?? 'docker'

  const createApp = useCreateApp()
  const updateApp = useUpdateApp()
  const replaceEnv = useReplaceAppEnv()
  const replaceVolumes = useReplaceAppVolumes()
  const rotateToken = useRotateTriggerToken()

  const envQuery = useAppEnv(editing?.id ?? null)
  const volumesQuery = useAppVolumes(editing?.id ?? null)

  useEffect(() => {
    if (envQuery.error) {
      message.error((envQuery.error as Error).message)
    }
  }, [envQuery.error, message])
  useEffect(() => {
    if (volumesQuery.error) {
      message.error((volumesQuery.error as Error).message)
    }
  }, [volumesQuery.error, message])

  useEffect(() => {
    if (!open) return
    if (!editing) {
      setEnvDraft([])
      setVolumeDraft([])
      setExposedPortsDraft([])
      form.resetFields()
      form.setFieldsValue({
        port: 80,
        deployMethod: 'docker',
        deleteVolumesOnRemove: false,
      })
      return
    }
    form.setFieldsValue({
      name: editing.name,
      image: editing.image,
      port: editing.port,
      deployMethod: editing.deployMethod ?? 'docker',
      composePath: editing.composePath ?? '',
      composeContent: editing.composeContent ?? '',
      registryUrl: editing.registryUrl ?? '',
      registryUsername: editing.registryUsername ?? '',
      enableTrigger: editing.triggerConfigured,
      deleteVolumesOnRemove: editing.deleteVolumesOnRemove,
    })
  }, [open, editing, form])

  useEffect(() => {
    if (!editing) {
      setEnvDraft([])
      setVolumeDraft([])
      setExposedPortsDraft([])
      return
    }
    if (envQuery.data) setEnvDraft(envQuery.data)
    if (volumesQuery.data) {
      setVolumeDraft(
        volumesQuery.data.map((row) => ({
          type: row.type,
          source: row.source.startsWith(`nanoku-${editing.name}-vol-`)
            ? ''
            : row.source,
          target: row.target,
          readOnly: row.readOnly,
        })),
      )
    }
    setExposedPortsDraft(editing.exposedPorts ?? [])
  }, [editing, envQuery.data, volumesQuery.data])

  function cleanedEnv(): EnvVar[] {
    return envDraft
      .map((r) => ({ key: r.key.trim(), value: r.value }))
      .filter((r) => r.key !== '')
  }
  function cleanedVolumes(): VolumeInput[] {
    return volumeDraft
      .map((r) => ({
        type: (r.type ?? 'volume') as 'volume' | 'bind',
        source: (r.source ?? '').trim(),
        target: (r.target ?? '').trim(),
        readOnly: !!r.readOnly,
      }))
      .filter((r) => r.target !== '')
  }
  // cleanedExposedPorts trims + drops rows with no service name. The
  // server is the final validator (port range, regex, duplicate name
  // detection) but we strip empty rows here so a half-typed "add row"
  // doesn't ship as { name: "", port: 0 } in the PATCH and bounce.
  function cleanedExposedPorts(): ExposedPort[] {
    return exposedPortsDraft
      .map((r) => ({ name: r.name.trim(), port: Number(r.port) }))
      .filter((r) => r.name !== '' && r.port > 0)
  }

  function handleSubmit() {
    void form.validateFields().then(async (values) => {
      const isCompose = values.deployMethod === 'compose'
      const isNew = !editing
      // Inject the in-component exposedPorts draft into the AppInput
      // payload. compose mode sends the cleaned list; docker mode
      // sends an empty array to clear any leftovers from a previous
      // compose deploy (so a docker→compose→docker round trip doesn't
      // leave a stale list in the DB).
      const payload: AppInput = {
        ...values,
        exposedPorts: isCompose ? cleanedExposedPorts() : [],
      }
      const saveMutation = isNew
        ? createApp.mutateAsync(payload)
        : updateApp.mutateAsync({ id: editing!.id, input: payload })
      try {
        const saved = await saveMutation
        await replaceEnv.mutateAsync({ id: saved.id, vars: cleanedEnv() })
        if (!isCompose) {
          await replaceVolumes.mutateAsync({
            id: saved.id,
            vols: cleanedVolumes(),
          })
        }
        onSaved({ saved, previous: editing, isNew })
      } catch (err) {
        message.error((err as Error).message)
      }
    })
  }

  function onRotate() {
    if (!editing) return
    rotateToken.mutate(editing.id, {
      onSuccess: (updated) => {
        if (updated.triggerToken) {
          onSaved({
            saved: updated,
            previous: editing,
            isNew: false,
          })
        }
      },
      onError: (err) => {
        message.error(err.message)
      },
    })
  }

  const isPending =
    createApp.isPending ||
    updateApp.isPending ||
    replaceEnv.isPending ||
    replaceVolumes.isPending

  return {
    form,
    deployMethod,
    envDraft,
    setEnvDraft,
    volumeDraft,
    setVolumeDraft,
    exposedPortsDraft,
    setExposedPortsDraft,
    handleSubmit,
    onRotate,
    isPending,
  }
}
