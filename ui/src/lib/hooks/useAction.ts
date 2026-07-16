import { App } from 'antd'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api'

/**
 * useAction centralises the three things every mutation call site
 * was doing by hand:
 *
 *   1. show a success toast (or run an onSuccess callback)
 *   2. show an error toast on rejection
 *   3. let the 401 → login redirect path run via triggerUnauthorized
 *      (already wired through the QueryClient global error handler
 *      and the api fetch wrapper, but the toast needed to be
 *      suppressed for 401s so we don't double up the "you've been
 *      logged out" message with the redirect)
 *
 * Before this hook, the same ~10 lines of try/catch + message
 * .success/.error showed up in apps.tsx (×3), sites.tsx (×3),
 * ExposedPortsEditor.tsx, useAppEditorForm.ts, and the editor's
 * trigger section. Each copy could (and did) forget the
 * `err instanceof ApiError && err.status === 401` branch.
 *
 * Usage:
 *   const action = useAction()
 *   const stop = useStopApp()
 *   action.run(stop.mutateAsync(app.id), {
 *     success: () => t('toast.stopped', { name: app.name }),
 *     onSuccess: () => reload(),
 *   })
 *
 * `success` is an i18n key lookup (with optional vars). The error
 * fallback uses `err.message`; ApiError(401) skips the toast so
 * the redirect path is the only signal.
 */
export function useAction() {
  const { message } = App.useApp()
  const { t } = useTranslation()
  const run = useCallback(
    async <T>(
      promise: Promise<T>,
      opts: {
        success?: string | ((value: T) => string)
        successVars?: (value: T) => Record<string, unknown>
        error?: string | ((err: Error) => string)
        onSuccess?: (value: T) => void
        onError?: (err: Error) => void
      } = {},
    ): Promise<T | undefined> => {
      try {
        const value = await promise
        if (opts.success) {
          const text =
            typeof opts.success === 'function'
              ? opts.success(value)
              : opts.successVars
                ? t(opts.success, opts.successVars(value))
                : t(opts.success)
          message.success(text)
        }
        opts.onSuccess?.(value)
        return value
      } catch (err) {
        const e = err as Error
        // 401 is handled by the global handler (login redirect);
        // do not also surface a toast.
        if (!(e instanceof ApiError && e.status === 401)) {
          if (opts.error) {
            const text =
              typeof opts.error === 'function' ? opts.error(e) : t(opts.error)
            message.error(text)
          } else {
            message.error(e.message)
          }
        }
        opts.onError?.(e)
        return undefined
      }
    },
    [message, t],
  )
  return { run }
}
