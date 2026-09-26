import { useI18n, T } from '../i18n'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { RotateCcw, X } from 'lucide-react'
import { useAuth } from '../auth'
import { isSessionError } from '../api'
import { canConfirmReset, factoryResetRequest, ResetOutcomeUnknown } from '../factoryReset'

export default function FactoryResetPanel() {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  return <section className="panel factory-reset-settings" aria-labelledby="factory-reset-heading">
    <div className="panel__header">
      <div><span className="panel__eyebrow">{t("FACTORY RESET")}</span><h2 id="factory-reset-heading">{t("Reset this portal")}</h2></div>
      <RotateCcw size={22} aria-hidden="true" />
    </div>
    <div className="credential-form">
      <p className="credential-intro">{t("Erase portal settings, accounts, saved OAuth credentials, traffic history, and logs. Log out of Tailscale and restart the portal. Only portal administrators can reset this device.")}</p>
      <p className="password-help">{t("This cannot be undone. Ensure you can access the portal without Tailscale before continuing.")}</p>
      <button type="button" className="secondary-button danger-button" onClick={() => setOpen(true)}>{t("Factory reset")}</button>
    </div>
    {open && <FactoryResetDialog onClose={() => setOpen(false)} />}
  </section>
}

function FactoryResetDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n()
  const { accessToken, logout, logoutAfterFactoryReset } = useAuth()
  const dialog = useRef<HTMLDialogElement>(null)
  const confirmationInput = useRef<HTMLInputElement>(null)
  const submitting = useRef(false)
  const [step, setStep] = useState<1 | 2>(1)
  const [acknowledged, setAcknowledged] = useState(false)
  const [confirmation, setConfirmation] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const element = dialog.current!
    const previousFocus = document.activeElement as HTMLElement | null
    const previousOverflow = document.body.style.overflow
    element.showModal()
    document.body.style.overflow = 'hidden'
    return () => {
      element.close()
      document.body.style.overflow = previousOverflow
      previousFocus?.focus()
    }
  }, [])

  useEffect(() => {
    if (step === 2) confirmationInput.current?.focus()
  }, [step])

  function leave(result: 'accepted' | 'unknown') {
    // Clear the persisted JWT, including other tabs via the storage event.
    logoutAfterFactoryReset(result)
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (step !== 2 || submitting.current || !accessToken || !canConfirmReset(acknowledged, confirmation, password, busy)) return
    submitting.current = true
    setBusy(true)
    setError('')
    try {
      await factoryResetRequest(accessToken, confirmation, password)
      leave('accepted')
    } catch (error) {
      if (error instanceof ResetOutcomeUnknown) {
        leave('unknown')
        return
      }
      if (isSessionError(error)) {
        logout()
        return
      }
      setError(error instanceof Error ? error.message : 'Unable to request factory reset.')
      setPassword('')
      setConfirmation('')
      submitting.current = false
      setBusy(false)
    }
  }

  return <dialog ref={dialog} className="setup-dialog factory-reset-dialog" aria-labelledby="reset-title" aria-describedby="reset-description"
    onCancel={event => { event.preventDefault(); if (!busy) onClose() }}>
    <div className="panel__header">
      <div><span className="panel__eyebrow"><T message="CONFIRMATION {0} OF 2" values={{ 0: step }} /></span><h2 id="reset-title">{step === 1 ? t("Erase portal data?") : t("Confirm factory reset")}</h2></div>
      <button type="button" className="icon-button" aria-label={t("Close factory reset dialog")} disabled={busy} onClick={onClose}><X size={22} /></button>
    </div>
    <p className="setup-dialog__description" id="reset-description"><T message="This permanently clears {0}, {1}, and the {2} folder, deletes {3}, logs out of Tailscale, signs this browser out, and restarts the portal. The new signing key invalidates all existing portal sessions." values={{ 0: <code>nanotail.yml</code>, 1: <code>nanotail.sqlite3</code>, 2: <code>logs</code>, 3: <code>nanotail.key</code> }} /></p>
    <form className="credential-form reset-form" onSubmit={submit}>
      <div className="lan-warning">{t("The portal will be temporarily unavailable while it restarts. Tailscale will be logged out, so use your usual portal address over a connection that does not depend on Tailscale.")}</div>
      <p className="password-help"><T message="After reset, sign in with {0} and change the password. Device LAN settings and remote Tailscale OAuth clients are not reset or revoked." values={{ 0: <strong>admin / admin</strong> }} /></p>
      {step === 1 ? <>
        <label className="reset-acknowledgement"><input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />
          <span>{t("I understand that the data cannot be recovered and I can access the portal without Tailscale.")}</span>
        </label>
        <button type="button" className="secondary-button danger-button" disabled={!acknowledged} onClick={() => setStep(2)}>{t("Continue to final confirmation")}</button>
      </> : <>
        <label htmlFor="reset-confirmation">{t("Type RESET to confirm")}</label>
        <input ref={confirmationInput} id="reset-confirmation" value={confirmation} onChange={event => setConfirmation(event.target.value)}
          autoComplete="off" spellCheck={false} disabled={busy} required />
        <label htmlFor="reset-password">{t("Current portal password")}</label>
        <input id="reset-password" type="password" value={password} onChange={event => setPassword(event.target.value)}
          autoComplete="current-password" disabled={busy} required />
        {error && <div className="form-error" role="alert">{t(error)}</div>}
        <button type="submit" className="secondary-button danger-button" disabled={!canConfirmReset(acknowledged, confirmation, password, busy)}>
          {busy ? t("Requesting factory reset…") : t("Erase data and restart")}
        </button>
        {busy && <p role="status">{t("Waiting for acceptance. Do not close this page or submit another reset.")}</p>}
      </>}
    </form>
  </dialog>
}
