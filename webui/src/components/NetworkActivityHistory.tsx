import { useI18n, T } from '../i18n'
import { ArrowDownToLine, ArrowUpFromLine } from 'lucide-react'
import { formatTrafficBytes } from '../traffic'
import { historyHourLabel, trafficHistoryView } from '../trafficHistory'
import type { NetworkActivityHistory as History } from '../api'

export default function NetworkActivityHistory({ history }: { history: History }) {
  const { t, locale } = useI18n()
  const view = trafficHistoryView(history)
  const peak = Math.max(1, ...view.hours.map((hour) => (hour.download ?? 0) + (hour.upload ?? 0)))
  return <div className="activity-history">
    {view.observedSeconds === 0 && <p className="activity-message" role="status">{t("History appears after an hourly save with valid traffic samples. Recording continues when this page is closed.")}</p>}
    <div className="activity-chart">
      <div className="activity-chart__scale"><span>{view.observedSeconds > 0 ? t("{0} / hour", { 0: formatTrafficBytes(peak, locale) }) : t("No measurements")}</span><span>{t("Download / upload")}</span></div>
      <svg viewBox="0 0 360 100" role="img" aria-label={t("Recorded VPN download and upload bytes for the last 24 completed hours; dashed marks are missing measurements")}>
        <path d="M0 96H360 M0 52H360 M0 8H360" className="activity-chart__grid" />
        {view.hours.map((hour, index) => {
          const down = 88 * (hour.download ?? 0) / peak, up = 88 * (hour.upload ?? 0) / peak
          return <g key={hour.at} className={`history-hour history-hour--${hour.coverage.toLowerCase()}`}>
            <title>{historyHourLabel(hour.at, locale)}: {hour.coverage === 'Missing' ? t("No measurements") :
              t("download {0}, upload {1}; {2} coverage", { 0: formatTrafficBytes(hour.download!, locale), 1: formatTrafficBytes(hour.upload!, locale), 2: t(hour.coverage.toLowerCase()) })}</title>
            {hour.coverage === 'Missing' ? <path d={`M${index * 15 + 2} 94h11`} className="history-hour__missing" /> : <>
              <rect x={index * 15 + 2} y={96 - down} width="11" height={down} className="activity-chart__download-dot" />
              <rect x={index * 15 + 2} y={96 - down - up} width="11" height={up} className="activity-chart__upload-dot" />
              {down + up === 0 && <circle cx={index * 15 + 7.5} cy="96" r="2" className="activity-chart__download-dot" />}
            </>}
          </g>
        })}
      </svg>
      <div className="activity-chart__scale"><span>{t("24 hours earlier")}</span><span>{t("Latest completed hour")}</span></div>
      <p className="traffic-caption"><T message="{0}/24 complete hours. Faded bars are partial; dashed marks are missing. Missing traffic is not counted as zero." values={{ 0: view.completeHours }} /></p>
    </div>
    <div className="traffic-breakdown">
      <span><T message="{0} Download {1}" values={{ 0: <ArrowDownToLine size={18} />, 1: <strong>{formatTrafficBytes(Number(history.totals.rxBytes24h), locale)}</strong> }} /></span>
      <span><T message="{0} Upload {1}" values={{ 0: <ArrowUpFromLine size={18} />, 1: <strong>{formatTrafficBytes(Number(history.totals.txBytes24h), locale)}</strong> }} /></span>
    </div>
    <details className="history-details"><summary>{t("Hourly details")}</summary>
      <div className="history-table-scroll" tabIndex={0} role="region" aria-label={t("Hourly VPN traffic details")}>
        <table><caption>{t("Recorded bytes per completed hour (local time)")}</caption>
          <thead><tr><th scope="col">{t("Hour starting")}</th><th scope="col">{t("Download")}</th><th scope="col">{t("Upload")}</th><th scope="col">{t("Coverage")}</th></tr></thead>
          <tbody>{view.hours.map((hour) => <tr key={hour.at}>
            <th scope="row">{historyHourLabel(hour.at, locale)}</th>
            <td>{hour.download === null ? '—' : formatTrafficBytes(hour.download, locale)}</td>
            <td>{hour.upload === null ? '—' : formatTrafficBytes(hour.upload, locale)}</td>
            <td>{t(hour.coverage)}{hour.coverage === 'Partial' ? t(" ({0} min)", { 0: (hour.observedSeconds / 60).toLocaleString(locale, { maximumFractionDigits: 1 }) }) : ''}</td>
          </tr>)}</tbody>
        </table>
      </div>
    </details>
  </div>
}
