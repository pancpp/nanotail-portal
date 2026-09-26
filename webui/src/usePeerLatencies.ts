import { useEffect, useState } from 'react'
import { isSessionError, tailscalePeerLatenciesRequest, type TailscaleStatus } from './api'
import { useAuth } from './auth'

// Mounted only with the peer table. Each visible status refresh requests one
// measurement; no independent polling or persistent latency cache is needed.
export function usePeerLatencies(status: TailscaleStatus | null, statusError: string) {
  const { accessToken, logout } = useAuth()
  const [values, setValues] = useState<Map<string, number | null>>(new Map())
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    let pending: AbortController | null = null
    let timeout: number | undefined

    function cancel() {
      const previous = pending
      pending = null
      previous?.abort()
      window.clearTimeout(timeout)
    }

    function refresh() {
      cancel()
      setValues(new Map())
      setLoading(false)
      if (!accessToken || document.hidden || statusError || status?.backendState !== 'Running' ||
        !status.peers.some((peer) => peer.online && peer.tailscaleIPs.length > 0)) return
      const controller = new AbortController()
      pending = controller
      setLoading(true)
      timeout = window.setTimeout(() => controller.abort(), 20_000)
      void tailscalePeerLatenciesRequest(accessToken, controller.signal).then((peers) => {
        if (pending === controller && !controller.signal.aborted) {
          setValues(new Map(peers.map((peer) => [peer.id, peer.latencyMs])))
        }
      }).catch((error: unknown) => {
        if (pending === controller && isSessionError(error)) logout()
      }).finally(() => {
        if (pending === controller) {
          window.clearTimeout(timeout)
          pending = null
          setLoading(false)
        }
      })
    }

    refresh()
    document.addEventListener('visibilitychange', refresh)
    return () => {
      cancel()
      document.removeEventListener('visibilitychange', refresh)
    }
  }, [accessToken, logout, status, statusError])

  return { values, loading }
}
