import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { api } from '../api'
import { queryKeys } from '../queryKeys'
import type {
  App,
  CleanupStatus,
  ContainerFileContent,
  ContainerFileListing,
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

/**
 * splitLogLines turns the newline-joined string the log endpoints
 * return into the array of lines useLogStream expects. We drop a
 * single trailing empty row (the common "tail ends with \n" case)
 * so the seed doesn't render a blank line at the bottom of the
 * panel.
 */
function splitLogLines(s: string): string[] {
  if (s === '') return []
  const lines = s.split('\n')
  if (lines.length > 0 && lines[lines.length - 1] === '') {
    lines.pop()
  }
  return lines
}

export function useAppLogs(
  id: number | null | undefined,
  tail: number,
  container?: string,
  opts: { enabled?: boolean } = {},
): UseQueryResult<string[]> {
  return useQuery({
    queryKey: queryKeys.apps.logs(id ?? -1, tail, container),
    queryFn: () => api.appLogs(id as number, tail, container),
    // Decode the newline-joined payload once, in the query layer,
    // so callers receive `string[]` directly. The `select` runs
    // after the queryFn resolves; `useLogStream` then takes the
    // array as-is for its `initialLines` seed.
    select: splitLogLines,
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

export function useAppContainerFiles(
  id: number | null | undefined,
  container: string | null | undefined,
  path: string,
  opts: { enabled?: boolean } = {},
): UseQueryResult<ContainerFileListing> {
  return useQuery({
    queryKey: queryKeys.apps.files(id ?? -1, container ?? '', path),
    queryFn: () => api.listAppContainerFiles(id as number, container as string, path),
    enabled: (opts.enabled ?? false) && id != null && container != null && container !== '',
  })
}

export function useAppContainerFile(
  id: number | null | undefined,
  container: string | null | undefined,
  path: string | null | undefined,
  opts: { enabled?: boolean } = {},
): UseQueryResult<ContainerFileContent> {
  return useQuery({
    queryKey: queryKeys.apps.fileContent(id ?? -1, container ?? '', path ?? ''),
    queryFn: () => api.readAppContainerFile(id as number, container as string, path as string),
    // The file content is gated on a non-empty path so we
    // don't fire a request to "?path=" on initial mount.
    enabled:
      (opts.enabled ?? false) &&
      id != null &&
      container != null &&
      container !== '' &&
      path != null &&
      path !== '',
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
): UseQueryResult<string[]> {
  return useQuery({
    queryKey: queryKeys.system.logs(source, tail),
    queryFn: () => api.systemLogs(source, tail),
    // See useAppLogs — callers want the lines already split so
    // useLogStream can seed its buffer without an extra useMemo.
    select: splitLogLines,
    enabled: opts.enabled ?? false,
  })
}
