import Button from './Button'
import { useI18n, T } from '../i18n'
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { Power } from 'lucide-react'
import { ApiError, isSessionError, setTailscaleEnabledRequest, tailscaleConnectionRequest, type TailscaleConnection } from '../api'
import { useAuth } from '../auth'
import { useTailscale } from '../tailscale'

export default function TailnetConnectionForm() {
  const { t } = useI18n()
  const { accessToken, logout } = useAuth()
  const { refresh } = useTailscale()
  const [connection, setConnection] = useState<TailscaleConnection | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
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
    void applyConnection()
  }

  async function applyConnection() {
    if (submitting.current || loading || needsReload || !connection || !acknowledged) return
    if (!connection.enabled && !connection.canEnable) return
    if (!accessToken) { logout(); return }
    const enabled = !connection.enabled
    submitting.current = true; setBusy(true); setError(''); setSuccess('')
    try {
      await setTailscaleEnabledRequest(accessToken, enabled)
      if (mounted.current) {
        setSuccess(enabled ? 'Tailnet enabled on this device.' : 'Tailnet disabled on this device. Its login and preferences are kept.')
        void refresh()
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
      if (mounted.current) { setBusy(false); setAcknowledged(false) }
    }
  }

  return <section className="panel tailnet-settings" aria-labelledby="tailnet-heading">
    <div className="panel__header"><div><span className="panel__eyebrow">TAILSCALE</span><h2 id="tailnet-heading">{t("Tailnet connection")}</h2></div><Power size={20} /></div>
    <form className="login-form credential-form lan-form" onSubmit={submit} aria-busy={busy || loading}>
      <p className="credential-intro">{t("Turn this device’s existing tailnet connection on or off. Turning it off does not log out, remove credentials, or reset routing preferences. Only portal administrators can apply changes.")}</p>
      {loading && <p role="status">{t("Loading tailnet connection…")}</p>}
      {error && <div className="form-error" role="alert">{t(error)}</div>}
      {success && <p className="form-success" role="status">{t(success)}</p>}
      {connection && !loading && <>
        <p className="tailnet-state"><T message="Tailnet: {0} · Tailscale state: {1}" values={{ 0: <strong>{connection.enabled ? t("On") : t("Off")}</strong>, 1: t(connection.backendState) }} /></p>
        {!connection.canEnable && <p className="password-help"><T message="Sign in to Tailscale and, if your tailnet requires it, approve this device before turning the tailnet on. Saving OAuth credentials alone does not enroll the device. {0}." values={{ 0: <Link to="/tailscale-setup/device-approval">{t("Read the sign-in and device approval guide")}</Link> }} /></p>}
        <div className="lan-warning" id="tailnet-warning">{t("Changing this connection can interrupt the portal, SSH, and traffic through Tailscale. To turn it back on after disconnecting, use this device’s LAN address. Keep LAN or local console access available.")}</div>
        <label className="lan-acknowledgement" htmlFor="tailnet-ack">
          <input id="tailnet-ack" type="checkbox" required disabled={busy || needsReload} checked={acknowledged}
            onChange={(event) => setAcknowledged(event.target.checked)} aria-describedby="tailnet-warning" />
          <span>{t("I have local access and understand that this may disconnect me.")}</span>
        </label>
        <Button className="login-submit" type="submit" disabledReason={busy ? t("Applying connection change…") : needsReload ? t("Reload connection settings before trying again.") : !connection.enabled && !connection.canEnable ? t("Sign in to Tailscale and approve this device before turning the tailnet on.") : !acknowledged ? t("Confirm the connection warning before applying.") : ""}>
          <span>{busy ? t("Applying connection change…") : connection.enabled ? t("Turn tailnet off") : t("Turn tailnet on")}</span><Power size={18} aria-hidden="true" />
        </Button>
      </>}
      <Button className="text-action" type="button" disabledReason={busy ? t("Applying connection change…") : loading ? t("Loading tailnet connection…") : ""} onClick={() => { setSuccess(''); void reload() }}>{t("Reload connection")}</Button>
    </form>
  </section>
}
