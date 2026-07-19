export type ExposedPort = {
  name: string;
  port: number;
};

export type Site = {
  id: number;
  domain: string;
  upstream: string;
  enabled: boolean;
  scheme: 'http' | 'https';
  appId?: number;
  appName?: string;
  // Service within a compose-mode app that this site proxies to.
  // Empty for docker-mode apps and for free-upstream sites.
  appService?: string;
  // Exposed services on the linked compose app. Populated by the
  // server on every Site DTO so the editor can render a service
  // select without re-fetching the app. Empty for docker-mode apps
  // and sites without a linked app.
  exposedServices?: ExposedPort[];
  createdAt: string;
  updatedAt: string;
};

export type SiteInput = {
  domain?: string;
  upstream?: string;
  enabled?: boolean;
  appId?: number;
  // Service within the linked compose-mode app. Ignored when the
  // target app is docker-mode. Empty / null = no service (free
  // upstream or auto-pick when only one service is declared).
  appService?: string;
  // Set true to detach the site from any app (also clears appService
  // and upstream). More explicit than sending appId: 0; the API
  // accepts both.
  clearApp?: boolean;
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

/** ContainerInfo is the lightweight container summary returned by
 * GET /api/apps/{id}/containers. Used by the Logs tab to populate the
 * container selector for compose apps. */
export type ContainerInfo = {
  name: string;
  image: string;
  status: string;
};

/** ContainerFile is a single entry in a container directory listing,
 * returned by GET /api/apps/{id}/containers/{name}/files. The mode
 * string follows `ls -l` convention (e.g. "-rw-r--r--") and the size
 * is bytes. modTime is the RFC 3339 timestamp the server parsed from
 * `ls`'s month+day fields with the current year; it can be a zero
 * string if the upstream column was unparseable. */
export type ContainerFile = {
  name: string;
  size: number;
  mode: string;
  modTime: string;
  isDir: boolean;
  isLink: boolean;
  linkTarget?: string;
};

/** ContainerFileListing is the wrapper returned by the file-browser
 * list endpoint. We wrap entries in an object (rather than returning
 * a bare array) so the API can grow extra fields — e.g. a "truncated"
 * flag if we ever cap large directory reads — without breaking
 * existing clients. */
export type ContainerFileListing = {
  path: string;
  entries: ContainerFile[];
};

/** ContainerFileContent is the response from the text-view path of
 * the file endpoint (no ?download=1). The content is base64-encoded
 * JSON-side because raw bytes don't survive a JSON string round-trip
 * (NULs, embedded newlines, surrogate pairs). The UI decodes back
 * to bytes and renders as utf-8 text. */
export type ContainerFileContent = {
  path: string;
  size: number;
  mode: string;
  modTime: string;
  content: string;
  encoding: 'base64';
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

  // Exposed services the app wants to make reachable via Sites. Only
  // meaningful for compose-mode apps. Always undefined/empty for
  // docker-mode apps (which use port instead).
  exposedPorts?: ExposedPort[];

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
  // Nil leaves exposed_ports alone. Empty array clears it. Non-empty
  // array replaces it. Only meaningful for compose-mode apps; the
  // server silently drops it on docker-mode.
  exposedPorts?: ExposedPort[];
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
  version: string;
  commit: string;
  date: string;
  buildType: 'source' | 'release';
};

// CleanupStatus mirrors the wire shape of GET /api/system/cleanup.
// Every field is optional/zero so older nanoku binaries that don't
// return the same keys (e.g. a release without the Janitor) still
// render an empty section instead of crashing the page.
export type CleanupResult = {
  scanned: number;
  pruned: number;
  bytes: number;
};

export type CleanupStatusEntry = {
  lastRun: string;
  nextRun: string;
  lastResult: CleanupResult;
  lastErr?: string;
  lastErrAt?: string;
  runCount: number;
  errCount: number;
};

export type CleanupStatus = {
  tasks: Record<string, CleanupStatusEntry>;
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
  appService?: string;
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
  exposedPorts?: ExposedPort[];
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