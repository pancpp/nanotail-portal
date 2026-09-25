import { useEffect, useState } from 'react'
import { KeyRound, RefreshCw } from 'lucide-react'
import { nodeKeyStatus } from '../nodeKey'
import { useTailscale } from '../tailscale'
import NodeKeyRenewalStatus from './NodeKeyRenewalStatus'

export default function NodeKeyCard({ onRenew }: { onRenew: () => void }) {
  const { status, statusError, keyRenewalActive, keyRenewal } = useTailscale()
  const [now, setNow] = useState(Date.now)

  useEffect(() => {
    const update = () => setNow(Date.now())
    const timer = window.setInterval(update, 1000)
    window.addEventListener('focus', update)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', update) }
  }, [])

  const key = nodeKeyStatus(status, statusError, now)
  return <article className={`status-card node-key-card node-key-card--${key.state}`} aria-labelledby="node-key-heading">
    <div className="status-card__topline">
      <span className="status-icon status-icon--amber"><KeyRound size={20} /></span>
      <span className="quiet-label">KEY EXPIRY</span>
    </div>
    <div className="status-card__body">
      <span id="node-key-heading">Node key</span>
      <strong>{key.label}</strong>
      <p>{key.description}{key.expiresAt && <> <time dateTime={key.expiresAt}>
        {new Date(key.expiresAt).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}
      </time> (local time)</>}</p>
    </div>
    {key.state === 'expired' && <p className="node-key-warning">Reauthenticate this device to restore access.</p>}
    <NodeKeyRenewalStatus snapshot={keyRenewal} compact signIn={status?.backendState === 'NeedsLogin' && !status.haveNodeKey} />
    <button type="button" className="status-card__configure node-key-renew" onClick={onRenew}
      disabled={!keyRenewalActive && (statusError !== '' || !status ||
        (status.backendState !== 'NeedsLogin' && key.state === 'unavailable'))}>
      Renew <RefreshCw size={16} />
    </button>
  </article>
}
