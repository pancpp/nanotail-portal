import { useI18n, T } from '../i18n'
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
  const { t } = useI18n()
  const { guide } = useParams()
  const selected = guides.find(item => item.slug === guide)

  useEffect(() => { window.scrollTo(0, 0) }, [guide])

  if (guide && !selected) return <Navigate to="/tailscale-setup" replace />
  const Content = selected?.content

  return <>
    <section className="page-heading"><div>
      {selected && <Link className="guide-back" to="/tailscale-setup"><T message="{0} All setup guides" values={{ 0: <ArrowLeft size={16} /> }} /></Link>}
      <div className="eyebrow eyebrow--light">{t("TAILSCALE SETUP")}</div>
      <h1>{selected ? t(selected.title) : t("Setup guides")}</h1>
      <p>{selected ? t(selected.description) : t("Start with sign-in, then approve the connections this device provides.")}</p>
    </div></section>
    {Content ? <Content /> : <>
      <nav className="setup-guides" aria-label={t("Setup guides")}>
        {guides.map(({ slug, title, description, icon: Icon }) => <Link className="panel guide-card" to={`/tailscale-setup/${slug}`} key={slug}>
          <Icon size={26} aria-hidden="true" />
          <h2>{t(title)}</h2><p>{t(description)}</p>
          <span><T message="Read guide {0}" values={{ 0: <ArrowRight size={16} aria-hidden="true" /> }} /></span>
        </Link>)}
      </nav>
      <p className="guide-note">{t("Device approval, subnet-route approval, and exit-node approval are separate. Saving OAuth credentials can automate route approval, but does not sign in or approve a new device.")}</p>
    </>}
  </>
}

function MachinesLink() {
  return <a href={MACHINES_URL} target="_blank" rel="noopener noreferrer"><T message="Open Tailscale Machines {0}" values={{ 0: <ExternalLink size={16} /> }} /></a>
}

function RouteApprovalNote() {
  const { t } = useI18n()
  return <div className="guide-summary"><ShieldCheck size={24} /><p><T message="Saved OAuth credentials with {0} write permission let nanotail approve its advertised routes automatically. Check {1} in {2}. If approval is confirmed, or your tailnet’s auto-approvers already approved these routes, skip the manual console steps below. Otherwise, ask a tailnet administrator to approve them." values={{ 0: <code>devices:routes</code>, 1: <strong>{t("Tailnet approval")}</strong>, 2: <Link to="/access-control">{t("Access control")}</Link> }} /></p></div>
}

function DeviceApprovalGuide() {
  const { t } = useI18n()
  return <article className="panel setup-guide">
    <div className="guide-summary"><ShieldCheck size={24} /><p>{t("Device approval is needed only when your tailnet requires it. An Owner, Admin, or IT admin can approve new devices. Your portal account is separate from your Tailscale account.")}</p></div>
    <ol className="guide-steps">
      <li><h2>{t("Complete browser sign-in")}</h2><p><T message="In {0}, choose {1}, acknowledge the notice, then select {2}. Choose {3} in the panel to open Tailscale, sign in, and select your tailnet. Keep the portal open." values={{ 0: <Link to="/">{t("Overview")}</Link>, 1: <strong>{t("Sign in to Tailscale")}</strong>, 2: <strong>{t("Prepare sign-in")}</strong>, 3: <strong>{t("Sign in to Tailscale")}</strong> }} /></p></li>
      <li><h2>{t("Find this device")}</h2><p><T message="After sign-in, open {0} in the correct tailnet’s admin console. Match the device name and Tailscale IP with Overview, when available, and review the owner before approving." values={{ 0: <strong>{t("Machines")}</strong> }} /></p><MachinesLink /></li>
      <li><h2>{t("Approve the new device")}</h2><p><T message="If it has a {0} badge, open its {1} menu and choose {2}. If it is already approved or device approval is not enabled, there is nothing to approve here." values={{ 0: <strong>{t("Needs approval")}</strong>, 1: <strong>…</strong>, 2: <strong>{t("Approve")}</strong> }} /></p></li>
      <li><h2>{t("Return to nanotail")}</h2><p>{t("The sign-in panel checks progress automatically. Once sign-in completes, close it with the top-right X and confirm the connection in Overview. Device approval does not require a restart.")}</p>
        <p><T message="Next, check {0} and {1}; allowing the device to join does not approve its routing roles." values={{ 0: <Link to="/tailscale-setup/subnet-routes">{t("subnet-route approval")}</Link>, 1: <Link to="/tailscale-setup/exit-node">{t("exit-node approval")}</Link> }} /></p></li>
    </ol>
    <aside className="guide-safety"><h2>{t("Still waiting for approval?")}</h2><p>{t("Ask your tailnet administrator to review this device in the correct tailnet. Saved OAuth route credentials do not perform device approval.")}</p></aside>
    <div className="credential-links guide-sources"><a href="https://tailscale.com/docs/features/access-control/device-management/device-approval" target="_blank" rel="noopener noreferrer"><T message="Official device approval guide {0}" values={{ 0: <ExternalLink size={16} /> }} /></a></div>
  </article>
}

function SubnetRouteGuide() {
  const { t } = useI18n()
  return <article className="panel setup-guide">
    <RouteApprovalNote />
    <ol className="guide-steps">
      <li><h2>{t("Enable and review subnet advertisements")}</h2><p><T message="First {0}. In {1}, open {2}. Advertisement defaults to the local LAN. To change it, enable {3}, review the CIDRs or choose {4}, acknowledge the notice, then {5}. No save is needed if the desired routes are already advertised." values={{ 0: <Link to="/tailscale-setup/device-approval">{t("sign in and approve this device")}</Link>, 1: <Link to="/access-control">{t("Access control")}</Link>, 2: <strong>{t("Subnet routes")}</strong>, 3: <strong>{t("Advertise subnet routes")}</strong>, 4: <strong>{t("Use local LAN")}</strong>, 5: <strong>{t("Save and apply routing")}</strong> }} /></p>
        <p>{t("Enabling an advertisement is not approval. Complete the approval steps below unless OAuth or tailnet policy already approved it.")}</p></li>
      <li><h2>{t("Open this device’s route settings")}</h2><p><T message="In the admin console’s {0} list, find this device by name or Tailscale IP. Open the device with the {1} badge, then choose {2} in its Subnets section to open {3}." values={{ 0: <strong>{t("Machines")}</strong>, 1: <strong>{t("Subnets")}</strong>, 2: <strong>{t("Edit")}</strong>, 3: <strong>{t("Edit route settings")}</strong> }} /></p><MachinesLink /></li>
      <li><h2>{t("Approve only the intended networks")}</h2><p><T message="Under {0}, select the advertised CIDRs you want to allow, then choose {1}. Check each network against the saved values in nanotail. Newly added routes also need approval." values={{ 0: <strong>{t("Subnet routes")}</strong>, 1: <strong>{t("Save")}</strong> }} /></p></li>
      <li><h2>{t("Check access from another device")}</h2><p><T message="Tailnet access rules must allow the intended subnet traffic, and clients must accept subnet routes. Linux clients may need {0}; run this on the client, not this portal device. Test a permitted LAN service from another tailnet device." values={{ 0: <code>sudo tailscale set --accept-routes</code> }} /></p></li>
    </ol>
    <aside className="guide-safety"><h2>{t("Advertised does not mean reachable")}</h2><p>{t("If no routes appear in the console, confirm Tailscale is connected and Overview shows the expected subnet advertisements. Routing also requires OS forwarding and suitable firewall rules; nanotail does not change those settings.")}</p>
      <p>{t("Without OAuth credentials, nanotail does not verify manual approvals. Confirm them in the console; the portal can continue to show manual or policy-based approval.")}</p></aside>
    <div className="credential-links guide-sources"><a href="https://tailscale.com/docs/features/subnet-routers" target="_blank" rel="noopener noreferrer"><T message="Official subnet router guide {0}" values={{ 0: <ExternalLink size={16} /> }} /></a></div>
  </article>
}

function ExitNodeGuide() {
  const { t } = useI18n()
  return <article className="panel setup-guide">
    <RouteApprovalNote />
    <ol className="guide-steps">
      <li><h2>{t("Confirm this device is advertising")}</h2><p><T message="First {0}. nanotail always advertises itself as an exit node; there is no enable switch. In {1}, check that the exit-node advertisement is {2}." values={{ 0: <Link to="/tailscale-setup/device-approval">{t("sign in and approve this device")}</Link>, 1: <Link to="/access-control">{t("Access control")}</Link>, 2: <strong>{t("Advertised")}</strong> }} /></p></li>
      <li><h2>{t("Find this exit node in Tailscale")}</h2><p><T message="Open {0} in the correct tailnet’s admin console. Match this device’s name or Tailscale IP and look for its {1} badge." values={{ 0: <strong>{t("Machines")}</strong>, 1: <strong>{t("Exit Node")}</strong> }} /></p><MachinesLink /></li>
      <li><h2>{t("Allow exit-node use")}</h2><p><T message="Open the device’s {0} menu and choose {1}. Enable {2} and save the change. This is separate from approving its subnet routes." values={{ 0: <strong>…</strong>, 1: <strong>{t("Edit route settings")}</strong>, 2: <strong>{t("Use as exit node")}</strong> }} /></p></li>
      <li><h2>{t("Select it on your other devices")}</h2><p>{t("In another device’s Tailscale app, select this device from the exit-node list. Each client chooses its own exit node; the portal does not select another exit node for itself.")}</p>
        <p><T message="Your tailnet policy must allow exit-node use. With a restricted policy, ask your administrator about access to {0}. Test internet access from the client and verify its public IP matches this gateway’s internet connection." values={{ 0: <code>autogroup:internet</code> }} /></p></li>
    </ol>
    <aside className="guide-safety"><h2>{t("Check approval and connectivity separately")}</h2><p>{t("If this device is missing from the exit-node list, check its sign-in, advertisement, console approval, and your access policy. OS forwarding and firewall rules must also support routing; nanotail does not configure them.")}</p>
      <p>{t("Without OAuth credentials, verify manual approval in the console. A portal advertisement alone is not confirmation of approval or working internet access.")}</p></aside>
    <div className="credential-links guide-sources"><a href="https://tailscale.com/docs/features/exit-nodes" target="_blank" rel="noopener noreferrer"><T message="Official exit-node guide {0}" values={{ 0: <ExternalLink size={16} /> }} /></a></div>
  </article>
}

function OAuthGuide() {
  const { t } = useI18n()
  return <article className="panel setup-guide">
      <div className="guide-summary"><KeyRound size={24} /><p>{t("You need access to your tailnet’s admin console. A portal account is separate from your Tailscale account.")}</p></div>
      <ol className="guide-steps">
        <li><h2>{t("Open Trust credentials")}</h2><p>{t("Sign in to the correct tailnet as an owner, admin, or another role permitted to create OAuth clients.")}</p>
          <a href={TAILSCALE_CREDENTIALS_URL} target="_blank" rel="noopener noreferrer"><T message="Open Tailscale admin console {0}" values={{ 0: <ExternalLink size={16} /> }} /></a></li>
        <li><h2>{t("Create an OAuth credential")}</h2><p><T message="Select {0}. Give it a recognizable description, such as “nanotail”. This is an OAuth client, not an OAuth app or a personal API access token." values={{ 0: <strong>{t("Credential → OAuth")}</strong> }} /></p></li>
        <li><h2>{t("Allow route approval")}</h2><p><T message="Select route write permission ({0}). This permits approving exit nodes and subnet routes. {1} alone is not sufficient; avoid granting “All” access." values={{ 0: <code>devices:routes</code>, 1: <code>auth_keys</code> }} /></p>
          <p>{t("The portal uses this permission only for this device. The credential itself can manage routes across the tailnet, so protect it carefully. Saving credentials does not enroll the device.")}</p></li>
        <li><h2>{t("Copy both values")}</h2><p><T message="Select {0}. Copy the {1} and {2} before closing the result. Tailscale will not show that secret again." values={{ 0: <strong>{t("Generate credential")}</strong>, 1: <strong>{t("client ID")}</strong>, 2: <strong>{t("client secret")}</strong> }} /></p></li>
        <li><h2>{t("Save them in nanotail")}</h2><p><T message="Open Settings, paste both values, and choose {0}. The portal then checks and approves this device’s advertised exit node and subnet routes automatically. Check {1} for success or permission errors. It does not sign in or switch tailnets. For browser-based sign-in, use {2}. Network lets you turn an already enrolled device’s connection on or off." values={{ 0: <strong>{t("Save credentials")}</strong>, 1: <strong>{t("Tailnet approval")}</strong>, 2: <Link to="/">{t("Overview")}</Link> }} /></p>
          <Link className="secondary-button" to="/settings">{t("Go to credential settings")}</Link></li>
      </ol>
      <aside className="guide-safety"><h2>{t("Keep the secret private")}</h2><p>{t("Use trusted HTTPS when entering it. Never share it in screenshots, URLs, or support messages. Protect the device’s database and backups: stored credentials are not encrypted at rest.")}</p>
        <p>{t("Lost or exposed secret? Create a replacement in Tailscale, update Settings, then revoke the old credential in the admin console. Removing it from nanotail only deletes the local copy.")}</p></aside>
      <div className="credential-links guide-sources">
        <a href="https://tailscale.com/docs/features/oauth-clients" target="_blank" rel="noopener noreferrer"><T message="Official OAuth client guide {0}" values={{ 0: <ExternalLink size={16} /> }} /></a>
        <a href="https://tailscale.com/docs/reference/trust-credentials" target="_blank" rel="noopener noreferrer"><T message="Scopes and permissions {0}" values={{ 0: <ExternalLink size={16} /> }} /></a>
      </div>
    </article>
}
