import type { TailscalePeer } from './api'

export function peerConnection(peer: TailscalePeer, connected: boolean) {
  if (!connected) return { kind: 'unavailable', label: 'Unavailable', detail: '' }
  if (!peer.online) return { kind: 'offline', label: '—', detail: '' }
  if (!peer.active) return { kind: 'idle', label: 'Idle', detail: '' }
  // Match tailscale status: a current direct address takes precedence over
  // peer relays; a DERP home region alone does not imply an active connection.
  if (peer.curAddr) return { kind: 'direct', label: 'Direct', detail: '' }
  if (peer.peerRelay) return { kind: 'peer-relay', label: 'Peer relay', detail: peer.peerRelay }
  if (peer.relay) return { kind: 'derp', label: 'DERP', detail: peer.relay }
  return { kind: 'unknown', label: 'Unknown', detail: '' }
}
