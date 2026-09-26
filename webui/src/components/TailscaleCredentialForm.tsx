import { useId, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { BookOpen, ExternalLink, Eye, EyeOff, KeyRound, Trash2 } from 'lucide-react'
import { useTailscale } from '../tailscale'

export const TAILSCALE_CREDENTIALS_URL = 'https://console.tailscale.com/admin/settings/trust-credentials'

export default function TailscaleCredentialForm({ onSaved, onGuide, onBusy }: {
  onSaved?: () => void
  onGuide?: () => void
  onBusy?: (busy: boolean) => void
}) {
  const { client, save, clear } = useTailscale()
  const id = useId()
  const [clientId, setClientId] = useState(client?.clientId ?? '')
  const [secret, setSecret] = useState('')
  const [showSecret, setShowSecret] = useState(false)
  const [action, setAction] = useState<'save' | 'remove' | null>(null)
  const busy = action !== null
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const needsSecret = !client?.hasClientSecret || clientId.trim() !== client.clientId

  function setWorking(value: typeof action) { setAction(value); onBusy?.(value !== null) }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (busy || confirmRemove) return
    setError(''); setSuccess('')
    if (!clientId.trim() || (needsSecret && !secret.trim())) {
      setError('Enter both the client ID and client secret. A new client ID needs its matching secret.')
      return
    }
    setWorking('save')
    try {
      await save({ clientId, clientSecret: secret || undefined })
      setSecret(''); setShowSecret(false)
      setSuccess('Credentials saved. The portal will automatically approve this device’s advertised exit node and subnet routes. Check Tailnet approval for the result; saving does not sign in or switch tailnets.')
      onSaved?.()
    } catch (error) { setError(error instanceof Error ? error.message : 'Unable to save credentials.') }
    finally { setWorking(null) }
  }

  async function remove() {
    if (busy || !confirmRemove || !client) return
    setWorking('remove'); setError(''); setSuccess('')
    try {
      await clear()
      setClientId(''); setSecret(''); setShowSecret(false); setConfirmRemove(false)
      setSuccess('Saved credentials removed. Automatic OAuth approval has stopped. Existing route approvals remain; the OAuth client has not been revoked and the device was not disconnected.')
    } catch (error) { setError(error instanceof Error ? error.message : 'Unable to remove credentials.') }
    finally { setWorking(null) }
  }

  return <form className="login-form credential-form" onSubmit={submit} aria-busy={busy}>
    <p className="credential-intro">Use an OAuth client from this device’s tailnet with devices:routes write permission. Saving enables automatic approval of this device’s advertised exit node and subnet routes.</p>
    <div className="credential-links">
      <a href={TAILSCALE_CREDENTIALS_URL} target="_blank" rel="noopener noreferrer">Create OAuth credentials <ExternalLink size={16} /></a>
      <Link to="/tailscale-setup/oauth-credentials" onClick={onGuide}><BookOpen size={16} /> Step-by-step guide</Link>
    </div>
    <label htmlFor={`${id}-client`}>Client ID</label>
    <input id={`${id}-client`} name="client_id" autoComplete="off" autoCapitalize="none" spellCheck={false}
      maxLength={512} value={clientId} onChange={(event) => setClientId(event.target.value)} required disabled={busy} />
    <label className="password-label" htmlFor={`${id}-secret`}>{client?.hasClientSecret ? 'Replace client secret' : 'Client secret'}</label>
    <div className="password-field">
      <input id={`${id}-secret`} name="client_secret" type={showSecret ? 'text' : 'password'} autoComplete="new-password"
        autoCapitalize="none" spellCheck={false} maxLength={4096} required={needsSecret} disabled={busy}
        aria-describedby={`${id}-help`} placeholder={needsSecret ? 'Paste the client secret' : 'Leave blank to keep it'}
        value={secret} onChange={(event) => setSecret(event.target.value)} />
      <button className="password-field__toggle" type="button" disabled={busy} aria-label={showSecret ? 'Hide client secret' : 'Show client secret'}
        aria-pressed={showSecret} onClick={() => setShowSecret(!showSecret)}>{showSecret ? <EyeOff size={19} /> : <Eye size={19} />}</button>
    </div>
    <p className="password-help" id={`${id}-help`}>{client?.hasClientSecret ? 'Leave blank to keep the saved secret for this client ID. The saved secret is never sent back to this browser.' : 'The secret is shown only once by Tailscale. Copy it before closing that page.'}</p>
    {error && <div className="form-error" role="alert">{error}</div>}
    {success && <div className="form-success" role="status">{success}</div>}
    <div className="credential-actions">
      <button className="login-submit" type="submit" disabled={busy || confirmRemove}>{action === 'save' ? 'Saving changes…' : 'Save credentials'}<KeyRound size={17} /></button>
      <button className="text-action danger-action" type="button" disabled={busy || !client || confirmRemove}
        aria-expanded={confirmRemove} aria-controls={`${id}-remove`}
        onClick={() => { setError(''); setSuccess(''); setConfirmRemove(true) }}>
        Remove credentials <Trash2 size={17} />
      </button>
    </div>
    <p className="password-help">This does not join or switch tailnets. Use a trusted HTTPS connection when entering secrets. Saved secrets and API tokens are never sent back to the browser.</p>
    {confirmRemove && <div className="remove-confirmation" id={`${id}-remove`} role="group" aria-label="Confirm credential removal">
      <p>Remove the saved client ID and secret from this device? This does not disconnect Tailscale or revoke the OAuth client in Tailscale.</p>
      <div className="credential-links">
        <button className="secondary-button danger-action" type="button" disabled={busy || !client} onClick={() => { void remove() }}>{action === 'remove' ? 'Removing credentials…' : 'Confirm removal'}</button>
        <button className="secondary-button" type="button" disabled={busy} onClick={() => setConfirmRemove(false)}>Cancel</button>
      </div>
    </div>}
  </form>
}
