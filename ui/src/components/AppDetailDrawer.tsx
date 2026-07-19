import { Suspense, useEffect, useRef, useState } from 'react'
import { Drawer, Tabs } from 'antd'
import { Container as ContainerIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  useSuspenseApp,
  useSuspenseAppDeploys,
  useSuspenseAppEnv,
  useSuspenseAppVolumes,
} from '../lib/hooks'
import { containerStatusMeta } from '../lib/containerStatus'
import { queryKeys } from '../lib/queryKeys'
import { useQueryClient } from '@tanstack/react-query'
import type { Deploy } from '../lib/types'
import { RouteFallback } from './RouteFallback'
import { StatusTag } from './StatusTag'
import { AppDetailOverview } from './AppDetailOverview'
import { AppDetailDeploys } from './AppDetailDeploys'
import { AppDetailFiles } from './AppDetailFiles'
import { AppDetailLogs } from './AppDetailLogs'

const DEPLOY_POLL_INTERVAL_MS = 3000

type TabKey = 'overview' | 'deploys' | 'logs' | 'files'

export function AppDetail({
  appId,
  initialTab,
  onClose,
  onChanged,
}: {
  appId: number
  initialTab?: TabKey
  onClose: () => void
  onChanged: () => void
}) {
  // The Drawer is rendered OUTSIDE the Suspense boundary so it mounts
  // once and stays in the DOM while the body suspends. Earlier the
  // Suspense fallback was a hand-rolled fake drawer div that appeared,
  // vanished, then got replaced by the real antd Drawer — visually a
  // "white flash → drawer disappears → drawer reappears with content"
  // sequence. Lifting <Drawer> above <Suspense> makes the Drawer mount
  // exactly once: the slide-in plays once, and during data load only
  // the body swaps from <RouteFallback> to the real tabs.
  return (
    <Drawer
      open
      onClose={onClose}
      width={680}
      destroyOnClose
      title={
        <Suspense
          fallback={
            <div className="h-6 w-40 bg-[var(--bg-input)] rounded animate-pulse" />
          }
        >
          <AppDetailTitle appId={appId} />
        </Suspense>
      }
    >
      <Suspense fallback={<RouteFallback variant="drawer" />}>
        <AppDetailContent
          appId={appId}
          initialTab={initialTab}
          onChanged={onChanged}
        />
      </Suspense>
    </Drawer>
  )
}

function AppDetailTitle({ appId }: { appId: number }) {
  const appQuery = useSuspenseApp(appId)
  const app = appQuery.data
  // Action buttons live on the apps list row, not on the drawer
  // header — the drawer is for inspection (logs / deploys / env /
  // volumes / config), not control. Keeping it that way means we
  // never have to reconcile two action surfaces; the list is
  // canonical.
  return (
    <div className="flex items-center gap-3">
      <ContainerIcon size={16} className="text-[var(--fg-muted)]" />
      <span className="mono text-base">{app.name}</span>
      {app.container && (
        <StatusTag
          {...containerStatusMeta(app.container.status)}
          size="small"
        />
      )}
    </div>
  )
}

function AppDetailContent({
  appId,
  initialTab,
  onChanged,
}: {
  appId: number
  initialTab?: TabKey
  onChanged: () => void
}) {
  const { t } = useTranslation('apps')
  const [activeTab, setActiveTab] = useState<TabKey>(initialTab ?? 'overview')
  const queryClient = useQueryClient()

  const appQuery = useSuspenseApp(appId)
  const envQuery = useSuspenseAppEnv(appId)
  const volumesQuery = useSuspenseAppVolumes(appId)
  const deploysQuery = useSuspenseAppDeploys(appId)

  const app = appQuery.data
  const env = envQuery.data
  const volumes = volumesQuery.data
  const deploys = deploysQuery.data

  // Self-managed deploys polling. The Drawer renders inside a modal
  // portal that mounts/unmounts with tab visibility, and we want the
  // polling to keep ticking even when the user isn't actively looking
  // at the Deploys tab — for example to surface a CI-triggered deploy
  // that lands while the user is reading Overview.
  useEffect(() => {
    const id = setInterval(() => {
      void deploysQuery.refetch()
    }, DEPLOY_POLL_INTERVAL_MS)
    return () => clearInterval(id)
  }, [deploysQuery])

  // When a deploy finishes, the App row's container status changes too.
  const inFlight = hasRunningDeploy(deploys)
  const wasInFlight = useRef(false)
  useEffect(() => {
    if (wasInFlight.current && !inFlight) {
      void appQuery.refetch()
      void envQuery.refetch()
      void volumesQuery.refetch()
      void queryClient.invalidateQueries({ queryKey: queryKeys.apps.all() })
      onChanged()
    }
    wasInFlight.current = inFlight
  }, [inFlight, appQuery, envQuery, volumesQuery, queryClient, onChanged])

  return (
    <Tabs
      activeKey={activeTab}
      onChange={(k) => setActiveTab(k as TabKey)}
      items={[
        {
          key: 'overview',
          label: t('detail.tabOverview'),
          children: <AppDetailOverview app={app} env={env} volumes={volumes} />,
        },
        {
          key: 'deploys',
          label: (
            <span className="inline-flex items-center gap-2">
              {t('detail.tabDeploys', { count: deploys.length })}
              {inFlight && (
                <span
                  className="inline-block size-1.5 rounded-full bg-[var(--accent)] animate-pulse"
                  aria-label={t('detail.deployInFlight')}
                />
              )}
            </span>
          ),
          children: <AppDetailDeploys deploys={deploys} appId={appId} />,
        },
        {
          key: 'logs',
          label: t('detail.tabLogs'),
          children: (
            <AppDetailLogs
              appId={appId}
              hasContainer={!!app.container}
              deployMethod={app.deployMethod}
            />
          ),
        },
        {
          key: 'files',
          label: t('detail.tabFiles'),
          children: (
            <AppDetailFiles
              appId={appId}
              hasContainer={!!app.container}
              deployMethod={app.deployMethod}
              currentContainerName={app.container?.name}
            />
          ),
        },
      ]}
    />
  )
}

function hasRunningDeploy(deploys: Deploy[]): boolean {
  return deploys.length > 0 && deploys[0].status === 'running'
}
