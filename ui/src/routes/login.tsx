import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { App, Button, Input } from 'antd'
import { ArrowRight, Lock } from 'lucide-react'
import { useState } from 'react'
import { testCredentials } from '../lib/api'
import { setCredentials } from '../lib/auth'

export const Route = createFileRoute('/login')({
  component: Login,
})

function Login() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const [user, setUser] = useState('admin')
  const [pass, setPass] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!pass) return
    setBusy(true)
    try {
      const ok = await testCredentials(user, pass)
      if (!ok) {
        message.error('Invalid credentials')
        return
      }
      setCredentials({ user, pass })
      navigate({ to: '/sites' })
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
          Sign in to admin
        </h1>
        <p className="text-sm text-[var(--fg-muted)] mb-8">
          Self-hosted deployment hub. Enter your admin password.
        </p>

        <form onSubmit={onSubmit} className="space-y-4">
          <div>
            <label className="block text-[11px] tracking-widest uppercase text-[var(--fg-muted)] mb-1.5">
              User
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
              Password
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
            Continue
          </Button>
        </form>
      </div>
    </div>
  )
}