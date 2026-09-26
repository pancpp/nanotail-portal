import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { deviceStatusRequest, isSessionError, type DeviceStatus } from './api'
import { useAuth } from './auth'

interface DeviceContextValue {
  status: DeviceStatus | null
  error: string
  refreshing: boolean
  refresh: () => Promise<void>
}

const DeviceContext = createContext<DeviceContextValue | null>(null)

export function DeviceProvider({ children }: { children: ReactNode }) {
  const { accessToken, logout } = useAuth()
  const [status, setStatus] = useState<DeviceStatus | null>(null)
  const [error, setError] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const pending = useRef<AbortController | null>(null)

  const refresh = useCallback(async () => {
    if (!accessToken) return
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setRefreshing(true)
    try {
      const value = await deviceStatusRequest(accessToken, controller.signal)
      if (!controller.signal.aborted) { setStatus(value); setError('') }
    } catch (error) {
      if (controller.signal.aborted) return
      if (isSessionError(error)) logout()
      // Do not leave stale measurements or a healthy label visible on failure.
      setStatus(null)
      setError(error instanceof Error ? error.message : 'Unable to read device status.')
    } finally {
      if (!controller.signal.aborted) setRefreshing(false)
    }
  }, [accessToken, logout])

  useEffect(() => {
    setStatus(null)
    setError('')
    void refresh()
    return () => { pending.current?.abort() }
  }, [refresh])

  return <DeviceContext.Provider value={{ status, error, refreshing, refresh }}>
    {children}
  </DeviceContext.Provider>
}

export function useDevice() {
  const context = useContext(DeviceContext)
  if (!context) throw new Error('useDevice must be used inside DeviceProvider')
  return context
}
