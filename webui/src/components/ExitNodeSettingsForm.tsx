import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Route } from 'lucide-react'
import { ApiError, isSessionError, setExitNodeRequest, tailscaleRoutingRequest, type TailscaleRouting } from '../api'
import { useAuth } from '../auth'
import { selectedExitNode } from '../routing'
import { useTailscale } from '../tailscale'

export default function ExitNodeSettingsForm() {
  const { accessToken, logout } = useAuth()
  const { refresh } = useTailscale()
  const pending = useRef<AbortController | null>(null)
  const submitting = useRef(false)
  const mounted = useRef(false)
  const [routing, setRouting] = useState<TailscaleRouting | null>(null)
  const [selected, setSelected] = useState('')
  const [allowLAN, setAllowLAN] = useState(false)
  const [acknowledged, setAcknowledged] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [needsReload, setNeedsReload] = useState(false)
  const [saved, setSaved] = useState(false)

  // Read fresh preferences on entering this tab, but never overwrite an in-progress draft
  // with the dashboard's periodic refreshes.
  const reload = useCallback(async () => {
    if (!accessToken) { logout(); return }
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setLoading(true); setError(''); setAcknowledged(false)
    try {
      const value = await tailscaleRoutingRequest(accessToken, controller.signal)
      if (controller.signal.aborted) return
      setRouting(value); setSelected(selectedExitNode(value)); setAllowLAN(value.allowLANAccess)
      setNeedsReload(false)
    } catch (error) {
      if (controller.signal.aborted) return
      if (isSessionError(error)) { logout(); return }
      setNeedsReload(true)
      setError(error instanceof Error ? error.message : 'Unable to read routing settings.')
    } finally { if (!controller.signal.aborted) setLoading(false) }
  }, [accessToken, logout])

  useEffect(() => {
    mounted.current = true
    void reload()
    return () => {
      mounted.current = false; pending.current?.abort()
    }
  }, [reload])

  const peer = routing?.exitNodes.find((node) => node.id === selected)
  const canSelect = routing?.backendState === 'Running' && !routing.advertiseExitNode
  const valid = selected === '' || (!!canSelect && peer?.online === true)
  const changed = !!routing && (selected !== selectedExitNode(routing) || allowLAN !== routing.allowLANAccess)

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting.current || loading || needsReload || !valid || !changed || !acknowledged) return
    if (!accessToken) { logout(); return }
    submitting.current = true; setBusy(true); setError(''); setSaved(false)
    try {
      await setExitNodeRequest(accessToken, { exitNodeID: selected, allowLANAccess: selected ? allowLAN : false })
      if (mounted.current) { setSaved(true); void refresh(); await reload() }
    } catch (error) {
      if (!mounted.current) return
      if (isSessionError(error)) { logout(); return }
      setNeedsReload(true)
      setError(error instanceof ApiError && error.isGraphQLError && error.status < 500 && error.message !== 'Internal Server Error' ? error.message :
        'The connection was interrupted or the server did not confirm the change. Routing may already have changed. Reconnect and reload settings before retrying.')
    } finally {
      submitting.current = false
      if (mounted.current) { setBusy(false); setAcknowledged(false) }
    }
  }

  return <section className="panel routing-settings" aria-labelledby="routing-settings-title">
    <div className="panel__header">
      <div><span className="panel__eyebrow">TAILSCALE ROUTING</span><h2 id="routing-settings-title">Exit node configuration</h2></div><Route size={20} />
    </div>
    <form className="login-form credential-form lan-form" onSubmit={submit} aria-busy={busy || loading}>
      <p className="credential-intro" id="routing-description">Route this device’s internet traffic through another device in your tailnet, or use its local gateway. Only portal administrators can apply changes.</p>
      {loading && <p role="status">Loading routing settings…</p>}
      {error && <div className="form-error" role="alert">{error}</div>}
      {saved && <p className="form-success" role="status">Routing settings saved.</p>}
      {routing && !loading && <>
        <label htmlFor="routing-exit-node">Exit node</label>
        <select id="routing-exit-node" name="exit_node" value={selected} disabled={busy || needsReload} onChange={(event) => {
          const next = event.target.value
          setSelected(next); setAllowLAN(next ? selected ? allowLAN : true : false); setAcknowledged(false); setSaved(false)
        }}>
          <option value="">None — use local gateway</option>
          {selected && !peer && <option value={selected} disabled>Unavailable — {routing.exitNodeIP || routing.exitNodeID}</option>}
          {routing.exitNodes.map((node) => <option key={node.id} value={node.id} disabled={!canSelect || !node.online}>
            {node.hostName || node.dnsName || node.id} · {node.tailscaleIPs.join(', ')}{node.online ? '' : ' (offline)'}
          </option>)}
        </select>
        {!routing.exitNodes.some((node) => node.online) && <p className="password-help">No online, approved exit nodes are visible. Set up an exit node on another device and approve it in the Tailscale admin console, then reload.</p>}
        {routing.backendState !== 'Running' && <p className="password-help">Tailscale is not running. Connect it before selecting an exit node. You can still clear a saved selection.</p>}
        {routing.advertiseExitNode && <p className="password-help">This device advertises itself as an exit node. Using another exit node is unavailable; this form does not change its advertisements.</p>}
        <label className="lan-acknowledgement" htmlFor="routing-allow-lan">
          <input id="routing-allow-lan" type="checkbox" name="exit_node_allow_lan" disabled={busy || needsReload || !selected} checked={!!selected && allowLAN}
            onChange={(event) => { setAllowLAN(event.target.checked); setAcknowledged(false); setSaved(false) }} />
          <span>Allow access to the local LAN while using the exit node</span>
        </label>
        <div className="lan-warning" id="routing-warning">Changing routes may interrupt this browser, SSH, or internet access. Keep local access available, especially if you turn off LAN access. Changes are not automatically reverted.</div>
        <label className="lan-acknowledgement" htmlFor="routing-ack">
          <input id="routing-ack" type="checkbox" required disabled={busy || needsReload} checked={acknowledged}
            onChange={(event) => setAcknowledged(event.target.checked)} aria-describedby="routing-warning" />
          <span>I understand that changing routing may disconnect me.</span>
        </label>
        <button className="login-submit" type="submit" disabled={busy || needsReload || !changed || !valid || !acknowledged}>
          {busy ? 'Applying routing settings…' : 'Save and apply routing'}
        </button>
      </>}
      <div className="routing-actions">
        <button className="secondary-button" type="button" disabled={busy || loading} onClick={() => { void reload() }}>Reload settings</button>
      </div>
      <a className="routing-guide" href="https://tailscale.com/docs/features/exit-nodes/how-to/setup" target="_blank" rel="noopener noreferrer">Exit-node setup guide ↗</a>
    </form>
  </section>
}
