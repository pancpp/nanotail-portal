import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import {
  changePasswordRequest,
  isSessionError,
  loginRequest,
  tokenExpiry,
  type LoginCredentials,
  type PasswordChange,
} from './api'

const TOKEN_STORAGE_KEY = 'nanotail_access_token'

interface AuthContextValue {
  isAuthenticated: boolean
  login: (credentials: LoginCredentials) => Promise<void>
  changePassword: (passwords: PasswordChange) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

function readStoredToken(): string | null {
  try {
    const token = window.localStorage.getItem(TOKEN_STORAGE_KEY)
    return token && tokenExpiry(token) !== null ? token : null
  } catch {
    return null
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [accessToken, setAccessToken] = useState<string | null>(readStoredToken)

  const logout = useCallback(() => {
    try {
      window.localStorage.removeItem(TOKEN_STORAGE_KEY)
    } catch {
      // Local sign-out still works when browser storage is unavailable.
    }
    setAccessToken(null)
  }, [])

  useEffect(() => {
    if (!accessToken) {
      logout()
      return
    }

    let timer: number
    const checkExpiry = () => {
      window.clearTimeout(timer)
      const expiresAt = tokenExpiry(accessToken)
      if (expiresAt === null) {
        logout()
        return
      }
      timer = window.setTimeout(checkExpiry, Math.min(expiresAt - Date.now(), 2_147_483_647))
    }
    checkExpiry()
    window.addEventListener('focus', checkExpiry)
    return () => {
      window.clearTimeout(timer)
      window.removeEventListener('focus', checkExpiry)
    }
  }, [accessToken, logout])

  useEffect(() => {
    const syncSession = (event: StorageEvent) => {
      if (event.key === TOKEN_STORAGE_KEY || event.key === null) {
        setAccessToken(readStoredToken())
      }
    }
    window.addEventListener('storage', syncSession)
    return () => window.removeEventListener('storage', syncSession)
  }, [])

  const login = useCallback(async (credentials: LoginCredentials) => {
    const token = await loginRequest(credentials)
    try {
      window.localStorage.setItem(TOKEN_STORAGE_KEY, token)
    } catch {
      // Keep this tab signed in even if persistent storage is disabled.
    }
    setAccessToken(token)
  }, [])

  const changePassword = useCallback(async (passwords: PasswordChange) => {
    if (!accessToken || tokenExpiry(accessToken) === null) {
      logout()
      throw new Error('Your session has expired. Please sign in again.')
    }
    try {
      await changePasswordRequest(accessToken, passwords)
    } catch (error) {
      if (isSessionError(error)) logout()
      throw error
    }
  }, [accessToken, logout])

  const value = useMemo(
    () => ({ isAuthenticated: Boolean(accessToken), login, changePassword, logout }),
    [accessToken, login, changePassword, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error('useAuth must be used inside AuthProvider')
  }
  return context
}
