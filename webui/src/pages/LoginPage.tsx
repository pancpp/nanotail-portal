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

export default function LoginPage() {
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
      <section className="login-story" aria-label="Product introduction">
        <div className="login-story__glow login-story__glow--one" />
        <div className="login-story__glow login-story__glow--two" />
        <div className="login-story__grid" />

        <Brand />

        <div className="login-story__content">
          <div className="eyebrow">
            <span className="eyebrow__dot" />
            Built for nanotail
          </div>
          <h1>Your private network, under your control.</h1>
          <p>
            Configure Tailscale, inspect connectivity, and manage your edge
            device from one focused workspace.
          </p>

          <ul className="feature-list">
            <li>
              <span><Check size={15} strokeWidth={3} /></span>
              Private, device-local administration
            </li>
            <li>
              <span><Check size={15} strokeWidth={3} /></span>
              Clear network health at a glance
            </li>
            <li>
              <span><Check size={15} strokeWidth={3} /></span>
              Simple Tailscale configuration
            </li>
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

        <p className="login-story__footer">Secure access for your edge network</p>
      </section>

      <section className="login-panel">
        <div className="login-panel__mobile-brand">
          <Brand compact />
        </div>

        <div className="login-card">
          <div className="login-card__heading">
            <span className="login-card__icon"><LockKeyhole size={22} /></span>
            <h2>Welcome back</h2>
            <p>Sign in to manage your nanotail gateway.</p>
          </div>

          {resetResult && <div className="form-error reset-result" role="status">
            <p>{resetResult === 'accepted' ? 'Factory reset was accepted. You have been signed out while the device attempts to reset and restart.' : 'The reset outcome is unknown because the connection ended. You have been signed out; the reset may still be running.'}</p>
            <p>When the portal returns, reopen your usual portal address over a connection that does not depend on Tailscale and verify the result before trying again. After a successful reset, use admin / admin and change the password.</p>
            <p>If Tailscale logout failed, no files were cleared and your existing password still applies. Check the device’s local service logs if it does not return.</p>
          </div>}
          <form onSubmit={handleSubmit} className="login-form">
            <label htmlFor="username">Username</label>
            <input
              id="username"
              name="username"
              type="text"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              placeholder="Enter your username"
              autoComplete="username"
              autoFocus
              required
            />

            <div className="password-label">
              <label htmlFor="password">Password</label>
            </div>
            <div className="password-field">
              <input
                id="password"
                name="password"
                type={showPassword ? 'text' : 'password'}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder="Enter your password"
                autoComplete="current-password"
                required
              />
              <button
                type="button"
                className="password-field__toggle"
                onClick={() => setShowPassword((visible) => !visible)}
                aria-label={showPassword ? 'Hide password' : 'Show password'}
              >
                {showPassword ? <EyeOff size={18} /> : <Eye size={18} />}
              </button>
            </div>

            {error && (
              <div className="form-error" role="alert" aria-live="polite">
                {error}
              </div>
            )}

            <button
              type="submit"
              className="login-submit"
              disabled={isSubmitting}
            >
              <span>{isSubmitting ? 'Signing in…' : 'Sign in'}</span>
              <ArrowRight size={18} />
            </button>
          </form>

          <div className="login-card__security">
            <ShieldCheck size={16} />
            Credentials are sent only to this device.
          </div>
        </div>

        <p className="login-panel__version">Nanotail Portal · nanotail</p>
      </section>
    </main>
  )
}
