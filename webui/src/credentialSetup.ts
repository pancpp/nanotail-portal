import type { TailscaleStatus } from './api.ts'

export const CREDENTIAL_SETUP_STORAGE_KEY = 'nanotail_credential_setup'

export interface CredentialSetupState {
  binding: string | null
  awaitingBinding: boolean
  pending: boolean
}

export const emptyCredentialSetup: CredentialSetupState = { binding: null, awaitingBinding: false, pending: false }

// Only successful status observations can identify a new enrollment. Expired
// keys, temporary outages, and reconnecting an enrolled device are not new binds.
export function observeCredentialSetup(previous: CredentialSetupState, status: TailscaleStatus | null, error = ''): CredentialSetupState {
  if (error || !status) return previous
  if (status.backendState === 'NeedsLogin' && !status.haveNodeKey) {
    return previous.awaitingBinding && !previous.pending ? previous : { ...previous, awaitingBinding: true, pending: false }
  }
  if (!status.haveNodeKey || !['Running', 'Starting', 'Stopped', 'NeedsMachineAuth'].includes(status.backendState) ||
    !status.currentTailnet?.name || !status.self?.id) return previous
  const binding = JSON.stringify([status.currentTailnet.name, status.self.id])
  if (binding === previous.binding && !previous.awaitingBinding) return previous
  return {
    binding,
    awaitingBinding: false,
    pending: previous.awaitingBinding || (previous.binding !== null && previous.binding !== binding) || previous.pending,
  }
}

export function readCredentialSetup(storage: Pick<Storage, 'getItem'>): CredentialSetupState {
  try {
    const value = JSON.parse(storage.getItem(CREDENTIAL_SETUP_STORAGE_KEY) ?? 'null')
    if (value && (value.binding === null || typeof value.binding === 'string') &&
      typeof value.awaitingBinding === 'boolean' && typeof value.pending === 'boolean') {
      return { binding: value.binding, awaitingBinding: value.awaitingBinding, pending: value.pending }
    }
  } catch { /* Blocked storage or an invalid record must not prevent sign-in. */ }
  return emptyCredentialSetup
}
