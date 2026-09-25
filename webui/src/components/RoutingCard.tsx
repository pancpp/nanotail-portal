import { Link } from 'react-router-dom'
import { ChevronRight, Route } from 'lucide-react'
import { useTailscale } from '../tailscale'
import { routingSummary } from '../routing'

export default function RoutingCard() {
  const { routing, routingError } = useTailscale()
  const summary = routingSummary(routing, routingError)
  return <article className="status-card routing-card">
    <div className="status-card__topline">
      <span className="status-icon status-icon--violet"><Route size={20} /></span>
      <span className="quiet-label">ROUTING</span>
    </div>
    <div className="status-card__body">
      <span>Exit node</span><strong>{summary.title}</strong><p>{summary.detail}</p>
    </div>
    <Link className="status-card__configure" to="/access-control">
      Configure <ChevronRight size={15} />
    </Link>
  </article>
}
