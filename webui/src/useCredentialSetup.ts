import { useCallback, useEffect, useState } from 'react'
import type { TailscaleStatus } from './api'
import { CREDENTIAL_SETUP_STORAGE_KEY, emptyCredentialSetup, observeCredentialSetup, readCredentialSetup } from './credentialSetup'

export function useCredentialSetup(status: TailscaleStatus | null, statusError: string) {
  const [state, setState] = useState(() => {
    try { return readCredentialSetup(window.sessionStorage) } catch { return emptyCredentialSetup }
  })
  useEffect(() => {
    setState(previous => observeCredentialSetup(previous, status, statusError))
  }, [status, statusError])
  useEffect(() => {
    // Remember only the enrollment and prompt state across page reloads, never
    // the OAuth client ID, secret, or any form draft.
    try { window.sessionStorage.setItem(CREDENTIAL_SETUP_STORAGE_KEY, JSON.stringify(state)) } catch { /* In-memory state still works. */ }
  }, [state])
  const dismiss = useCallback(() => setState(previous => ({ ...previous, pending: false })), [])
  return { ...state, dismiss }
}
