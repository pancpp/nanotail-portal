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
        <li><h2>Allow route approval</h2><p>Select route write permission (<code>devices:routes</code>). This permits approving exit nodes and subnet routes. <code>auth_keys</code> alone is not sufficient; avoid granting “All” access.</p>
          <p>The portal uses this permission only for this device. The credential itself can manage routes across the tailnet, so protect it carefully. Saving credentials does not enroll the device.</p></li>
        <li><h2>Copy both values</h2><p>Select <strong>Generate credential</strong>. Copy the <strong>client ID</strong> and <strong>client secret</strong> before closing the result. Tailscale will not show that secret again.</p></li>
        <li><h2>Save them in nanotail</h2><p>Open Access control, paste both values, and choose <strong>Save credentials</strong>. The portal then checks and approves this device’s advertised exit node and subnet routes automatically. Check <strong>Tailnet approval</strong> for success or permission errors. It does not sign in or switch tailnets. For browser-based sign-in, use <Link to="/">Overview</Link>. Network lets you turn an already enrolled device’s connection on or off.</p>
          <Link className="secondary-button" to="/access-control">Go to credential settings</Link></li>
      </ol>
      <aside className="guide-safety"><h2>Keep the secret private</h2><p>Use trusted HTTPS when entering it. Never share it in screenshots, URLs, or support messages. Protect the device’s database and backups: stored credentials are not encrypted at rest.</p>
        <p>Lost or exposed secret? Create a replacement in Tailscale, update Access control, then revoke the old credential in the admin console. Removing it from nanotail only deletes the local copy.</p></aside>
      <div className="credential-links guide-sources">
        <a href="https://tailscale.com/docs/features/oauth-clients" target="_blank" rel="noopener noreferrer">Official OAuth client guide <ExternalLink size={16} /></a>
        <a href="https://tailscale.com/docs/reference/trust-credentials" target="_blank" rel="noopener noreferrer">Scopes and permissions <ExternalLink size={16} /></a>
      </div>
    </article>
  </>
}
