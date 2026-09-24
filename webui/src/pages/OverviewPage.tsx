import {
  Activity,
  ArrowDownToLine,
  ArrowUpFromLine,
  Check,
  ChevronRight,
  Clock3,
  Cpu,
  KeyRound,
  Route,
  SlidersHorizontal,
  Users,
  Wifi,
  Zap,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import { useTailscale } from '../tailscale'

const peers = [
  { name: 'workstation', address: '100.82.14.7', os: 'Linux', online: true },
  { name: 'phone', address: '100.71.22.19', os: 'Android', online: true },
  { name: 'home-server', address: '100.69.40.2', os: 'Linux', online: false },
]

export default function OverviewPage() {
  const { status, statusError } = useTailscale()
  const connected = Boolean(status?.connected && !statusError)
  const label = statusError ? 'Unavailable' : !status ? 'Checking…' : connected ? 'Connected' :
    status.needsLogin ? 'Needs setup' : status.backendState === 'Stopped' ? 'Stopped' : 'Not connected'
  return (
    <>
      <section className="page-heading">
        <div>
          <div className="eyebrow eyebrow--light">
            <span className="eyebrow__dot" />
            nanotail portal
          </div>
          <h1>Good to see you.</h1>
          <p>Here’s what’s happening on your private network.</p>
        </div>
        <div className="integration-note">
          <SlidersHorizontal size={16} />
          Connection is live · other panels are previews
        </div>
      </section>

      <section className="status-grid" aria-label="Network status">
        <article className="status-card status-card--primary">
          <div className="status-card__topline">
            <span className="status-icon"><Wifi size={20} /></span>
            <span className={`status-pill${connected ? '' : ' connection-muted'}`}><i /> {label}</span>
          </div>
          <div className="status-card__body">
            <span>Tailscale status</span>
            <strong>{connected ? 'Connected to your tailnet' : label}</strong>
            <p>{!statusError && status?.tailnet ? status.tailnet : 'No active tailnet connection'}</p>
          </div>
          <div className="status-card__footer">
            <span>{!statusError && status?.ips.length ? status.ips.join(', ') : 'No Tailscale address'}</span>
            <Link to="/settings">Settings <ChevronRight size={15} /></Link>
          </div>
        </article>

        <article className="status-card">
          <div className="status-card__topline">
            <span className="status-icon status-icon--violet"><Route size={20} /></span>
            <span className="quiet-label">ROUTING PREVIEW</span>
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
            <span className="quiet-label">SECURITY PREVIEW</span>
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
              <span className="panel__eyebrow">SAMPLE TAILNET</span>
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
            <span className="activity-live">Preview</span>
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
    </>
  )
}
