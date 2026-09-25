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
import { useDevice } from '../device'

const navItems = [
  { label: 'Overview', icon: CircleGauge, path: '/' },
  { label: 'Network', icon: Network, path: '/network' },
  { label: 'Access control', icon: ShieldCheck, path: '/access-control' },
  { label: 'Settings', icon: Settings, path: '/settings' },
  { label: 'Setup guide', icon: BookOpen, path: '/tailscale-setup' },
]

export default function DashboardPage() {
  const { logout } = useAuth()
  const { status, statusError, refreshing: tailscaleRefreshing, refresh, keyRenewalActive } = useTailscale()
  const { status: deviceStatus, refreshing: deviceRefreshing, refresh: refreshDevice } = useDevice()
  const refreshing = tailscaleRefreshing || deviceRefreshing
  const { pathname } = useLocation()
  const pageTitle = navItems.find((item) => item.path === pathname)?.label ?? 'Overview'
  const [menuOpen, setMenuOpen] = useState(false)
  const needsSetup = shouldPromptForTailscale(status, statusError) && !keyRenewalActive
  const connected = isTailscaleConnected(status, statusError)
  const connectionLabel = tailscaleStatusLabel(status, statusError)

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
            <strong title={deviceStatus?.hostname}>{deviceStatus?.hostname || 'nanotail'}</strong>
            <small className={connected ? '' : 'connection-muted'}><i /> {connectionLabel}</small>
          </span>
        </div>

        <nav className="sidebar__nav" aria-label="Main navigation">
          <span className="sidebar__label">Workspace</span>
          {navItems.map(({ label, icon: Icon, path }) => (
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
          {['/', '/network', '/access-control'].includes(pathname) && (
            <div className="topbar__actions">
              <span className="updated-at">Auto-refresh every 30s</span>
              <button
                className="secondary-button"
                type="button"
                aria-label="Refresh Tailscale and device status"
                onClick={() => { void refresh(); void refreshDevice() }}
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
            <p>Use Sign in to Tailscale on the Overview to connect this device. Your portal login is separate from your Tailscale account.</p>
            {pathname !== '/' && <Link to="/">Open the sign-in guide</Link>}</div>}
          <Outlet />
        </main>
      </div>
    </div>
  )
}
