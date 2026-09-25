import type { TailscaleStatus } from './api'

export function nodeKeyExpiryDisabled(status: TailscaleStatus | null): boolean {
  return status?.haveNodeKey === true && status.self?.keyExpiry === null
}

export function nodeKeyRenewDisabled(status: TailscaleStatus | null, statusError = '', active = false): boolean {
  if (nodeKeyExpiryDisabled(status)) return true
  return !active && (statusError !== '' || !status ||
    (status.backendState !== 'NeedsLogin' && nodeKeyStatus(status, statusError).state === 'unavailable'))
}

// Choose the dialog by key presence, not the daemon's NeedsLogin state.
// A device without a key needs the sign-in guide first.
export function nodeKeyDialogMode(status: TailscaleStatus | null): 'signin' | 'renewal' {
  return status?.haveNodeKey === false ? 'signin' : 'renewal'
}

interface NodeKeyStatus {
  state: 'loading' | 'unavailable' | 'unconfigured' | 'no-expiry' | 'active' | 'expiring' | 'expired'
  label: string
  description: string
  expiresAt: string | null
}

export function nodeKeyStatus(status: TailscaleStatus | null, statusError = '', now = Date.now()): NodeKeyStatus {
  const result = (state: NodeKeyStatus['state'], label: string, description: string, expiresAt: string | null = null): NodeKeyStatus =>
    ({ state, label, description, expiresAt })
  if (statusError) return result('unavailable', 'Unavailable', 'Unable to read Tailscale key expiry. Refresh status to retry.')
  if (!status) return result('loading', 'Checking…', 'Loading Tailscale node key status.')
  if (!status.haveNodeKey) return result('unconfigured', 'Not configured', 'This device does not have a Tailscale node key.')
  if (!status.self) return result('unavailable', 'Unavailable', 'Tailscale has not reported this device’s key expiry.')
  const expiresAt = status.self.keyExpiry
  // For an existing node key, a null expiration means key expiry is disabled.
  if (expiresAt === null) return result('no-expiry', 'Expiry disabled', 'Key expiry is disabled for this device.')
  const remaining = Date.parse(expiresAt) - now
  if (!Number.isFinite(remaining)) return result('unavailable', 'Unavailable', 'Tailscale did not report a valid key expiry date.')
  if (remaining <= 0) return result('expired', 'Expired', 'Expired', expiresAt)
  const days = Math.floor(remaining / 86_400_000)
  const hours = Math.floor(remaining / 3_600_000)
  const minutes = Math.floor(remaining / 60_000)
  const quantity = days || hours || minutes
  const unit = days ? 'day' : hours ? 'hour' : 'minute'
  const label = quantity ? `${quantity} ${unit}${quantity === 1 ? '' : 's'} remaining` : 'Less than a minute remaining'
  return result(remaining <= 7 * 86_400_000 ? 'expiring' : 'active', label, 'Expires', expiresAt)
}
