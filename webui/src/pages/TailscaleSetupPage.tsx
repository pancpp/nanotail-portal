import { Link } from 'react-router-dom'
import { ExternalLink, KeyRound } from 'lucide-react'
import { TAILSCALE_CREDENTIALS_URL } from '../components/TailscaleCredentialForm'

export default function TailscaleSetupPage() {
  return <>
    <section className="page-heading"><div><div className="eyebrow eyebrow--light">TAILSCALE SETUP</div>
      <h1>Create your client credentials.</h1><p>A short guide to preparing an OAuth client for nanotail.</p></div></section>
    <article className="panel setup-guide">
      <div className="guide-summary"><KeyRound size={24} /><p>You need access to your tailnet’s admin console. A portal account is separate from your Tailscale account.</p></div>
      <ol className="guide-steps">
        <li><h2>Open Trust credentials</h2><p>Sign in to the correct tailnet as an owner, admin, or another role permitted to create OAuth clients.</p>
          <a href={TAILSCALE_CREDENTIALS_URL} target="_blank" rel="noopener noreferrer">Open Tailscale admin console <ExternalLink size={16} /></a></li>
        <li><h2>Create an OAuth credential</h2><p>Select <strong>Credential → OAuth</strong>. Give it a recognizable description, such as “nanotail”. This is an OAuth client, not an OAuth app or a personal API access token.</p></li>
        <li><h2>Choose limited permissions</h2><p>For future device enrollment, select <strong>Auth keys → Write</strong> (<code>auth_keys</code>) and an existing device tag, for example <code>tag:nanotail</code>. Enrollment must use a tag allowed by this client. Avoid granting “All” access.</p>
          <p>Saving credentials in this portal does not yet use these permissions or enroll the device. Ask your tailnet administrator if you need a tag or permission.</p></li>
        <li><h2>Copy both values</h2><p>Select <strong>Generate credential</strong>. Copy the <strong>client ID</strong> and <strong>client secret</strong> before closing the result. Tailscale will not show that secret again.</p></li>
        <li><h2>Save them in nanotail</h2><p>Return to the setup dialog or Network, paste both values, and choose <strong>Save credentials</strong>. The portal stores them on this device; it does not validate them or enroll the device in a tailnet. Network also lets you turn an already enrolled device’s connection on or off.</p>
          <Link className="secondary-button" to="/network">Go to credential settings</Link></li>
      </ol>
      <aside className="guide-safety"><h2>Keep the secret private</h2><p>Use trusted HTTPS when entering it. Never share it in screenshots, URLs, or support messages. Protect the device’s database and backups: stored credentials are not encrypted at rest.</p>
        <p>Lost or exposed secret? Create a replacement in Tailscale, update Network, then revoke the old credential in the admin console. Removing it from nanotail only deletes the local copy.</p></aside>
      <div className="credential-links guide-sources">
        <a href="https://tailscale.com/docs/features/oauth-clients" target="_blank" rel="noopener noreferrer">Official OAuth client guide <ExternalLink size={16} /></a>
        <a href="https://tailscale.com/docs/reference/trust-credentials" target="_blank" rel="noopener noreferrer">Scopes and permissions <ExternalLink size={16} /></a>
      </div>
    </article>
  </>
}
