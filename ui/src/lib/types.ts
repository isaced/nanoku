export type Site = {
  id: number;
  domain: string;
  upstream: string;
  enabled: boolean;
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
};

export type Container = {
  id: number;
  name: string;
  image: string;
  status: string;
  startedAt?: string;
  stoppedAt?: string;
};

export type App = {
  id: number;
  name: string;
  image: string;
  port: number;
  repoUrl?: string;
  branch: string;
  container?: Container;
  envVars?: EnvVar[];
  createdAt: string;
  updatedAt: string;
};

export type AppInput = {
  name?: string;
  image?: string;
  port?: number;
  repoUrl?: string;
  branch?: string;
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