import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from 'react'

const TOKEN_STORAGE_KEY = 'fairnet_access_token'

interface LoginCredentials {
  username: string
  password: string
}

interface LoginResponse {
  access_token?: string
  message?: string
}

interface AuthContextValue {
  isAuthenticated: boolean
  login: (credentials: LoginCredentials) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [accessToken, setAccessToken] = useState<string | null>(() =>
    window.localStorage.getItem(TOKEN_STORAGE_KEY),
  )

  const login = useCallback(async (credentials: LoginCredentials) => {
    const response = await fetch('/api/user/login', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(credentials),
    })

    let payload: LoginResponse = {}
    try {
      payload = (await response.json()) as LoginResponse
    } catch {
      // A non-JSON response is handled by the generic error below.
    }

    if (!response.ok) {
      throw new Error(payload.message || 'Unable to sign in. Check your credentials.')
    }

    if (!payload.access_token) {
      throw new Error('The server did not return an access token.')
    }

    window.localStorage.setItem(TOKEN_STORAGE_KEY, payload.access_token)
    setAccessToken(payload.access_token)
  }, [])

  const logout = useCallback(() => {
    window.localStorage.removeItem(TOKEN_STORAGE_KEY)
    setAccessToken(null)
  }, [])

  const value = useMemo(
    () => ({ isAuthenticated: Boolean(accessToken), login, logout }),
    [accessToken, login, logout],
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
