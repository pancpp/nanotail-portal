import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  CircleGauge,
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

const navItems = [
  { label: 'Overview', icon: CircleGauge, path: '/' },
  { label: 'Network', icon: Network },
  { label: 'Access control', icon: ShieldCheck },
  { label: 'Settings', icon: Settings, path: '/settings' },
]

export default function DashboardPage() {
  const { logout } = useAuth()
  const { pathname } = useLocation()
  const pageTitle = pathname === '/settings' ? 'Settings' : 'Overview'
  const [menuOpen, setMenuOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [lastUpdated, setLastUpdated] = useState('Just now')

  useEffect(() => {
    if (!menuOpen) return

    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setMenuOpen(false)
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [menuOpen])

  function refreshStatus() {
    setRefreshing(true)
    window.setTimeout(() => {
      setRefreshing(false)
      setLastUpdated('Just now')
    }, 650)
  }

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
            <small><i /> Online</small>
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
              <span className="updated-at">Updated {lastUpdated}</span>
              <button
                className="secondary-button"
                type="button"
                onClick={refreshStatus}
                disabled={refreshing}
              >
                <RefreshCw size={16} className={refreshing ? 'spin' : ''} />
                <span>Refresh status</span>
              </button>
            </div>
          )}
        </header>

        <main className="dashboard-content">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
