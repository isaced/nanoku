import { createFileRoute, redirect } from '@tanstack/react-router'
import { DashboardPage } from '../components/Dashboard'
import { ensureAuth, isAuthenticated } from '../lib/auth'

export const Route = createFileRoute('/dashboard')({
  beforeLoad: async () => {
    if (isAuthenticated()) return
    if (!(await ensureAuth())) {
      throw redirect({ to: '/login' })
    }
  },
  component: DashboardPage,
})
