import {
  useSuspenseQuery,
  type UseSuspenseQueryResult,
} from '@tanstack/react-query'
import { api } from '../api'
import { queryKeys } from '../queryKeys'
import type {
  App,
  Dashboard,
  Deploy,
  EnvVar,
  Site,
  Status,
  SystemStatus,
  Volume,
} from '../types'

export function useSuspenseDashboard(): UseSuspenseQueryResult<Dashboard> {
  return useSuspenseQuery({
    queryKey: queryKeys.dashboard.all(),
    queryFn: api.dashboard,
  })
}

export function useSuspenseStatus(): UseSuspenseQueryResult<Status> {
  return useSuspenseQuery({
    queryKey: queryKeys.status.all(),
    queryFn: api.status,
  })
}

export function useSuspenseApps(): UseSuspenseQueryResult<App[]> {
  return useSuspenseQuery({
    queryKey: queryKeys.apps.all(),
    queryFn: api.listApps,
  })
}

export function useSuspenseApp(id: number): UseSuspenseQueryResult<App> {
  return useSuspenseQuery({
    queryKey: queryKeys.apps.detail(id),
    queryFn: () => api.getApp(id),
  })
}

export function useSuspenseAppEnv(id: number): UseSuspenseQueryResult<EnvVar[]> {
  return useSuspenseQuery({
    queryKey: queryKeys.apps.env(id),
    queryFn: () => api.listAppEnv(id),
  })
}

export function useSuspenseAppVolumes(
  id: number,
): UseSuspenseQueryResult<Volume[]> {
  return useSuspenseQuery({
    queryKey: queryKeys.apps.volumes(id),
    queryFn: () => api.listAppVolumes(id),
  })
}

export function useSuspenseAppDeploys(
  id: number,
): UseSuspenseQueryResult<Deploy[]> {
  return useSuspenseQuery({
    queryKey: queryKeys.apps.deploys(id),
    queryFn: () => api.listAppDeploys(id),
  })
}

export function useSuspenseSites(): UseSuspenseQueryResult<Site[]> {
  return useSuspenseQuery({
    queryKey: queryKeys.sites.all(),
    queryFn: api.listSites,
  })
}

export function useSuspenseCaddyfile(): UseSuspenseQueryResult<string> {
  return useSuspenseQuery({
    queryKey: queryKeys.caddyfile.all(),
    queryFn: api.caddyfile,
  })
}

export function useSuspenseSystemStatus(): UseSuspenseQueryResult<SystemStatus> {
  return useSuspenseQuery({
    queryKey: queryKeys.system.status(),
    queryFn: api.systemStatus,
  })
}