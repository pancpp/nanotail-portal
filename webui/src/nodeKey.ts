import type { TailscaleStatus } from './api'

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
  // A null date does not establish the reason: expiry may be disabled, or the
  // daemon may lack expiry information. Do not invent a lifetime or percentage.
  if (expiresAt === null) return result('no-expiry', 'No expiry reported', 'Tailscale returned no expiration date for this node key.')
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
