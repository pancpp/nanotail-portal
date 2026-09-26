import type { TailscaleRouting } from './api'
import { translate, type Language } from './localization.ts'

export function forwardingWarnings(routing: TailscaleRouting, exitNode: boolean, routes: string[], language: Language = 'en'): string[] {
  const warnings: string[] = []
  for (const [family, required, enabled] of [
    ['IPv4', exitNode || routes.some(route => !route.includes(':')), routing.ipv4Forwarding],
    ['IPv6', exitNode || routes.some(route => route.includes(':')), routing.ipv6Forwarding],
  ] as const) {
    if (required && enabled !== true) warnings.push(translate(family + (enabled === false
      ? ' forwarding is disabled. Enable it in the OS for routing to work.'
      : ' forwarding could not be checked. Verify it in the OS before using routing.'), language))
  }
  return warnings
}

export function routingSummary(routing: TailscaleRouting | null, error: string, kind: 'exit' | 'subnet' = 'exit', language: Language = 'en') {
  if (error) return { title: 'Unavailable', detail: 'Unable to read routing settings' }
  if (!routing) return { title: 'Checking…', detail: 'Reading Tailscale routing settings' }
  const enabled = kind === 'exit' ? routing.advertiseExitNode : routing.subnetRoutes.length > 0
  if (!enabled) return kind === 'exit'
    ? { title: 'Pending', detail: routing.usingExitNode ? 'Portal is switching this device from using another exit node to offering one' : 'Always-on exit node · waiting for automatic configuration' }
    : routing.subnetDefaultsPending
      ? { title: 'Pending', detail: 'Enabled by default · waiting to advertise the local LAN' }
      : { title: 'Not advertised', detail: 'Share the local LAN with devices in your tailnet' }
  if (routing.backendState !== 'Running') return { title: 'Paused', detail: 'Advertisements saved · Tailscale is not running' }
  const warnings = forwardingWarnings(routing, kind === 'exit', kind === 'subnet' ? routing.subnetRoutes : [], language)
  if (warnings.length) return { title: 'Needs OS setup', detail: warnings.join(' ') }
  if (!routing.snatEnabled) return { title: 'Check routing', detail: 'SNAT is disabled · verify upstream/return routes before use' }
  if (routing.routeApprovalState === 'APPROVED') return { title: 'Approved', detail: 'Tailscale approval confirmed · access rules and client settings still apply' }
  if (routing.routeApprovalState === 'PENDING') return { title: 'Approval pending', detail: routing.routeApprovalMessage }
  if (routing.routeApprovalState === 'ERROR') return { title: 'Approval unconfirmed', detail: routing.routeApprovalMessage }
  return { title: 'Advertised', detail: kind === 'exit'
    ? 'This device offers internet access · verify tailnet approval'
    : 'Subnet routes advertised · verify tailnet approval and access rules' }
}

export function routingDraft(routing: TailscaleRouting) {
  return {
    subnetEnabled: routing.subnetDefaultsPending || routing.subnetRoutes.length > 0,
    routeText: (routing.subnetRoutes.length ? routing.subnetRoutes : routing.defaultSubnetRoutes).join('\n'),
  }
}
