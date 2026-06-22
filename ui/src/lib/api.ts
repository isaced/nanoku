import { getCredentials, clearCredentials } from './auth';
import type {
  App,
  AppInput,
  Dashboard,
  Deploy,
  EnvVar,
  Site,
  SiteInput,
  Status,
  SystemStatus,
} from './types';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const creds = getCredentials();
  if (!creds) {
    throw new ApiError(401, 'Not authenticated');
  }
  const headers = new Headers(init.headers);
  headers.set('Authorization', 'Basic ' + btoa(`${creds.user}:${creds.pass}`));
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const res = await fetch(path, { ...init, headers });
  if (res.status === 401) {
    clearCredentials();
    if (typeof window !== 'undefined') {
      window.location.reload();
    }
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
    request<App>(`/api/apps/${id}/deploy`, { method: 'POST' }),
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
  listAppDeploys: (id: number) =>
    request<Deploy[]>(`/api/apps/${id}/deploys`),

  systemStatus: () => request<SystemStatus>('/api/system/status'),
  systemLogs: (source: 'caddy' | 'nanoku', tail = 200) =>
    request<string>(`/api/system/logs?source=${source}&tail=${tail}`),

  dashboard: () => request<Dashboard>('/api/dashboard'),
};

export async function testCredentials(user: string, pass: string): Promise<boolean> {
  const res = await fetch('/api/status', {
    headers: { Authorization: 'Basic ' + btoa(`${user}:${pass}`) },
  });
  return res.ok;
}