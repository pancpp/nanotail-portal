import {
  ChevronRight,
  Cpu,
  SlidersHorizontal,
  Users,
  Wifi,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import { isTailscaleConnected, tailscaleStatusLabel } from '../api'
import { useTailscale } from '../tailscale'
import DeviceStatusPanel from '../components/DeviceStatusPanel'
import NetworkActivityPanel from '../components/NetworkActivityPanel'
import NodeKeyCard from '../components/NodeKeyCard'
import RoutingCard from '../components/RoutingCard'

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
          Live device and Tailscale status
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
            <Link to="/network">Configure <ChevronRight size={15} /></Link>
            <span>{!statusError && status?.tailscaleIPs.length ? status.tailscaleIPs.join(', ') : 'No Tailscale address'}</span>
          </div>
        </article>

        <RoutingCard />

        <NodeKeyCard />
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

        <NetworkActivityPanel />
      </section>

      <DeviceStatusPanel />
    </>
  )
}
