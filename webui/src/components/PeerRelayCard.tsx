import { ChevronRight, Network } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useI18n, T } from '../i18n'
import { useTailscale } from '../tailscale'

export default function PeerRelayCard() {
  const { t } = useI18n()
  const { routing, routingError } = useTailscale()
  const title = routingError ? t('Unavailable') : !routing ? t('Checking…') : !routing.peerRelayEnabled ? t('Disabled') :
    routing.backendState !== 'Running' ? t('Paused') : t('Enabled')
  const detail = routingError ? t('Unable to read peer relay settings.') : !routing ? t('Loading peer relay settings…') :
    !routing.peerRelayEnabled ? t('Help tailnet devices connect through this device') :
      routing.peerRelayPort === 0 ? t('Automatic UDP port') : t('UDP port {port}', { port: routing.peerRelayPort! })
  return <article className="status-card peer-relay-card">
    <div className="status-card__topline"><span className="status-icon status-icon--violet"><Network size={20} /></span><span className="quiet-label">{t('PEER RELAY')}</span></div>
    <div className="status-card__body"><span>{t('Tailnet relay')}</span><strong>{title}</strong><p>{detail}</p>
      {!routingError && routing?.peerRelayEnabled && <p>{t('Saved setting · policy and UDP access still apply')}</p>}
    </div>
    <Link className="status-card__configure" to="/access-control#peer-relay"><T message="Configure {0}" values={{ 0: <ChevronRight size={15} /> }} /></Link>
  </article>
}
