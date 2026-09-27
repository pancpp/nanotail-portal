import Button from './Button'
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Network } from 'lucide-react'
import { ApiError, DEFAULT_PEER_RELAY_PORT, isSessionError, setPeerRelayRequest, tailscaleRoutingRequest, type TailscaleRouting } from '../api'
import { useAuth } from '../auth'
import { useI18n } from '../i18n'
import { useTailscale } from '../tailscale'

export default function PeerRelaySettingsForm() {
  const { t } = useI18n()
  const { accessToken, logout } = useAuth()
  const { refresh } = useTailscale()
  const [settings, setSettings] = useState<TailscaleRouting | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [portText, setPortText] = useState(String(DEFAULT_PEER_RELAY_PORT))
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [needsReload, setNeedsReload] = useState(false)
  const [saved, setSaved] = useState(false)
  const pending = useRef<AbortController | null>(null)
  const submitting = useRef(false)
  const mounted = useRef(false)

  // Background status updates never overwrite an unsaved form.
  const reload = useCallback(async () => {
    if (!accessToken) { logout(); return }
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setLoading(true); setError('')
    try {
      const value = await tailscaleRoutingRequest(accessToken, controller.signal)
      if (controller.signal.aborted) return
      setSettings(value); setEnabled(value.peerRelayEnabled)
      setPortText(String(value.peerRelayPort || DEFAULT_PEER_RELAY_PORT))
      setNeedsReload(false)
    } catch (error) {
      if (controller.signal.aborted) return
      if (isSessionError(error)) { logout(); return }
      setNeedsReload(true)
      setError(error instanceof Error ? error.message : 'Unable to read peer relay settings.')
    } finally { if (!controller.signal.aborted) setLoading(false) }
  }, [accessToken, logout])

  useEffect(() => {
    mounted.current = true
    void reload()
    return () => { mounted.current = false; pending.current?.abort() }
  }, [reload])

  const port = enabled ? Number(portText) : DEFAULT_PEER_RELAY_PORT
  const valid = !enabled || (/^\d+$/.test(portText) && Number.isInteger(port) && port >= 1 && port <= 65535)
  const changed = !!settings && (enabled !== settings.peerRelayEnabled || (enabled && port !== settings.peerRelayPort))
  // Saving an already enabled relay can repair a missing console grant.
  const canSave = changed || enabled
  function edited() { setSaved(false) }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting.current || loading || needsReload || !settings || !valid || !canSave) return
    if (!accessToken) { logout(); return }
    submitting.current = true; setBusy(true); setError(''); setSaved(false)
    try {
      await setPeerRelayRequest(accessToken, { enabled, port })
      if (mounted.current) { setSaved(true); void refresh(); await reload() }
    } catch (error) {
      if (!mounted.current) return
      if (isSessionError(error)) { logout(); return }
      setNeedsReload(true)
      setError(error instanceof ApiError && error.isGraphQLError && error.status < 500 && error.message !== 'Internal Server Error' ? error.message :
        'The connection was interrupted or the server did not confirm the peer relay change. Reload peer relay settings before retrying.')
    } finally {
      submitting.current = false
      if (mounted.current) setBusy(false)
    }
  }

  return <section id="peer-relay" className="panel peer-relay-settings" aria-labelledby="peer-relay-heading">
    <div className="panel__header"><div><span className="panel__eyebrow">TAILSCALE</span><h2 id="peer-relay-heading">{t("Peer relay")}</h2></div><Network size={20} /></div>
    <form className="login-form credential-form lan-form" onSubmit={submit} aria-busy={loading || busy}>
      <p className="credential-intro">{t("Relay encrypted traffic between tailnet devices when they cannot connect directly. Only portal administrators can apply changes.")}</p>
      {loading && <p role="status">{t("Loading peer relay settings…")}</p>}
      {error && <p className="form-error" role="alert">{t(error)}</p>}
      {saved && !loading && !needsReload && <p className="form-success" role="status">{t("Peer relay settings saved.")}</p>}
      {settings && !loading && <>
        <label className="lan-acknowledgement" htmlFor="peer-relay-enabled">
          <input id="peer-relay-enabled" type="checkbox" checked={enabled} disabled={busy || needsReload}
            onChange={event => { setEnabled(event.target.checked); edited() }} />
          <span>{t("Use this device as a peer relay")}</span>
        </label>
        <label htmlFor="peer-relay-port">{t("UDP port")}</label>
        <input id="peer-relay-port" type="number" inputMode="numeric" min={1} max={65535} step={1} required value={portText}
          disabled={!enabled || busy || needsReload} aria-invalid={enabled && !valid} aria-describedby="peer-relay-port-help"
          onChange={event => { setPortText(event.target.value); edited() }} />
        <p className="password-help" id="peer-relay-port-help">{t("Default: {port}. Choose a UDP port from 1 to 65535.", { port: DEFAULT_PEER_RELAY_PORT })}</p>
        {enabled && !valid && <p className="form-error" role="alert">{t("Enter a UDP port from 1 to 65535")}</p>}
        {settings.peerRelayEnabled && settings.peerRelayPort === 0 && <p className="password-help">{t("Tailscale currently chooses a port automatically. Saving an enabled relay will use the port entered above.")}</p>}
        {settings.backendState !== 'Running' && <p className="password-help">{t("Tailscale is not running. Enabling requires this device to be signed in; the relay takes effect when Tailscale is connected.")}</p>}
        <p className="password-help">{t("Saving an enabled relay grants all tailnet devices access to relay through this device, preserving existing policy rules. Save OAuth credentials with policy_file write permission first. Requires Tailscale 1.86 or later and a reachable UDP port; saving does not verify reachability.")}</p>
        <a className="routing-guide" href="https://tailscale.com/docs/features/peer-relay" target="_blank" rel="noopener noreferrer">{t("Peer relay setup guide ↗")}</a>
        <Button className="login-submit" type="submit" disabledReason={busy ? t("Saving peer relay…") : needsReload ? t("Reload peer relay settings before trying again.") : !valid ? t("Enter a UDP port from 1 to 65535") : !canSave ? t("No changes to save.") : ""}>
          {busy ? t("Saving peer relay…") : t("Save peer relay")}
        </Button>
      </>}
      <Button className="text-action" type="button" disabledReason={busy ? t("Saving peer relay…") : loading ? t("Loading peer relay settings…") : ""} onClick={() => { setSaved(false); void refresh(); void reload() }}>{t("Reload peer relay settings")}</Button>
    </form>
  </section>
}
