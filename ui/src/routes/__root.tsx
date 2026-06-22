import { Outlet, createRootRoute, HeadContent } from '@tanstack/react-router'
import { App as AntdApp, ConfigProvider, theme } from 'antd'
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
        theme={{
          algorithm: theme.defaultAlgorithm,
          token: {
            colorPrimary: '#f0b429',
            colorInfo: '#f0b429',
            colorLink: '#1f2937',
            colorLinkHover: '#f0b429',
            borderRadius: 6,
          },
          components: {
            Menu: {
              itemColor: '#6b7280',
              itemHoverColor: '#1f2937',
              itemHoverBg: '#fafafa',
              itemSelectedColor: '#f0b429',
              horizontalItemSelectedColor: '#f0b429',
              horizontalItemHoverColor: '#1f2937',
              horizontalItemHoverBg: '#fafafa',
              itemActiveBg: 'transparent',
            },
            Layout: {
              headerBg: '#ffffff',
              headerHeight: 64,
            },
          },
        }}
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
