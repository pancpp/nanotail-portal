export interface LoginCredentials {
  username: string
  password: string
}

export interface PasswordChange {
  oldpassword: string
  newpassword: string
}

export class ApiError extends Error {
  readonly status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

// Reading expiry only controls the UI. The backend verifies JWT signatures.
export function tokenExpiry(token: string, now = Date.now()): number | null {
  try {
    const parts = token.split('.')
    if (parts.length !== 3 || parts.some((part) => !part)) return null
    const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')))
    const expiresAt = payload.exp * 1000
    if (
      !Number.isSafeInteger(payload.pid) || payload.pid <= 0 ||
      !Number.isSafeInteger(payload.exp) || !Number.isSafeInteger(expiresAt) ||
      expiresAt <= now
    ) return null
    return expiresAt
  } catch {
    return null
  }
}

async function apiError(response: Response, fallback: string): Promise<ApiError> {
  try {
    const payload = await response.json()
    if (typeof payload?.message === 'string' && payload.message) {
      return new ApiError(payload.message, response.status)
    }
  } catch {
    // Proxies and unavailable devices may return a non-JSON error page.
  }
  return new ApiError(fallback, response.status)
}

export async function loginRequest(credentials: LoginCredentials): Promise<string> {
  const response = await fetch('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(credentials),
  })
  if (!response.ok) {
    throw await apiError(response, 'Unable to sign in. Check your credentials.')
  }

  const payload = await response.json().catch(() => null)
  if (typeof payload?.token !== 'string' || tokenExpiry(payload.token) === null) {
    throw new Error('The server did not return a valid login token.')
  }
  return payload.token
}

const CHANGE_PASSWORD_MUTATION = `
  mutation ChangePassword($passwords: ChangePassword!) {
    changePassword(passwords: $passwords)
  }
`

export async function changePasswordRequest(token: string, passwords: PasswordChange): Promise<void> {
  const bytes = new TextEncoder().encode(passwords.newpassword).length
  if (bytes < 8 || bytes > 72) {
    throw new Error('New password must contain between 8 and 72 bytes.')
  }

  const response = await fetch('/api/v1/query', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({
      operationName: 'ChangePassword',
      query: CHANGE_PASSWORD_MUTATION,
      variables: { passwords },
    }),
  })
  const payload = await response.json().catch(() => null)
  const errors: unknown[] = Array.isArray(payload?.errors) ? payload.errors : []
  // Resolver failures can use HTTP 200, even when data is also present.
  if (!response.ok || errors.length > 0) {
    const messages = errors.flatMap((error) => {
      if (typeof error !== 'object' || error === null || !('message' in error)) return []
      return typeof error.message === 'string' && error.message.trim() ? [error.message] : []
    })
    const message = messages.join('\n') ||
      (typeof payload?.message === 'string' && payload.message) ||
      'Unable to change your password. Please try again.'
    throw new ApiError(message, response.status)
  }
  if (payload?.data?.changePassword !== true ||
      (payload.errors !== undefined && !Array.isArray(payload.errors))) {
    throw new ApiError('The server did not confirm the password change.', response.status)
  }
}

export function isSessionError(error: unknown): boolean {
  // JWT rejection is HTTP 401; an incorrect password is a GraphQL resolver error.
  return error instanceof ApiError && error.status === 401
}

export interface TailscalePeer {
  id: string
  hostName: string
  dnsName: string
  os: string
  tailscaleIPs: string[]
  online: boolean
}

// Only the fields selected by the WebUI's status query are included here.
export interface TailscaleStatus {
  backendState: string
  tailscaleIPs: string[]
  currentTailnet: { name: string } | null
  self: { online: boolean } | null
  peers: TailscalePeer[]
}

export interface TailscaleClient {
  clientId: string
  hasClientSecret: boolean
  updateTime: string
}

export interface TailscaleCredential {
  clientId: string
  clientSecret?: string
}

async function tailscaleGraphQL(token: string, operationName: string, query: string,
  variables: object = {}, signal?: AbortSignal) {
  const response = await fetch('/api/v1/query', {
    method: 'POST', signal,
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ operationName, query, variables }),
  })
  const payload = await response.json().catch(() => null)
  if (!response.ok || (Array.isArray(payload?.errors) && payload.errors.length > 0)) {
    const messages = Array.isArray(payload?.errors) ? payload.errors.flatMap((error: unknown) =>
      typeof error === 'object' && error !== null && 'message' in error &&
      typeof error.message === 'string' && error.message.trim() ? [error.message] : []) : []
    throw new ApiError(messages.join('\n') ||
      (typeof payload?.message === 'string' && payload.message) ||
      'Unable to complete the Tailscale request. Please retry.', response.status)
  }
  if (!payload?.data || (payload.errors !== undefined && !Array.isArray(payload.errors))) {
    throw new ApiError('The server returned an invalid Tailscale response.', response.status)
  }
  return payload.data
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === 'string')
}

function isTailscalePeer(value: unknown): value is TailscalePeer {
  return isRecord(value) && typeof value.id === 'string' && typeof value.hostName === 'string' &&
    typeof value.dnsName === 'string' && typeof value.os === 'string' &&
    isStringArray(value.tailscaleIPs) && typeof value.online === 'boolean'
}

function isTailscaleStatus(value: unknown): value is TailscaleStatus {
  return isRecord(value) && typeof value.backendState === 'string' && isStringArray(value.tailscaleIPs) &&
    (value.currentTailnet === null || (isRecord(value.currentTailnet) && typeof value.currentTailnet.name === 'string')) &&
    (value.self === null || (isRecord(value.self) && typeof value.self.online === 'boolean')) &&
    Array.isArray(value.peers) && value.peers.every(isTailscalePeer)
}

export async function tailscaleStatusRequest(token: string, signal?: AbortSignal): Promise<TailscaleStatus> {
  const data = await tailscaleGraphQL(token, 'TailscaleStatus', `query TailscaleStatus {
    tailscaleStatus {
      backendState
      tailscaleIPs
      currentTailnet { name }
      self { online }
      peers { id hostName dnsName os tailscaleIPs online }
    }
  }`, {}, signal)
  const status: unknown = data.tailscaleStatus
  if (!isTailscaleStatus(status)) {
    throw new Error('The server did not return a valid Tailscale status.')
  }
  return status
}

export async function tailscaleClientRequest(token: string, signal?: AbortSignal): Promise<TailscaleClient | null> {
  const data = await tailscaleGraphQL(token, 'TailscaleClient', `query TailscaleClient {
    tailscaleClient { clientId hasClientSecret updateTime }
  }`, {}, signal)
  const client = data.tailscaleClient
  if (client === null) return null
  if (!client || typeof client.clientId !== 'string' || typeof client.hasClientSecret !== 'boolean' ||
    typeof client.updateTime !== 'string') throw new Error('The server did not return valid credential settings.')
  return { clientId: client.clientId, hasClientSecret: client.hasClientSecret, updateTime: client.updateTime }
}

export async function setTailscaleCredentialRequest(token: string, credential: TailscaleCredential): Promise<void> {
  const clientId = credential.clientId.trim()
  const clientSecret = credential.clientSecret?.trim() || undefined
  if (!clientId || clientId.length > 512 || (clientSecret?.length ?? 0) > 4096 ||
    /[\s\p{Cc}]/u.test(clientId + (clientSecret ?? ''))) {
    throw new Error('Enter a valid client ID and secret without spaces.')
  }
  const data = await tailscaleGraphQL(token, 'SetTailscaleCredential', `mutation SetTailscaleCredential($credential: TailscaleCredential!) {
    setTailscaleCredential(credential: $credential)
  }`, { credential: { clientId, clientSecret } })
  if (data.setTailscaleCredential !== true) throw new Error('The server did not confirm that credentials were saved.')
}

export async function clearTailscaleCredentialRequest(token: string): Promise<void> {
  const data = await tailscaleGraphQL(token, 'ClearTailscaleCredential', `mutation ClearTailscaleCredential {
    clearTailscaleCredential
  }`)
  if (data.clearTailscaleCredential !== true) throw new Error('The server did not confirm that credentials were removed.')
}

export function shouldPromptForTailscale(status: TailscaleStatus | null, statusError: string): boolean {
  // Offline, stopped, starting, or unreachable daemons do not prove credentials are missing.
  return !statusError && status?.backendState === 'NeedsLogin'
}

export function isTailscaleConnected(status: TailscaleStatus | null, statusError = ''): boolean {
  return !statusError && status?.backendState === 'Running' && status.self?.online === true
}

export function tailscaleStatusLabel(status: TailscaleStatus | null, statusError: string): string {
  if (statusError) return 'Unavailable'
  if (!status) return 'Checking…'
  if (isTailscaleConnected(status)) return 'Connected'
  switch (status.backendState) {
    case 'NeedsLogin': return 'Needs setup'
    case 'Stopped': return 'Stopped'
    case 'Starting': return 'Starting'
    case 'NeedsMachineAuth': return 'Awaiting approval'
    default: return 'Not connected'
  }
}
