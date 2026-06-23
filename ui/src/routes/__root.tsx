import { Outlet, createRootRoute, HeadContent } from '@tanstack/react-router'
import { App as AntdApp, ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'
import { useTranslation } from 'react-i18next'

import '../styles.css'

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: 'utf-8' },
      { name: 'viewport', content: 'width=device-width, initial-scale=1' },
      { title: 'nanoku' },
      { name: 'description', content: 'Self-hosted deployment hub' },
    ],
  }),
  component: RootComponent,
})

function RootComponent() {
  const { i18n } = useTranslation()
  const locale = i18n.language?.startsWith('zh') ? zhCN : enUS
  return (
    <>
      <HeadContent />
      <ConfigProvider
        locale={locale}
      >
        <AntdApp>
          <div className="min-h-screen flex flex-col bg-[var(--bg)]">
            <Outlet />
          </div>
        </AntdApp>
      </ConfigProvider>
    </>
  )
}
