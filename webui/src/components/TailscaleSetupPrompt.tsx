import { useEffect, useRef, useState } from 'react'
import { X } from 'lucide-react'
import TailscaleCredentialForm from './TailscaleCredentialForm'

export default function TailscaleSetupPrompt({ onClose }: { onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [busy, setBusy] = useState(false)
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
  return <dialog ref={dialog} className="setup-dialog" aria-labelledby="setup-dialog-title" aria-describedby="setup-dialog-description"
    onCancel={(event) => { event.preventDefault(); if (!busy) onClose() }}>
    <div className="panel__header">
      <div><span className="panel__eyebrow">TAILSCALE SETUP</span><h2 id="setup-dialog-title">Add your tailnet credentials</h2></div>
      <button className="icon-button" type="button" aria-label="Close setup dialog" disabled={busy} onClick={onClose}><X size={22} /></button>
    </div>
    <p className="setup-dialog__description" id="setup-dialog-description">This device needs to sign in to a tailnet. Save your OAuth client ID and secret to prepare its setup. You can also do this later in Network.</p>
    <TailscaleCredentialForm onSaved={onClose} onGuide={onClose} onBusy={setBusy} />
    <div className="setup-dialog__footer"><button className="secondary-button" type="button" disabled={busy} onClick={onClose}>Set up later</button></div>
  </dialog>
}
