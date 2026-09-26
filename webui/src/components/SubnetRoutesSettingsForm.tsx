import { useI18n, T } from '../i18n'
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { Network } from 'lucide-react'
import { ApiError, isSessionError, setRoutingRequest, tailscaleRoutingRequest, type TailscaleRouting } from '../api'
import { useAuth } from '../auth'
import { forwardingWarnings, routingDraft } from '../routing'
import { parseSubnetRouteText } from '../subnetRoutes'
import { useTailscale } from '../tailscale'

export default function SubnetRoutesSettingsForm() {
  const { t, language } = useI18n()
  const { accessToken, logout } = useAuth()
  const { refresh, routing: liveRouting, routingError } = useTailscale()
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
  const warnings = routing ? forwardingWarnings(routing, true, routes, language) : []
  const approval = liveRouting ?? routing
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
      <div><span className="panel__eyebrow">{t("TAILSCALE ROUTING")}</span><h2 id="routing-settings-title">{t("Subnet routes")}</h2></div><Network size={20} />
    </div>
    <form className="login-form credential-form lan-form" onSubmit={submit} aria-busy={busy || loading}>
      <p className="credential-intro">{t("Give tailnet devices access to your local LAN. Only portal administrators can apply changes.")}</p>
      {loading && <p role="status">{t("Loading routing settings…")}</p>}
      {error && <div className="form-error" role="alert">{t(error)}</div>}
      {saved && <p className="form-success" role="status">{t("Routing advertisements saved. OAuth approval runs automatically when credentials are configured; check Tailnet approval below.")}</p>}
      {routing && !loading && <>
        <fieldset className="routing-section routing-section--standalone" disabled={busy || needsReload} aria-labelledby="routing-settings-title">
          <p className="password-help">{t("Enabled by default using the local LAN. Give tailnet devices access to networks behind this device, including devices without Tailscale.")}</p>
          {routing.subnetDefaultsPending && <p className="lan-warning">{t("Local LAN advertisement is pending. The portal will apply it automatically when Tailscale is connected and LAN addresses are available. You can save custom routes or disable it below.")}</p>}
          <label className="lan-acknowledgement" htmlFor="routing-subnet-enabled">
            <input id="routing-subnet-enabled" type="checkbox" checked={subnetEnabled}
              aria-describedby="routing-subnet-approval"
              onChange={event => { setSubnetEnabled(event.target.checked); edited() }} />
            <span>{t("Advertise subnet routes")}</span>
          </label>
          <p className="password-help" id="routing-subnet-approval"><T message="After enabling and saving subnet advertisements, approve the intended routes in Tailscale unless OAuth or tailnet policy already approved them. Advertising alone does not grant access. {0}." values={{ 0: <Link to="/tailscale-setup/subnet-routes" target="_blank" rel="noopener noreferrer">{t("Subnet route approval guide (new tab)")}</Link> }} /></p>
          <label htmlFor="routing-subnets">{t("Subnet CIDRs")}</label>
          <textarea id="routing-subnets" rows={4} value={routeText} disabled={!subnetEnabled} maxLength={8192}
            placeholder="192.168.1.0/24" aria-describedby="routing-subnet-help" aria-invalid={subnetEnabled && !!validation}
            onChange={event => { setRouteText(event.target.value); edited() }} />
          <p className="password-help" id="routing-subnet-help">{t("One network per line, or separate with commas. IPv4 and IPv6 are supported. Default routes are managed automatically for the exit node.")}</p>
          <p className="password-help"><T message="Local LAN ({0}): {1}. Used automatically for initial setup; later LAN changes do not replace saved routes. Disabling subnet advertising is remembered across restarts." values={{ 0: routing.lanInterface, 1: routing.defaultSubnetRoutes.join(', ') || t("Not detected") }} /></p>
          {routing.lanWarning && <p className="form-error">{t(routing.lanWarning)}</p>}
          <button className="text-action" type="button" disabled={!subnetEnabled || !routing.defaultSubnetRoutes.length}
            onClick={() => { setRouteText(routing.defaultSubnetRoutes.join('\n')); edited() }}>{t("Use local LAN")}</button>
          {validation && <p className="form-error" role="alert">{t(validation)}</p>}
        </fieldset>

        <div className="routing-readiness">
          <strong>{t("OS forwarding")}</strong>
          <p><T message="IPv4: {0} · IPv6: {1}" values={{ 0: t(forwardingLabel(routing.ipv4Forwarding)), 1: t(forwardingLabel(routing.ipv6Forwarding)) }} /></p>
          <p className="password-help">{t("Managed in the OS, not by this portal. Saving advertisements does not configure forwarding or the firewall.")}</p>
          {warnings.map(warning => <p className="lan-warning" key={warning}>{t(warning)}</p>)}
          {!routing.snatEnabled && <p className="lan-warning">{t("Subnet SNAT is disabled. Verify return routes and exit-node compatibility; this portal preserves the existing SNAT setting.")}</p>}
          {!!routing.health.length && <ul className="routing-health">{routing.health.map((message, index) => <li key={index}>{t(message)}</li>)}</ul>}
        </div>
        <div className="routing-readiness routing-approval" aria-live="polite">
          <strong><T message="Tailnet approval · {0}" values={{ 0: routingError ? t("Unavailable") : approval?.routeApprovalState === 'APPROVED' ? t("Approved") : approval?.routeApprovalState === 'ERROR' ? t("Needs attention") : approval?.routeApprovalState === 'DISABLED' ? t("Manual or policy-based") : t("Pending") }} /></strong>
          <p className={approval?.routeApprovalState === 'ERROR' || routingError ? 'form-error' : 'password-help'}>
            {routingError ? t("Unable to refresh approval status. Reload settings to check again.") : t(approval?.routeApprovalMessage)}
          </p>
          <p className="password-help">{t("Saved OAuth credentials automatically approve this device’s advertised exit node and subnets using devices:routes write permission. Approval runs in the background, including after startup or sign-in. This status is for saved advertisements, not unsaved edits.")}</p>
          <p className="password-help"><T message="Without OAuth credentials, approve routes in the {0} or configure auto-approvers. Tailnet access rules and client settings must still allow their use." values={{ 0: <a href="https://login.tailscale.com/admin/machines" target="_blank" rel="noopener noreferrer">{t("Tailscale admin console ↗")}</a> }} /></p>
        </div>
        {routing.backendState !== 'Running' && <p className="lan-warning">{t("Tailscale is not running. Connect it before advertising routes. You can still remove subnet advertisements. The exit-node setting remains enabled.")}</p>}
        <div className="lan-warning" id="routing-warning">{t("Advertising routes can expose your LAN to permitted tailnet devices. Changing subnet routes may disconnect this browser, SSH, or other clients. Keep local access available. Changes are not automatically reverted.")}</div>
        <label className="lan-acknowledgement" htmlFor="routing-ack">
          <input id="routing-ack" type="checkbox" required disabled={busy || needsReload} checked={acknowledged}
            onChange={event => setAcknowledged(event.target.checked)} aria-describedby="routing-warning" />
          <span>{t("I understand the access and connectivity changes.")}</span>
        </label>
        <button className="login-submit" type="submit" disabled={busy || needsReload || !changed || !canApply || !acknowledged}>
          {busy ? t("Applying routing settings…") : t("Save and apply routing")}
        </button>
      </>}
      <button className="text-action" type="button" disabled={busy || loading} onClick={() => { setSaved(false); void refresh(); void reload() }}>{t("Reload settings")}</button>
    </form>
  </section>
}

function forwardingLabel(value: boolean | null) {
  return value === null ? 'Unknown' : value ? 'Enabled' : 'Disabled'
}
