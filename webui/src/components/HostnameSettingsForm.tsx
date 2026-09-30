import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { Monitor } from 'lucide-react'
import { ApiError, isSessionError, setDeviceHostnameRequest, validateDeviceHostname } from '../api'
import { useAuth } from '../auth'
import { useDevice } from '../device'
import { useI18n } from '../i18n'
import Button from './Button'

export default function HostnameSettingsForm() {
  const { t } = useI18n()
  const { accessToken, logout } = useAuth()
  const { status, error: statusError, refreshing, refresh } = useDevice()
  const [draft, setDraft] = useState<string | null>(null)
  const [saved, setSaved] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const submitting = useRef(false)
  const id = useId()

  // Background status refreshes must not overwrite a hostname being edited.
  useEffect(() => {
    if (status && draft === null) { setDraft(status.hostname); setSaved(status.hostname) }
  }, [status, draft])

  let hostname = '', validationError = ''
  if (draft !== null) {
    try { hostname = validateDeviceHostname(draft) }
    catch (error) { validationError = error instanceof Error ? error.message : '' }
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (submitting.current || draft === null || validationError || hostname === saved) return
    if (!accessToken) { logout(); return }
    submitting.current = true; setBusy(true); setError(''); setSuccess('')
    try {
      await setDeviceHostnameRequest(accessToken, hostname)
      setDraft(hostname); setSaved(hostname); setSuccess('Hostname saved.')
      await refresh()
    } catch (error) {
      if (isSessionError(error)) { logout(); return }
      setError(error instanceof ApiError && error.isGraphQLError && error.status < 500 && error.message !== 'Internal Server Error' ? error.message :
        'The server did not confirm the hostname change. Check device status before retrying.')
    } finally { submitting.current = false; setBusy(false) }
  }

  return <section className="panel hostname-settings" aria-labelledby={`${id}-heading`}>
    <div className="panel__header"><div><span className="panel__eyebrow">{t('DEVICE')}</span><h2 id={`${id}-heading`}>{t('Device hostname')}</h2></div><Monitor size={20} /></div>
    <form className="login-form credential-form lan-form" onSubmit={submit} aria-busy={busy}>
      <p className="credential-intro">{t('Change the device’s system hostname. Only portal administrators can apply changes.')}</p>
      {statusError && <div className="form-error" role="status">{t(statusError)}</div>}
      {draft === null ? <p role="status">{statusError ? t('Load device status before editing the hostname.') : t('Loading hostname…')}</p> : <>
        <label htmlFor={`${id}-hostname`}>{t('Hostname')}</label>
        <input id={`${id}-hostname`} name="device_hostname" autoComplete="off" autoCapitalize="none" spellCheck={false} maxLength={63}
          required disabled={busy} value={draft} aria-describedby={`${id}-help`} aria-invalid={Boolean(validationError)}
          onChange={event => { setDraft(event.target.value); setError(''); setSuccess('') }} />
        <p className="password-help" id={`${id}-help`}>{t('Use 1–63 letters, digits, or hyphens. Start and end with a letter or digit.')}</p>
        <p className="password-help">{t('If you access the portal by hostname, use the new name or the device’s IP address after saving.')}</p>
        {validationError && <div className="form-error" role="alert">{t(validationError)}</div>}
        {error && <div className="form-error" role="alert">{t(error)}</div>}
        {success && <div className="form-success" role="status">{t(success)}</div>}
        <Button className="login-submit" type="submit" disabledReason={busy ? t('Saving hostname…') : validationError ? t(validationError) : hostname === saved ? t('No changes to save.') : ''}>
          {busy ? t('Saving hostname…') : t('Save hostname')}
        </Button>
      </>}
      <Button className="text-action" type="button" disabledReason={busy ? t('Saving hostname…') : refreshing ? t('Refreshing device status…') : ''}
        onClick={() => { setError(''); setSuccess(''); void refresh() }}>{t('Refresh device status')}</Button>
      {draft !== null && <Button className="text-action" type="button" disabledReason={busy ? t('Saving hostname…') : !status || statusError ? t('Load device status before discarding edits.') : ''}
        onClick={() => { if (status) { setDraft(status.hostname); setSaved(status.hostname); setError(''); setSuccess('') } }}>{t('Discard edits and use current values')}</Button>}
    </form>
  </section>
}
