import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, isSessionError } from './api'
import {
  checkUpgradeRequest, downloadUpgradeRequest, installUpgradeRequest, installationPending,
  upgradeStatusRequest, UpgradeInstallOutcomeUnknown, versionComparable,
  type StagedUpgradePackage, type UpgradeInstallation, type UpgradeStatus,
} from './upgrade'

const ATTEMPT_KEY = 'nanotail_upgrade_attempt'
const WATCH_MS = 6 * 60_000
type Action = 'loading' | 'checking' | 'downloading' | 'installing'
interface Attempt {
  version: string
  sha256: string
  previousID: string | null
  id: string | null
  startedAt: number
  serverStartedAt?: string
  state: 'submitting' | 'accepted' | 'unknown' | 'rejected'
}

function readAttempt(): Attempt | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(ATTEMPT_KEY) ?? 'null')
    const validID = (id: unknown) => id === null || (typeof id === 'string' && id.length > 0 && id.length <= 128)
    if (value && versionComparable(value.version) && /^[a-f0-9]{64}$/.test(value.sha256) &&
      validID(value.id) && validID(value.previousID) && Number.isSafeInteger(value.startedAt) &&
      value.startedAt > 0 && value.startedAt <= Date.now() + 60_000 &&
      ['submitting', 'accepted', 'unknown', 'rejected'].includes(value.state)) {
      return { ...value, state: value.state === 'submitting' ? 'unknown' : value.state }
    }
  } catch { /* Status still discovers backend operations when storage is unavailable. */ }
  return null
}

function matches(attempt: Attempt, installation: UpgradeInstallation | null): boolean {
  return !!installation && installation.version === attempt.version &&
    (attempt.id ? installation.id === attempt.id : installation.id !== attempt.previousID)
}

function rejectedStatus(error: unknown): error is ApiError {
  return error instanceof ApiError && (error.status === 403 || error.isGraphQLError)
}

export function useUpgradeFlow(accessToken: string | null, logout: () => void) {
  const [status, setStatus] = useState<UpgradeStatus | null>(null)
  const [busy, setBusy] = useState<Action | null>('loading')
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(readAttempt)
  const attemptRef = useRef(attempt)
  const request = useRef<AbortController | null>(null)
  const [connectionLost, setConnectionLost] = useState(false)
  const [pollExpired, setPollExpired] = useState(false)
  const [pollPaused, setPollPaused] = useState(false)
  const [watchCycle, setWatchCycle] = useState(0)

  const saveAttempt = useCallback((next: Attempt | null) => {
    attemptRef.current = next
    setAttempt(next)
    try {
      if (next) sessionStorage.setItem(ATTEMPT_KEY, JSON.stringify(next))
      else sessionStorage.removeItem(ATTEMPT_KEY)
    } catch { /* Keep the current tab's operation state in memory. */ }
  }, [])

  const receiveStatus = useCallback((next: UpgradeStatus) => {
    const active = attemptRef.current
    if (active) {
      if (matches(active, next.installation)) {
        const installation = next.installation!
        if (!installationPending(installation) &&
          (installation.phase !== 'complete' || next.currentVersion === installation.version)) {
          saveAttempt(null)
        } else {
          saveAttempt({ ...active, id: installation.id, serverStartedAt: installation.startedAt, state: 'accepted' })
        }
        setError('')
      } else if (active.id && active.serverStartedAt && next.installation &&
        next.installation.id !== active.previousID &&
        Date.parse(next.installation.startedAt) > Date.parse(active.serverStartedAt)) {
        // Another administrator may finish a later operation while this tab is
        // away. Its server timestamp supersedes our accepted ID; a historical
        // completion (even of the same version) must not clear an unknown request.
        saveAttempt(null)
        setError('A later installation replaced this attempt. Showing the latest result.')
      } else if (!installationPending(next.installation) && ['idle', 'staged'].includes(next.phase) &&
        (next.installation === null || next.installation.id === active.previousID)) {
        // An explicit rejection is safe to retry only after checking for a
        // published journal. A lost response also needs the server's five-minute
        // preparation window to expire; one idle response is not proof of failure.
        if (active.state === 'rejected') saveAttempt(null)
        else if (!active.id && Date.now() - active.startedAt >= WATCH_MS) {
          saveAttempt(null)
          setError('The server did not record this installation. You can try again.')
        }
      }
    }
    setStatus(next)
    setConnectionLost(false)
  }, [saveAttempt])

  useEffect(() => {
    if (!accessToken) return
    const controller = new AbortController()
    request.current = controller
    setBusy('loading')
    void upgradeStatusRequest(accessToken, controller.signal).then(next => {
      if (!controller.signal.aborted) receiveStatus(next)
    }).catch(failure => {
      if (controller.signal.aborted) return
      if (isSessionError(failure)) logout()
      else if (rejectedStatus(failure)) { setError(failure.message); setPollPaused(true) }
      else if (attemptRef.current) setConnectionLost(true)
      else setError(failure instanceof Error ? failure.message : 'Unable to load upgrade status.')
    }).finally(() => {
      if (request.current === controller) request.current = null
      if (!controller.signal.aborted) setBusy(null)
    })
    return () => { controller.abort(); request.current?.abort(); request.current = null }
  }, [accessToken, logout, receiveStatus])

  const serverBusy = !!status && ['checking', 'downloading', 'verifying'].includes(status.phase)
  const monitoring = !!attempt || installationPending(status?.installation) || serverBusy

  useEffect(() => {
    if (!accessToken || !monitoring) return
    let stopped = false
    let controller: AbortController | null = null
    let timer: ReturnType<typeof setTimeout>
    const deadline = Date.now() + WATCH_MS
    setPollExpired(false)
    setPollPaused(false)
    const poll = async () => {
      if (stopped) return
      if (!request.current) {
        controller = new AbortController()
        request.current = controller
        try {
          const next = await upgradeStatusRequest(accessToken, controller.signal)
          if (!stopped) receiveStatus(next)
        } catch (failure) {
          if (stopped) return
          if (isSessionError(failure)) { logout(); return }
          if (rejectedStatus(failure)) {
            setError(failure.message)
            setConnectionLost(false)
            setPollPaused(true)
            return
          }
          setConnectionLost(true)
        } finally {
          if (request.current === controller) request.current = null
        }
      }
      if (stopped) return
      if (Date.now() >= deadline) setPollExpired(true)
      else timer = setTimeout(poll, 2000)
    }
    timer = setTimeout(poll, 2000)
    return () => {
      stopped = true
      clearTimeout(timer)
      controller?.abort()
      if (request.current === controller) request.current = null
    }
  }, [accessToken, logout, monitoring, receiveStatus, watchCycle])

  async function run(action: Exclude<Action, 'installing'>) {
    if (!accessToken || request.current || (monitoring && action !== 'loading')) return
    const release = status?.latestRelease
    if (action === 'downloading' && (!release || (!status?.updateAvailable && versionComparable(status?.currentVersion ?? '')))) return
    const controller = new AbortController()
    request.current = controller
    setBusy(action)
    setError('')
    try {
      const next = await (action === 'checking' ? checkUpgradeRequest(accessToken, controller.signal) :
        action === 'downloading' ? downloadUpgradeRequest(accessToken, release!.version, controller.signal) : upgradeStatusRequest(accessToken, controller.signal))
      if (controller.signal.aborted) return
      receiveStatus(next)
      if (action === 'loading') setWatchCycle(value => value + 1)
    } catch (failure) {
      if (controller.signal.aborted) return
      if (isSessionError(failure)) logout()
      else if (rejectedStatus(failure)) { setError(failure.message); setPollPaused(true) }
      else if (monitoring) setConnectionLost(true)
      else setError(failure instanceof Error ? failure.message : 'Unable to complete the upgrade request. Please try again.')
    } finally {
      if (request.current === controller) request.current = null
      if (!controller.signal.aborted) setBusy(null)
    }
  }

  async function install(selected: StagedUpgradePackage) {
    if (!accessToken || request.current || monitoring || !status?.installationSupported) return
    if (status.stagedPackage?.version !== selected.version || status.stagedPackage.sha256 !== selected.sha256) {
      setError('The verified package changed. Review it before installing.')
      return
    }
    const controller = new AbortController()
    request.current = controller
    setBusy('installing')
    setError('')
    setConnectionLost(false)
    const submitted: Attempt = { version: selected.version, sha256: selected.sha256,
      previousID: status.installation?.id ?? null, id: null, startedAt: Date.now(), state: 'submitting' }
    // Persist before sending: navigation or a disconnect must never cause a
    // repeated mutation or mistake a historical result for this installation.
    saveAttempt(submitted)
    try {
      const result = await installUpgradeRequest(accessToken, selected.version, selected.sha256, controller.signal)
      if (controller.signal.aborted) return
      saveAttempt({ ...submitted, id: result.installation.id, serverStartedAt: result.installation.startedAt, state: 'accepted' })
      setStatus(previous => previous && { ...previous, installation: result.installation })
    } catch (failure) {
      if (controller.signal.aborted) return
      if (isSessionError(failure)) { saveAttempt(null); logout(); return }
      const unknown = failure instanceof UpgradeInstallOutcomeUnknown
      saveAttempt({ ...submitted, state: unknown ? 'unknown' : 'rejected' })
      if (!unknown) setError(failure instanceof Error ? failure.message : 'Unable to start installation.')
      try {
        const next = await upgradeStatusRequest(accessToken, controller.signal)
        if (!controller.signal.aborted) receiveStatus(next)
      } catch (statusFailure) {
        if (controller.signal.aborted) return
        if (isSessionError(statusFailure)) logout()
        else if (rejectedStatus(statusFailure)) { setError(statusFailure.message); setPollPaused(true) }
        else setConnectionLost(true)
      }
    } finally {
      if (request.current === controller) request.current = null
      if (!controller.signal.aborted) setBusy(null)
    }
  }

  const installation = status?.installation && (!attempt || matches(attempt, status.installation) ||
    installationPending(status.installation)) ? status.installation : null
  return { status, busy, error, attempt, installation, monitoring, connectionLost, pollExpired, pollPaused, run, install }
}
