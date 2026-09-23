export interface LoginCredentials {
  username: string
  password: string
}

export interface PasswordChange {
  current_password: string
  new_password: string
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

export async function changePasswordRequest(token: string, passwords: PasswordChange): Promise<void> {
  const bytes = new TextEncoder().encode(passwords.new_password).length
  if (bytes < 8 || bytes > 72) {
    throw new Error('New password must contain between 8 and 72 bytes.')
  }

  const response = await fetch('/api/change-password', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(passwords),
  })
  if (!response.ok) {
    throw await apiError(response, 'Unable to change your password. Please try again.')
  }
  // Success is 204 No Content, so there is no response JSON to decode.
}

export function isSessionError(error: unknown): boolean {
  // The endpoint also uses 401 for an incorrect current password; allow retries.
  return error instanceof ApiError && error.status === 401 &&
    error.message !== 'Current password is incorrect'
}
