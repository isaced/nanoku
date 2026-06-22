import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { App, Button, Input } from 'antd'
import { ArrowRight, Lock } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '../lib/api'
import { markLoggedIn } from '../lib/auth'

export const Route = createFileRoute('/login')({
  component: Login,
})

function Login() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const { t } = useTranslation('login')
  const [user, setUser] = useState('admin')
  const [pass, setPass] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!pass) return
    setBusy(true)
    try {
      await api.login(user, pass)
      markLoggedIn()
      navigate({ to: '/sites' })
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        message.error(t('tooManyAttempts'))
      } else {
        message.error(t('invalidCredentials'))
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex-1 grid place-items-center bg-[var(--bg)] px-4">
      <div className="w-full max-w-sm">
        <div className="flex items-center gap-2 mb-1">
          <div className="size-2 rounded-full bg-[var(--accent)]" />
          <span className="mono text-xs tracking-widest uppercase text-[var(--fg-muted)]">
            nanoku
          </span>
        </div>
        <h1 className="text-3xl font-medium tracking-tight mb-2">
          {t('title')}
        </h1>
        <p className="text-sm text-[var(--fg-muted)] mb-8">{t('subtitle')}</p>

        <form onSubmit={onSubmit} className="space-y-4">
          <div>
            <label className="block text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1.5">
              {t('user')}
            </label>
            <Input
              value={user}
              onChange={(e) => setUser(e.target.value)}
              size="large"
              autoComplete="username"
            />
          </div>
          <div>
            <label className="block text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1.5">
              {t('password')}
            </label>
            <Input.Password
              value={pass}
              onChange={(e) => setPass(e.target.value)}
              size="large"
              autoComplete="current-password"
              autoFocus
              prefix={<Lock size={14} className="text-[var(--fg-muted)]" />}
            />
          </div>

          <Button
            type="primary"
            htmlType="submit"
            size="large"
            block
            loading={busy}
            icon={<ArrowRight size={14} />}
            iconPosition="end"
          >
            {t('submit')}
          </Button>
        </form>
      </div>
    </div>
  )
}