import { useLocation, useNavigate } from '@tanstack/react-router'
import { Button, Dropdown, Layout, Menu, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import {
  CircleCheck,
  CircleDashed,
  CircleX,
  Globe,
  RefreshCw,
} from 'lucide-react'
import type { Status } from '../lib/types'
import { SUPPORTED_LANGUAGES, type SupportedLanguage } from '../i18n'

const navKeys = ['/dashboard', '/sites', '/apps', '/system'] as const
type NavKey = (typeof navKeys)[number]

export function TopNav({
  status,
  onRefresh,
  loading,
}: {
  status: Status | null
  onRefresh: () => void
  loading: boolean
}) {
  const navigate = useNavigate()
  const location = useLocation()
  const { t, i18n } = useTranslation('nav')

  const navItems = [
    { key: '/dashboard', label: t('dashboard') },
    { key: '/sites', label: t('sites') },
    { key: '/apps', label: t('apps') },
    { key: '/system', label: t('system') },
  ]

  const currentLang: SupportedLanguage = i18n.language?.startsWith('zh')
    ? 'zh'
    : 'en'

  return (
    <div className="w-full bg-white border-b border-[var(--border)]">
      <Layout.Header className="!h-16 !bg-transparent flex items-center !px-8 !gap-8 max-w-6xl w-full mx-auto">
      <button
        type="button"
        onClick={() => navigate({ to: '/dashboard' })}
        className="flex items-center gap-2.5 cursor-pointer bg-transparent border-0 p-0"
      >
        <div className="size-7 rounded-md bg-[var(--accent)] flex items-center justify-center text-white font-bold text-[15px] leading-none shadow-[0_1px_2px_rgba(240,180,41,0.35)]">
          N
        </div>
        <span className="text-[15px] font-semibold tracking-tight text-[var(--fg)]">
          nanoku
        </span>
      </button>
      <Menu
        mode="horizontal"
        selectedKeys={[location.pathname as NavKey]}
        onClick={({ key }) => navigate({ to: key as NavKey })}
        items={navItems}
        className="flex-1 !min-w-0 !border-0 !bg-transparent"
      />
      <div className="flex items-center gap-2">
        {status && (
          <div className="flex items-center gap-3 px-3 py-1.5 rounded-md bg-[var(--bg-elevated)] border border-[var(--border)]">
            <DockerBadge status={status} />
            <div className="w-px h-3.5 bg-[var(--border)]" />
            <CaddyBadge status={status} />
          </div>
        )}
        <div className="w-px h-5 bg-[var(--border)] mx-1" />
        <Tooltip title={t('language')}>
          <Dropdown
            placement="bottomRight"
            menu={{
              selectable: true,
              selectedKeys: [currentLang],
              items: [
                { key: 'en', label: t('languageEnglish') },
                { key: 'zh', label: t('languageChinese') },
              ],
              onClick: ({ key }) => {
                if (
                  (SUPPORTED_LANGUAGES as readonly string[]).includes(key) &&
                  key !== i18n.language
                ) {
                  void i18n.changeLanguage(key)
                }
              },
            }}
          >
            <Button
              type="text"
              size="small"
              icon={<Globe size={15} />}
              className="!font-mono !text-[var(--fg-muted)] hover:!text-[var(--fg)] hover:!bg-[var(--bg-elevated)]"
            >
              {currentLang === 'zh' ? '中' : 'EN'}
            </Button>
          </Dropdown>
        </Tooltip>
        <Tooltip title={t('refresh')}>
          <Button
            type="text"
            size="small"
            icon={
              <RefreshCw
                size={15}
                className={loading ? 'animate-spin' : ''}
              />
            }
            onClick={onRefresh}
            className="!text-[var(--fg-muted)] hover:!text-[var(--fg)] hover:!bg-[var(--bg-elevated)]"
          />
        </Tooltip>
      </div>
      </Layout.Header>
    </div>
  )
}

export function DockerBadge({ status }: { status: Status | null }) {
  const { t } = useTranslation('nav')
  if (!status) return null
  if (status.dockerConnected) {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--success)]">
        <CircleCheck size={13} />
        <span className="mono text-[11px]">docker</span>
      </span>
    )
  }
  return (
    <Tooltip title={t('dockerUnreachable')}>
      <span className="inline-flex items-center gap-1.5 text-[var(--danger)]">
        <CircleX size={13} />
        <span className="mono text-[11px]">docker</span>
      </span>
    </Tooltip>
  )
}

export function CaddyBadge({ status }: { status: Status | null }) {
  if (!status) return null
  const s = status.caddyStatus
  if (s === 'running') {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--success)]">
        <CircleCheck size={13} />
        <span className="mono text-[11px]">caddy</span>
      </span>
    )
  }
  if (s === 'not_found' || s === 'skipped') {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--fg-muted)]">
        <CircleDashed size={13} />
        <span className="mono text-[11px]">caddy · {s}</span>
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-[var(--danger)]">
      <CircleX size={13} />
      <span className="mono text-[11px]">caddy · {s}</span>
    </span>
  )
}
