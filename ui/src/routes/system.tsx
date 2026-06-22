import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import {
  App,
  Button,
  InputNumber,
  Space,
  Tag,
  Tooltip,
} from 'antd'
import {
  CircleCheck,
  CircleDashed,
  CircleX,
  Container as ContainerIcon,
  Info,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { api, ApiError } from '../lib/api'
import { clearCredentials, getCredentials } from '../lib/auth'
import type { Status, SystemStatus } from '../lib/types'
import { TopNav } from '../components/TopNav'

export const Route = createFileRoute('/system')({
  beforeLoad: () => {
    if (!getCredentials()) {
      throw redirect({ to: '/login' })
    }
  },
  component: SystemPage,
})

const SELF_CONTAINER_ENV = 'NANOKU_SELF_CONTAINER'

function SystemPage() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const { t } = useTranslation('system')
  const [status, setStatus] = useState<Status | null>(null)
  const [systemStatus, setSystemStatus] = useState<SystemStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [caddyLogs, setCaddyLogs] = useState<string>('')
  const [caddyLoading, setCaddyLoading] = useState(false)
  const [selfLogs, setSelfLogs] = useState<string>('')
  const [selfLoading, setSelfLoading] = useState(false)
  const [tail, setTail] = useState<number>(200)

  const reload = useCallback(async () => {
    if (!getCredentials()) {
      navigate({ to: '/login' })
      return
    }
    setLoading(true)
    try {
      const [st, sys] = await Promise.all([api.status(), api.systemStatus()])
      setStatus(st)
      setSystemStatus(sys)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        clearCredentials()
        navigate({ to: '/login' })
      } else {
        message.error((err as Error).message)
      }
    } finally {
      setLoading(false)
    }
  }, [message, navigate])

  useEffect(() => {
    void reload()
  }, [reload])

  const loadCaddy = useCallback(async () => {
    setCaddyLoading(true)
    try {
      const out = await api.systemLogs('caddy', tail)
      setCaddyLogs(out)
    } catch (err) {
      message.error((err as Error).message)
    } finally {
      setCaddyLoading(false)
    }
  }, [message, tail])

  const loadSelf = useCallback(async () => {
    if (!systemStatus?.nanokuContainerConfigured) return
    setSelfLoading(true)
    try {
      const out = await api.systemLogs('nanoku', tail)
      setSelfLogs(out)
    } catch (err) {
      message.error((err as Error).message)
    } finally {
      setSelfLoading(false)
    }
  }, [message, tail, systemStatus?.nanokuContainerConfigured])

  useEffect(() => {
    if (systemStatus?.dockerAvailable) {
      void loadCaddy()
    }
  }, [loadCaddy, systemStatus?.dockerAvailable])

  useEffect(() => {
    if (systemStatus?.nanokuContainerConfigured) {
      void loadSelf()
    }
  }, [loadSelf, systemStatus?.nanokuContainerConfigured])

  return (
    <div className="flex-1 flex flex-col">
      <TopNav status={status} onRefresh={reload} loading={loading} />

      <main className="flex-1 px-8 py-8 max-w-6xl w-full mx-auto w-full">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-medium tracking-tight">{t('title')}</h1>
            <p className="text-sm text-[var(--fg-muted)] mt-1">{t('subtitle')}</p>
          </div>
          <Space>
            <span className="text-xs text-[var(--fg-muted)]">{t('tailLines')}</span>
            <InputNumber
              min={50}
              max={5000}
              step={50}
              value={tail}
              onChange={(v) => v && setTail(v as number)}
              className="!w-24"
            />
            <Button
              icon={<RefreshCw size={13} />}
              onClick={() => {
                void loadCaddy()
                void loadSelf()
              }}
              loading={caddyLoading || selfLoading}
            >
              {t('refreshBoth')}
            </Button>
          </Space>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <LogPanel
            icon={<ShieldCheck size={14} />}
            title={t('logPanel.nanokuSelf')}
            containerName={systemStatus?.nanokuContainerName}
            status={systemStatus?.nanokuContainerConfigured ? 'configured' : undefined}
            loading={selfLoading}
            logs={selfLogs}
            onRefresh={loadSelf}
            emptyHint={
              !systemStatus?.nanokuContainerConfigured ? (
                <div className="text-xs text-[var(--fg-muted)] space-y-1">
                  <div className="flex items-center gap-1.5">
                    <Info size={12} />
                    <Trans
                      ns="system"
                      i18nKey="nanokuHint.set"
                      values={{ envVar: SELF_CONTAINER_ENV }}
                      components={{ code: <code className="mono text-[var(--fg)]" /> }}
                    />
                  </div>
                  <div>{t('nanokuHint.docker')}</div>
                </div>
              ) : null
            }
          />
          <LogPanel
            icon={<ContainerIcon size={14} />}
            title={t('logPanel.caddy')}
            containerName={systemStatus?.caddyContainer}
            status={status?.caddyStatus}
            loading={caddyLoading}
            logs={caddyLogs}
            onRefresh={loadCaddy}
            emptyHint={
              !systemStatus?.dockerAvailable ? (
                <div className="text-xs text-[var(--fg-muted)] flex items-center gap-1.5">
                  <Info size={12} />
                  {t('dockerHint')}
                </div>
              ) : null
            }
          />
        </div>
      </main>
    </div>
  )
}

function LogPanel({
  icon,
  title,
  containerName,
  status,
  loading,
  logs,
  onRefresh,
  emptyHint,
}: {
  icon: React.ReactNode
  title: string
  containerName?: string
  status?: string
  loading: boolean
  logs: string
  onRefresh: () => void
  emptyHint?: React.ReactNode
}) {
  const { t } = useTranslation('system')
  return (
    <section className="border border-[var(--border)] rounded-lg bg-[var(--bg-elevated)] overflow-hidden flex flex-col">
      <div className="px-4 py-3 border-b border-[var(--border)] flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 min-w-0">
          <span className="text-[var(--fg-muted)]">{icon}</span>
          <span className="mono text-sm">{title}</span>
          {containerName && (
            <Tag className="!m-0 mono text-[10px]">{containerName}</Tag>
          )}
          {status && <StatusTag status={status} />}
        </div>
        <Tooltip title={t('logPanel.refresh')}>
          <Button
            type="text"
            size="small"
            icon={
              <RefreshCw
                size={13}
                className={loading ? 'animate-spin' : ''}
              />
            }
            onClick={onRefresh}
            loading={loading}
            disabled={!containerName}
          />
        </Tooltip>
      </div>
      <div className="flex-1 min-h-0">
        {emptyHint ? (
          <div className="p-4">{emptyHint}</div>
        ) : (
          <pre className="mono text-xs leading-relaxed bg-[var(--bg-input)] p-3 overflow-auto h-96 whitespace-pre-wrap break-all text-[var(--fg-muted)]">
            {logs || t('logPanel.clickRefresh')}
          </pre>
        )}
      </div>
    </section>
  )
}

function StatusTag({ status }: { status: string }) {
  if (status === 'running' || status === 'configured') {
    const color = status === 'running' ? 'green' : 'blue'
    return (
      <Tag color={color} className="!m-0">
        <span className="inline-flex items-center gap-1">
          <CircleCheck size={10} /> {status}
        </span>
      </Tag>
    )
  }
  if (status === 'not_found' || status === 'skipped') {
    return (
      <Tag className="!m-0">
        <span className="inline-flex items-center gap-1">
          <CircleDashed size={10} /> {status}
        </span>
      </Tag>
    )
  }
  return (
    <Tag color="red" className="!m-0">
      <span className="inline-flex items-center gap-1">
        <CircleX size={10} /> {status}
      </span>
    </Tag>
  )
}
