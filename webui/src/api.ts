import { canonicalSubnetRoutes } from './subnetRoutes.ts'

export interface LoginCredentials {
  username: string
  password: string
}

export interface PasswordChange {
  oldpassword: string
  newpassword: string
}

export class ApiError extends Error {
  readonly status: number
  readonly isGraphQLError: boolean

  constructor(message: string, status: number, isGraphQLError = false) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.isGraphQLError = isGraphQLError
  }
}

// Reading expiry only controls the UI. The backend verifies JWT signatures.
export function tokenExpiry(token: string, now = Date.now()): number | null {
  try {
    const parts = token.split('.')
    if (parts.length !== 3 || parts.some((part) => !part)) return null
    const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')))
    const expiresAt = payload.exp * 1000
    if (
      !Number.isSafeInteger(payload.pid) || payload.pid <= 0 ||
      !Number.isSafeInteger(payload.exp) || !Number.isSafeInteger(expiresAt) ||
      expiresAt <= now
    ) return null
    return expiresAt
  } catch {
    return null
  }
}

async function apiError(response: Response, fallback: string): Promise<ApiError> {
  try {
    const payload = await response.json()
    if (typeof payload?.message === 'string' && payload.message) {
      return new ApiError(payload.message, response.status)
    }
  } catch {
    // Proxies and unavailable devices may return a non-JSON error page.
  }
  return new ApiError(fallback, response.status)
}

export async function loginRequest(credentials: LoginCredentials): Promise<string> {
  const response = await fetch('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(credentials),
  })
  if (!response.ok) {
    throw await apiError(response, 'Unable to sign in. Check your credentials.')
  }

  const payload = await response.json().catch(() => null)
  if (typeof payload?.token !== 'string' || tokenExpiry(payload.token) === null) {
    throw new Error('The server did not return a valid login token.')
  }
  return payload.token
}

const CHANGE_PASSWORD_MUTATION = `
  mutation ChangePassword($passwords: ChangePassword!) {
    changePassword(passwords: $passwords)
  }
`

export async function changePasswordRequest(token: string, passwords: PasswordChange): Promise<void> {
  const bytes = new TextEncoder().encode(passwords.newpassword).length
  if (bytes < 8 || bytes > 72) {
    throw new Error('New password must contain between 8 and 72 bytes.')
  }

  const response = await fetch('/api/v1/query', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({
      operationName: 'ChangePassword',
      query: CHANGE_PASSWORD_MUTATION,
      variables: { passwords },
    }),
  })
  const payload = await response.json().catch(() => null)
  const errors: unknown[] = Array.isArray(payload?.errors) ? payload.errors : []
  // Resolver failures can use HTTP 200, even when data is also present.
  if (!response.ok || errors.length > 0) {
    const messages = errors.flatMap((error) => {
      if (typeof error !== 'object' || error === null || !('message' in error)) return []
      return typeof error.message === 'string' && error.message.trim() ? [error.message] : []
    })
    const message = messages.join('\n') ||
      (typeof payload?.message === 'string' && payload.message) ||
      'Unable to change your password. Please try again.'
    throw new ApiError(message, response.status)
  }
  if (payload?.data?.changePassword !== true ||
      (payload.errors !== undefined && !Array.isArray(payload.errors))) {
    throw new ApiError('The server did not confirm the password change.', response.status)
  }
}

export function isSessionError(error: unknown): boolean {
  // JWT rejection is HTTP 401; an incorrect password is a GraphQL resolver error.
  return error instanceof ApiError && error.status === 401
}

export interface TailscalePeer {
  id: string
  hostName: string
  dnsName: string
  os: string
  tailscaleIPs: string[]
  online: boolean
  active: boolean
  curAddr: string
  peerRelay: string
  relay: string
}

export interface TailscalePeerLatency {
  id: string
  latencyMs: number | null
}

// Only the fields selected by the WebUI's status query are included here.
export interface TailscaleStatus {
  backendState: string
  haveNodeKey: boolean
  tailscaleIPs: string[]
  currentTailnet: { name: string } | null
  self: { id?: string; online: boolean; keyExpiry: string | null } | null
  peers: TailscalePeer[]
}

export interface TailscaleClient {
  clientId: string
  hasClientSecret: boolean
  updateTime: string
}

export interface TailscaleCredential {
  clientId: string
  clientSecret?: string
}

export interface DeviceStatus {
  hostname: string
  lanIPType: string
  lanIP: string
  gateway: string
  dns: string[]
  lanIPv6Type: string
  lanIPv6: string
  gateway6: string
  ethAddr: string
  cpuload: number
  memory: number
  lastRestart: string
  uptime: number
  health: string
}

export interface NetworkActivity {
  interfaceName: 'tailscale0'
  rxBytes: string
  txBytes: string
  sampledAt: string
  counterEpoch: string
}

function isByteCounter(value: unknown): value is string {
  return typeof value === 'string' && /^(0|[1-9][0-9]{0,19})$/.test(value) && BigInt(value) <= 18446744073709551615n
}

export async function networkActivityRequest(token: string, signal?: AbortSignal): Promise<NetworkActivity> {
  const data = await graphQLRequest(token, 'NetworkActivity', `query NetworkActivity {
    networkActivity { interfaceName rxBytes txBytes sampledAt counterEpoch }
  }`, {}, signal, 'VPN traffic')
  const sample: unknown = data.networkActivity
  if (!isRecord(sample) || sample.interfaceName !== 'tailscale0' || !isByteCounter(sample.rxBytes) || !isByteCounter(sample.txBytes) ||
      typeof sample.sampledAt !== 'string' || !Number.isFinite(Date.parse(sample.sampledAt)) ||
      typeof sample.counterEpoch !== 'string' || !sample.counterEpoch.trim()) {
    throw new Error('The server did not return valid VPN traffic counters.')
  }
  return sample as unknown as NetworkActivity
}

export interface DeviceIP {
  type: 'static' | 'DHCP'
  ip: string
  gateway: string
  dns: string[]
}

export interface NetworkActivityHour {
  startedAt: string
  rxBytes: string
  txBytes: string
  observedSeconds: number
}

export interface NetworkActivityHistory {
  windowStart: string
  windowEnd: string
  hours: NetworkActivityHour[]
  totals: {
    rxBytes24h: string
    txBytes24h: string
    observedSeconds24h: number
    totalRxBytes: string
    totalTxBytes: string
    totalObservedSeconds: number
    recordedSince: string | null
  }
}

export async function networkActivityHistoryRequest(token: string, signal?: AbortSignal): Promise<NetworkActivityHistory> {
  const data = await graphQLRequest(token, 'NetworkActivityHistory', `query NetworkActivityHistory {
    networkActivityHistory {
      windowStart windowEnd hours { startedAt rxBytes txBytes observedSeconds }
      totals { rxBytes24h txBytes24h observedSeconds24h totalRxBytes totalTxBytes totalObservedSeconds recordedSince }
    }
  }`, {}, signal, 'VPN history')
  const history: unknown = data.networkActivityHistory
  const invalid = () => new Error('The server did not return valid VPN history.')
  if (!isRecord(history) || typeof history.windowStart !== 'string' || typeof history.windowEnd !== 'string' ||
    !Array.isArray(history.hours) || history.hours.length > 24) throw invalid()
  const start = Date.parse(history.windowStart), end = Date.parse(history.windowEnd)
  if (!Number.isFinite(start) || !Number.isFinite(end) || end - start !== 86_400_000 || start % 3_600_000 !== 0) throw invalid()
  let previous = start - 1
  let rx = 0n, tx = 0n, observed = 0
  for (const hour of history.hours) {
    if (!isRecord(hour) || typeof hour.startedAt !== 'string' ||
      typeof hour.rxBytes !== 'string' || !/^(0|[1-9][0-9]{0,39})$/.test(hour.rxBytes) ||
      typeof hour.txBytes !== 'string' || !/^(0|[1-9][0-9]{0,39})$/.test(hour.txBytes) ||
      typeof hour.observedSeconds !== 'number' || !Number.isFinite(hour.observedSeconds) || hour.observedSeconds < 0 || hour.observedSeconds > 3600) throw invalid()
    const at = Date.parse(hour.startedAt)
    if (!Number.isFinite(at) || at < start || at >= end || at % 3_600_000 !== 0 || at <= previous) throw invalid()
    previous = at
    if (hour.observedSeconds === 0 && (hour.rxBytes !== '0' || hour.txBytes !== '0')) throw invalid()
    rx += BigInt(hour.rxBytes); tx += BigInt(hour.txBytes); observed += hour.observedSeconds
  }
  const totals = history.totals
  if (!isRecord(totals)) throw invalid()
  for (const key of ['rxBytes24h', 'txBytes24h', 'totalRxBytes', 'totalTxBytes']) {
    if (typeof totals[key] !== 'string' || !/^(0|[1-9][0-9]{0,39})$/.test(totals[key] as string)) throw invalid()
  }
  if (typeof totals.observedSeconds24h !== 'number' || !Number.isFinite(totals.observedSeconds24h) ||
    totals.observedSeconds24h < 0 || totals.observedSeconds24h > 86400 ||
    Math.abs(totals.observedSeconds24h - observed) > 0.00001 ||
    typeof totals.totalObservedSeconds !== 'number' || !Number.isFinite(totals.totalObservedSeconds) || totals.totalObservedSeconds < totals.observedSeconds24h ||
    BigInt(totals.rxBytes24h as string) !== rx || BigInt(totals.txBytes24h as string) !== tx ||
    BigInt(totals.totalRxBytes as string) < rx || BigInt(totals.totalTxBytes as string) < tx) throw invalid()
  if (totals.totalObservedSeconds === 0) {
    if (totals.recordedSince !== null || totals.totalRxBytes !== '0' || totals.totalTxBytes !== '0') throw invalid()
  } else if (typeof totals.recordedSince !== 'string' || !Number.isFinite(Date.parse(totals.recordedSince)) ||
    Date.parse(totals.recordedSince) >= end || Date.parse(totals.recordedSince) % 3_600_000 !== 0) throw invalid()
  return history as unknown as NetworkActivityHistory
}

function ipv4Number(value: string): number | null {
  const parts = value.split('.')
  if (parts.length !== 4 || parts.some((part) => !/^(0|[1-9][0-9]{0,2})$/.test(part) || Number(part) > 255)) return null
  const bytes = parts.map(Number)
  if (bytes[0] === 0 || bytes[0] === 127 || bytes[0] >= 224) return null
  return bytes.reduce((address, byte) => address * 256 + byte, 0)
}

export function validateDeviceIP(input: DeviceIP): DeviceIP {
  const type = input.type.trim().toLowerCase()
  const ip = input.ip.trim(), gateway = input.gateway.trim()
  if (type === 'dhcp') {
    if (ip || gateway || input.dns.length) throw new Error('DHCP requires empty address, gateway, and DNS fields.')
    return { type: 'DHCP', ip: '', gateway: '', dns: [] }
  }
  if (type !== 'static') throw new Error('Select static or DHCP.')
  const match = ip.match(/^([^/]+)\/([1-9]|[12][0-9]|3[0-2])$/)
  const address = match ? ipv4Number(match[1]) : null
  if (!match || address === null) throw new Error('Enter a unicast IPv4 address with a /1–/32 CIDR prefix, such as 192.168.1.20/24.')
  const prefix = Number(match[2]), mask = (0xffffffff << (32 - prefix)) >>> 0
  const network = (address & mask) >>> 0, broadcast = (network | ~mask) >>> 0
  if (prefix < 31 && (address === network || address === broadcast)) throw new Error('The address cannot be the subnet’s network or broadcast address.')
  if (gateway) {
    const route = ipv4Number(gateway)
    if (route === null || route === address || ((route & mask) >>> 0) !== network ||
        (prefix < 31 && (route === network || route === broadcast))) {
      throw new Error('The gateway must be a different usable IPv4 address in the same subnet.')
    }
  }
  if (input.dns.length > 8 || input.dns.some((value) => ipv4Number(value.trim()) === null)) {
    throw new Error('Enter at most eight unicast IPv4 DNS server addresses.')
  }
  return { type: 'static', ip, gateway, dns: [...new Set(input.dns.map((value) => value.trim()))] }
}

export async function setDeviceIPRequest(token: string, input: DeviceIP): Promise<void> {
  const deviceIP = validateDeviceIP(input)
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 60_000)
  try {
    const data = await graphQLRequest(token, 'SetDeviceIP', `mutation SetDeviceIP($deviceIP: DeviceIP!) {
      setDeviceIP(deviceIP: $deviceIP)
    }`, { deviceIP }, controller.signal, 'LAN configuration')
    if (data.setDeviceIP !== true) throw new Error('The server did not confirm the LAN change. Check the device before retrying.')
  } finally { clearTimeout(timer) }
}

export function deviceReconnectURL(currentURL: string, ip: string): string | null {
  const address = ip.split('/')[0]
  if (ipv4Number(address) === null) return null
  const url = new URL(currentURL)
  if (!['http:', 'https:'].includes(url.protocol)) return null
  url.hostname = address
  url.username = ''; url.password = ''; url.search = ''; url.hash = '/login'
  return url.toString()
}

async function graphQLRequest(token: string, operationName: string, query: string,
  variables: object = {}, signal?: AbortSignal, resource = 'Tailscale') {
  const response = await fetch('/api/v1/query', {
    method: 'POST', signal, cache: 'no-store',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ operationName, query, variables }),
  })
  const payload = await response.json().catch(() => null)
  if (!response.ok || (Array.isArray(payload?.errors) && payload.errors.length > 0)) {
    const messages = Array.isArray(payload?.errors) ? payload.errors.flatMap((error: unknown) =>
      typeof error === 'object' && error !== null && 'message' in error &&
      typeof error.message === 'string' && error.message.trim() ? [error.message] : []) : []
    throw new ApiError(messages.join('\n') ||
      (typeof payload?.message === 'string' && payload.message) ||
      `Unable to complete the ${resource} request. Please retry.`, response.status, messages.length > 0)
  }
  if (!payload?.data || (payload.errors !== undefined && !Array.isArray(payload.errors))) {
    throw new ApiError(`The server returned an invalid ${resource} response.`, response.status)
  }
  return payload.data
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === 'string')
}

function isTailscalePeer(value: unknown): value is TailscalePeer {
  return isRecord(value) && typeof value.id === 'string' && typeof value.hostName === 'string' &&
    typeof value.dnsName === 'string' && typeof value.os === 'string' &&
    isStringArray(value.tailscaleIPs) && typeof value.online === 'boolean' && typeof value.active === 'boolean' &&
    typeof value.curAddr === 'string' && typeof value.peerRelay === 'string' && typeof value.relay === 'string'
}

function isTailscaleStatus(value: unknown): value is TailscaleStatus {
  return isRecord(value) && typeof value.backendState === 'string' && typeof value.haveNodeKey === 'boolean' && isStringArray(value.tailscaleIPs) &&
    (value.currentTailnet === null || (isRecord(value.currentTailnet) && typeof value.currentTailnet.name === 'string')) &&
    (value.self === null || (isRecord(value.self) && (value.self.id === undefined || typeof value.self.id === 'string') && typeof value.self.online === 'boolean' &&
      (value.self.keyExpiry === null || (typeof value.self.keyExpiry === 'string' && Number.isFinite(Date.parse(value.self.keyExpiry)))))) &&
    Array.isArray(value.peers) && value.peers.every(isTailscalePeer)
}

function isPercentage(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= 100
}

function isDeviceStatus(value: unknown): value is DeviceStatus {
  return isRecord(value) && typeof value.hostname === 'string' &&
    typeof value.lanIPType === 'string' && typeof value.lanIP === 'string' && typeof value.gateway === 'string' &&
    isStringArray(value.dns) && typeof value.lanIPv6Type === 'string' && typeof value.lanIPv6 === 'string' &&
    typeof value.gateway6 === 'string' && typeof value.ethAddr === 'string' &&
    isPercentage(value.cpuload) && isPercentage(value.memory) &&
    typeof value.lastRestart === 'string' && Number.isFinite(Date.parse(value.lastRestart)) &&
    typeof value.uptime === 'number' && Number.isSafeInteger(value.uptime) && value.uptime >= 0 &&
    typeof value.health === 'string'
}

export async function portalVersionRequest(token: string, signal?: AbortSignal): Promise<string> {
  const data = await graphQLRequest(token, 'PortalVersion', 'query PortalVersion { portalVersion }', {}, signal, 'portal version')
  if (typeof data.portalVersion !== 'string') throw new Error('The server did not return a valid portal version.')
  return data.portalVersion
}

export async function deviceStatusRequest(token: string, signal?: AbortSignal): Promise<DeviceStatus> {
  const data = await graphQLRequest(token, 'DeviceStatus', `query DeviceStatus {
    deviceStatus { hostname lanIPType lanIP gateway dns lanIPv6Type lanIPv6 gateway6 ethAddr cpuload memory lastRestart uptime health }
  }`, {}, signal, 'device')
  const status: unknown = data.deviceStatus
  if (!isDeviceStatus(status)) throw new Error('The server did not return a valid device status.')
  return status
}

export function formatDeviceUptime(seconds: number, locale?: string): string {
  if (!Number.isSafeInteger(seconds) || seconds < 0) return 'Unavailable'
  const days = Math.floor(seconds / 86_400)
  const hours = Math.floor(seconds % 86_400 / 3_600)
  const minutes = Math.floor(seconds % 3_600 / 60)
  if (locale === 'zh-CN') {
    if (days > 0) return `${days} 天 ${hours} 小时 ${minutes} 分钟`
    if (hours > 0) return `${hours} 小时 ${minutes} 分钟`
    if (minutes > 0) return `${minutes} 分钟`
    return `${seconds} 秒`
  }
  if (days > 0) return `${days}d ${hours}h ${minutes}m`
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m`
  return `${seconds}s`
}

export function deviceIPTypeLabel(method: string, ipv6 = false): string {
  switch (method.toLowerCase()) {
    case 'static': return 'Static'
    case 'dhcp': return ipv6 ? 'DHCPv6' : 'DHCP'
    case 'auto': return ipv6 ? 'Automatic (SLAAC / DHCPv6)' : 'Automatic'
    case 'disabled': return 'Disabled'
    case 'ignore': return 'Not managed'
    case 'link-local': return 'Link-local only'
    case 'shared': return 'Shared connection'
    case 'unknown': case '': return 'Unknown'
    default: return method
  }
}

export async function tailscaleStatusRequest(token: string, signal?: AbortSignal): Promise<TailscaleStatus> {
  const data = await graphQLRequest(token, 'TailscaleStatus', `query TailscaleStatus {
    tailscaleStatus {
      backendState
      haveNodeKey
      tailscaleIPs
      currentTailnet { name }
      self { id online keyExpiry }
      peers { id hostName dnsName os tailscaleIPs online active curAddr peerRelay relay }
    }
  }`, {}, signal)
  const status: unknown = data.tailscaleStatus
  if (!isTailscaleStatus(status)) {
    throw new Error('The server did not return a valid Tailscale status.')
  }
  return status
}

// Keep active probes separate from ordinary status reads and background services.
export async function tailscalePeerLatenciesRequest(token: string, signal?: AbortSignal): Promise<TailscalePeerLatency[]> {
  const data = await graphQLRequest(token, 'TailscalePeerLatencies', `query TailscalePeerLatencies {
    tailscaleStatus { peers { id latencyMs } }
  }`, {}, signal)
  const peers: unknown = data.tailscaleStatus?.peers
  if (!Array.isArray(peers) || !peers.every((peer) => isRecord(peer) && typeof peer.id === 'string' &&
    (peer.latencyMs === null || (typeof peer.latencyMs === 'number' && Number.isFinite(peer.latencyMs) && peer.latencyMs >= 0)))) {
    throw new Error('The server did not return valid peer latencies.')
  }
  return peers as TailscalePeerLatency[]
}

export interface TailscaleRouting {
  peerRelayEnabled: boolean
  peerRelayPort: number | null
  routeApprovalState: 'DISABLED' | 'PENDING' | 'APPROVED' | 'ERROR'
  routeApprovalMessage: string
  backendState: string
  advertiseExitNode: boolean
  subnetDefaultsPending: boolean
  subnetRoutes: string[]
  usingExitNode: boolean
  snatEnabled: boolean
  health: string[]
  lanInterface: string
  defaultSubnetRoutes: string[]
  lanWarning: string
  ipv4Forwarding: boolean | null
  ipv6Forwarding: boolean | null
}

export interface TailscaleConnection {
  enabled: boolean
  backendState: string
  canEnable: boolean
}

export async function tailscaleConnectionRequest(token: string, signal?: AbortSignal): Promise<TailscaleConnection> {
  const timeout = AbortSignal.timeout(20_000)
  const data = await graphQLRequest(token, 'TailscaleConnection', `query TailscaleConnection {
    tailscaleConnection { enabled backendState canEnable }
  }`, {}, signal ? AbortSignal.any([signal, timeout]) : timeout, 'tailnet connection')
  const value: unknown = data.tailscaleConnection
  if (!isRecord(value) || typeof value.enabled !== 'boolean' || typeof value.canEnable !== 'boolean' ||
    typeof value.backendState !== 'string' || !value.backendState) throw new Error('The server did not return valid tailnet connection settings.')
  return value as unknown as TailscaleConnection
}

export async function setTailscaleEnabledRequest(token: string, enabled: boolean): Promise<void> {
  if (typeof enabled !== 'boolean') throw new Error('Choose whether the tailnet connection is enabled.')
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 60_000)
  try {
    const data = await graphQLRequest(token, 'SetTailscaleEnabled', `mutation SetTailscaleEnabled($enabled: Boolean!) {
      setTailscaleEnabled(enabled: $enabled)
    }`, { enabled }, controller.signal, 'tailnet connection')
    if (data.setTailscaleEnabled !== true) throw new Error('The server did not confirm the connection change. Reload settings before retrying.')
  } finally { clearTimeout(timer) }
}

export async function logoutTailscaleRequest(token: string): Promise<void> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 60_000)
  try {
    const data = await graphQLRequest(token, 'LogoutTailscale', `mutation LogoutTailscale {
      logoutTailscale
    }`, {}, controller.signal, 'Tailscale logout')
    if (data.logoutTailscale !== true) throw new Error('The server did not confirm Tailscale logout. Reload connection settings before retrying.')
  } finally { clearTimeout(timer) }
}

export interface RoutingInput {
  subnetRoutes: string[]
}

export const DEFAULT_PEER_RELAY_PORT = 40001

export interface PeerRelayInput {
  enabled: boolean
  port: number
}

export async function setPeerRelayRequest(token: string, input: PeerRelayInput): Promise<void> {
  if (!isRecord(input) || typeof input.enabled !== 'boolean' || !Number.isInteger(input.port) || input.port < 1 || input.port > 65535) {
    throw new Error('Enter a UDP port from 1 to 65535')
  }
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 60_000)
  try {
    const data = await graphQLRequest(token, 'SetPeerRelay', `mutation SetPeerRelay($input: PeerRelayInput!) {
      setPeerRelay(input: $input)
    }`, { input }, controller.signal, 'peer relay')
    if (data.setPeerRelay !== true) throw new Error('The server did not confirm the peer relay change. Reload peer relay settings before retrying.')
  } finally { clearTimeout(timer) }
}

export async function tailscaleRoutingRequest(token: string, signal?: AbortSignal): Promise<TailscaleRouting> {
  const timeout = AbortSignal.timeout(20_000)
  const data = await graphQLRequest(token, 'TailscaleRouting', `query TailscaleRouting {
    tailscaleRouting {
      backendState advertiseExitNode subnetRoutes subnetDefaultsPending usingExitNode snatEnabled health
      peerRelayEnabled peerRelayPort
      lanInterface defaultSubnetRoutes lanWarning ipv4Forwarding ipv6Forwarding
      routeApprovalState routeApprovalMessage
    }
  }`, {}, signal ? AbortSignal.any([signal, timeout]) : timeout, 'routing')
  const value: unknown = data.tailscaleRouting
  const strings = (v: unknown): v is string[] => Array.isArray(v) && v.every(item => typeof item === 'string')
  const forwarding = (v: unknown) => v === null || typeof v === 'boolean'
  const relayPort = (v: unknown) => typeof v === 'number' && Number.isInteger(v) && v >= 0 && v <= 65535
  if (!isRecord(value) || typeof value.peerRelayEnabled !== 'boolean' ||
    (value.peerRelayEnabled ? !relayPort(value.peerRelayPort) : value.peerRelayPort !== null)) {
    throw new Error('The server did not return valid routing settings.')
  }
  if (!isRecord(value) || typeof value.backendState !== 'string' || !value.backendState ||
    !['DISABLED', 'PENDING', 'APPROVED', 'ERROR'].includes(value.routeApprovalState as string) || typeof value.routeApprovalMessage !== 'string' ||
    typeof value.subnetDefaultsPending !== 'boolean' || typeof value.advertiseExitNode !== 'boolean' || typeof value.usingExitNode !== 'boolean' ||
    typeof value.snatEnabled !== 'boolean' || !strings(value.subnetRoutes) || !strings(value.health) ||
    !strings(value.defaultSubnetRoutes) || typeof value.lanInterface !== 'string' || typeof value.lanWarning !== 'string' ||
    !forwarding(value.ipv4Forwarding) || !forwarding(value.ipv6Forwarding)) {
    throw new Error('The server did not return valid routing settings.')
  }
  return value as unknown as TailscaleRouting
}

export async function setRoutingRequest(token: string, input: RoutingInput): Promise<void> {
  if (!input || 'advertiseExitNode' in input) throw new Error('Exit-node advertising is always enabled; only subnet routes can be configured.')
  const normalized = { subnetRoutes: canonicalSubnetRoutes(input.subnetRoutes) }
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 60_000)
  try {
    const data = await graphQLRequest(token, 'SetRouting', `mutation SetRouting($input: RoutingInput!) {
      setRouting(input: $input)
    }`, { input: normalized }, controller.signal, 'routing')
    if (data.setRouting !== true) throw new Error('The server did not confirm the routing change. Reload settings before retrying.')
  } finally { clearTimeout(timer) }
}

export async function tailscaleClientRequest(token: string, signal?: AbortSignal): Promise<TailscaleClient | null> {
  const data = await graphQLRequest(token, 'TailscaleClient', `query TailscaleClient {
    tailscaleClient { clientId hasClientSecret updateTime }
  }`, {}, signal)
  const client = data.tailscaleClient
  if (client === null) return null
  if (!client || typeof client.clientId !== 'string' || typeof client.hasClientSecret !== 'boolean' ||
    typeof client.updateTime !== 'string') throw new Error('The server did not return valid credential settings.')
  return { clientId: client.clientId, hasClientSecret: client.hasClientSecret, updateTime: client.updateTime }
}

export async function setTailscaleCredentialRequest(token: string, credential: TailscaleCredential): Promise<void> {
  const clientId = credential.clientId.trim()
  const clientSecret = credential.clientSecret?.trim() || undefined
  if (!clientId || clientId.length > 512 || (clientSecret?.length ?? 0) > 4096 ||
    /[\s\p{Cc}]/u.test(clientId + (clientSecret ?? ''))) {
    throw new Error('Enter a valid client ID and secret without spaces.')
  }
  const data = await graphQLRequest(token, 'SetTailscaleCredential', `mutation SetTailscaleCredential($credential: TailscaleCredential!) {
    setTailscaleCredential(credential: $credential)
  }`, { credential: { clientId, clientSecret } })
  if (data.setTailscaleCredential !== true) throw new Error('The server did not confirm that credentials were saved.')
}

export async function clearTailscaleCredentialRequest(token: string): Promise<void> {
  const data = await graphQLRequest(token, 'ClearTailscaleCredential', `mutation ClearTailscaleCredential {
    clearTailscaleCredential
  }`)
  if (data.clearTailscaleCredential !== true) throw new Error('The server did not confirm that credentials were removed.')
}

export type KeyRenewalState = 'IDLE' | 'READY' | 'CANCELLED' | 'STARTING' | 'AWAITING_LOGIN' | 'AWAITING_APPROVAL' | 'COMPLETE' | 'SIGNED_IN'

export interface TailscaleKeyRenewal {
  state: KeyRenewalState
  authURL: string
  canRenew: boolean
  attemptID: string
}

export function isKeyRenewalPending(value: TailscaleKeyRenewal): boolean {
  return ['READY', 'STARTING', 'AWAITING_LOGIN', 'AWAITING_APPROVAL'].includes(value.state)
}

function parseKeyRenewal(value: unknown): TailscaleKeyRenewal {
  const invalid = () => new Error('The server did not return valid node key renewal status. Check status before retrying.')
  if (!value || typeof value !== 'object') throw invalid()
  const result = value as TailscaleKeyRenewal
  if (!['IDLE', 'READY', 'CANCELLED', 'STARTING', 'AWAITING_LOGIN', 'AWAITING_APPROVAL', 'COMPLETE', 'SIGNED_IN'].includes(result.state) ||
    typeof result.authURL !== 'string' || typeof result.canRenew !== 'boolean' || typeof result.attemptID !== 'string' ||
    (result.attemptID !== '' && !/^[A-Za-z0-9_-]{1,128}$/.test(result.attemptID)) ||
    (!['IDLE', 'AWAITING_APPROVAL'].includes(result.state) && !result.attemptID) ||
    (result.state === 'READY' && result.canRenew)) throw invalid()
  if (result.state === 'AWAITING_LOGIN') {
    let url: URL
    try { url = new URL(result.authURL) } catch { throw invalid() }
    if (url.protocol !== 'https:' || url.host !== 'login.tailscale.com' || url.username || url.password ||
      url.hash || !url.pathname.startsWith('/a/') || url.pathname.length <= 3 || /[\\\s\p{Cc}]/u.test(result.authURL) ||
      result.canRenew) throw invalid()
  } else if (result.authURL !== '' || (result.state === 'AWAITING_APPROVAL' && result.canRenew)) throw invalid()
  return { state: result.state, authURL: result.authURL, canRenew: result.canRenew, attemptID: result.attemptID }
}

// Sign-in URLs are sensitive: keep them only in memory, never in browser storage.
export async function tailscaleKeyRenewalRequest(token: string, signal?: AbortSignal): Promise<TailscaleKeyRenewal> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 60_000)
  try {
    const data = await graphQLRequest(token, 'TailscaleKeyRenewal', `query TailscaleKeyRenewal {
      tailscaleKeyRenewal { state authURL canRenew attemptID }
    }`, {}, signal ? AbortSignal.any([signal, controller.signal]) : controller.signal, 'node key renewal')
    return parseKeyRenewal(data.tailscaleKeyRenewal)
  } finally { clearTimeout(timer) }
}

export async function renewTailscaleNodeKeyRequest(token: string, signal?: AbortSignal): Promise<TailscaleKeyRenewal> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 60_000)
  try {
    const data = await graphQLRequest(token, 'RenewTailscaleNodeKey', `mutation RenewTailscaleNodeKey {
      renewTailscaleNodeKey { state authURL canRenew attemptID }
    }`, {}, signal ? AbortSignal.any([signal, controller.signal]) : controller.signal, 'node key renewal')
    const value = parseKeyRenewal(data.renewTailscaleNodeKey)
    if (!isKeyRenewalPending(value)) throw new Error('The server did not confirm the renewal request. Check status before retrying.')
    return value
  } finally { clearTimeout(timer) }
}

export async function beginTailscaleNodeKeyRenewalRequest(token: string, attemptID: string, signal?: AbortSignal): Promise<TailscaleKeyRenewal> {
  if (typeof attemptID !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(attemptID)) throw new Error('Check renewal status before starting sign-in.')
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 60_000)
  try {
    const data = await graphQLRequest(token, 'BeginTailscaleNodeKeyRenewal', `mutation BeginTailscaleNodeKeyRenewal($attemptID: String!) {
      beginTailscaleNodeKeyRenewal(attemptID: $attemptID) { state authURL canRenew attemptID }
    }`, { attemptID }, signal ? AbortSignal.any([signal, controller.signal]) : controller.signal, 'node key renewal')
    const value = parseKeyRenewal(data.beginTailscaleNodeKeyRenewal)
    if (value.attemptID !== attemptID || !['STARTING', 'AWAITING_LOGIN', 'AWAITING_APPROVAL', 'COMPLETE', 'SIGNED_IN', 'IDLE'].includes(value.state)) {
      throw new Error('The server did not confirm that sign-in started. Check status before retrying.')
    }
    return value
  } finally { clearTimeout(timer) }
}

export async function cancelTailscaleNodeKeyRenewalRequest(token: string, attemptID: string, signal?: AbortSignal): Promise<TailscaleKeyRenewal> {
  if (typeof attemptID !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(attemptID)) throw new Error('Check renewal status before cancelling.')
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 60_000)
  try {
    const data = await graphQLRequest(token, 'CancelTailscaleNodeKeyRenewal', `mutation CancelTailscaleNodeKeyRenewal($attemptID: String!) {
      cancelTailscaleNodeKeyRenewal(attemptID: $attemptID) { state authURL canRenew attemptID }
    }`, { attemptID }, signal ? AbortSignal.any([signal, controller.signal]) : controller.signal, 'renewal cancellation')
    const value = parseKeyRenewal(data.cancelTailscaleNodeKeyRenewal)
    if (value.state !== 'CANCELLED' || value.attemptID !== attemptID) throw new Error('The server did not confirm cancellation. Check status before closing.')
    return value
  } finally { clearTimeout(timer) }
}

export function shouldPromptForTailscale(status: TailscaleStatus | null, statusError: string): boolean {
  // Offline, stopped, starting, or unreachable daemons do not prove credentials are missing.
  return !statusError && status?.backendState === 'NeedsLogin'
}

export function isTailscaleConnected(status: TailscaleStatus | null, statusError = ''): boolean {
  return !statusError && status?.backendState === 'Running' && status.self?.online === true
}

export function tailscaleStatusLabel(status: TailscaleStatus | null, statusError: string): string {
  if (statusError) return 'Unavailable'
  if (!status) return 'Checking…'
  if (isTailscaleConnected(status)) return 'Connected'
  switch (status.backendState) {
    case 'NeedsLogin': return 'Needs setup'
    case 'Stopped': return 'Stopped'
    case 'Starting': return 'Starting'
    case 'NeedsMachineAuth': return 'Awaiting approval'
    default: return 'Not connected'
  }
}
