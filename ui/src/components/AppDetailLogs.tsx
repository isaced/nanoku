import { useEffect, useState } from 'react'
import { Select } from 'antd'
import { useTranslation } from 'react-i18next'
import { useAppContainers, useAppLogs } from '../lib/hooks'
import { LogViewer } from './LogViewer'

/**
 * AppDetailLogs is the "Logs" tab in the app detail drawer.
 *
 * Docker-mode apps just stream the single `current_container`'s
 * stdout. Compose-mode apps may have N services; we list the
 * containers in a Select and default to the first one once the
 * list lands. The container param is appended to the SSE URL so
 * the server-side log router picks the right stream.
 *
 * The query layer (useAppLogs) decodes the newline-joined
 * payload to `string[]` (see useAppLogs's `select`); we hand
 * the array straight to useLogStream as the seed buffer.
 */
export function AppDetailLogs({
  appId,
  hasContainer,
  deployMethod,
}: {
  appId: number
  hasContainer: boolean
  deployMethod: 'docker' | 'compose'
}) {
  const { t } = useTranslation('apps')
  const isCompose = deployMethod === 'compose'
  const [selectedContainer, setSelectedContainer] = useState<string | undefined>()
  const containersQuery = useAppContainers(appId, {
    enabled: isCompose && hasContainer,
  })

  // Compose: default to the first container once the list loads.
  // docker-mode skips the selector entirely and uses current_container.
  useEffect(() => {
    if (isCompose && containersQuery.data && containersQuery.data.length > 0 && !selectedContainer) {
      setSelectedContainer(containersQuery.data[0].name)
    }
  }, [isCompose, containersQuery.data, selectedContainer])

  const containerParam = isCompose ? selectedContainer : undefined
  const logsQuery = useAppLogs(appId, 300, containerParam, { enabled: hasContainer })
  // Seed the SSE stream with the last 300 lines from the one-shot
  // GET, so the user sees history the moment the tab opens. While
  // the GET is in flight the stream is already open at "now" and
  // the panel shows a connecting indicator - when the GET resolves
  // the seed lands and live lines append on top of it. The query
  // layer does the newline split (see useAppLogs), so we hand the
  // array straight to useLogStream.
  const initialLines = logsQuery.data
  if (!hasContainer) {
    return (
      <div className="mono text-xs text-[var(--fg-muted)] py-6 text-center">
        {t('detail.noContainer')}
      </div>
    )
  }
  const showSelector =
    isCompose && containersQuery.data && containersQuery.data.length > 0
  const streamUrl = `/api/apps/${appId}/logs/stream?tail=300${
    containerParam ? `&container=${encodeURIComponent(containerParam)}` : ''
  }`
  return (
    <div className="space-y-3">
      {showSelector && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-[var(--fg-muted)] whitespace-nowrap">
            {t('detail.container')}
          </span>
          <Select
            value={selectedContainer}
            onChange={setSelectedContainer}
            options={containersQuery.data!.map((c) => ({
              value: c.name,
              label: c.name,
            }))}
            className="!w-64"
            popupMatchSelectWidth={false}
          />
        </div>
      )}
      <LogViewer url={streamUrl} initialLines={initialLines} />
    </div>
  )
}
