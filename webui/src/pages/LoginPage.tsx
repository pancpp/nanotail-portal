import Button from '../components/Button'
import { useI18n, T } from '../i18n'
import { useState, type FormEvent } from 'react'
import {
  ArrowRight,
  Check,
  Eye,
  EyeOff,
  LockKeyhole,
  Network,
  ShieldCheck,
  Wifi,
} from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import Brand from '../components/Brand'
import LanguageSelector from '../components/LanguageSelector'

export default function LoginPage() {
  const { t } = useI18n()
  const { login, factoryResetResult: resetResult } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [error, setError] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)

  const previousPath = (
    location.state as { from?: { pathname?: string } } | null
  )?.from?.pathname

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    setIsSubmitting(true)

    try {
      await login({ username, password })
      navigate(previousPath || '/', { replace: true })
    } catch (caughtError) {
      setError(
        caughtError instanceof Error
          ? caughtError.message
          : 'Unable to sign in. Please try again.',
      )
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <main className="login-layout">
      <section className="login-story" aria-label={t("Product introduction")}>
        <div className="login-story__glow login-story__glow--one" />
        <div className="login-story__glow login-story__glow--two" />
        <div className="login-story__grid" />

        <Brand />

        <div className="login-story__content">
          <div className="eyebrow"><T message="{0}Built for nanotail" values={{ 0: <span className="eyebrow__dot" /> }} /></div>
          <h1>{t("Your private network, under your control.")}</h1>
          <p>{t("Configure Tailscale, inspect connectivity, and manage your edge device from one focused workspace.")}</p>

          <ul className="feature-list">
            <li><T message="{0}Private, device-local administration" values={{ 0: <span><Check size={15} strokeWidth={3} /></span> }} /></li>
            <li><T message="{0}Clear network health at a glance" values={{ 0: <span><Check size={15} strokeWidth={3} /></span> }} /></li>
            <li><T message="{0}Simple Tailscale configuration" values={{ 0: <span><Check size={15} strokeWidth={3} /></span> }} /></li>
          </ul>
        </div>

        <div className="network-visual" aria-hidden="true">
          <div className="network-visual__line network-visual__line--one" />
          <div className="network-visual__line network-visual__line--two" />
          <span className="network-node network-node--main"><Network /></span>
          <span className="network-node network-node--top"><ShieldCheck /></span>
          <span className="network-node network-node--right"><Wifi /></span>
          <span className="network-node network-node--bottom"><LockKeyhole /></span>
        </div>

        <p className="login-story__footer">{t("Secure access for your edge network")}</p>
      </section>

      <section className="login-panel">
        <LanguageSelector />
        <div className="login-panel__mobile-brand">
          <Brand compact />
        </div>

        <div className="login-card">
          <div className="login-card__heading">
            <span className="login-card__icon"><LockKeyhole size={22} /></span>
            <h2>{t("Welcome back")}</h2>
            <p>{t("Sign in to manage your nanotail gateway.")}</p>
          </div>

          {resetResult && <div className="form-error reset-result" role="status">
            <p>{resetResult === 'accepted' ? t("Factory reset was accepted. You have been signed out while the device attempts to reset and restart.") : t("The reset outcome is unknown because the connection ended. You have been signed out; the reset may still be running.")}</p>
            <p>{t("When the portal returns, reopen your usual portal address over a connection that does not depend on Tailscale and verify the result before trying again. After a successful reset, use admin / admin and change the password.")}</p>
            <p>{t("If Tailscale logout failed, no files were cleared and your existing password still applies. Check the device’s local service logs if it does not return.")}</p>
          </div>}
          <form onSubmit={handleSubmit} className="login-form">
            <label htmlFor="username">{t("Username")}</label>
            <input
              id="username"
              name="username"
              type="text"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              placeholder={t("Enter your username")}
              autoComplete="username"
              autoFocus
              required
            />

            <div className="password-label">
              <label htmlFor="password">{t("Password")}</label>
            </div>
            <div className="password-field">
              <input
                id="password"
                name="password"
                type={showPassword ? 'text' : 'password'}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder={t("Enter your password")}
                autoComplete="current-password"
                required
              />
              <button
                type="button"
                className="password-field__toggle"
                onClick={() => setShowPassword((visible) => !visible)}
                aria-label={showPassword ? t("Hide password") : t("Show password")}
              >
                {showPassword ? <EyeOff size={18} /> : <Eye size={18} />}
              </button>
            </div>

            {error && (
              <div className="form-error" role="alert" aria-live="polite">
                {t(error)}
              </div>
            )}

            <Button
              type="submit"
              className="login-submit"
              disabledReason={isSubmitting ? t("Signing in…") : ""}
            >
              <span>{isSubmitting ? t("Signing in…") : t("Sign in")}</span>
              <ArrowRight size={18} />
            </Button>
          </form>

          <div className="login-card__security"><T message="{0}Credentials are sent only to this device." values={{ 0: <ShieldCheck size={16} /> }} /></div>
        </div>

        <p className="login-panel__version">Nanotail Portal · nanotail</p>
      </section>
    </main>
  )
}
