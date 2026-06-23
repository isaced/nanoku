import {
  useMutation,
  useQueryClient,
  type UseMutationResult,
} from '@tanstack/react-query'
import { api } from '../api'
import { queryKeys } from '../queryKeys'
import type {
  App,
  AppInput,
  DeployResponse,
  EnvVar,
  Site,
  SiteInput,
  VolumeInput,
} from '../types'

function invalidateAppList(qc: ReturnType<typeof useQueryClient>, id?: number) {
  void qc.invalidateQueries({ queryKey: queryKeys.apps.all() })
  void qc.invalidateQueries({ queryKey: queryKeys.dashboard.all() })
  void qc.invalidateQueries({ queryKey: queryKeys.status.all() })
  if (id != null) {
    void qc.invalidateQueries({ queryKey: queryKeys.apps.detail(id) })
  }
}

function invalidateSiteList(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: queryKeys.sites.all() })
  void qc.invalidateQueries({ queryKey: queryKeys.dashboard.all() })
  void qc.invalidateQueries({ queryKey: queryKeys.status.all() })
  void qc.invalidateQueries({ queryKey: queryKeys.caddyfile.all() })
}

export function useCreateApp(): UseMutationResult<App, Error, AppInput> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input) => api.createApp(input),
    onSuccess: () => invalidateAppList(qc),
  })
}

export function useUpdateApp(): UseMutationResult<
  App,
  Error,
  { id: number; input: AppInput }
> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }) => api.updateApp(id, input),
    onSuccess: (_data, { id }) => invalidateAppList(qc, id),
  })
}

export function useDeleteApp(): UseMutationResult<void, Error, number> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteApp(id),
    onSuccess: () => invalidateAppList(qc),
  })
}

export function useDeployApp(): UseMutationResult<DeployResponse, Error, number> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deployApp(id),
    onSuccess: (_data, id) => {
      invalidateAppList(qc, id)
      // Refresh the deploys list so the new "running" row shows up
      // immediately; the drawer's poll loop takes over after that.
      void qc.invalidateQueries({ queryKey: queryKeys.apps.deploys(id) })
    },
  })
}

export function useStartApp(): UseMutationResult<App, Error, number> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.startApp(id),
    onSuccess: (_data, id) => invalidateAppList(qc, id),
  })
}

export function useStopApp(): UseMutationResult<App, Error, number> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.stopApp(id),
    onSuccess: (_data, id) => invalidateAppList(qc, id),
  })
}

export function useRestartApp(): UseMutationResult<App, Error, number> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.restartApp(id),
    onSuccess: (_data, id) => invalidateAppList(qc, id),
  })
}

export function useRotateTriggerToken(): UseMutationResult<App, Error, number> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.rotateTriggerToken(id),
    onSuccess: (_data, id) => {
      void qc.invalidateQueries({ queryKey: queryKeys.apps.detail(id) })
      void qc.invalidateQueries({ queryKey: queryKeys.apps.all() })
    },
  })
}

export function useReplaceAppEnv(): UseMutationResult<
  { count: number; hint: string; appId: number },
  Error,
  { id: number; vars: EnvVar[] }
> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, vars }) => api.replaceAppEnv(id, vars),
    onSuccess: (_data, { id }) => {
      void qc.invalidateQueries({ queryKey: queryKeys.apps.env(id) })
    },
  })
}

export function useReplaceAppVolumes(): UseMutationResult<
  { count: number; hint: string; appId: number },
  Error,
  { id: number; vols: VolumeInput[] }
> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, vols }) => api.replaceAppVolumes(id, vols),
    onSuccess: (_data, { id }) => {
      void qc.invalidateQueries({ queryKey: queryKeys.apps.volumes(id) })
    },
  })
}

export function useCreateSite(): UseMutationResult<Site, Error, SiteInput> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input) => api.createSite(input),
    onSuccess: () => invalidateSiteList(qc),
  })
}

export function useUpdateSite(): UseMutationResult<
  Site,
  Error,
  { id: number; input: SiteInput }
> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }) => api.updateSite(id, input),
    onSuccess: () => invalidateSiteList(qc),
  })
}

export function useDeleteSite(): UseMutationResult<void, Error, number> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteSite(id),
    onSuccess: () => invalidateSiteList(qc),
  })
}

export function useToggleSite(): UseMutationResult<Site, Error, number> {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.toggleSite(id),
    onSuccess: () => invalidateSiteList(qc),
  })
}

export function useLogin(): UseMutationResult<
  { username: string; role: string },
  Error,
  { username: string; password: string }
> {
  return useMutation({
    mutationFn: ({ username, password }) => api.login(username, password),
  })
}
