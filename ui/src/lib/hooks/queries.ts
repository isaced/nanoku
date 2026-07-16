import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { api } from '../api'
import { queryKeys } from '../queryKeys'
import type {
  App,
  CleanupStatus,
  ContainerInfo,
  Dashboard,
  Deploy,
  EnvVar,
  Site,
  Status,
  SystemStatus,
  Volume,
} from '../types'

export function useDashboard(): UseQueryResult<Dashboard> {
  return useQuery({ queryKey: queryKeys.dashboard.all(), queryFn: api.dashboard })
}

export function useStatus(): UseQueryResult<Status> {
  return useQuery({ queryKey: queryKeys.status.all(), queryFn: api.status })
}

export function useApps(): UseQueryResult<App[]> {
  return useQuery({ queryKey: queryKeys.apps.all(), queryFn: api.listApps })
}

export function useApp(id: number | null | undefined): UseQueryResult<App> {
  return useQuery({
    queryKey: queryKeys.apps.detail(id ?? -1),
    queryFn: () => api.getApp(id as number),
    enabled: id != null,
  })
}

export function useAppEnv(
  id: number | null | undefined,
): UseQueryResult<EnvVar[]> {
  return useQuery({
    queryKey: queryKeys.apps.env(id ?? -1),
    queryFn: () => api.listAppEnv(id as number),
    enabled: id != null,
  })
}

export function useAppVolumes(
  id: number | null | undefined,
): UseQueryResult<Volume[]> {
  return useQuery({
    queryKey: queryKeys.apps.volumes(id ?? -1),
    queryFn: () => api.listAppVolumes(id as number),
    enabled: id != null,
  })
}

export function useAppDeploys(
  id: number | null | undefined,
): UseQueryResult<Deploy[]> {
  return useQuery({
    queryKey: queryKeys.apps.deploys(id ?? -1),
    queryFn: () => api.listAppDeploys(id as number),
    enabled: id != null,
  })
}

export function useAppLogs(
  id: number | null | undefined,
  tail: number,
  container?: string,
  opts: { enabled?: boolean } = {},
): UseQueryResult<string> {
  return useQuery({
    queryKey: queryKeys.apps.logs(id ?? -1, tail, container),
    queryFn: () => api.appLogs(id as number, tail, container),
    enabled: (opts.enabled ?? false) && id != null,
  })
}

export function useAppContainers(
  id: number | null | undefined,
  opts: { enabled?: boolean } = {},
): UseQueryResult<ContainerInfo[]> {
  return useQuery({
    queryKey: queryKeys.apps.containers(id ?? -1),
    queryFn: () => api.appContainers(id as number),
    enabled: (opts.enabled ?? false) && id != null,
  })
}

export function useSites(): UseQueryResult<Site[]> {
  return useQuery({ queryKey: queryKeys.sites.all(), queryFn: api.listSites })
}

export function useCaddyfile(): UseQueryResult<string> {
  return useQuery({ queryKey: queryKeys.caddyfile.all(), queryFn: api.caddyfile })
}

export function useSystemStatus(): UseQueryResult<SystemStatus> {
  return useQuery({
    queryKey: queryKeys.system.status(),
    queryFn: api.systemStatus,
  })
}

export function useCleanupStatus(): UseQueryResult<CleanupStatus> {
  return useQuery({
    queryKey: queryKeys.system.cleanup(),
    queryFn: api.cleanupStatus,
    // Background cleanup runs on minute-scale intervals; polling
    // every 30s keeps the system page fresh without thrashing the
    // server. Stale-while-revalidate semantics: the operator sees
    // the previous result immediately on tab focus.
    refetchInterval: 30_000,
  })
}

export function useSystemLogs(
  source: 'caddy' | 'nanoku',
  tail: number,
  opts: { enabled?: boolean } = {},
): UseQueryResult<string> {
  return useQuery({
    queryKey: queryKeys.system.logs(source, tail),
    queryFn: () => api.systemLogs(source, tail),
    enabled: opts.enabled ?? false,
  })
}
