import { Outlet, createRootRoute, HeadContent } from '@tanstack/react-router'
import { App as AntdApp, ConfigProvider, theme } from 'antd'

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
  return (
    <>
      <HeadContent />
      <ConfigProvider
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
              horizontalItemSelectedColor: '#1f2937',
              horizontalItemHoverColor: '#f0b429',
              itemSelectedColor: '#1f2937',
              itemHoverColor: '#f0b429',
              itemActiveBg: 'transparent',
            },
            Layout: {
              headerBg: '#ffffff',
              headerHeight: 56,
              headerPadding: '0 32px',
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
