import { useI18n, T } from '../i18n'
import { useEffect, useState } from 'react'
import { KeyRound, RefreshCw } from 'lucide-react'
import { nodeKeyExpiryDisabled, nodeKeyRenewDisabled, nodeKeyStatus } from '../nodeKey'
import { useTailscale } from '../tailscale'
import NodeKeyRenewalStatus from './NodeKeyRenewalStatus'

export default function NodeKeyCard({ onRenew }: { onRenew: () => void }) {
  const { t, language, locale } = useI18n()
  const { status, statusError, keyRenewalActive, keyRenewal } = useTailscale()
  const [now, setNow] = useState(Date.now)

  useEffect(() => {
    const update = () => setNow(Date.now())
    const timer = window.setInterval(update, 1000)
    window.addEventListener('focus', update)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', update) }
  }, [])

  const key = nodeKeyStatus(status, statusError, now, language)
  return <article className={`status-card node-key-card node-key-card--${key.state}`} aria-labelledby="node-key-heading">
    <div className="status-card__topline">
      <span className="status-icon status-icon--amber"><KeyRound size={20} /></span>
      <span className="quiet-label">{t("KEY EXPIRY")}</span>
    </div>
    <div className="status-card__body">
      <span id="node-key-heading">{t("Node key")}</span>
      <strong>{t(key.label)}</strong>
      <p>{t(key.description)}{key.expiresAt && <>{' '}<T message="{0} (local time)" values={{ 0: <time dateTime={key.expiresAt}>
        {new Date(key.expiresAt).toLocaleString(locale, { dateStyle: 'medium', timeStyle: 'short' })}
      </time> }} /></>}</p>
    </div>
    {key.state === 'expired' && <p className="node-key-warning">{t("Reauthenticate this device to restore access.")}</p>}
    <NodeKeyRenewalStatus snapshot={keyRenewal} compact signIn={status?.backendState === 'NeedsLogin' && !status.haveNodeKey} />
    <button type="button" className="status-card__configure node-key-renew" onClick={onRenew}
      title={nodeKeyExpiryDisabled(status) ? t("Renewal is disabled because node-key expiry is disabled.") : undefined}
      disabled={nodeKeyRenewDisabled(status, statusError, keyRenewalActive)}><T message="Renew {0}" values={{ 0: <RefreshCw size={16} /> }} /></button>
  </article>
}
