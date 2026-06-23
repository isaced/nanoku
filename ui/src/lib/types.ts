export type Site = {
  id: number;
  domain: string;
  upstream: string;
  enabled: boolean;
  scheme: 'http' | 'https';
  appId?: number;
  appName?: string;
  createdAt: string;
  updatedAt: string;
};

export type SiteInput = {
  domain?: string;
  upstream?: string;
  enabled?: boolean;
  appId?: number;
  scheme?: 'http' | 'https';
};

export type Container = {
  id: number;
  name: string;
  image: string;
  status: string;
  startedAt?: string;
  stoppedAt?: string;
};

export type Volume = {
  type: 'volume' | 'bind';
  source: string;
  target: string;
  readOnly: boolean;
};

export type VolumeInput = {
  type?: 'volume' | 'bind';
  source?: string;
  target?: string;
  readOnly?: boolean;
};

export type App = {
  id: number;
  name: string;
  image: string;
  port: number;
  container?: Container;
  envVars?: EnvVar[];
  volumes?: Volume[];

  deployMethod: 'docker' | 'compose';
  composePath?: string;
  composeContent?: string;
  composeFile?: string;

  registryConfigured: boolean;
  registryUrl?: string;
  registryUsername?: string;

  triggerConfigured: boolean;
  triggerToken?: string;

  deleteVolumesOnRemove: boolean;

  createdAt: string;
  updatedAt: string;
};

export type AppInput = {
  name?: string;
  image?: string;
  port?: number;
  deployMethod?: 'docker' | 'compose';
  composePath?: string;
  composeContent?: string;
  registryUrl?: string;
  registryUsername?: string;
  registryPassword?: string;
  clearRegistry?: boolean;
  enableTrigger?: boolean;
  deleteVolumesOnRemove?: boolean;
};

export type EnvVar = {
  key: string;
  value: string;
};

export type Deploy = {
  id: number;
  trigger: string;
  status: string;
  commitSha?: string;
  commitMessage?: string;
  error?: string;
  startedAt?: string;
  finishedAt?: string;
  createdAt: string;
  containerName?: string;
};

// Response from POST /api/apps/{id}/deployments and POST /api/apps/{name}/trigger.
// accepted=false means another deploy is already in flight for this app
// (lock contention); accepted=true means a new Deploy row was created
// with status=running and `deployId` is the id to poll for status.
export type DeployResponse = {
  accepted: boolean;
  appId: number;
  deployId?: number;
  reason?: string;
};

export type Status = {
  dockerConnected: boolean;
  caddyStatus: string;
  siteCount: number;
  enabledSiteCount: number;
  appCount: number;
  runningAppCount: number;
  acmeEmail: string;
};

export type SystemStatus = {
  nanokuContainerConfigured: boolean;
  nanokuContainerName?: string;
  caddyContainer: string;
  dockerAvailable: boolean;
};

export type ContainerStats = {
  name: string;
  cpuPerc: number;
  memUsedBytes: number;
  memLimitBytes: number;
  memPerc: number;
  netRxBytes: number;
  netTxBytes: number;
  blockReadBytes: number;
  blockWriteBytes: number;
  pids: number;
};

export type DashboardSite = {
  id: number;
  domain: string;
  upstream: string;
  enabled: boolean;
  appId?: number;
  appName?: string;
};

export type DashboardApp = {
  id: number;
  name: string;
  image: string;
  port: number;
  container?: Container;
  siteDomains: string[];
  stats?: ContainerStats;
  deployMethod: 'docker' | 'compose';
};

export type DashboardSummary = {
  totalSites: number;
  enabledSites: number;
  totalApps: number;
  runningApps: number;
  totalCpuPerc: number;
  totalMemBytes: number;
  totalMemLimitBytes: number;
  totalMemPerc: number;
  containerCount: number;
};

export type Dashboard = {
  summary: DashboardSummary;
  sites: DashboardSite[];
  apps: DashboardApp[];
  stats: ContainerStats[];
};