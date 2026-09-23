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
