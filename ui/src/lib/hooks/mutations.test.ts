import { describe, expect, it } from 'vitest'
import { appLifecycle } from './mutations'

describe('appLifecycle', () => {
  // The lifecycle contract is the single source of truth for which
  // buttons show in the apps list and the app detail drawer. The
  // most important property: a `restarting` container is `isLive`
  // (so the Stop button shows — without it, a short-lived image
  // on `unless-stopped` restart policy is unkillable from the UI).

  it('no container: only Start should be available', () => {
    const lc = appLifecycle(undefined)
    expect(lc.hasContainer).toBe(false)
    expect(lc.isLive).toBe(false)
    expect(lc.isRestarting).toBe(false)
    expect(lc.isRunning).toBe(false)
    expect(lc.isStopped).toBe(true)
  })

  it('null container: same as undefined', () => {
    const lc = appLifecycle(null)
    expect(lc.hasContainer).toBe(false)
    expect(lc.isStopped).toBe(true)
  })

  it('running: live, running, not stopped', () => {
    const lc = appLifecycle({ status: 'running' })
    expect(lc.hasContainer).toBe(true)
    expect(lc.isLive).toBe(true)
    expect(lc.isRunning).toBe(true)
    expect(lc.isRestarting).toBe(false)
    expect(lc.isStopped).toBe(false)
  })

  it('restarting: live, restarting, NOT stopped (key UX property)', () => {
    const lc = appLifecycle({ status: 'restarting' })
    expect(lc.hasContainer).toBe(true)
    expect(lc.isLive).toBe(true)
    expect(lc.isRestarting).toBe(true)
    expect(lc.isRunning).toBe(false)
    // isStopped is false: from the operator's POV, a restarting
    // container is not "stopped", it's "stuck looping" — they want
    // Stop, not Start.
    expect(lc.isStopped).toBe(false)
  })

  it('paused: live, not running, not stopped', () => {
    const lc = appLifecycle({ status: 'paused' })
    expect(lc.isLive).toBe(true)
    expect(lc.isRunning).toBe(false)
    expect(lc.isStopped).toBe(false)
  })

  it('exited: stopped, not live, container still present', () => {
    const lc = appLifecycle({ status: 'exited' })
    expect(lc.hasContainer).toBe(true)
    expect(lc.isLive).toBe(false)
    expect(lc.isStopped).toBe(true)
  })

  it('created: stopped (never started yet)', () => {
    const lc = appLifecycle({ status: 'created' })
    expect(lc.hasContainer).toBe(true)
    expect(lc.isLive).toBe(false)
    expect(lc.isStopped).toBe(true)
  })

  it('dead: stopped', () => {
    const lc = appLifecycle({ status: 'dead' })
    expect(lc.isStopped).toBe(true)
  })

  it('removing: stopped (transient state, treat as stopped)', () => {
    const lc = appLifecycle({ status: 'removing' })
    expect(lc.isStopped).toBe(true)
  })

  it('unknown status: not live, not stopped, hasContainer true', () => {
    // Future docker statuses (e.g. "deadcode" or custom) shouldn't
    // crash the UI; isStopped=false is the safe default so we
    // don't accidentally show a Start button for a stuck container.
    const lc = appLifecycle({ status: 'what-is-this' })
    expect(lc.hasContainer).toBe(true)
    expect(lc.isLive).toBe(false)
    expect(lc.isStopped).toBe(false)
  })
})
