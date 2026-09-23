import { useState, type FormEvent } from 'react'
import { KeyRound } from 'lucide-react'
import { useAuth } from '../auth'

export default function ChangePasswordForm() {
  const { changePassword } = useAuth()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (isSubmitting) return
    setError('')
    setSuccess('')
    if (newPassword !== confirmation) {
      setError('New passwords do not match.')
      return
    }

    setIsSubmitting(true)
    try {
      await changePassword({ current_password: currentPassword, new_password: newPassword })
      setCurrentPassword('')
      setNewPassword('')
      setConfirmation('')
      setSuccess('Password updated. Use your new password the next time you sign in.')
    } catch (caughtError) {
      setError(caughtError instanceof Error ? caughtError.message : 'Unable to change your password.')
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <section className="panel password-settings" id="account-settings" aria-labelledby="password-heading">
      <div className="panel__header">
        <div>
          <span className="panel__eyebrow">YOUR ACCOUNT</span>
          <h2 id="password-heading">Change password</h2>
        </div>
        <KeyRound size={20} aria-hidden="true" />
      </div>
      <form className="login-form password-change-form" onSubmit={handleSubmit} aria-busy={isSubmitting}>
        <label htmlFor="current-password">Current password</label>
        <input
          id="current-password" name="current_password" type="password"
          autoComplete="current-password" required disabled={isSubmitting}
          value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)}
        />
        <label className="password-label" htmlFor="new-password">New password</label>
        <input
          id="new-password" name="new_password" type="password"
          autoComplete="new-password" required disabled={isSubmitting}
          aria-describedby="password-help"
          value={newPassword} onChange={(event) => setNewPassword(event.target.value)}
        />
        <p className="password-help" id="password-help">
          Use 8–72 bytes. Non-ASCII characters may count as more than one byte.
        </p>
        <label className="password-label" htmlFor="confirm-password">Confirm new password</label>
        <input
          id="confirm-password" name="confirm_password" type="password"
          autoComplete="new-password" required disabled={isSubmitting}
          value={confirmation} onChange={(event) => setConfirmation(event.target.value)}
        />
        {error && <div className="form-error" role="alert">{error}</div>}
        {success && <div className="form-success" role="status">{success}</div>}
        <button className="login-submit" type="submit" disabled={isSubmitting}>
          {isSubmitting ? 'Updating password…' : 'Update password'}
          <KeyRound size={17} aria-hidden="true" />
        </button>
        <p className="password-help">Changing your password does not sign out existing sessions.</p>
      </form>
    </section>
  )
}
