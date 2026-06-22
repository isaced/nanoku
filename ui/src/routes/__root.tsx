import { Outlet, createRootRoute, HeadContent } from '@tanstack/react-router'
import { App as AntdApp } from 'antd'

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
      <AntdApp>
        <div className="min-h-screen flex flex-col">
          <Outlet />
        </div>
      </AntdApp>
    </>
  )
}