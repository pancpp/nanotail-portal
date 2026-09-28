import { useCallback, useEffect, useRef, useState } from 'react'
import { useI18n } from './i18n'
import { beginTailscaleNodeKeyRenewalRequest, cancelTailscaleNodeKeyRenewalRequest, isKeyRenewalPending, isSessionError, renewTailscaleNodeKeyRequest, tailscaleKeyRenewalRequest, type TailscaleKeyRenewal } from './api'

export interface NodeKeyRenewalSnapshot {
  value: TailscaleKeyRenewal | null
  pending: boolean
  busy: boolean
  starting: boolean
  preparing: boolean
  cancelling: boolean
  error: string
}

type RenewalAction = 'read' | 'prepare' | 'begin' | 'cancel'

// Owned by the Tailscale provider, not the dialog: closing it or changing tabs
// must not stop a pending renewal's read-only completion checks.
export function useNodeKeyRenewal(token: string | null, logout: () => void, refresh: () => Promise<void>) {
  const { t, language } = useI18n()
  const [snapshot, setSnapshot] = useState<NodeKeyRenewalSnapshot>({ value: null, pending: false, busy: false, starting: false, preparing: false, cancelling: false, error: '' })
  const request = useRef<AbortController | null>(null)
  const latest = useRef<TailscaleKeyRenewal | null>(null)
  const unresolved = useRef(false)
  const committed = useRef(false)
  const lastState = useRef<TailscaleKeyRenewal['state'] | null>(null)
  const signInWindow = useRef<{ window: Window, attemptID: string } | null>(null)
  const [popupBlocked, setPopupBlocked] = useState(false)

  const closeBlankWindow = useCallback(() => {
    // The reference is cleared before navigating to Tailscale, so this never
    // closes a real sign-in page or a window the user subsequently navigated.
    try {
      const popup = signInWindow.current?.window
      if (popup && !popup.closed && popup.location.href === 'about:blank') popup.close()
    } catch { /* The user navigated this tab elsewhere; leave it alone. */ }
    signInWindow.current = null
  }, [])

  useEffect(() => () => {
    request.current?.abort()
    request.current = null
    closeBlankWindow()
  }, [token, closeBlankWindow])

  const execute = useCallback(async (action: RenewalAction): Promise<TailscaleKeyRenewal | null> => {
    if (!token || request.current) return null
    const current = latest.current
    if (action === 'prepare' && !current?.canRenew) return null
    if (action === 'begin' && (!current?.attemptID || !(current.state === 'READY' || (current.state === 'STARTING' && current.canRenew)))) return null
    if (action === 'cancel' && current?.state !== 'READY') return null
    const controller = new AbortController()
    request.current = controller
    if (action === 'prepare') { committed.current = false; lastState.current = null; unresolved.current = true; setPopupBlocked(false) }
    if (action === 'begin') { committed.current = true; unresolved.current = true }
    if (action !== 'read') latest.current = null
    setSnapshot(previous => ({
      ...previous, busy: true, starting: action === 'begin', preparing: action === 'prepare', cancelling: action === 'cancel', error: '',
      ...(action !== 'read' ? { value: null, pending: true } : {}),
    }))
    try {
      const next = action === 'begin' ? await beginTailscaleNodeKeyRenewalRequest(token, current!.attemptID, controller.signal) :
        action === 'cancel' ? await cancelTailscaleNodeKeyRenewalRequest(token, current!.attemptID, controller.signal) :
          await (action === 'prepare' ? renewTailscaleNodeKeyRequest : tailscaleKeyRenewalRequest)(token, controller.signal)
      if (controller.signal.aborted) return null
      latest.current = next
      unresolved.current = isKeyRenewalPending(next)
      committed.current = !['IDLE', 'READY', 'CANCELLED'].includes(next.state)
      setSnapshot(previous => ({ ...previous, value: next, pending: isKeyRenewalPending(next) }))
      if (lastState.current !== next.state && ['COMPLETE', 'SIGNED_IN', 'AWAITING_APPROVAL', 'IDLE'].includes(next.state)) void refresh()
      lastState.current = next.state
      const popup = signInWindow.current
      if (popup && next.attemptID === popup.attemptID && next.state === 'AWAITING_LOGIN') {
        signInWindow.current = null
        try {
          if (!popup.window.closed && popup.window.location.href === 'about:blank') popup.window.location.replace(next.authURL)
          else setPopupBlocked(true)
        } catch { setPopupBlocked(true) }
      } else if (popup && ['COMPLETE', 'SIGNED_IN', 'IDLE', 'CANCELLED', 'AWAITING_APPROVAL'].includes(next.state)) closeBlankWindow()
      return next
    } catch (error) {
      if (controller.signal.aborted) return null
      if (isSessionError(error)) logout()
      latest.current = null
      closeBlankWindow()
      setSnapshot(previous => ({
        ...previous, value: null, // Never retain a stale sign-in URL or retry permission.
        error: error instanceof Error && error.name !== 'AbortError' ? error.message :
          (action === 'cancel' ? 'Cancellation could not be confirmed. Check status before closing.' :
            action === 'read' ? 'The status request timed out.' : 'The renewal request timed out.'),
      }))
      // A lost response is not proof of failure. Only an explicit status read
      // may resume polling; never automatically retry the mutation.
      return null
    } finally {
      if (request.current === controller) {
        request.current = null
        if (!controller.signal.aborted) setSnapshot(previous => ({ ...previous, busy: false, starting: false, preparing: false, cancelling: false }))
      }
    }
  }, [token, logout, refresh, closeBlankWindow])

  const checkStatus = useCallback(() => execute('read'), [execute])
  const renew = useCallback(() => execute('prepare'), [execute])
  const startSignIn = useCallback(() => {
    const current = latest.current
    if (request.current || !current?.attemptID || !(current.state === 'READY' || (current.state === 'STARTING' && current.canRenew))) return
    closeBlankWindow()
    // Open synchronously during the click, before awaiting the API, to avoid
    // popup blockers. Only a validated sign-in URL can replace this blank tab.
    try {
      const popup = window.open('about:blank', '_blank')
      setPopupBlocked(!popup)
      if (popup) {
        popup.opener = null
        const page = popup.document
        page.title = t('Preparing Tailscale sign-in')
        page.documentElement.lang = language
        const viewport = page.createElement('meta')
        viewport.name = 'viewport'
        viewport.content = 'width=device-width, initial-scale=1'
        page.head.append(viewport)
        page.body.style.cssText = 'margin:0;padding:32px 20px;background:#f4f7f5;color:#173d2a;font:18px/1.6 system-ui,sans-serif;'
        const content = page.createElement('main')
        content.style.cssText = 'max-width:560px;margin:10vh auto;padding:28px;background:white;border:1px solid #dce5df;border-radius:16px;'
        content.setAttribute('role', 'status')
        const heading = page.createElement('h1')
        heading.style.cssText = 'margin:0 0 16px;font-size:28px;line-height:1.3;'
        heading.textContent = page.title
        const message = page.createElement('p')
        message.textContent = t('Please wait while Tailscale prepares your sign-in link. This may take a few seconds.')
        const reminder = page.createElement('p')
        reminder.style.fontWeight = '600'
        reminder.textContent = t('Do not close this tab or the portal tab. You will be redirected automatically when the sign-in page is ready.')
        const progress = page.createElement('progress')
        progress.setAttribute('aria-label', page.title)
        progress.style.cssText = 'width:100%;accent-color:#07825b;'
        content.append(heading, message, reminder, progress)
        page.body.replaceChildren(content)
        signInWindow.current = { window: popup, attemptID: current.attemptID }
      }
    } catch { setPopupBlocked(true) }
    void execute('begin')
  }, [execute, closeBlankWindow, t, language])

  const close = useCallback(async (): Promise<boolean> => {
    if (request.current) return false
    let current = latest.current
    // Recover a lost preparation/cancellation response before dismissing it.
    if (!current && unresolved.current && !committed.current) {
      current = await execute('read')
      if (!current) return false
    }
    if (current?.state === 'READY') return (await execute('cancel'))?.state === 'CANCELLED'
    return true // No request, already cancelled/completed, or Sign in committed.
  }, [execute])

  useEffect(() => {
    if (!snapshot.pending || !snapshot.value || snapshot.error || snapshot.busy) return
    const timer = window.setTimeout(() => { void checkStatus() }, 1000)
    const onReturn = () => { if (!document.hidden) void checkStatus() }
    window.addEventListener('focus', onReturn)
    document.addEventListener('visibilitychange', onReturn)
    return () => {
      window.clearTimeout(timer)
      window.removeEventListener('focus', onReturn)
      document.removeEventListener('visibilitychange', onReturn)
    }
  }, [snapshot, checkStatus])

  const reset = useCallback(() => {
    request.current?.abort()
    request.current = null
    latest.current = null
    unresolved.current = false
    committed.current = false
    lastState.current = null
    closeBlankWindow()
    setPopupBlocked(false)
    setSnapshot({ value: null, pending: false, busy: false, starting: false, preparing: false, cancelling: false, error: '' })
  }, [closeBlankWindow])

  return { ...snapshot, popupBlocked, checkStatus, renew, startSignIn, close, reset }
}
