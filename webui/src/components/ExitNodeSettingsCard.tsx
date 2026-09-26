import { Route } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useI18n, T } from '../i18n'
import { useTailscale } from '../tailscale'

export default function ExitNodeSettingsCard() {
  const { t } = useI18n()
  const { routing, routingError, refresh } = useTailscale()

  return <section className="panel exit-node-settings" aria-labelledby="exit-node-settings-title">
    <div className="panel__header">
      <div><span className="panel__eyebrow">{t("TAILSCALE ROUTING")}</span><h2 id="exit-node-settings-title">{t("Exit node")}</h2></div><Route size={20} />
    </div>
    <div className="credential-form lan-form" aria-busy={!routing && !routingError}>
      <p className="credential-state">{t("Always enabled · managed by this portal")}</p>
      <p className="password-help">{t("This device is an exit node for other tailnet devices. The portal ensures this setting automatically; it cannot be disabled here.")}</p>
      {routingError ? <>
        <p className="form-error" role="alert">{t(routingError)}</p>
        <button className="text-action" type="button" onClick={() => { void refresh() }}>{t("Retry loading")}</button>
      </> : !routing ? <p role="status">{t("Loading routing settings…")}</p> : <>
        <p className="password-help"><T message="Current advertisement: {0}. Tailscale must be signed in and running to carry traffic; admin approval may still be required." values={{ 0: routing.advertiseExitNode ? t("Advertised") : t("Pending") }} /></p>
        {!routing.advertiseExitNode && <p className="lan-warning">{t("The portal will configure the advertisement when Tailscale is available. If it stays pending, check the service logs and daemon permissions.")}</p>}
        {routing.usingExitNode && <p className="lan-warning">{t("This device still has another exit node selected. The portal will clear that selection automatically to enforce this device’s exit-node role.")}</p>}
      </>}
      <p className="password-help"><T message="Approve this device for exit-node use in Tailscale unless OAuth or tailnet policy already approved it. {0}." values={{ 0: <Link to="/tailscale-setup/exit-node" target="_blank" rel="noopener noreferrer">{t("Exit-node approval guide (new tab)")}</Link> }} /></p>
    </div>
  </section>
}
