import { useCallback, useEffect, useRef, useState } from 'react'

const REFRESH_INTERVAL_MS = 30_000

// One deadline drives both the visible countdown and the actual refresh.
// Wait for requests to settle before restarting, including manual refreshes.
export function useAutoRefresh(refresh: () => Promise<void>) {
  const [nextRefreshAt, setNextRefreshAt] = useState<number | null>(() => Date.now() + REFRESH_INTERVAL_MS)
  const [secondsRemaining, setSecondsRemaining] = useState(30)
  const pending = useRef(false)

  const refreshNow = useCallback(async () => {
    if (pending.current) return
    pending.current = true
    setNextRefreshAt(null)
    try {
      await refresh()
    } finally {
      pending.current = false
      setSecondsRemaining(30)
      setNextRefreshAt(Date.now() + REFRESH_INTERVAL_MS)
    }
  }, [refresh])

  useEffect(() => {
    if (nextRefreshAt === null) return
    const tick = () => {
      // Use elapsed time rather than subtracting ticks: background tabs can
      // throttle timers, and should catch up without a burst of requests.
      const remaining = Math.max(0, Math.ceil((nextRefreshAt - Date.now()) / 1000))
      setSecondsRemaining(remaining)
      if (remaining === 0) {
        window.clearInterval(timer)
        void refreshNow()
      }
    }
    const timer = window.setInterval(tick, 250)
    tick()
    return () => window.clearInterval(timer)
  }, [nextRefreshAt, refreshNow])

  return { secondsRemaining, refreshNow, autoRefreshing: nextRefreshAt === null }
}
