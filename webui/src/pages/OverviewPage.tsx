import {
  ArrowDownToLine,
  ArrowUpFromLine,
  ChevronRight,
  Cpu,
  KeyRound,
  Route,
  SlidersHorizontal,
  Users,
  Wifi,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import { isTailscaleConnected, tailscaleStatusLabel } from '../api'
import { useTailscale } from '../tailscale'
import DeviceStatusPanel from '../components/DeviceStatusPanel'

export default function OverviewPage() {
  const { status, statusError } = useTailscale()
  const connected = isTailscaleConnected(status, statusError)
  const label = tailscaleStatusLabel(status, statusError)
  const peers = statusError ? [] : status?.peers ?? []
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
          Connection, peers, and device status are live · other panels are previews
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
            <p>{!statusError && status?.currentTailnet?.name ? status.currentTailnet.name : 'No active tailnet connection'}</p>
          </div>
          <div className="status-card__footer">
            <span>{!statusError && status?.tailscaleIPs.length ? status.tailscaleIPs.join(', ') : 'No Tailscale address'}</span>
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
              <span className="panel__eyebrow">TAILNET</span>
              <h2>Tailnet peers</h2>
            </div>
            <span className="peer-count"><Users size={15} /> {statusError ? 'Unavailable' : !status ? 'Checking…' : `${peers.length} ${peers.length === 1 ? 'device' : 'devices'}`}</span>
          </div>

          <div className="peer-table">
            <div className="peer-table__head">
              <span>Device</span>
              <span>Address</span>
              <span>Status</span>
            </div>
            {peers.map((peer) => (
              <div className="peer-row" key={peer.id}>
                <div className="peer-device">
                  <span className="peer-device__icon"><Cpu size={17} /></span>
                  <span><strong title={peer.hostName || peer.dnsName || peer.id}>{peer.hostName || peer.dnsName || peer.id}</strong><small>{peer.os || 'Unknown OS'}</small></span>
                </div>
                <code>{peer.tailscaleIPs.join(', ') || 'No address'}</code>
                <span className={`peer-status${peer.online ? '' : ' peer-status--offline'}`}>
                  <i /> {peer.online ? 'Online' : 'Offline'}
                </span>
              </div>
            ))}
            {peers.length === 0 && <p className="peer-table__empty" role="status">
              {statusError ? 'Unable to load peers. Retry the status request.' : !status ? 'Loading peers…' : 'No peers are visible to this device.'}
            </p>}
          </div>
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

      <DeviceStatusPanel />
    </>
  )
}
