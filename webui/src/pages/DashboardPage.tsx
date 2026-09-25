import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  CircleGauge,
  BookOpen,
  Cpu,
  LogOut,
  Menu,
  Network,
  RefreshCw,
  Settings,
  ShieldCheck,
  X,
} from 'lucide-react'
import { useAuth } from '../auth'
import Brand from '../components/Brand'
import { isTailscaleConnected, shouldPromptForTailscale, tailscaleStatusLabel } from '../api'
import { useTailscale } from '../tailscale'
import TailscaleSetupPrompt from '../components/TailscaleSetupPrompt'

const navItems = [
  { label: 'Overview', icon: CircleGauge, path: '/' },
  { label: 'Network', icon: Network },
  { label: 'Access control', icon: ShieldCheck },
  { label: 'Settings', icon: Settings, path: '/settings' },
  { label: 'Setup guide', icon: BookOpen, path: '/tailscale-setup' },
]

export default function DashboardPage() {
  const { logout } = useAuth()
  const { status, client, statusError, clientError, refreshing, refresh } = useTailscale()
  const { pathname } = useLocation()
  const pageTitle = pathname === '/settings' ? 'Settings' : pathname === '/tailscale-setup' ? 'Setup guide' : 'Overview'
  const [menuOpen, setMenuOpen] = useState(false)
  const [promptOpen, setPromptOpen] = useState(false)
  const [promptDismissed, setPromptDismissed] = useState(false)
  const needsSetup = shouldPromptForTailscale(status, statusError)
  const connected = isTailscaleConnected(status, statusError)
  const connectionLabel = tailscaleStatusLabel(status, statusError)

  useEffect(() => {
    if (needsSetup && client !== undefined && !clientError && !promptDismissed && pathname === '/') setPromptOpen(true)
  }, [needsSetup, client, clientError, promptDismissed, pathname])

  function closePrompt() { setPromptOpen(false); setPromptDismissed(true) }

  useEffect(() => {
    if (!menuOpen) return

    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setMenuOpen(false)
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [menuOpen])

  return (
    <div className="dashboard-shell">
      {menuOpen && (
        <button
          className="sidebar-backdrop"
          aria-label="Close navigation"
          onClick={() => setMenuOpen(false)}
        />
      )}

      <aside className={`sidebar${menuOpen ? ' sidebar--open' : ''}`}>
        <div className="sidebar__header">
          <Brand compact />
          <button
            className="icon-button sidebar__close"
            onClick={() => setMenuOpen(false)}
            aria-label="Close navigation"
          >
            <X size={19} />
          </button>
        </div>

        <div className="device-chip">
          <span className="device-chip__icon"><Cpu size={18} /></span>
          <span>
            <strong>nanotail</strong>
            <small className={connected ? '' : 'connection-muted'}><i /> {connectionLabel}</small>
          </span>
        </div>

        <nav className="sidebar__nav" aria-label="Main navigation">
          <span className="sidebar__label">Workspace</span>
          {navItems.map(({ label, icon: Icon, path }) => path ? (
            <NavLink
              key={label}
              to={path}
              end
              className={({ isActive }) => `nav-item${isActive ? ' nav-item--active' : ''}`}
              onClick={() => setMenuOpen(false)}
            >
              <Icon size={19} />
              <span>{label}</span>
            </NavLink>
          ) : (
            <button
              type="button"
              key={label}
              className="nav-item"
              title={`${label} is coming soon`}
              disabled
            >
              <Icon size={19} />
              <span>{label}</span>
              <small>Soon</small>
            </button>
          ))}
        </nav>

        <div className="sidebar__footer">
          <div className="sidebar__version">
            <span>Portal version</span>
            <strong>0.1.0</strong>
          </div>
          <button type="button" className="logout-button" onClick={logout}>
            <LogOut size={18} />
            Sign out
          </button>
        </div>
      </aside>

      <div className="dashboard-main">
        <header className="topbar">
          <button
            className="icon-button topbar__menu"
            onClick={() => setMenuOpen(true)}
            aria-label="Open navigation"
          >
            <Menu size={21} />
          </button>
          <div className="topbar__title">
            <span>Workspace</span>
            <strong>{pageTitle}</strong>
          </div>
          {pathname === '/' && (
            <div className="topbar__actions">
              <span className="updated-at">Auto-refresh every 30s</span>
              <button
                className="secondary-button"
                type="button"
                aria-label="Refresh Tailscale status"
                onClick={() => { void refresh() }}
                disabled={refreshing}
              >
                <RefreshCw size={16} className={refreshing ? 'spin' : ''} />
                <span>Refresh status</span>
              </button>
            </div>
          )}
        </header>

        <main className="dashboard-content">
          {statusError && <div className="connection-notice connection-notice--error" role="status"><strong>Tailscale status unavailable</strong>
            <p>{statusError}</p><p>This does not mean your credentials are missing.</p>
            <button className="secondary-button" disabled={refreshing} onClick={() => { void refresh() }}>Retry status</button></div>}
          {needsSetup && <div className="connection-notice" role="status"><strong>This device is not signed in to a tailnet.</strong>
            <p>{client?.hasClientSecret ? 'Credentials are saved. Saving alone does not connect the device.' : 'Add your OAuth client ID and secret to prepare the device for setup.'}</p>
            <div className="credential-links"><Link to="/settings">Manage credentials</Link><Link to="/tailscale-setup">Read the setup guide</Link></div></div>}
          <Outlet />
        </main>
      </div>
      {promptOpen && needsSetup && pathname === '/' && <TailscaleSetupPrompt onClose={closePrompt} />}
    </div>
  )
}
