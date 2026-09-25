import type { NetworkActivityHistory } from './api'

export function trafficHistoryView(history: NetworkActivityHistory) {
  const byHour = new Map(history.hours.map((hour) => [Date.parse(hour.startedAt), hour]))
  let received = 0n, sent = 0n, observedSeconds = 0
  const hours = Array.from({ length: 24 }, (_, index) => {
    const at = Date.parse(history.windowStart) + index * 3_600_000
    const hour = byHour.get(at)
    const measured = !!hour && hour.observedSeconds > 0
    if (measured) {
      received += BigInt(hour.rxBytes); sent += BigInt(hour.txBytes)
      observedSeconds += hour.observedSeconds
    }
    return { at, download: measured ? Number(hour.rxBytes) : null, upload: measured ? Number(hour.txBytes) : null,
      observedSeconds: hour?.observedSeconds ?? 0, coverage: !measured ? 'Missing' : hour.observedSeconds < 3600 ? 'Partial' : 'Complete' }
  })
  return { hours, received, sent, observedSeconds, completeHours: hours.filter((hour) => hour.coverage === 'Complete').length }
}

export function historyHourLabel(at: number) {
  return new Date(at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', timeZoneName: 'short' })
}

// Both figures come from the same hourly database snapshot, not live interface
// counters and not a browser-maintained running total.
export function trafficTotalsView(history: NetworkActivityHistory) {
  const totals = history.totals
  return {
    last24Bytes: totals.observedSeconds24h > 0 ? Number(BigInt(totals.rxBytes24h) + BigInt(totals.txBytes24h)) : null,
    totalBytes: totals.totalObservedSeconds > 0 ? Number(BigInt(totals.totalRxBytes) + BigInt(totals.totalTxBytes)) : null,
  }
}
