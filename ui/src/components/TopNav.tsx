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
    <Layout.Header className="!bg-white border-b border-[var(--border)] flex items-center gap-6">
      <div className="flex items-center gap-2">
        <div className="size-2 rounded-full bg-[var(--accent)]" />
        <span className="mono text-sm tracking-wider text-[var(--fg)]">nanoku</span>
      </div>
      <Menu
        mode="horizontal"
        selectedKeys={[location.pathname as NavKey]}
        onClick={({ key }) => navigate({ to: key as NavKey })}
        items={navItems.map((i) => ({ ...i }))}
        className="flex-1 !min-w-0 !border-0 !bg-transparent"
      />
      <div className="flex items-center gap-3 text-xs">
        <DockerBadge status={status} />
        <CaddyBadge status={status} />
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
              icon={<Globe size={13} />}
              className="!font-mono"
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
                size={13}
                className={loading ? 'animate-spin' : ''}
              />
            }
            onClick={onRefresh}
          />
        </Tooltip>
      </div>
    </Layout.Header>
  )
}

export function DockerBadge({ status }: { status: Status | null }) {
  const { t } = useTranslation('nav')
  if (!status) return null
  if (status.dockerConnected) {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--success)]">
        <CircleCheck size={12} />
        <span className="mono">docker</span>
      </span>
    )
  }
  return (
    <Tooltip title={t('dockerUnreachable')}>
      <span className="inline-flex items-center gap-1.5 text-[var(--danger)]">
        <CircleX size={12} />
        <span className="mono">docker</span>
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
        <CircleCheck size={12} />
        <span className="mono">caddy</span>
      </span>
    )
  }
  if (s === 'not_found' || s === 'skipped') {
    return (
      <span className="inline-flex items-center gap-1.5 text-[var(--fg-muted)]">
        <CircleDashed size={12} />
        <span className="mono">caddy · {s}</span>
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-[var(--danger)]">
      <CircleX size={12} />
      <span className="mono">caddy · {s}</span>
    </span>
  )
}
