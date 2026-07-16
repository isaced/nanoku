import { useEffect, useState } from 'react'
import { useAction, useRotateTriggerToken, useUpdateApp } from '../../lib/hooks'

/**
 * useTriggerToken owns the trigger section's transient state:
 * the freshly-rotated token (shown once, never persisted) and
 * the local "is the trigger configured?" mirror that lets the
 * UI reflect generate/disable without waiting for the editor
 * to be closed and reopened.
 *
 * Extracted from useAppEditorForm so the trigger section is
 * independently testable and so the main form hook doesn't
 * carry rotateToken / updateApp dependencies it doesn't otherwise
 * use (only the trigger mutations and the form patch on
 * enable:false use them).
 */
export function useTriggerToken({
  open,
  appId,
  initialConfigured,
}: {
  open: boolean
  appId: number | undefined
  initialConfigured: boolean
}) {
  const action = useAction()
  const rotateToken = useRotateTriggerToken()
  const updateApp = useUpdateApp()

  const [revealedToken, setRevealedToken] = useState<string | null>(null)
  // Mirrors `editing.triggerConfigured` but is updated locally on
  // generate/disable so the Trigger tab reflects the new state immediately,
  // without waiting for the editor to be closed and reopened.
  const [triggerEnabled, setTriggerEnabled] = useState<boolean>(false)

  // A freshly generated/rotated token lives only in this React state — it
  // is never persisted client-side and is cleared as soon as the editor
  // closes or a different app is opened, matching the "shown once" contract.
  // The trigger-enabled flag is re-derived from the app on (re)open.
  useEffect(() => {
    setRevealedToken(null)
    setTriggerEnabled(initialConfigured)
  }, [open, appId, initialConfigured])

  function onGenerate() {
    if (appId == null) return
    void action.run(rotateToken.mutateAsync(appId), {
      onSuccess: (updated) => {
        setTriggerEnabled(true)
        if (updated.triggerToken) {
          setRevealedToken(updated.triggerToken)
        }
      },
    })
  }

  function onDisable() {
    if (appId == null) return
    void action.run(
      updateApp.mutateAsync({ id: appId, input: { enableTrigger: false } }),
      {
        onSuccess: () => {
          setTriggerEnabled(false)
          setRevealedToken(null)
        },
      },
    )
  }

  return {
    revealedToken,
    triggerEnabled,
    onGenerate,
    onDisable,
  }
}
