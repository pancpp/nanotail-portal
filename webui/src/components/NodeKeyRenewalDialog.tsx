import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ExternalLink, LoaderCircle, RefreshCw, X } from 'lucide-react'
import { useTailscale } from '../tailscale'
import { nodeKeyExpiryDisabled } from '../nodeKey'
import NodeKeyRenewalStatus from './NodeKeyRenewalStatus'

export default function NodeKeyRenewalDialog({ onClose, signIn = false }: { onClose: () => void, signIn?: boolean }) {
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
      <div><span className="panel__eyebrow">{signIn ? 'TAILSCALE SIGN-IN' : 'KEY RENEW'}</span><h2 id="renewal-title">{signIn ? 'Connect to your tailnet' : 'Renew node key'}</h2></div>
      <button type="button" className="icon-button" aria-label={signIn ? 'Close sign-in dialog' : 'Close renewal dialog'} disabled={busy} onClick={() => { void requestClose() }}><X size={22} /></button>
    </div>
    <p className="setup-dialog__description" id="renewal-description">{signedIn ? 'Your device is signed in. You can close this panel to view your tailnet status.' : signIn ? 'Sign this device in to Tailscale using your browser. Your portal account is separate from your Tailscale account. A client ID and client secret are not required for browser sign-in.' : 'Force a new Tailscale sign-in for this device. Existing routing preferences are preserved; saved OAuth credentials are not used.'}</p>
    <div className="credential-form lan-form">
      {renewalDisabled && <p role="status">Node-key expiry is disabled for this device, so renewal is disabled.</p>}
      {signIn && !pending && !signedIn && !error && <ol className="signin-steps">
        <li>Confirm below and choose <strong>Prepare sign-in</strong>.</li>
        <li>Choose <strong>Sign in to Tailscale</strong>, then sign in and authorize this device in the new tab. Select the tailnet you want it to join.</li>
        <li>If your tailnet requires device approval, ask an administrator to approve this device after sign-in. <Link to="/tailscale-setup/device-approval" target="_blank" rel="noopener noreferrer">Device approval guide (new tab)</Link>.</li>
        <li>Return here; progress is checked automatically. Device approval is separate from exit-node and subnet-route approval.</li>
      </ol>}
      {!signedIn && <div className="lan-warning">{signIn ? 'Keep the portal open using the device’s LAN address. Signing in turns the tailnet connection on and preserves existing routing preferences. If this device was already enrolled, use the same account and tailnet.' : 'Renewal can disconnect Tailscale, including this browser session. Open the portal using the device’s LAN address first. Signing in also turns the tailnet connection on. Use the same Tailscale account and tailnet to keep this device in its current network.'}</div>}
      <div className="renewal-status">
        <NodeKeyRenewalStatus snapshot={keyRenewal} signIn={signIn} />
        {preparing && <p role="status">Preparing the request. Tailscale has not been changed.</p>}
        {value?.state === 'READY' && <>
          <p>The request is ready. Tailscale authentication starts only when you choose Sign in. Closing this panel cancels this request without changing the device.</p>
          <button type="button" className="secondary-button renewal-signin" disabled={busy || renewalDisabled} onClick={startSignIn}>Sign in to Tailscale <ExternalLink size={16} /></button>
        </>}
        {value?.state === 'STARTING' && value.canRenew && <button type="button" className="secondary-button renewal-signin" disabled={busy || renewalDisabled} onClick={startSignIn}>Retry sign-in <ExternalLink size={16} /></button>}
        {busy && !value && !pending && <p className="renewal-checking" role="status"><LoaderCircle size={20} className="spin" aria-hidden="true" /> Checking {signIn ? 'sign-in' : 'renewal'} status…</p>}
        {value?.state === 'AWAITING_LOGIN' && <>
          <p>Complete sign-in in a new tab, then return here. Keep this link private.</p>
          <a className="secondary-button" href={value.authURL} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">
            Open sign-in page <ExternalLink size={16} />
          </a>
        </>}
        {value?.state === 'AWAITING_APPROVAL' && <p>Sign-in is waiting for device approval. Ask your tailnet administrator to approve this device in the <a href="https://console.tailscale.com/admin/machines" target="_blank" rel="noopener noreferrer">Tailscale admin console</a>. <Link to="/tailscale-setup/device-approval" target="_blank" rel="noopener noreferrer">Device approval guide (new tab)</Link>.</p>}
        {popupBlocked && pending && <p>Your browser blocked the sign-in tab. Use Open sign-in page when the link is ready.</p>}
        {!renewalDisabled && value?.state === 'IDLE' && <p>{value.canRenew ? 'You can prepare a new sign-in request below. Opening this panel does not change the device.' : 'Tailscale is not ready for sign-in. Check the device status and try again.'}</p>}
      </div>
      {error && <div className="form-error" role="alert"><p>{error}</p><p>If access was interrupted, reconnect using the device’s LAN address. Use the top-right X to close this panel, then {signIn ? 'reopen sign-in from Overview' : 'reopen Renew'} to check status before making another request. Only portal administrators can sign this device in.</p></div>}
      {!renewalDisabled && value?.canRenew && value.state !== 'STARTING' && !signedIn && <>
        <label className="lan-acknowledgement"><input type="checkbox" checked={acknowledged} disabled={busy} onChange={event => setAcknowledged(event.target.checked)} />
          <span>{signIn ? 'I want to connect this device to my tailnet and can access the portal over the LAN.' : 'I can reconnect over the LAN and understand that I must sign in to Tailscale again.'}</span>
        </label>
        <button type="button" className="secondary-button renewal-submit" disabled={busy || !acknowledged} onClick={confirmRenewal}>
          <RefreshCw size={16} /> {signIn ? 'Prepare sign-in' : ['SIGNED_IN', 'COMPLETE'].includes(value.state) ? 'Renew again' : 'Renew node key'}
        </button>
      </>}
    </div>
    {pending && <div className="setup-dialog__footer credential-links">
      <p className="renewal-help">{value?.state === 'READY' || preparing ? 'Closing this panel cancels the prepared request. The device stays unchanged.' : 'After Sign in has started, closing this dialog keeps the completion checks running.'}</p>
    </div>}
  </dialog>
}
