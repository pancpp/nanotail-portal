import Button from './Button'
import { useI18n, T } from '../i18n'
import { useEffect, useId, useState, type FormEvent } from 'react'
import { Network } from 'lucide-react'
import { ApiError, deviceReconnectURL, isSessionError, setDeviceIPRequest, validateDeviceIP, type DeviceIP, type DeviceStatus } from '../api'
import { useAuth } from '../auth'
import { useDevice } from '../device'

function initialValues(status: DeviceStatus) {
  return {
    type: status.lanIPType === 'DHCP' ? 'DHCP' : status.lanIPType === 'static' ? 'static' : '',
    ip: status.lanIP, gateway: status.gateway,
    dns: status.dns.filter((address) => !address.includes(':')).join(', '),
  }
}

export default function LANSettingsForm() {
  const { t } = useI18n()
  const { accessToken, logout } = useAuth()
  const { status, error: statusError, refreshing, refresh } = useDevice()
  const [draft, setDraft] = useState<ReturnType<typeof initialValues> | null>(null)
  const [acknowledged, setAcknowledged] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [reconnect, setReconnect] = useState<string | null>(null)
  const id = useId()

  // Initialize once. Background status refreshes must not overwrite edits.
  useEffect(() => { if (status && draft === null) setDraft(initialValues(status)) }, [status, draft])

  function change(field: keyof NonNullable<typeof draft>, value: string) {
    if (!draft) return
    setDraft({ ...draft, [field]: value })
    setAcknowledged(false); setError(''); setSuccess(''); setReconnect(null)
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (busy || !draft) return
    setError(''); setSuccess(''); setReconnect(null)
    if (!accessToken) { logout(); return }
    let input: DeviceIP
    try {
      input = validateDeviceIP(draft.type === 'DHCP' ? { type: 'DHCP', ip: '', gateway: '', dns: [] } : {
        type: draft.type as DeviceIP['type'], ip: draft.ip, gateway: draft.gateway,
        dns: draft.dns.trim() ? draft.dns.trim().split(/[\s,]+/) : [],
      })
    } catch (error) { setError(error instanceof Error ? error.message : 'Check your LAN settings.'); return }
    if (!acknowledged) { setError('Confirm the connection warning before applying.'); return }
    setBusy(true)
    if (input.type === 'static') setReconnect(deviceReconnectURL(window.location.href, input.ip))
    try {
      await setDeviceIPRequest(accessToken, input)
      setSuccess(input.type === 'DHCP' ? 'DHCP settings saved and applied. If disconnected, find the assigned address in your router’s DHCP client list.' :
        'Static IPv4 settings saved and applied. If your address changed, reconnect below and sign in again.')
      setAcknowledged(false)
      void refresh()
    } catch (error) {
      if (isSessionError(error)) { logout(); return }
      setError(error instanceof ApiError && error.isGraphQLError && error.status < 500 ? error.message :
        'The connection was interrupted or the server did not confirm the change. Settings may already have been applied. Check the device before retrying.')
    } finally { setBusy(false); setAcknowledged(false) }
  }

  return <section className="panel lan-settings" aria-labelledby={`${id}-heading`}>
    <div className="panel__header"><div><span className="panel__eyebrow">{t("NETWORK · ETH0")}</span><h2 id={`${id}-heading`}>{t("LAN IPv4 settings")}</h2></div><Network size={20} /></div>
    <form className="login-form credential-form lan-form" onSubmit={submit} aria-busy={busy}>
      <p className="credential-intro">{t("Choose DHCP or a static IPv4 address for eth0. IPv6 configuration is unchanged. Only portal administrators can apply changes.")}</p>
      {statusError && <div className="form-error" role="status"><T message="Current device status is unavailable. {0}{1}" values={{ 0: t(statusError), 1: <Button className="secondary-button" type="button" disabledReason={busy ? t("Applying LAN settings…") : refreshing ? t("Refreshing device status…") : ""} onClick={() => { void refresh() }}>{t("Retry status")}</Button> }} /></div>}
      {!draft ? <p role="status">{statusError ? t("Load device status before editing LAN settings.") : t("Loading LAN settings…")}</p> : <>
        <label htmlFor={`${id}-type`}>{t("IPv4 configuration")}</label>
        <select id={`${id}-type`} name="lan_type" required disabled={busy} value={draft.type} onChange={(event) => change('type', event.target.value)}>
          <option value="" disabled>{t("Select a mode")}</option><option value="DHCP">{t("Automatic (DHCP)")}</option><option value="static">{t("Static IPv4")}</option>
        </select>
        {draft.type === 'static' ? <>
          <label htmlFor={`${id}-ip`}>{t("IPv4 address / CIDR prefix")}</label>
          <input id={`${id}-ip`} name="lan_ip" placeholder="192.168.1.20/24" autoComplete="off" spellCheck={false} maxLength={32}
            required disabled={busy} value={draft.ip} onChange={(event) => change('ip', event.target.value)} />
          <label htmlFor={`${id}-gateway`}>{t("IPv4 gateway (optional)")}</label>
          <input id={`${id}-gateway`} name="lan_gateway" placeholder="192.168.1.1" autoComplete="off" spellCheck={false} maxLength={32}
            disabled={busy} value={draft.gateway} onChange={(event) => change('gateway', event.target.value)} />
          <label htmlFor={`${id}-dns`}>{t("IPv4 DNS servers (optional)")}</label>
          <input id={`${id}-dns`} name="lan_dns" placeholder="192.168.1.1, 1.1.1.1" autoComplete="off" spellCheck={false} maxLength={256}
            disabled={busy} value={draft.dns} onChange={(event) => change('dns', event.target.value)} aria-describedby={`${id}-dns-help`} />
          <p className="password-help" id={`${id}-dns-help`}>{t("Separate addresses with commas or spaces. Empty fields clear the IPv4 gateway or DNS servers; IPv6 DNS is kept.")}</p>
        </> : draft.type === 'DHCP' && <p className="password-help">{t("DHCP obtains the IPv4 address, gateway, and DNS automatically, clearing saved manual IPv4 values.")}</p>}
        <div className="lan-warning" id={`${id}-warning`}>{t("Applying a different address may disconnect this browser and SSH. Use an unused address and keep local access available. Successful changes are not automatically reverted if you lose access.")}</div>
        <label className="lan-acknowledgement" htmlFor={`${id}-ack`}>
          <input id={`${id}-ack`} type="checkbox" name="lan_acknowledge" required disabled={busy} checked={acknowledged}
            onChange={(event) => setAcknowledged(event.target.checked)} aria-describedby={`${id}-warning`} />
          <span>{t("I understand that changing LAN settings may disconnect me.")}</span>
        </label>
        {error && <div className="form-error" role="alert">{t(error)}</div>}
        {success && <div className="form-success" role="status">{t(success)}</div>}
        {reconnect && <p className="lan-reconnect"><T message="{0}{1}Sign in again at the new address. HTTPS requires a certificate valid for that address." values={{ 0: <a href={reconnect} target="_blank" rel="noopener noreferrer">{t("Open portal at the new IPv4 address")}</a>, 1: <br /> }} /></p>}
        <Button className="login-submit" type="submit" disabledReason={busy ? t("Applying LAN settings…") : !draft.type ? t("Choose DHCP or static IPv4 first.") : !acknowledged ? t("Confirm the connection warning before applying.") : ""}>{busy ? t("Applying LAN settings…") : t("Save and apply LAN settings")}</Button>
        <Button className="text-action" type="button" disabledReason={busy ? t("Applying LAN settings…") : !status || statusError ? t("Load device status before discarding edits.") : ""} onClick={() => {
          if (status) { setDraft(initialValues(status)); setAcknowledged(false); setError(''); setSuccess(''); setReconnect(null) }
        }}>{t("Discard edits and use current values")}</Button>
      </>}
    </form>
  </section>
}
