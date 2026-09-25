import { Link } from 'react-router-dom'
import { ChevronRight, Network, Route } from 'lucide-react'
import { useTailscale } from '../tailscale'
import { routingSummary } from '../routing'

export default function RoutingCard({ kind = 'exit' }: { kind?: 'exit' | 'subnet' }) {
  const { routing, routingError } = useTailscale()
  const summary = routingSummary(routing, routingError, kind)
  return <article className="status-card routing-card">
    <div className="status-card__topline">
      <span className="status-icon status-icon--violet">{kind === 'exit' ? <Route size={20} /> : <Network size={20} />}</span>
      <span className="quiet-label">{kind === 'exit' ? 'EXIT NODE' : 'SUBNET ROUTES'}</span>
    </div>
    <div className="status-card__body">
      <span>{kind === 'exit' ? 'Internet gateway' : 'LAN gateway'}</span>
      <strong>{summary.title}</strong><p>{summary.detail}</p>
      {kind === 'subnet' && !routingError && !!routing?.subnetRoutes.length &&
        <ul className="route-list">{routing.subnetRoutes.map(route => <li key={route}><code>{route}</code></li>)}</ul>}
    </div>
    <Link className="status-card__configure" to="/access-control">
      Configure <ChevronRight size={15} />
    </Link>
  </article>
}
