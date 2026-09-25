import type { NetworkActivityHistory } from '../api'
import { formatTrafficBytes } from '../traffic'
import { historyHourLabel, trafficTotalsView } from '../trafficHistory'

export default function NetworkActivityTotals({ history, error, retry }: {
  history: NetworkActivityHistory | null; error: string; retry: () => void
}) {
  if (error) return <div className="activity-message activity-message--error saved-traffic-error" role="alert"><p>{error}</p>
    <button className="secondary-button" type="button" onClick={retry}>Retry saved traffic</button></div>
  if (!history) return <p className="activity-message" role="status">Loading saved traffic totals…</p>
  const totals = history.totals, view = trafficTotalsView(history)
  return <section className="saved-traffic" aria-label="Saved VPN traffic totals">
    <div className="saved-traffic__figures">
      <div className="saved-traffic__24h"><span>Last 24 hours</span>
        <strong>{view.last24Bytes === null ? 'No measurements' : formatTrafficBytes(view.last24Bytes)}</strong>
        {view.last24Bytes !== null && <small>↓ {formatTrafficBytes(Number(totals.rxBytes24h))} · ↑ {formatTrafficBytes(Number(totals.txBytes24h))}</small>}
      </div>
      <div className="saved-traffic__total"><span>Total traffic</span>
        <strong>{view.totalBytes === null ? 'No measurements yet' : formatTrafficBytes(view.totalBytes)}</strong>
        {view.totalBytes !== null && <small>↓ {formatTrafficBytes(Number(totals.totalRxBytes))} · ↑ {formatTrafficBytes(Number(totals.totalTxBytes))}</small>}
      </div>
    </div>
    <p className="traffic-caption">Saved hourly · as of {historyHourLabel(Date.parse(history.windowEnd))}. Last 24 hours covers completed hours only.</p>
    <p className="traffic-caption">{totals.recordedSince ? `Total recorded since ${historyHourLabel(Date.parse(totals.recordedSince))}.` : 'Totals begin with the first hourly save of valid samples.'} Gaps are not estimated.</p>
  </section>
}
