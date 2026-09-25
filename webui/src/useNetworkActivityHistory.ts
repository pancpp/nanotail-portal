import { useEffect, useState } from 'react'
import { isSessionError, networkActivityHistoryRequest, type NetworkActivityHistory } from './api'
import { useAuth } from './auth'

export function useNetworkActivityHistory() {
  const { accessToken, logout } = useAuth()
  const [history, setHistory] = useState<NetworkActivityHistory | null>(null)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let stopped = false
    let next: number | undefined
    let pending: AbortController | null = null
    setHistory(null); setError('')
    async function poll() {
      if (!accessToken || stopped || document.hidden) return
      const controller = new AbortController()
      pending = controller
      const timeout = window.setTimeout(() => controller.abort(), 5_000)
      try {
        const value = await networkActivityHistoryRequest(accessToken, controller.signal)
        if (!stopped && pending === controller && !controller.signal.aborted) { setHistory(value); setError('') }
      } catch (error) {
        if (stopped || pending !== controller) return
        if (isSessionError(error)) { stopped = true; logout(); return }
        setHistory(null)
        setError(controller.signal.aborted ? 'VPN history request timed out.' : error instanceof Error ? error.message : 'Unable to read VPN history.')
      } finally {
        window.clearTimeout(timeout)
        if (!stopped && pending === controller) {
          pending = null
          if (!document.hidden) next = window.setTimeout(() => { void poll() }, 60_000)
        }
      }
    }
    function visibilityChanged() {
      window.clearTimeout(next)
      const previous = pending; pending = null; previous?.abort()
      if (!document.hidden) void poll()
    }
    document.addEventListener('visibilitychange', visibilityChanged)
    visibilityChanged()
    return () => { stopped = true; window.clearTimeout(next); pending?.abort(); document.removeEventListener('visibilitychange', visibilityChanged) }
  }, [accessToken, logout, attempt])

  return { history, error, retry: () => setAttempt((value) => value + 1) }
}
