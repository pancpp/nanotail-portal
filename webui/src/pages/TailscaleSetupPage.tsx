import { useEffect } from 'react'
import { Link, Navigate, useParams } from 'react-router-dom'
import { ArrowLeft, ArrowRight, ExternalLink, KeyRound, Network, Route, ShieldCheck } from 'lucide-react'
import { TAILSCALE_CREDENTIALS_URL } from '../components/TailscaleCredentialForm'

const MACHINES_URL = 'https://console.tailscale.com/admin/machines'
const guides = [
  { slug: 'device-approval', title: 'Sign in and approve this device', description: 'Connect for the first time and complete device approval when your tailnet requires it.', icon: ShieldCheck, content: DeviceApprovalGuide },
  { slug: 'subnet-routes', title: 'Approve subnet routes', description: 'Allow other tailnet devices to reach the local LAN through this device.', icon: Network, content: SubnetRouteGuide },
  { slug: 'exit-node', title: 'Approve this exit node', description: 'Let other tailnet devices use this device as their internet gateway.', icon: Route, content: ExitNodeGuide },
  { slug: 'oauth-credentials', title: 'Create your client credentials', description: 'Set up optional OAuth credentials to approve advertised routes automatically.', icon: KeyRound, content: OAuthGuide },
]

export default function TailscaleSetupPage() {
  const { guide } = useParams()
  const selected = guides.find(item => item.slug === guide)

  useEffect(() => { window.scrollTo(0, 0) }, [guide])

  if (guide && !selected) return <Navigate to="/tailscale-setup" replace />
  const Content = selected?.content

  return <>
    <section className="page-heading"><div>
      {selected && <Link className="guide-back" to="/tailscale-setup"><ArrowLeft size={16} /> All setup guides</Link>}
      <div className="eyebrow eyebrow--light">TAILSCALE SETUP</div>
      <h1>{selected ? selected.title : 'Setup guides'}</h1>
      <p>{selected ? selected.description : 'Start with sign-in, then approve the connections this device provides.'}</p>
    </div></section>
    {Content ? <Content /> : <>
      <nav className="setup-guides" aria-label="Setup guides">
        {guides.map(({ slug, title, description, icon: Icon }) => <Link className="panel guide-card" to={`/tailscale-setup/${slug}`} key={slug}>
          <Icon size={26} aria-hidden="true" />
          <h2>{title}</h2><p>{description}</p>
          <span>Read guide <ArrowRight size={16} aria-hidden="true" /></span>
        </Link>)}
      </nav>
      <p className="guide-note">Device approval, subnet-route approval, and exit-node approval are separate. Saving OAuth credentials can automate route approval, but does not sign in or approve a new device.</p>
    </>}
  </>
}

function MachinesLink() {
  return <a href={MACHINES_URL} target="_blank" rel="noopener noreferrer">Open Tailscale Machines <ExternalLink size={16} /></a>
}

function RouteApprovalNote() {
  return <div className="guide-summary"><ShieldCheck size={24} /><p>Saved OAuth credentials with <code>devices:routes</code> write permission let nanotail approve its advertised routes automatically. Check <strong>Tailnet approval</strong> in <Link to="/access-control">Access control</Link>. If approval is confirmed, or your tailnet’s auto-approvers already approved these routes, skip the manual console steps below. Otherwise, ask a tailnet administrator to approve them.</p></div>
}

function DeviceApprovalGuide() {
  return <article className="panel setup-guide">
    <div className="guide-summary"><ShieldCheck size={24} /><p>Device approval is needed only when your tailnet requires it. An Owner, Admin, or IT admin can approve new devices. Your portal account is separate from your Tailscale account.</p></div>
    <ol className="guide-steps">
      <li><h2>Complete browser sign-in</h2><p>In <Link to="/">Overview</Link>, choose <strong>Sign in to Tailscale</strong>, acknowledge the notice, then select <strong>Prepare sign-in</strong>. Choose <strong>Sign in to Tailscale</strong> in the panel to open Tailscale, sign in, and select your tailnet. Keep the portal open.</p></li>
      <li><h2>Find this device</h2><p>After sign-in, open <strong>Machines</strong> in the correct tailnet’s admin console. Match the device name and Tailscale IP with Overview, when available, and review the owner before approving.</p><MachinesLink /></li>
      <li><h2>Approve the new device</h2><p>If it has a <strong>Needs approval</strong> badge, open its <strong>…</strong> menu and choose <strong>Approve</strong>. If it is already approved or device approval is not enabled, there is nothing to approve here.</p></li>
      <li><h2>Return to nanotail</h2><p>The sign-in panel checks progress automatically. Once sign-in completes, close it with the top-right X and confirm the connection in Overview. Device approval does not require a restart.</p>
        <p>Next, check <Link to="/tailscale-setup/subnet-routes">subnet-route approval</Link> and <Link to="/tailscale-setup/exit-node">exit-node approval</Link>; allowing the device to join does not approve its routing roles.</p></li>
    </ol>
    <aside className="guide-safety"><h2>Still waiting for approval?</h2><p>Ask your tailnet administrator to review this device in the correct tailnet. Saved OAuth route credentials do not perform device approval.</p></aside>
    <div className="credential-links guide-sources"><a href="https://tailscale.com/docs/features/access-control/device-management/device-approval" target="_blank" rel="noopener noreferrer">Official device approval guide <ExternalLink size={16} /></a></div>
  </article>
}

function SubnetRouteGuide() {
  return <article className="panel setup-guide">
    <RouteApprovalNote />
    <ol className="guide-steps">
      <li><h2>Enable and review subnet advertisements</h2><p>First <Link to="/tailscale-setup/device-approval">sign in and approve this device</Link>. In <Link to="/access-control">Access control</Link>, open <strong>Subnet routes</strong>. Advertisement defaults to the local LAN. To change it, enable <strong>Advertise subnet routes</strong>, review the CIDRs or choose <strong>Use local LAN</strong>, acknowledge the notice, then <strong>Save and apply routing</strong>. No save is needed if the desired routes are already advertised.</p>
        <p>Enabling an advertisement is not approval. Complete the approval steps below unless OAuth or tailnet policy already approved it.</p></li>
      <li><h2>Open this device’s route settings</h2><p>In the admin console’s <strong>Machines</strong> list, find this device by name or Tailscale IP. Open the device with the <strong>Subnets</strong> badge, then choose <strong>Edit</strong> in its Subnets section to open <strong>Edit route settings</strong>.</p><MachinesLink /></li>
      <li><h2>Approve only the intended networks</h2><p>Under <strong>Subnet routes</strong>, select the advertised CIDRs you want to allow, then choose <strong>Save</strong>. Check each network against the saved values in nanotail. Newly added routes also need approval.</p></li>
      <li><h2>Check access from another device</h2><p>Tailnet access rules must allow the intended subnet traffic, and clients must accept subnet routes. Linux clients may need <code>sudo tailscale set --accept-routes</code>; run this on the client, not this portal device. Test a permitted LAN service from another tailnet device.</p></li>
    </ol>
    <aside className="guide-safety"><h2>Advertised does not mean reachable</h2><p>If no routes appear in the console, confirm Tailscale is connected and Overview shows the expected subnet advertisements. Routing also requires OS forwarding and suitable firewall rules; nanotail does not change those settings.</p>
      <p>Without OAuth credentials, nanotail does not verify manual approvals. Confirm them in the console; the portal can continue to show manual or policy-based approval.</p></aside>
    <div className="credential-links guide-sources"><a href="https://tailscale.com/docs/features/subnet-routers" target="_blank" rel="noopener noreferrer">Official subnet router guide <ExternalLink size={16} /></a></div>
  </article>
}

function ExitNodeGuide() {
  return <article className="panel setup-guide">
    <RouteApprovalNote />
    <ol className="guide-steps">
      <li><h2>Confirm this device is advertising</h2><p>First <Link to="/tailscale-setup/device-approval">sign in and approve this device</Link>. nanotail always advertises itself as an exit node; there is no enable switch. In <Link to="/access-control">Access control</Link>, check that the exit-node advertisement is <strong>Advertised</strong>.</p></li>
      <li><h2>Find this exit node in Tailscale</h2><p>Open <strong>Machines</strong> in the correct tailnet’s admin console. Match this device’s name or Tailscale IP and look for its <strong>Exit Node</strong> badge.</p><MachinesLink /></li>
      <li><h2>Allow exit-node use</h2><p>Open the device’s <strong>…</strong> menu and choose <strong>Edit route settings</strong>. Enable <strong>Use as exit node</strong> and save the change. This is separate from approving its subnet routes.</p></li>
      <li><h2>Select it on your other devices</h2><p>In another device’s Tailscale app, select this device from the exit-node list. Each client chooses its own exit node; the portal does not select another exit node for itself.</p>
        <p>Your tailnet policy must allow exit-node use. With a restricted policy, ask your administrator about access to <code>autogroup:internet</code>. Test internet access from the client and verify its public IP matches this gateway’s internet connection.</p></li>
    </ol>
    <aside className="guide-safety"><h2>Check approval and connectivity separately</h2><p>If this device is missing from the exit-node list, check its sign-in, advertisement, console approval, and your access policy. OS forwarding and firewall rules must also support routing; nanotail does not configure them.</p>
      <p>Without OAuth credentials, verify manual approval in the console. A portal advertisement alone is not confirmation of approval or working internet access.</p></aside>
    <div className="credential-links guide-sources"><a href="https://tailscale.com/docs/features/exit-nodes" target="_blank" rel="noopener noreferrer">Official exit-node guide <ExternalLink size={16} /></a></div>
  </article>
}

function OAuthGuide() {
  return <article className="panel setup-guide">
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
}
