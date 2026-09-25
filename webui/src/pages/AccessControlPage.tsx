import ExitNodeSettingsForm from '../components/ExitNodeSettingsForm'
import TailscaleCredentialForm from '../components/TailscaleCredentialForm'
import { useTailscale } from '../tailscale'

export default function AccessControlPage() {
  const { client, clientError, refresh } = useTailscale()
  return <>
    <section className="page-heading"><div><h1>Access control</h1><p>Offer this device as an exit node, share LAN subnets, and manage Tailscale OAuth credentials.</p></div></section>
    <ExitNodeSettingsForm />
    <section className="panel credential-settings" aria-labelledby="credential-heading">
      <div className="panel__header"><div><span className="panel__eyebrow">TAILSCALE</span><h2 id="credential-heading">OAuth client credentials</h2></div>
        <span className="credential-state">{clientError ? 'Unavailable' : client === undefined ? 'Loading…' : client?.hasClientSecret ? 'Secret saved' : 'Not configured'}</span></div>
      {clientError ? <div className="credential-form"><p className="form-error" role="alert">{clientError}</p>
        <button className="secondary-button" type="button" onClick={() => { void refresh() }}>Retry loading</button></div> :
        client === undefined ? <p className="credential-form" role="status">Loading credential settings…</p> :
        <TailscaleCredentialForm />}
    </section>
  </>
}
