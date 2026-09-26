import { useI18n } from '../i18n'
import ExitNodeSettingsCard from '../components/ExitNodeSettingsCard'
import SubnetRoutesSettingsForm from '../components/SubnetRoutesSettingsForm'
import TailscaleCredentialForm from '../components/TailscaleCredentialForm'
import { useTailscale } from '../tailscale'
import { useEffect } from 'react'
import { useLocation } from 'react-router-dom'
import PeerRelaySettingsForm from '../components/PeerRelaySettingsForm'

export default function AccessControlPage() {
  const { t } = useI18n()
  const { client, clientError, refresh } = useTailscale()
  const { hash } = useLocation()
  useEffect(() => {
    if (hash === '#peer-relay') document.getElementById('peer-relay')?.scrollIntoView({ block: 'start' })
  }, [hash])
  return <>
    <section className="page-heading"><div><h1>{t("Access control")}</h1><p>{t("Configure exit-node, subnet, and peer relay services, and manage Tailscale OAuth credentials.")}</p></div></section>
    <ExitNodeSettingsCard />
    <SubnetRoutesSettingsForm />
    <PeerRelaySettingsForm />
    <section className="panel credential-settings" aria-labelledby="credential-heading">
      <div className="panel__header"><div><span className="panel__eyebrow">TAILSCALE</span><h2 id="credential-heading">{t("OAuth client credentials")}</h2></div>
        <span className="credential-state">{clientError ? t("Unavailable") : client === undefined ? t("Loading…") : client?.hasClientSecret ? t("Secret saved") : t("Not configured")}</span></div>
      {clientError ? <div className="credential-form"><p className="form-error" role="alert">{t(clientError)}</p>
        <button className="secondary-button" type="button" onClick={() => { void refresh() }}>{t("Retry loading")}</button></div> :
        client === undefined ? <p className="credential-form" role="status">{t("Loading credential settings…")}</p> :
        <TailscaleCredentialForm />}
    </section>
  </>
}
