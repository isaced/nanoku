import type {
  App,
  AppInput,
  Dashboard,
  Deploy,
  DeployResponse,
  EnvVar,
  ExposedPort,
  Site,
  SiteInput,
  Status,
  SystemStatus,
  Volume,
  VolumeInput,
} from './types';
import { markLoggedOut } from './auth';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

type UnauthorizedHandler = () => void;
let onUnauthorized: UnauthorizedHandler | null = null;

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  onUnauthorized = handler;
}

// Single entry point for the 401 → redirect flow. The fetch wrapper calls it
// for direct api.* calls, and the QueryClient global error handler calls it
// for useQuery / useMutation failures. Routing and auth-state mutation live
// in one place.
export function triggerUnauthorized(): void {
  markLoggedOut();
  onUnauthorized?.();
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const res = await fetch(path, { ...init, headers, credentials: 'include' });
  if (res.status === 401) {
    triggerUnauthorized();
    throw new ApiError(401, 'Unauthorized');
  }
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body.error) msg = body.error;
    } catch {}
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  const ct = res.headers.get('Content-Type') ?? '';
  return ct.includes('application/json')
    ? ((await res.json()) as T)
    : ((await res.text()) as T);
}

export const api = {
  // auth
  login: (username: string, password: string) =>
    request<{ username: string; role: string }>('/api/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<void>('/api/logout', { method: 'POST' }),
  me: () => request<{ username: string; role: string }>('/api/me'),

  // admin
  listSites: () => request<Site[]>('/api/sites'),
  createSite: (input: SiteInput) =>
    request<Site>('/api/sites', { method: 'POST', body: JSON.stringify(input) }),
  updateSite: (id: number, input: SiteInput) =>
    request<Site>(`/api/sites/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  deleteSite: (id: number) =>
    request<void>(`/api/sites/${id}`, { method: 'DELETE' }),
  toggleSite: (id: number) =>
    request<Site>(`/api/sites/${id}/toggle`, { method: 'POST' }),
  status: () => request<Status>('/api/status'),
  caddyfile: () => request<string>('/api/caddyfile'),

  listApps: () => request<App[]>('/api/apps'),
  getApp: (id: number) => request<App>(`/api/apps/${id}`),
  createApp: (input: AppInput) =>
    request<App>('/api/apps', { method: 'POST', body: JSON.stringify(input) }),
  updateApp: (id: number, input: AppInput) =>
    request<App>(`/api/apps/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  deleteApp: (id: number) =>
    request<void>(`/api/apps/${id}`, { method: 'DELETE' }),
  deployApp: (id: number) =>
    request<DeployResponse>(`/api/apps/${id}/deployments`, { method: 'POST' }),
  startApp: (id: number) =>
    request<App>(`/api/apps/${id}/start`, { method: 'POST' }),
  stopApp: (id: number) =>
    request<App>(`/api/apps/${id}/stop`, { method: 'POST' }),
  restartApp: (id: number) =>
    request<App>(`/api/apps/${id}/restart`, { method: 'POST' }),
  appLogs: (id: number, tail = 200) =>
    request<string>(`/api/apps/${id}/logs?tail=${tail}`),
  listAppEnv: (id: number) => request<EnvVar[]>(`/api/apps/${id}/env`),
  replaceAppEnv: (id: number, vars: EnvVar[]) =>
    request<{ count: number; hint: string; appId: number }>(
      `/api/apps/${id}/env`,
      { method: 'PUT', body: JSON.stringify(vars) },
    ),
  listAppVolumes: (id: number) => request<Volume[]>(`/api/apps/${id}/volumes`),
  replaceAppVolumes: (id: number, vols: VolumeInput[]) =>
    request<{ count: number; hint: string; appId: number }>(
      `/api/apps/${id}/volumes`,
      { method: 'PUT', body: JSON.stringify(vols) },
    ),
  listAppDeploys: (id: number) =>
    request<Deploy[]>(`/api/apps/${id}/deployments`),
  rotateTriggerToken: (id: number) =>
    request<App>(`/api/apps/${id}/rotate-trigger-token`, { method: 'POST' }),
  rollbackApp: (id: number, deployId: number) =>
    request<DeployResponse>(`/api/apps/${id}/rollback`, {
      method: 'POST',
      body: JSON.stringify({ deployId }),
    }),
  // importExposedPorts parses the app's compose YAML and returns a
  // {name, port} list the operator can review before saving.
  // Compose-only: the server returns 400 for docker-mode apps and
  // for apps with compose_path set (since the import only reads
  // inline content). The mutation caller should map those errors
  // to a user-visible message.
  importExposedPorts: (id: number) =>
    request<{ appId: number; exposedPorts: ExposedPort[]; hint: string }>(
      `/api/apps/${id}/exposed-ports/import`,
      { method: 'POST' },
    ),

  systemStatus: () => request<SystemStatus>('/api/system/status'),
  systemLogs: (source: 'caddy' | 'nanoku', tail = 200) =>
    request<string>(`/api/system/logs?source=${source}&tail=${tail}`),

  dashboard: () => request<Dashboard>('/api/dashboard'),
};