import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { X } from 'lucide-react'
import { T, useI18n } from '../i18n'
import { useTailscale } from '../tailscale'
import Button from './Button'
import TailscaleCredentialForm from './TailscaleCredentialForm'

export default function TailscaleCredentialSetupPrompt() {
  const { t } = useI18n()
  const { client, clientError, refresh, refreshing, credentialSetup } = useTailscale()
  const dialog = useRef<HTMLDialogElement>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  const [manual, setManual] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const element = dialog.current!
    let previousFocus: HTMLElement | null = null
    let previousOverflow: string | undefined
    const open = () => {
      // Wait for any existing modal to close instead of stacking dialogs.
      if (element.open || document.querySelector('dialog[open]')) return
      previousFocus = document.activeElement as HTMLElement | null
      previousOverflow = document.body.style.overflow
      element.showModal()
      document.body.style.overflow = 'hidden'
      heading.current?.focus()
    }
    const observer = new MutationObserver(open)
    observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['open'] })
    open()
    return () => {
      observer.disconnect()
      element.close()
      if (previousOverflow !== undefined) document.body.style.overflow = previousOverflow
      if (previousFocus?.isConnected) previousFocus.focus()
    }
  }, [])

  useEffect(() => {
    if (dialog.current?.open) {
      dialog.current.scrollTop = 0
      heading.current?.focus()
    }
  }, [manual])

  function skip() {
    if (busy) return
    if (manual) credentialSetup.dismiss()
    else setManual(true)
  }

  return <dialog ref={dialog} className="setup-dialog credential-setup-dialog" aria-labelledby="credential-setup-title" aria-describedby="credential-setup-description"
    onCancel={event => { event.preventDefault(); skip() }}>
    <div className="panel__header">
      <div><span className="panel__eyebrow">TAILSCALE</span><h2 ref={heading} tabIndex={-1} id="credential-setup-title">{manual ? t("Continue with manual setup") : t("Add your client credentials")}</h2></div>
      <Button type="button" className="icon-button" aria-label={manual ? t("Close manual setup reminder") : t("Skip client credentials")} disabledReason={busy ? t("Wait for the credential update to finish.") : ""} onClick={skip}><X size={22} /></Button>
    </div>
    <p className="setup-dialog__description" id="credential-setup-description">{manual
      ? t("Without client credentials, complete these steps yourself or ask your tailnet administrator:")
      : t("Your device is now bound to a Tailscale account. Generate an OAuth client in this tailnet, then paste its client ID and secret below.")}</p>
    {manual ? <>
      <ol className="credential-setup-checklist">
        <li><p>{t("Manually approve this device in the Tailscale admin console if your tailnet requires device approval.")}</p><a href="https://console.tailscale.com/admin/machines" target="_blank" rel="noopener noreferrer">{t("Tailscale admin console")}</a></li>
        <li><p>{t("Manually approve this device as an exit node unless your tailnet policy has already approved it.")}</p><Link to="/tailscale-setup/exit-node" target="_blank" rel="noopener noreferrer">{t("Exit-node approval guide (new tab)")}</Link></li>
        <li><p>{t("Manually approve the advertised subnet routes to use this device as a subnet router, unless your tailnet policy has already approved them.")}</p><Link to="/tailscale-setup/subnet-routes" target="_blank" rel="noopener noreferrer">{t("Subnet route approval guide (new tab)")}</Link></li>
        <li><p>{t("Manually set up this device as a peer relay, including its relay port, network access, and tailnet policy grant.")}</p><a href="https://tailscale.com/docs/features/peer-relay" target="_blank" rel="noopener noreferrer">{t("Peer relay setup guide ↗")}</a></li>
        <li><p><T message="You can add the client credentials later in {0}." values={{ 0: <Link to="/settings" onClick={credentialSetup.dismiss}>{t("Settings")}</Link> }} /></p></li>
      </ol>
      <div className="setup-dialog__footer credential-links">
        <button type="button" className="secondary-button" onClick={() => setManual(false)}>{t("Add credentials now")}</button>
        <button type="button" className="secondary-button" onClick={credentialSetup.dismiss}>{t("Continue without credentials")}</button>
      </div>
    </> : <>
      {clientError ? <div className="credential-form"><p className="form-error" role="alert">{t(clientError)}</p>
        <Button type="button" className="secondary-button" disabledReason={refreshing ? t("Refreshing device and Tailscale status…") : ""} onClick={() => { void refresh() }}>{t("Retry loading")}</Button></div>
        : client === undefined ? <p className="credential-form" role="status">{t("Loading credential settings…")}</p>
          : <TailscaleCredentialForm onSaved={credentialSetup.dismiss} onBusy={setBusy} guideInNewTab />}
      <div className="setup-dialog__footer">
        <p className="password-help">{t("Device approval, when required by your tailnet, is still manual. Saving credentials does not enable peer relay; configure it in Access control.")}</p>
        <Button type="button" className="secondary-button" disabledReason={busy ? t("Wait for the credential update to finish.") : ""} onClick={skip}>{t("Skip for now")}</Button>
      </div>
    </>}
  </dialog>
}
