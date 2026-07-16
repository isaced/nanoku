// @vitest-environment jsdom
import { act, render } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import i18n from 'i18next'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAction } from './useAction'
import { ApiError } from '../api'

// Spy on the same module's message.* exports to capture calls.
// vi.mock has to be hoisted, but the spy fns are referenced by
// the mock factory and we replace their impl in beforeEach.
const successSpy = vi.fn()
const errorSpy = vi.fn()

vi.mock('antd', async () => {
  const actual = await vi.importActual<typeof import('antd')>('antd')
  return {
    ...actual,
    App: {
      ...actual.App,
      // The `useApp` hook is called inside `useAction`. Replace
      // it with a stable, test-controlled object so we can assert
      // on the calls without rendering a full AntdApp tree.
      useApp: () => ({
        message: {
          success: (...args: unknown[]) => successSpy(...args),
          error: (...args: unknown[]) => errorSpy(...args),
          warning: vi.fn(),
          info: vi.fn(),
          loading: vi.fn(),
        },
        notification: {
          success: vi.fn(),
          error: vi.fn(),
          warning: vi.fn(),
          info: vi.fn(),
        },
        modal: { confirm: vi.fn() },
      }),
    },
  }
})

beforeAll(async () => {
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'translation',
    ns: ['translation'],
    resources: {
      en: { translation: { ok: 'OK', failed: 'Failed: {{msg}}' } },
    },
    interpolation: { escapeValue: false },
  })
})

beforeEach(() => {
  successSpy.mockReset()
  errorSpy.mockReset()
})

afterEach(() => {
  vi.restoreAllMocks()
})

function ActionCapture({
  ref,
}: {
  ref: React.MutableRefObject<ReturnType<typeof useAction> | null>
}) {
  // The ref pattern avoids the re-render loop that an onReady
  // callback would create (which made earlier versions of this
  // test OOM).
  ref.current = useAction()
  return null
}

async function setupAction(): Promise<ReturnType<typeof useAction>> {
  const ref: React.MutableRefObject<ReturnType<typeof useAction> | null> = {
    current: null,
  }
  render(
    <I18nextProvider i18n={i18n}>
      <ActionCapture ref={ref} />
    </I18nextProvider>,
  )
  for (let i = 0; i < 50; i++) {
    if (ref.current) break
    await act(async () => {
      await Promise.resolve()
    })
  }
  if (!ref.current) throw new Error('useAction did not initialise')
  return ref.current
}

describe('useAction', () => {
  it('toasts the success key on resolve', async () => {
    const action = await setupAction()
    await act(async () => {
      await action.run(Promise.resolve('ok'), { success: 'ok' })
    })
    expect(successSpy).toHaveBeenCalledWith('OK')
  })

  it('falls back to err.message on reject when no error key is set', async () => {
    const action = await setupAction()
    await act(async () => {
      await action.run(Promise.reject(new Error('boom')))
    })
    expect(errorSpy).toHaveBeenCalledWith('boom')
  })

  it('skips the error toast on 401 ApiError (redirect handles it)', async () => {
    const action = await setupAction()
    await act(async () => {
      await action.run(
        Promise.reject(new ApiError(401, 'Unauthorized')),
      )
    })
    expect(errorSpy).not.toHaveBeenCalled()
  })

  it('calls onSuccess with the resolved value', async () => {
    const action = await setupAction()
    const onSuccess = vi.fn()
    await act(async () => {
      await action.run(Promise.resolve(42), { onSuccess })
    })
    expect(onSuccess).toHaveBeenCalledWith(42)
  })

  it('calls onError with the rejected error', async () => {
    const action = await setupAction()
    const onError = vi.fn()
    await act(async () => {
      await action.run(Promise.reject(new Error('nope')), { onError })
    })
    expect(onError).toHaveBeenCalledWith(expect.any(Error))
    expect((onError.mock.calls[0][0] as Error).message).toBe('nope')
  })

  it('uses successVars for templated success messages', async () => {
    const action = await setupAction()
    await act(async () => {
      await action.run(Promise.resolve('ok'), {
        success: 'failed',
        successVars: () => ({ msg: 'did it' }),
      })
    })
    expect(successSpy).toHaveBeenCalledWith('Failed: did it')
  })
})
