import { useI18n, T } from '../i18n'
import { Link } from 'react-router-dom'
import { ChevronRight, Network, Route } from 'lucide-react'
import { useTailscale } from '../tailscale'
import { routingSummary } from '../routing'

export default function RoutingCard({ kind = 'exit' }: { kind?: 'exit' | 'subnet' }) {
  const { t, language } = useI18n()
  const { routing, routingError } = useTailscale()
  const summary = routingSummary(routing, routingError, kind, language)
  return <article className="status-card routing-card">
    <div className="status-card__topline">
      <span className="status-icon status-icon--violet">{kind === 'exit' ? <Route size={20} /> : <Network size={20} />}</span>
      <span className="quiet-label">{kind === 'exit' ? t("EXIT NODE") : t("SUBNET ROUTES")}</span>
    </div>
    <div className="status-card__body">
      <span>{kind === 'exit' ? t("Internet gateway") : t("LAN gateway")}</span>
      <strong>{t(summary.title)}</strong><p>{t(summary.detail)}</p>
      {kind === 'subnet' && !routingError && !!routing?.subnetRoutes.length &&
        <ul className="route-list">{routing.subnetRoutes.map(route => <li key={route}><code>{route}</code></li>)}</ul>}
    </div>
    <Link className="status-card__configure" to="/access-control"><T message="Configure {0}" values={{ 0: <ChevronRight size={15} /> }} /></Link>
  </article>
}
