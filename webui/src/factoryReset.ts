import { ApiError } from './api.ts'

export class ResetOutcomeUnknown extends Error {
  constructor() {
    super('The connection ended before reset acceptance could be confirmed. The reset may still be running. Reopen your usual portal address over a connection that does not depend on Tailscale and verify the result before trying again.')
    this.name = 'ResetOutcomeUnknown'
  }
}

export function canConfirmReset(acknowledged: boolean, confirmation: string, password: string, busy: boolean): boolean {
  return acknowledged && confirmation === 'RESET' && password.length > 0 && !busy
}

// Do not retry a destructive request. A lost or malformed response is an
// unknown outcome, never evidence that it is safe to issue another reset.
export async function factoryResetRequest(token: string, confirmation: string, password: string): Promise<void> {
  if (!canConfirmReset(true, confirmation, password, false)) throw new Error('Type RESET and enter your current password.')
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 20_000)
  try {
    let response: Response
    try {
      response = await fetch('/api/v1/factory-reset', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ confirmed: true, confirmation, password }),
        signal: controller.signal,
      })
    } catch {
      throw new ResetOutcomeUnknown()
    }
    // A proxy can report 502/504 after the backend accepted and disconnected.
    if (response.status >= 500 || response.status === 408) throw new ResetOutcomeUnknown()
    if (!response.ok) {
      const payload = await response.json().catch(() => null)
      throw new ApiError(typeof payload?.message === 'string' ? payload.message : 'The server rejected the factory reset. No reset was accepted by this request.', response.status)
    }
    const payload = await response.json().catch(() => null)
    if (response.status !== 202 || payload?.accepted !== true) throw new ResetOutcomeUnknown()
  } finally {
    clearTimeout(timer)
  }
}
