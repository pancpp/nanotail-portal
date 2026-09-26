import type { NetworkActivity } from './api'

export const TRAFFIC_POLL_MS = 2_000
export const TRAFFIC_WINDOW_MS = 60_000

export interface TrafficRate {
  at: number
  download: number
  upload: number
}

export interface TrafficWindow {
  latest: NetworkActivity | null
  rate: TrafficRate | null
  history: TrafficRate[]
}

export function emptyTrafficWindow(): TrafficWindow {
  return { latest: null, rate: null, history: [] }
}

export function appendTrafficSample(state: TrafficWindow, sample: NetworkActivity): TrafficWindow {
  const previous = state.latest
  const baseline = { latest: sample, rate: null, history: [] }
  if (!previous || sample.counterEpoch !== previous.counterEpoch || sample.interfaceName !== previous.interfaceName) return baseline
  const at = Date.parse(sample.sampledAt)
  const elapsed = at - Date.parse(previous.sampledAt)
  const received = BigInt(sample.rxBytes) - BigInt(previous.rxBytes)
  const sent = BigInt(sample.txBytes) - BigInt(previous.txBytes)
  // Never invent a spike after a restart, clock change, or suspended browser.
  if (elapsed <= 0 || elapsed > 10_000 || received < 0n || sent < 0n) return baseline
  const rate = { at, download: Number(received) * 1000 / elapsed, upload: Number(sent) * 1000 / elapsed }
  return {
    latest: sample, rate,
    history: [...state.history.filter((point) => point.at > at - TRAFFIC_WINDOW_MS), rate].slice(-30),
  }
}

export function formatTrafficBytes(bytes: number, locale?: string): string {
  if (!Number.isFinite(bytes) || bytes < 0) return 'Unavailable'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB']
  let value = bytes, unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${value.toLocaleString(locale, { maximumFractionDigits: unit === 0 ? 0 : 1 })} ${units[unit]}`
}

export function formatTrafficRate(bytesPerSecond: number, locale?: string): string {
  return `${formatTrafficBytes(bytesPerSecond, locale)}/s`
}
