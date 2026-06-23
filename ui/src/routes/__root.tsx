import {
  Outlet,
  createRootRoute,
  HeadContent,
} from '@tanstack/react-router'
import { App as AntdApp, ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'
import { useTranslation } from 'react-i18next'

import '../styles.css'
import { Footer } from '../components/Footer'
import { RouteError } from '../components/RouteError'
import { TopNav } from '../components/TopNav'
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
  return (
    <>
      <HeadContent />
      <ConfigProvider locale={locale}>
        <AntdApp>
          <div className="min-h-screen flex flex-col bg-[var(--bg)]">
            <RootShell />
            <Outlet />
            <Footer systemStatus={systemStatusQuery.data ?? null} />
          </div>
        </AntdApp>
      </ConfigProvider>
    </>
  )
}

function RootShell() {
  const statusQuery = useStatus()
  return (
    <TopNav
      status={statusQuery.data ?? null}
    />
  )
}
