import { useI18n, T } from '../i18n'
import { useCallback, useEffect, useState } from 'react'
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
import LanguageSelector from '../components/LanguageSelector'
import PortalVersion from '../components/PortalVersion'
import { isTailscaleConnected, shouldPromptForTailscale, tailscaleStatusLabel } from '../api'
import { useTailscale } from '../tailscale'
import { useDevice } from '../device'
import { useAutoRefresh } from '../useAutoRefresh'

const navItems = [
  { label: 'Overview', icon: CircleGauge, path: '/' },
  { label: 'Network', icon: Network, path: '/network' },
  { label: 'Access control', icon: ShieldCheck, path: '/access-control' },
  { label: 'Settings', icon: Settings, path: '/settings' },
  { label: 'Setup guides', icon: BookOpen, path: '/tailscale-setup' },
]

export default function DashboardPage() {
  const { t } = useI18n()
  const { logout } = useAuth()
  const { status, statusError, refreshing: tailscaleRefreshing, refresh, keyRenewalActive } = useTailscale()
  const { status: deviceStatus, refreshing: deviceRefreshing, refresh: refreshDevice } = useDevice()
  const refreshAll = useCallback(async () => {
    await Promise.all([refresh(), refreshDevice()])
  }, [refresh, refreshDevice])
  const { secondsRemaining, refreshNow, autoRefreshing } = useAutoRefresh(refreshAll)
  const refreshing = tailscaleRefreshing || deviceRefreshing || autoRefreshing
  const { pathname } = useLocation()
  const pageTitle = navItems.find((item) => item.path === pathname || (item.path !== '/' && pathname.startsWith(item.path + '/')))?.label ?? 'Overview'
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
          aria-label={t("Close navigation")}
          onClick={() => setMenuOpen(false)}
        />
      )}

      <aside className={`sidebar${menuOpen ? ' sidebar--open' : ''}`}>
        <div className="sidebar__header">
          <Brand compact />
          <button
            className="icon-button sidebar__close"
            onClick={() => setMenuOpen(false)}
            aria-label={t("Close navigation")}
          >
            <X size={19} />
          </button>
        </div>

        <div className="device-chip">
          <span className="device-chip__icon"><Cpu size={18} /></span>
          <span>
            <strong title={deviceStatus?.hostname}>{deviceStatus?.hostname || 'nanotail'}</strong>
            <small className={connected ? '' : 'connection-muted'}><i /> {t(connectionLabel)}</small>
          </span>
        </div>

        <nav className="sidebar__nav" aria-label={t("Main navigation")}>
          <span className="sidebar__label">{t("Workspace")}</span>
          {navItems.map(({ label, icon: Icon, path }) => (
            <NavLink
              key={label}
              to={path}
              end={path !== '/tailscale-setup'}
              className={({ isActive }) => `nav-item${isActive ? ' nav-item--active' : ''}`}
              onClick={() => setMenuOpen(false)}
            >
              <Icon size={19} />
              <span>{t(label)}</span>
            </NavLink>
          ))}
        </nav>

        <div className="sidebar__footer">
          <PortalVersion />
          <button type="button" className="logout-button" onClick={logout}><T message="{0}Sign out" values={{ 0: <LogOut size={18} /> }} /></button>
        </div>
      </aside>

      <div className="dashboard-main">
        <header className="topbar">
          <button
            className="icon-button topbar__menu"
            onClick={() => setMenuOpen(true)}
            aria-label={t("Open navigation")}
          >
            <Menu size={21} />
          </button>
          <div className="topbar__title">
            <span>{t("Workspace")}</span>
            <strong>{t(pageTitle)}</strong>
          </div>
          {['/', '/network', '/access-control'].includes(pathname) && (
            <div className="topbar__actions">
              <span className="updated-at" role="timer" aria-live="off">
                {refreshing ? t("Updating…") : t("Auto-refresh in {seconds}s", { seconds: secondsRemaining })}
              </span>
              <button
                className="secondary-button"
                type="button"
                aria-label={t("Refresh Tailscale and device status")}
                onClick={() => { void refreshNow() }}
                disabled={refreshing}
              >
                <RefreshCw size={16} className={refreshing ? 'spin' : ''} />
                <span>{t("Refresh status")}</span>
              </button>
            </div>
          )}
          <LanguageSelector />
        </header>

        <main className="dashboard-content">
          {statusError && <div className="connection-notice connection-notice--error" role="status"><strong>{t("Tailscale status unavailable")}</strong>
            <p>{t(statusError)}</p><p>{t("This does not mean your credentials are missing.")}</p>
            <button className="secondary-button" disabled={refreshing} onClick={() => { void refresh() }}>{t("Retry status")}</button></div>}
          {needsSetup && <div className="connection-notice" role="status"><strong>{t("This device is not signed in to a tailnet.")}</strong>
            <p>{t("Use Sign in to Tailscale on the Overview to connect this device. Your portal login is separate from your Tailscale account.")}</p>
            {pathname !== '/' && <Link to="/">{t("Open the sign-in guide")}</Link>}</div>}
          <Outlet />
        </main>
      </div>
    </div>
  )
}
