import { useI18n, T } from '../i18n'
import { useEffect, useState } from 'react'
import {
  ChevronRight,
  Cpu,
  SlidersHorizontal,
  Users,
  Wifi,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import { isTailscaleConnected, shouldPromptForTailscale, tailscaleStatusLabel } from '../api'
import { useTailscale } from '../tailscale'
import { usePeerLatencies } from '../usePeerLatencies'
import { nodeKeyDialogMode } from '../nodeKey'
import DeviceStatusPanel from '../components/DeviceStatusPanel'
import NetworkActivityPanel from '../components/NetworkActivityPanel'
import NodeKeyCard from '../components/NodeKeyCard'
import RoutingCard from '../components/RoutingCard'
import NodeKeyRenewalDialog from '../components/NodeKeyRenewalDialog'
import TailscaleSetupPrompt from '../components/TailscaleSetupPrompt'
import PeerConnection from '../components/PeerConnection'
import PeerRelayCard from '../components/PeerRelayCard'

export default function OverviewPage() {
  const { t, locale } = useI18n()
  const { status, statusError, setKeyRenewalDialogOpen } = useTailscale()
  const latencies = usePeerLatencies(status, statusError)
  const [dialogMode, setDialogMode] = useState<'signin' | 'renewal' | null>(null)
  const [promptDismissed, setPromptDismissed] = useState(false)
  const needsSignIn = shouldPromptForTailscale(status, statusError)
  // Local to this route: show once per Overview visit, never on each poll.
  // A single dialog also serves manual Renew, so prompts cannot stack.
  useEffect(() => {
    if (needsSignIn && !promptDismissed && !dialogMode) {
      setPromptDismissed(true)
      setDialogMode('signin')
    }
  }, [needsSignIn, promptDismissed, dialogMode])
  function openDialog(mode: 'signin' | 'renewal') {
    setPromptDismissed(true)
    setKeyRenewalDialogOpen(true)
    setDialogMode(mode)
  }
  const connected = isTailscaleConnected(status, statusError)
  const label = tailscaleStatusLabel(status, statusError)
  const peers = statusError ? [] : status?.peers ?? []
  return (
    <>
      <section className="page-heading">
        <div>
          <div className="eyebrow eyebrow--light">
            <span className="eyebrow__dot" />
            nanotail portal
          </div>
          <h1>{t("Good to see you.")}</h1>
          <p>{t("Here’s what’s happening on your private network.")}</p>
        </div>
        <div className="integration-note"><T message="{0}Live device and Tailscale status" values={{ 0: <SlidersHorizontal size={16} /> }} /></div>
      </section>

      <section className="status-grid" aria-label={t("Network status")}>
        <div className="status-grid__row">
          <article className="status-card status-card--primary">
            <div className="status-card__topline">
              <span className="status-icon"><Wifi size={20} /></span>
              <span className={`status-pill${connected ? '' : ' connection-muted'}`}><i /> {t(label)}</span>
            </div>
            <div className="status-card__body">
              <span>{t("Tailscale status")}</span>
              <strong>{connected ? t("Connected to your tailnet") : t(label)}</strong>
              <p>{!statusError && status?.currentTailnet?.name ? status.currentTailnet.name : t("No active tailnet connection")}</p>
              {needsSignIn && <button type="button" className="status-card__configure tailscale-signin" onClick={() => openDialog('signin')}><T message="Sign in to Tailscale {0}" values={{ 0: <ChevronRight size={15} /> }} /></button>}
            </div>
            <div className="status-card__footer">
              <Link className="status-card__configure" to="/network"><T message="Configure {0}" values={{ 0: <ChevronRight size={15} /> }} /></Link>
              <span>{!statusError && status?.tailscaleIPs.length ? status.tailscaleIPs.join(', ') : t("No Tailscale address")}</span>
            </div>
          </article>

          <NodeKeyCard onRenew={() => openDialog(nodeKeyDialogMode(status))} />
        </div>
        <div className="status-grid__row status-grid__row--secondary">
          <RoutingCard />
          <RoutingCard kind="subnet" />
          <PeerRelayCard />
        </div>
      </section>

      <section className="dashboard-columns">
        <article className="panel peers-panel">
          <div className="panel__header">
            <div>
              <span className="panel__eyebrow">TAILNET</span>
              <h2>{t("Tailnet peers")}</h2>
            </div>
            <span className="peer-count"><Users size={15} /> {statusError ? t("Unavailable") : !status ? t("Checking…") : t(peers.length === 1 ? '{count} device' : '{count} devices', { count: peers.length })}</span>
          </div>

          <div className="peer-table">
            <div className="peer-table__head">
              <span>{t("Device")}</span>
              <span>{t("Address")}</span>
              <span>{t("Status")}</span>
              <span>{t("Connection")}</span>
              <span title={t("Round-trip latency from this device")}>{t("Latency")}</span>
            </div>
            {peers.map((peer) => (
              <div className="peer-row" key={peer.id}>
                <div className="peer-device">
                  <span className="peer-device__icon"><Cpu size={17} /></span>
                  <span><strong title={peer.hostName || peer.dnsName || peer.id}>{peer.hostName || peer.dnsName || peer.id}</strong><small>{peer.os || t("Unknown OS")}</small></span>
                </div>
                <code>{peer.tailscaleIPs.join(', ') || t('No address')}</code>
                <span className={`peer-status${peer.online ? '' : ' peer-status--offline'}`}>
                  <i /> {peer.online ? t("Online") : t("Offline")}
                </span>
                <PeerConnection peer={peer} connected={connected} />
                <span className="peer-latency" aria-label={t("Latency")}>
                  {!peer.online ? '—' : latencies.loading ? t("Checking…") : latencies.values.get(peer.id) != null
                    ? t('{latency} ms', { latency: latencies.values.get(peer.id)!.toLocaleString(locale, { maximumFractionDigits: 1 }) })
                    : t("Unavailable")}
                </span>
              </div>
            ))}
            {peers.length === 0 && <p className="peer-table__empty" role="status">
              {statusError ? t("Unable to load peers. Retry the status request.") : !status ? t("Loading peers…") : t("No peers are visible to this device.")}
            </p>}
          </div>
        </article>

        <NetworkActivityPanel />
      </section>

      <DeviceStatusPanel />
      {dialogMode === 'signin' && <TailscaleSetupPrompt onClose={() => setDialogMode(null)} />}
      {dialogMode === 'renewal' && <NodeKeyRenewalDialog onClose={() => setDialogMode(null)} />}
    </>
  )
}
