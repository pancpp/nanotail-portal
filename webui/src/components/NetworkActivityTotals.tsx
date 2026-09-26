import { useI18n, T } from '../i18n'
import type { NetworkActivityHistory } from '../api'
import { formatTrafficBytes } from '../traffic'
import { historyHourLabel, trafficTotalsView } from '../trafficHistory'

export default function NetworkActivityTotals({ history, error, retry }: {
  history: NetworkActivityHistory | null; error: string; retry: () => void
}) {
  const { t, locale } = useI18n()
  if (error) return <div className="activity-message activity-message--error saved-traffic-error" role="alert"><p>{t(error)}</p>
    <button className="secondary-button" type="button" onClick={retry}>{t("Retry saved traffic")}</button></div>
  if (!history) return <p className="activity-message" role="status">{t("Loading saved traffic totals…")}</p>
  const totals = history.totals, view = trafficTotalsView(history)
  return <section className="saved-traffic" aria-label={t("Saved VPN traffic totals")}>
    <div className="saved-traffic__figures">
      <div className="saved-traffic__24h"><span>{t("Last 24 hours")}</span>
        <strong>{formatTrafficBytes(view.last24Bytes, locale)}</strong>
        <small>↓ {formatTrafficBytes(Number(totals.rxBytes24h), locale)} · ↑ {formatTrafficBytes(Number(totals.txBytes24h), locale)}</small>
      </div>
      <div className="saved-traffic__total"><span>{t("Total traffic")}</span>
        <strong>{formatTrafficBytes(view.totalBytes, locale)}</strong>
        <small>↓ {formatTrafficBytes(Number(totals.totalRxBytes), locale)} · ↑ {formatTrafficBytes(Number(totals.totalTxBytes), locale)}</small>
      </div>
    </div>
    <p className="traffic-caption"><T message="Saved hourly · as of {0}. Last 24 hours covers completed hours only." values={{ 0: historyHourLabel(Date.parse(history.windowEnd), locale) }} /></p>
    <p className="traffic-caption"><T message="{0} Gaps are not estimated." values={{ 0: totals.recordedSince ? t("Total recorded since {0}.", { 0: historyHourLabel(Date.parse(totals.recordedSince), locale) }) : t("Totals begin with the first hourly save of valid samples.") }} /></p>
  </section>
}
