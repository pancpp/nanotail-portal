import Button from './Button'
import { useI18n, T } from '../i18n'
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ExternalLink, LoaderCircle, RefreshCw, X } from 'lucide-react'
import { useTailscale } from '../tailscale'
import { nodeKeyExpiryDisabled } from '../nodeKey'
import NodeKeyRenewalStatus from './NodeKeyRenewalStatus'

export default function NodeKeyRenewalDialog({ onClose, signIn = false }: { onClose: () => void, signIn?: boolean }) {
  const { t } = useI18n()
  const { status, keyRenewal, setKeyRenewalDialogOpen } = useTailscale()
  const { value, busy, preparing, pending, error, popupBlocked, checkStatus, renew, startSignIn, close } = keyRenewal
  const dialog = useRef<HTMLDialogElement>(null)
  const [acknowledged, setAcknowledged] = useState(false)
  const signedIn = signIn && ['SIGNED_IN', 'COMPLETE'].includes(value?.state ?? '')
  const renewalDisabled = nodeKeyExpiryDisabled(status)

  useEffect(() => {
    setKeyRenewalDialogOpen(true)
    const element = dialog.current!
    const previousFocus = document.activeElement as HTMLElement | null
    const previousOverflow = document.body.style.overflow
    element.showModal()
    document.body.style.overflow = 'hidden'
    void checkStatus()
    return () => {
      setKeyRenewalDialogOpen(false)
      element.close()
      document.body.style.overflow = previousOverflow
      previousFocus?.focus()
    }
  }, [checkStatus, setKeyRenewalDialogOpen])

  useEffect(() => {
    if (error) setAcknowledged(false)
  }, [error])

  function confirmRenewal() {
    if (renewalDisabled || !value?.canRenew || !acknowledged || busy) return
    setAcknowledged(false)
    void renew()
  }

  async function requestClose() {
    if (await close()) onClose()
  }

  return <dialog ref={dialog} className="setup-dialog renewal-dialog" aria-labelledby="renewal-title" aria-describedby="renewal-description"
    onCancel={(event) => { event.preventDefault(); if (!busy) void requestClose() }}>
    <div className="panel__header">
      <div><span className="panel__eyebrow">{signIn ? t("TAILSCALE SIGN-IN") : t("KEY RENEW")}</span><h2 id="renewal-title">{signIn ? t("Connect to your tailnet") : t("Renew node key")}</h2></div>
      <Button type="button" className="icon-button" aria-label={signIn ? t("Close sign-in dialog") : t("Close renewal dialog")} disabledReason={busy ? t("Wait for the Tailscale sign-in request to finish.") : ""} onClick={() => { void requestClose() }}><X size={22} /></Button>
    </div>
    <p className="setup-dialog__description" id="renewal-description">{signedIn ? t("Your device is signed in. You can close this panel to view your tailnet status.") : signIn ? t("Sign this device in to Tailscale using your browser. Your portal account is separate from your Tailscale account. A client ID and client secret are not required for browser sign-in.") : t("Force a new Tailscale sign-in for this device. Existing routing preferences are preserved; saved OAuth credentials are not used.")}</p>
    <div className="credential-form lan-form">
      {renewalDisabled && <p role="status">{t("Node-key expiry is disabled for this device, so renewal is disabled.")}</p>}
      {signIn && !pending && !signedIn && !error && <ol className="signin-steps">
        <li><T message="Confirm below and choose {0}." values={{ 0: <strong>{t("Prepare sign-in")}</strong> }} /></li>
        <li><T message="Choose {0}, then sign in and authorize this device in the new tab. Select the tailnet you want it to join." values={{ 0: <strong>{t("Sign in to Tailscale")}</strong> }} /></li>
        <li><T message="If your tailnet requires device approval, ask an administrator to approve this device after sign-in. {0}." values={{ 0: <Link to="/tailscale-setup/device-approval" target="_blank" rel="noopener noreferrer">{t("Device approval guide (new tab)")}</Link> }} /></li>
        <li>{t("Return here; progress is checked automatically. Device approval is separate from exit-node and subnet-route approval.")}</li>
      </ol>}
      {!signedIn && <div className="lan-warning">{signIn ? t("Keep the portal open using the device’s LAN address. Signing in turns the tailnet connection on and preserves existing routing preferences. If this device was already enrolled, use the same account and tailnet.") : t("Renewal can disconnect Tailscale, including this browser session. Open the portal using the device’s LAN address first. Signing in also turns the tailnet connection on. Use the same Tailscale account and tailnet to keep this device in its current network.")}</div>}
      <div className="renewal-status">
        <NodeKeyRenewalStatus snapshot={keyRenewal} signIn={signIn} />
        {preparing && <p role="status">{t("Preparing the request. Tailscale has not been changed.")}</p>}
        {value?.state === 'READY' && <>
          <p>{t("The request is ready. Tailscale authentication starts only when you choose Sign in. Closing this panel cancels this request without changing the device.")}</p>
          <Button type="button" className="secondary-button renewal-signin" disabledReason={busy ? t("Wait for the Tailscale sign-in request to finish.") : renewalDisabled ? t("Renewal is disabled because node-key expiry is disabled.") : ""} onClick={startSignIn}><T message="Sign in to Tailscale {0}" values={{ 0: <ExternalLink size={16} /> }} /></Button>
        </>}
        {value?.state === 'STARTING' && value.canRenew && <Button type="button" className="secondary-button renewal-signin" disabledReason={busy ? t("Wait for the Tailscale sign-in request to finish.") : renewalDisabled ? t("Renewal is disabled because node-key expiry is disabled.") : ""} onClick={startSignIn}><T message="Retry sign-in {0}" values={{ 0: <ExternalLink size={16} /> }} /></Button>}
        {busy && !value && !pending && <p className="renewal-checking" role="status"><T message="{0} Checking {1} status…" values={{ 0: <LoaderCircle size={20} className="spin" aria-hidden="true" />, 1: signIn ? t("sign-in") : t("renewal") }} /></p>}
        {value?.state === 'AWAITING_LOGIN' && <>
          <p>{t("Complete sign-in in a new tab, then return here. Keep this link private.")}</p>
          <a className="secondary-button" href={value.authURL} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer"><T message="Open sign-in page {0}" values={{ 0: <ExternalLink size={16} /> }} /></a>
        </>}
        {value?.state === 'AWAITING_APPROVAL' && <p><T message="Sign-in is waiting for device approval. Ask your tailnet administrator to approve this device in the {0}. {1}." values={{ 0: <a href="https://console.tailscale.com/admin/machines" target="_blank" rel="noopener noreferrer">{t("Tailscale admin console")}</a>, 1: <Link to="/tailscale-setup/device-approval" target="_blank" rel="noopener noreferrer">{t("Device approval guide (new tab)")}</Link> }} /></p>}
        {popupBlocked && pending && <p>{t("Your browser blocked the sign-in tab. Use Open sign-in page when the link is ready.")}</p>}
        {!renewalDisabled && value?.state === 'IDLE' && <p>{value.canRenew ? t("You can prepare a new sign-in request below. Opening this panel does not change the device.") : t("Tailscale is not ready for sign-in. Check the device status and try again.")}</p>}
      </div>
      {error && <div className="form-error" role="alert"><p>{t(error)}</p><p><T message="If access was interrupted, reconnect using the device’s LAN address. Use the top-right X to close this panel, then {0} to check status before making another request. Only portal administrators can sign this device in." values={{ 0: signIn ? t("reopen sign-in from Overview") : t("reopen Renew") }} /></p></div>}
      {!renewalDisabled && value?.canRenew && value.state !== 'STARTING' && !signedIn && <>
        <label className="lan-acknowledgement"><input type="checkbox" checked={acknowledged} disabled={busy} onChange={event => setAcknowledged(event.target.checked)} />
          <span>{signIn ? t("I want to connect this device to my tailnet and can access the portal over the LAN.") : t("I can reconnect over the LAN and understand that I must sign in to Tailscale again.")}</span>
        </label>
        <Button type="button" className="secondary-button renewal-submit" disabledReason={busy ? t("Wait for the Tailscale sign-in request to finish.") : !acknowledged ? t("Confirm that you can reconnect over the LAN before continuing.") : ""} onClick={confirmRenewal}>
          <RefreshCw size={16} /> {signIn ? t("Prepare sign-in") : ['SIGNED_IN', 'COMPLETE'].includes(value.state) ? t("Renew again") : t("Renew node key")}
        </Button>
      </>}
    </div>
    {pending && <div className="setup-dialog__footer credential-links">
      <p className="renewal-help">{value?.state === 'READY' || preparing ? t("Closing this panel cancels the prepared request. The device stays unchanged.") : t("After Sign in has started, closing this dialog keeps the completion checks running.")}</p>
    </div>}
  </dialog>
}
