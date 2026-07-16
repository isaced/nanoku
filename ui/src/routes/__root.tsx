import {
  Outlet,
  createRootRoute,
  HeadContent,
  useLocation,
  useNavigate,
} from '@tanstack/react-router'
import { App as AntdApp, ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'
import { Globe, LayoutDashboard, Package, Settings } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import '../styles.css'
import { CommandPalette, useRoutePaletteItems } from '../components/CommandPalette'
import { Footer } from '../components/Footer'
import { RouteError } from '../components/RouteError'
import { TopNav } from '../components/TopNav'
import { useKeyCombo } from '../lib/keys'
import { useStatus, useSystemStatus } from '../lib/hooks'

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: 'utf-8' },
      { name: 'viewport', content: 'width=device-width, initial-scale=1' },
      { title: 'nanoku' },
      { name: 'description', content: 'Self-hosted deployment hub' },
    ],
  }),
  component: RootLayout,
  errorComponent: RouteError,
})

function RootLayout() {
  const { i18n } = useTranslation()
  const locale = i18n.language?.startsWith('zh') ? zhCN : enUS
  const systemStatusQuery = useSystemStatus()
  const location = useLocation()
  const navigate = useNavigate()
  // On the login page we drop the global chrome for an immersive full-screen
  // experience — no TopNav, no Footer.
  const chrome = location.pathname !== '/login'

  // Command palette: opens with ⌘K / Ctrl-K, lets the user type
  // to filter the route list and Enter to navigate. We also wire
  // the vim-style \`g d / g s / g a / g y\` keys as a power-user
  // shortcut. Both handlers live in the same place so adding a
  // new route is one entry in useRoutePaletteItems.
  const paletteItems = useRoutePaletteItems({
    dashboard: <LayoutDashboard size={14} />,
    sites: <Globe size={14} />,
    apps: <Package size={14} />,
    system: <Settings size={14} />,
  })
  useKeyCombo('g d', () => navigate({ to: '/dashboard' }))
  useKeyCombo('g s', () => navigate({ to: '/sites' }))
  useKeyCombo('g a', () => navigate({ to: '/apps' }))
  useKeyCombo('g y', () => navigate({ to: '/system' }))

  return (
    <>
      <HeadContent />
      <ConfigProvider locale={locale}>
        <AntdApp>
          <div className="min-h-screen flex flex-col bg-[var(--bg)]">
            {chrome && <RootShell />}
            <Outlet />
            {chrome && <Footer systemStatus={systemStatusQuery.data ?? null} />}
            <CommandPalette items={paletteItems} />
          </div>
        </AntdApp>
      </ConfigProvider>
    </>
  )
}

function RootShell() {
  const statusQuery = useStatus()
  return <TopNav status={statusQuery.data ?? null} />
}
