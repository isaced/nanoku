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
  ExposedPort,
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

/**
 * Container lifecycle actions share the same set of UI rules:
 * which buttons to show in which state. Centralising the rules here
 * keeps the apps list and the app detail drawer in sync — without
 * this, one of them inevitably drifts (e.g. the list shows Stop
 * only for `running`, the drawer shows it for `running |
 * restarting`).
 */
export type AppLifecycle = {
  /** The container exists AND docker considers it live. */
  isLive: boolean
  /** A container row exists, regardless of state. */
  hasContainer: boolean
  /** The container is actively re-spawning (short-lived image on
   * `unless-stopped` is the common case). Restart is a no-op here;
   * Stop is the only escape. */
  isRestarting: boolean
  /** The container is running. */
  isRunning: boolean
  /** The container is stopped / exited / not yet started. */
  isStopped: boolean
}

export function appLifecycle(container?: { status: string } | null): AppLifecycle {
  const status = container?.status
  const isRunning = status === 'running'
  const isRestarting = status === 'restarting'
  const isPaused = status === 'paused'
  const isStopped =
    !container ||
    status === 'exited' ||
    status === 'dead' ||
    status === 'created' ||
    status === 'removing'
  return {
    isLive: isRunning || isRestarting || isPaused,
    hasContainer: !!container,
    isRestarting,
    isRunning,
    isStopped,
  }
}

export type AppActions = {
  start: UseMutationResult<App, Error, number>
  stop: UseMutationResult<App, Error, number>
  restart: UseMutationResult<App, Error, number>
}

export function useAppActions(): AppActions {
  return {
    start: useStartApp(),
    stop: useStopApp(),
    restart: useRestartApp(),
  }
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

// useImportExposedPorts parses the app's compose YAML and returns
// a scaffolding list of services + best-effort ports. It does NOT
// write to App.exposed_ports — the caller is expected to take the
// result, let the user review/edit, and submit through the regular
// UpdateApp path. No cache invalidation needed since the result is
// transient.
export function useImportExposedPorts(): UseMutationResult<
  { appId: number; exposedPorts: ExposedPort[]; hint: string },
  Error,
  number
> {
  return useMutation({
    mutationFn: (id) => api.importExposedPorts(id),
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
