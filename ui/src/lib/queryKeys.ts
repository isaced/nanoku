export const queryKeys = {
  dashboard: {
    all: () => ['dashboard'] as const,
  },
  status: {
    all: () => ['status'] as const,
  },
  apps: {
    all: () => ['apps'] as const,
    detail: (id: number) => ['apps', id] as const,
    env: (id: number) => ['apps', id, 'env'] as const,
    volumes: (id: number) => ['apps', id, 'volumes'] as const,
    deploys: (id: number) => ['apps', id, 'deploys'] as const,
    logs: (id: number, tail: number, container?: string) =>
      ['apps', id, 'logs', tail, container ?? ''] as const,
    containers: (id: number) => ['apps', id, 'containers'] as const,
    files: (id: number, container: string, path: string) =>
      ['apps', id, 'files', container, path] as const,
    fileContent: (id: number, container: string, path: string) =>
      ['apps', id, 'file', container, path] as const,
  },
  sites: {
    all: () => ['sites'] as const,
  },
  caddyfile: {
    all: () => ['caddyfile'] as const,
  },
  system: {
    status: () => ['system', 'status'] as const,
    logs: (source: 'caddy' | 'nanoku', tail: number) =>
      ['system', 'logs', source, tail] as const,
    cleanup: () => ['system', 'cleanup'] as const,
  },
}
