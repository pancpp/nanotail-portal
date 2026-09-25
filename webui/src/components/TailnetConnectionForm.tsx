import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { LogOut, Power } from 'lucide-react'
import { ApiError, isSessionError, setTailscaleEnabledRequest, tailscaleConnectionRequest, type TailscaleConnection } from '../api'
import { useAuth } from '../auth'
import { useTailscale } from '../tailscale'

export default function TailnetConnectionForm() {
  const { accessToken, logout } = useAuth()
  const { refresh, logoutTailnet } = useTailscale()
  const [connection, setConnection] = useState<TailscaleConnection | null>(null)
  const [loading, setLoading] = useState(true)
  const [action, setAction] = useState<'toggle' | 'logout' | null>(null)
  const busy = action !== null
  const [acknowledged, setAcknowledged] = useState(false)
  const [needsReload, setNeedsReload] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const pending = useRef<AbortController | null>(null)
  const submitting = useRef(false)
  const mounted = useRef(false)

  const reload = useCallback(async () => {
    if (!accessToken) { logout(); return }
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setLoading(true); setError(''); setAcknowledged(false)
    try {
      const value = await tailscaleConnectionRequest(accessToken, controller.signal)
      if (controller.signal.aborted) return
      setConnection(value); setNeedsReload(false)
    } catch (error) {
      if (controller.signal.aborted) return
      if (isSessionError(error)) { logout(); return }
      setConnection(null); setNeedsReload(true)
      setError(error instanceof Error ? error.message : 'Unable to read the tailnet connection.')
    } finally { if (!controller.signal.aborted) setLoading(false) }
  }, [accessToken, logout])

  useEffect(() => {
    mounted.current = true; void reload()
    return () => { mounted.current = false; pending.current?.abort() }
  }, [reload])

  function submit(event: FormEvent) {
    event.preventDefault()
    void applyConnection('toggle')
  }

  async function applyConnection(nextAction: 'toggle' | 'logout') {
    if (submitting.current || loading || needsReload || !connection || !acknowledged) return
    if (nextAction === 'toggle' && !connection.enabled && !connection.canEnable) return
    if (!accessToken) { logout(); return }
    const enabled = !connection.enabled
    submitting.current = true; setAction(nextAction); setError(''); setSuccess('')
    try {
      if (nextAction === 'logout') await logoutTailnet()
      else await setTailscaleEnabledRequest(accessToken, enabled)
      if (mounted.current) {
        setSuccess(nextAction === 'logout' ? 'Logged out of Tailscale. Open Overview to sign in again.' :
          enabled ? 'Tailnet enabled on this device.' : 'Tailnet disabled on this device. Its login and preferences are kept.')
        if (nextAction === 'toggle') void refresh()
        await reload()
      }
    } catch (error) {
      if (!mounted.current) return
      if (isSessionError(error)) { logout(); return }
      setNeedsReload(true); setConnection(null)
      setError(error instanceof ApiError && error.isGraphQLError && error.status < 500 && error.message !== 'Internal Server Error' ? error.message :
        'The connection was interrupted or the server did not confirm the change. It may already have applied. Reconnect over the LAN and reload settings before retrying.')
    } finally {
      submitting.current = false
      if (mounted.current) { setAction(null); setAcknowledged(false) }
    }
  }

  return <section className="panel tailnet-settings" aria-labelledby="tailnet-heading">
    <div className="panel__header"><div><span className="panel__eyebrow">TAILSCALE</span><h2 id="tailnet-heading">Tailnet connection</h2></div><Power size={20} /></div>
    <form className="login-form credential-form lan-form" onSubmit={submit} aria-busy={busy || loading}>
      <p className="credential-intro">Turn this device’s existing tailnet connection on or off. Turning it off does not log out, remove credentials, or reset routing preferences. Only portal administrators can apply changes.</p>
      {loading && <p role="status">Loading tailnet connection…</p>}
      {error && <div className="form-error" role="alert">{error}</div>}
      {success && <p className="form-success" role="status">{success}</p>}
      {connection && !loading && <>
        <p className="tailnet-state">Tailnet: <strong>{connection.enabled ? 'On' : 'Off'}</strong> · Tailscale state: {connection.backendState}</p>
        {!connection.canEnable && <p className="password-help">Sign in to Tailscale and approve this device before turning the tailnet on. Saving OAuth credentials alone does not enroll the device. <Link to="/tailscale-setup">Read the setup guide</Link>.</p>}
        <div className="lan-warning" id="tailnet-warning">Changing this connection can interrupt the portal, SSH, and traffic through Tailscale. To turn it back on after disconnecting, use this device’s LAN address. Keep LAN or local console access available.</div>
        <label className="lan-acknowledgement" htmlFor="tailnet-ack">
          <input id="tailnet-ack" type="checkbox" required disabled={busy || needsReload} checked={acknowledged}
            onChange={(event) => setAcknowledged(event.target.checked)} aria-describedby="tailnet-warning" />
          <span>I have local access and understand that this may disconnect me.</span>
        </label>
        <button className="login-submit" type="submit" disabled={busy || needsReload || !acknowledged || (!connection.enabled && !connection.canEnable)}>
          <Power size={18} />{action === 'toggle' ? 'Applying connection change…' : connection.enabled ? 'Turn tailnet off' : 'Turn tailnet on'}
        </button>
        <p className="password-help" id="tailnet-logout-help">Logging out disconnects this device from Tailscale and requires signing in again from <Link to="/">Overview</Link>. Your portal login and saved OAuth credentials are kept.</p>
        <button className="secondary-button danger-action" type="button" disabled={busy || needsReload || !acknowledged}
          aria-describedby="tailnet-logout-help" onClick={() => { void applyConnection('logout') }}>
          <LogOut size={18} />{action === 'logout' ? 'Logging out of Tailscale…' : 'Log out of Tailscale'}
        </button>
      </>}
      <button className="secondary-button" type="button" disabled={busy || loading} onClick={() => { setSuccess(''); void reload() }}>Reload connection</button>
    </form>
  </section>
}
