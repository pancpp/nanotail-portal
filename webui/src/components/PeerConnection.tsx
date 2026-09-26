import type { TailscalePeer } from '../api'
import { useI18n } from '../i18n'
import { peerConnection } from '../peerConnection'

export default function PeerConnection({ peer, connected }: { peer: TailscalePeer; connected: boolean }) {
  const { t } = useI18n()
  const { kind, label, detail } = peerConnection(peer, connected)
  return <div className={`peer-connection peer-connection--${kind}`} role="group" aria-label={t("Connection")}>
    <span className="peer-connection__type">{t(label)}</span>
    {detail && <small className="peer-connection__detail">{detail}</small>}
  </div>
}
