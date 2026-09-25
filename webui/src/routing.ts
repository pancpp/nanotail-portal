import type { TailscaleRouting } from './api'

export function selectedExitNode(routing: TailscaleRouting): string {
  return routing.exitNodeID || (routing.exitNodeIP ? `unavailable:${routing.exitNodeIP}` : '')
}

export function routingSummary(routing: TailscaleRouting | null, error: string) {
  if (error) return { title: 'Unavailable', detail: 'Unable to read routing settings' }
  if (!routing) return { title: 'Checking…', detail: 'Reading Tailscale routing settings' }
  const selected = selectedExitNode(routing)
  if (!selected) return { title: 'Local gateway', detail: routing.advertiseExitNode ? 'This device advertises itself as an exit node' : 'No Tailscale exit node selected' }
  const peer = routing.exitNodes.find((node) => node.id === selected)
  const title = peer ? peer.hostName || peer.dnsName || peer.id : routing.exitNodeIP || routing.exitNodeID
  const detail = routing.backendState !== 'Running' ? 'Saved exit node · Tailscale is not running' :
    !peer ? 'Selected exit node is no longer available' : !peer.online ? 'Exit node is offline · internet access may be unavailable' :
    `Selected exit node · local LAN access ${routing.allowLANAccess ? 'allowed' : 'blocked'}`
  return { title, detail }
}
