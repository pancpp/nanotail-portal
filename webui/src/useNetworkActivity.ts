import { useEffect, useState } from 'react'
import { isSessionError, networkActivityRequest } from './api'
import { useAuth } from './auth'
import { appendTrafficSample, emptyTrafficWindow, TRAFFIC_POLL_MS } from './traffic'

export function useNetworkActivity() {
  const { accessToken, logout } = useAuth()
  const [traffic, setTraffic] = useState(emptyTrafficWindow)
  const [error, setError] = useState('')
  const [paused, setPaused] = useState(document.hidden)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let stopped = false
    let next: number | undefined
    let pending: AbortController | null = null
    setTraffic(emptyTrafficWindow()); setError('')

    async function poll() {
      if (!accessToken || stopped || document.hidden) return
      const controller = new AbortController()
      pending = controller
      const timeout = window.setTimeout(() => controller.abort(), 5_000)
      try {
        const sample = await networkActivityRequest(accessToken, controller.signal)
        if (!stopped && pending === controller && !controller.signal.aborted) {
          setTraffic((previous) => appendTrafficSample(previous, sample)); setError('')
        }
      } catch (error) {
        if (stopped || pending !== controller) return
        if (isSessionError(error)) { stopped = true; logout(); return }
        setTraffic(emptyTrafficWindow())
        setError(controller.signal.aborted ? 'VPN traffic request timed out.' : error instanceof Error ? error.message : 'Unable to read VPN traffic.')
      } finally {
        window.clearTimeout(timeout)
        if (!stopped && pending === controller) {
          pending = null
          if (!document.hidden) next = window.setTimeout(() => { void poll() }, TRAFFIC_POLL_MS)
        }
      }
    }

    function visibilityChanged() {
      window.clearTimeout(next)
      const previous = pending; pending = null; previous?.abort()
      setPaused(document.hidden); setTraffic(emptyTrafficWindow()); setError('')
      if (!document.hidden) void poll()
    }

    document.addEventListener('visibilitychange', visibilityChanged)
    visibilityChanged()
    return () => {
      stopped = true
      window.clearTimeout(next)
      pending?.abort()
      document.removeEventListener('visibilitychange', visibilityChanged)
    }
  }, [accessToken, logout, attempt])

  return { ...traffic, error, paused, retry: () => setAttempt((value) => value + 1) }
}
