import { useI18n } from '../i18n'
import ChangePasswordForm from '../components/ChangePasswordForm'
import FactoryResetPanel from '../components/FactoryResetPanel'
import TailscaleCredentialForm from '../components/TailscaleCredentialForm'
import { useTailscale } from '../tailscale'

export default function SettingsPage() {
  const { t } = useI18n()
  const { client, clientError, refresh } = useTailscale()
  return (
    <>
      <section className="page-heading">
        <div>
          <h1>{t("Settings")}</h1>
          <p>{t("Manage Tailscale OAuth credentials, your portal account, and device reset.")}</p>
        </div>
      </section>
      <section className="panel credential-settings" aria-labelledby="credential-heading">
        <div className="panel__header"><div><span className="panel__eyebrow">TAILSCALE</span><h2 id="credential-heading">{t("OAuth client credentials")}</h2></div>
          <span className="credential-state">{clientError ? t("Unavailable") : client === undefined ? t("Loading…") : client?.hasClientSecret ? t("Secret saved") : t("Not configured")}</span></div>
        {clientError ? <div className="credential-form"><p className="form-error" role="alert">{t(clientError)}</p>
          <button className="secondary-button" type="button" onClick={() => { void refresh() }}>{t("Retry loading")}</button></div> :
          client === undefined ? <p className="credential-form" role="status">{t("Loading credential settings…")}</p> :
          <TailscaleCredentialForm />}
      </section>
      <ChangePasswordForm />
      <FactoryResetPanel />
    </>
  )
}
