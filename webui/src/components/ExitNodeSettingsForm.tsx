import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Network, Route } from 'lucide-react'
import { ApiError, isSessionError, setRoutingRequest, tailscaleRoutingRequest, type TailscaleRouting } from '../api'
import { useAuth } from '../auth'
import { forwardingWarnings, routingDraft } from '../routing'
import { parseSubnetRouteText } from '../subnetRoutes'
import { useTailscale } from '../tailscale'

export default function ExitNodeSettingsForm() {
  const { accessToken, logout } = useAuth()
  const { refresh } = useTailscale()
  const pending = useRef<AbortController | null>(null)
  const submitting = useRef(false)
  const mounted = useRef(false)
  const [routing, setRouting] = useState<TailscaleRouting | null>(null)
  const [subnetEnabled, setSubnetEnabled] = useState(false)
  const [routeText, setRouteText] = useState('')
  const [acknowledged, setAcknowledged] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [needsReload, setNeedsReload] = useState(false)
  const [saved, setSaved] = useState(false)

  // Polling on Overview must never replace a draft. Only explicit reload/apply
  // reads new settings into this form.
  const reload = useCallback(async () => {
    if (!accessToken) { logout(); return }
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setLoading(true); setError(''); setAcknowledged(false)
    try {
      const value = await tailscaleRoutingRequest(accessToken, controller.signal)
      if (controller.signal.aborted) return
      const draft = routingDraft(value)
      setRouting(value)
      setSubnetEnabled(draft.subnetEnabled); setRouteText(draft.routeText)
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
    return () => { mounted.current = false; pending.current?.abort() }
  }, [reload])

  let routes: string[] = [], validation = ''
  try {
    routes = subnetEnabled ? parseSubnetRouteText(routeText) : []
    if (subnetEnabled && routes.length === 0) validation = 'Enter at least one subnet route, or turn off subnet routing.'
  } catch (error) { validation = error instanceof Error ? error.message : 'Invalid subnet routes.' }
  const canApply = !validation && (!!routing && (routing.backendState === 'Running' || !routes.length))
  const changed = !!routing && (routing.subnetDefaultsPending || !routing.advertiseExitNode || routing.usingExitNode ||
    JSON.stringify(routes) !== JSON.stringify([...routing.subnetRoutes].sort()))
  const warnings = routing ? forwardingWarnings(routing, true, routes) : []
  function edited() { setAcknowledged(false); setSaved(false) }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting.current || loading || needsReload || !canApply || !changed || !acknowledged) return
    if (!accessToken) { logout(); return }
    submitting.current = true; setBusy(true); setError(''); setSaved(false)
    try {
      await setRoutingRequest(accessToken, { subnetRoutes: routes })
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
      <div><span className="panel__eyebrow">TAILSCALE ROUTING</span><h2 id="routing-settings-title">Exit node & subnet routes</h2></div><Route size={20} />
    </div>
    <form className="login-form credential-form lan-form" onSubmit={submit} aria-busy={busy || loading}>
      <p className="credential-intro">Use this device as an internet gateway and a gateway to your local LAN for other tailnet devices. Only portal administrators can apply changes.</p>
      {loading && <p role="status">Loading routing settings…</p>}
      {error && <div className="form-error" role="alert">{error}</div>}
      {saved && <p className="form-success" role="status">Routing advertisements saved. Verify approval and access rules in the Tailscale admin console before use.</p>}
      {routing && !loading && <>
        <fieldset className="routing-section" disabled={busy || needsReload}>
          <legend><Route size={18} /> Exit node</legend>
          <p className="credential-state">Always enabled · managed by this portal</p>
          <p className="password-help">This device is an exit node for other tailnet devices. The portal ensures this setting automatically; it cannot be disabled here.</p>
          <p className="password-help">Current advertisement: {routing.advertiseExitNode ? 'Advertised' : 'Pending'}. Tailscale must be signed in and running to carry traffic; admin approval may still be required.</p>
          {!routing.advertiseExitNode && <p className="lan-warning">The portal will configure the advertisement when Tailscale is available. If it stays pending, check the service logs and daemon permissions.</p>}
        </fieldset>

        <fieldset className="routing-section" disabled={busy || needsReload}>
          <legend><Network size={18} /> Subnet routes</legend>
          <p className="password-help">Enabled by default using the local LAN. Give tailnet devices access to networks behind this device, including devices without Tailscale.</p>
          {routing.subnetDefaultsPending && <p className="lan-warning">Local LAN advertisement is pending. The portal will apply it automatically when Tailscale is connected and LAN addresses are available. You can save custom routes or disable it below.</p>}
          <label className="lan-acknowledgement" htmlFor="routing-subnet-enabled">
            <input id="routing-subnet-enabled" type="checkbox" checked={subnetEnabled}
              onChange={event => { setSubnetEnabled(event.target.checked); edited() }} />
            <span>Advertise subnet routes</span>
          </label>
          <label htmlFor="routing-subnets">Subnet CIDRs</label>
          <textarea id="routing-subnets" rows={4} value={routeText} disabled={!subnetEnabled} maxLength={8192}
            placeholder="192.168.1.0/24" aria-describedby="routing-subnet-help" aria-invalid={subnetEnabled && !!validation}
            onChange={event => { setRouteText(event.target.value); edited() }} />
          <p className="password-help" id="routing-subnet-help">One network per line, or separate with commas. IPv4 and IPv6 are supported. Default routes are managed automatically for the exit node.</p>
          <p className="password-help">Local LAN ({routing.lanInterface}): {routing.defaultSubnetRoutes.join(', ') || 'Not detected'}. Used automatically for initial setup; later LAN changes do not replace saved routes. Disabling subnet advertising is remembered across restarts.</p>
          {routing.lanWarning && <p className="form-error">{routing.lanWarning}</p>}
          <button className="text-action" type="button" disabled={!subnetEnabled || !routing.defaultSubnetRoutes.length}
            onClick={() => { setRouteText(routing.defaultSubnetRoutes.join('\n')); edited() }}>Use local LAN</button>
          {validation && <p className="form-error" role="alert">{validation}</p>}
        </fieldset>

        <div className="routing-readiness">
          <strong>OS forwarding</strong>
          <p>IPv4: {forwardingLabel(routing.ipv4Forwarding)} · IPv6: {forwardingLabel(routing.ipv6Forwarding)}</p>
          <p className="password-help">Managed in the OS, not by this portal. Saving advertisements does not configure forwarding or the firewall.</p>
          {warnings.map(warning => <p className="lan-warning" key={warning}>{warning}</p>)}
          {!routing.snatEnabled && <p className="lan-warning">Subnet SNAT is disabled. Verify return routes and exit-node compatibility; this portal preserves the existing SNAT setting.</p>}
          {!!routing.health.length && <ul className="routing-health">{routing.health.map((message, index) => <li key={index}>{message}</li>)}</ul>}
        </div>
        <p className="password-help">Approve this device’s exit node and subnet routes in the <a href="https://login.tailscale.com/admin/machines" target="_blank" rel="noopener noreferrer">Tailscale admin console ↗</a>, unless auto-approvers already cover them. Tailnet access rules and client settings must also allow their use.</p>
        {routing.backendState !== 'Running' && <p className="lan-warning">Tailscale is not running. Connect it before advertising routes. You can still remove subnet advertisements. The exit-node setting remains enabled.</p>}
        {routing.usingExitNode && <p className="lan-warning">This device still has another exit node selected. The portal will clear that selection automatically to enforce this device’s exit-node role.</p>}
        <div className="lan-warning" id="routing-warning">Advertising routes can expose your LAN to permitted tailnet devices. Changing subnet routes may disconnect this browser, SSH, or other clients. Keep local access available. Changes are not automatically reverted.</div>
        <label className="lan-acknowledgement" htmlFor="routing-ack">
          <input id="routing-ack" type="checkbox" required disabled={busy || needsReload} checked={acknowledged}
            onChange={event => setAcknowledged(event.target.checked)} aria-describedby="routing-warning" />
          <span>I understand the access and connectivity changes.</span>
        </label>
        <button className="login-submit" type="submit" disabled={busy || needsReload || !changed || !canApply || !acknowledged}>
          {busy ? 'Applying routing settings…' : 'Save and apply routing'}
        </button>
      </>}
      <button className="text-action" type="button" disabled={busy || loading} onClick={() => { setSaved(false); void refresh(); void reload() }}>Reload settings</button>
    </form>
  </section>
}

function forwardingLabel(value: boolean | null) {
  return value === null ? 'Unknown' : value ? 'Enabled' : 'Disabled'
}
