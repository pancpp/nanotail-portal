import { useEffect, useState } from 'react'
import {
  Activity,
  ArrowDownToLine,
  ArrowUpFromLine,
  Check,
  ChevronRight,
  CircleGauge,
  Clock3,
  Cpu,
  KeyRound,
  LogOut,
  Menu,
  Network,
  RefreshCw,
  Route,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Users,
  Wifi,
  X,
  Zap,
} from 'lucide-react'
import { useAuth } from '../auth'
import Brand from '../components/Brand'
import ChangePasswordForm from '../components/ChangePasswordForm'

const navItems = [
  { label: 'Overview', icon: CircleGauge, active: true },
  { label: 'Network', icon: Network },
  { label: 'Access control', icon: ShieldCheck },
  { label: 'Settings', icon: Settings },
]

const peers = [
  { name: 'workstation', address: '100.82.14.7', os: 'Linux', online: true },
  { name: 'phone', address: '100.71.22.19', os: 'Android', online: true },
  { name: 'home-server', address: '100.69.40.2', os: 'Linux', online: false },
]

export default function DashboardPage() {
  const { logout } = useAuth()
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
          {navItems.map(({ label, icon: Icon, active }) => (
            <button
              type="button"
              key={label}
              className={`nav-item${active ? ' nav-item--active' : ''}`}
              aria-current={active ? 'page' : undefined}
              title={active ? undefined : `${label} is coming soon`}
            >
              <Icon size={19} />
              <span>{label}</span>
              {!active && <small>Soon</small>}
            </button>
          ))}
        </nav>

        <div className="sidebar__footer">
          <div className="sidebar__version">
            <span>Portal version</span>
            <strong>0.1.0</strong>
          </div>
          <button
            type="button"
            className="logout-button"
            onClick={() => {
              setMenuOpen(false)
              document.getElementById('account-settings')?.scrollIntoView({ behavior: 'smooth' })
            }}
          >
            <KeyRound size={18} />
            Change password
          </button>
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
            <strong>Overview</strong>
          </div>
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
        </header>

        <main className="dashboard-content">
          <section className="page-heading">
            <div>
              <div className="eyebrow eyebrow--light">
                <span className="eyebrow__dot" />
                Device online
              </div>
              <h1>Good to see you.</h1>
              <p>Here’s what’s happening on your private network.</p>
            </div>
            <div className="integration-note">
              <SlidersHorizontal size={16} />
              Live status integration is next
            </div>
          </section>

          <section className="status-grid" aria-label="Network status">
            <article className="status-card status-card--primary">
              <div className="status-card__topline">
                <span className="status-icon"><Wifi size={20} /></span>
                <span className="status-pill"><i /> Connected</span>
              </div>
              <div className="status-card__body">
                <span>Tailscale status</span>
                <strong>Network is healthy</strong>
                <p>nanotail.tailnet</p>
              </div>
              <div className="status-card__footer">
                <span>100.84.17.23</span>
                <button type="button">View details <ChevronRight size={15} /></button>
              </div>
            </article>

            <article className="status-card">
              <div className="status-card__topline">
                <span className="status-icon status-icon--violet"><Route size={20} /></span>
                <span className="quiet-label">ROUTING</span>
              </div>
              <div className="status-card__body">
                <span>Exit node</span>
                <strong>Not configured</strong>
                <p>Traffic uses the local gateway</p>
              </div>
              <button className="text-action" type="button">
                Configure <ChevronRight size={15} />
              </button>
            </article>

            <article className="status-card">
              <div className="status-card__topline">
                <span className="status-icon status-icon--amber"><KeyRound size={20} /></span>
                <span className="quiet-label">SECURITY</span>
              </div>
              <div className="status-card__body">
                <span>Node key</span>
                <strong>178 days remaining</strong>
                <p>Expires March 19, 2027</p>
              </div>
              <div className="key-progress"><span /></div>
            </article>
          </section>

          <section className="dashboard-columns">
            <article className="panel peers-panel">
              <div className="panel__header">
                <div>
                  <span className="panel__eyebrow">TAILNET</span>
                  <h2>Recent peers</h2>
                </div>
                <span className="peer-count"><Users size={15} /> {peers.length} devices</span>
              </div>

              <div className="peer-table">
                <div className="peer-table__head">
                  <span>Device</span>
                  <span>Address</span>
                  <span>Status</span>
                </div>
                {peers.map((peer) => (
                  <div className="peer-row" key={peer.name}>
                    <div className="peer-device">
                      <span className="peer-device__icon"><Cpu size={17} /></span>
                      <span><strong>{peer.name}</strong><small>{peer.os}</small></span>
                    </div>
                    <code>{peer.address}</code>
                    <span className={`peer-status${peer.online ? '' : ' peer-status--offline'}`}>
                      <i /> {peer.online ? 'Online' : 'Offline'}
                    </span>
                  </div>
                ))}
              </div>

              <button type="button" className="panel__footer-action">
                View all devices <ChevronRight size={16} />
              </button>
            </article>

            <article className="panel activity-panel">
              <div className="panel__header">
                <div>
                  <span className="panel__eyebrow">LAST 24 HOURS</span>
                  <h2>Network activity</h2>
                </div>
                <span className="activity-live"><i /> Live</span>
              </div>

              <div className="traffic-total">
                <span>Total traffic</span>
                <strong>1.84 <small>GB</small></strong>
              </div>

              <div className="activity-chart" aria-label="Illustrative traffic chart">
                {[30, 44, 28, 52, 48, 72, 62, 88, 66, 78, 56, 70].map((height, index) => (
                  <span key={index} style={{ height: `${height}%` }} />
                ))}
              </div>

              <div className="traffic-breakdown">
                <span><ArrowDownToLine size={16} /> Download <strong>1.42 GB</strong></span>
                <span><ArrowUpFromLine size={16} /> Upload <strong>420 MB</strong></span>
              </div>
            </article>
          </section>

          <section className="device-panel" id="device">
            <div className="device-panel__intro">
              <span className="device-panel__icon"><Zap size={22} /></span>
              <div>
                <span className="panel__eyebrow">THIS DEVICE</span>
                <h2>nanotail</h2>
                <p>Edge gateway · nanotail portal</p>
              </div>
            </div>
            <div className="device-metrics">
              <span><Activity size={17} /><small>Uptime</small><strong>6d 14h</strong></span>
              <span><Cpu size={17} /><small>CPU load</small><strong>18%</strong></span>
              <span><Clock3 size={17} /><small>Last restart</small><strong>Sep 15</strong></span>
              <span><Check size={17} /><small>Service</small><strong>Healthy</strong></span>
            </div>
          </section>
          <ChangePasswordForm />
        </main>
      </div>
    </div>
  )
}
