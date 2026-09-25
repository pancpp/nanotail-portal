import { ArrowDownToLine, ArrowUpFromLine } from 'lucide-react'
import { formatTrafficBytes } from '../traffic'
import { historyHourLabel, trafficHistoryView } from '../trafficHistory'
import type { NetworkActivityHistory as History } from '../api'

export default function NetworkActivityHistory({ history }: { history: History }) {
  const view = trafficHistoryView(history)
  const peak = Math.max(1, ...view.hours.map((hour) => (hour.download ?? 0) + (hour.upload ?? 0)))
  return <div className="activity-history">
    {view.observedSeconds === 0 && <p className="activity-message" role="status">History appears after an hourly save with valid traffic samples. Recording continues when this page is closed.</p>}
    <div className="activity-chart">
      <div className="activity-chart__scale"><span>{view.observedSeconds > 0 ? `${formatTrafficBytes(peak)} / hour` : 'No measurements'}</span><span>Download / upload</span></div>
      <svg viewBox="0 0 360 100" role="img" aria-label="Recorded VPN download and upload bytes for the last 24 completed hours; dashed marks are missing measurements">
        <path d="M0 96H360 M0 52H360 M0 8H360" className="activity-chart__grid" />
        {view.hours.map((hour, index) => {
          const down = 88 * (hour.download ?? 0) / peak, up = 88 * (hour.upload ?? 0) / peak
          return <g key={hour.at} className={`history-hour history-hour--${hour.coverage.toLowerCase()}`}>
            <title>{historyHourLabel(hour.at)}: {hour.coverage === 'Missing' ? 'No measurements' :
              `download ${formatTrafficBytes(hour.download!)}, upload ${formatTrafficBytes(hour.upload!)}; ${hour.coverage.toLowerCase()} coverage`}</title>
            {hour.coverage === 'Missing' ? <path d={`M${index * 15 + 2} 94h11`} className="history-hour__missing" /> : <>
              <rect x={index * 15 + 2} y={96 - down} width="11" height={down} className="activity-chart__download-dot" />
              <rect x={index * 15 + 2} y={96 - down - up} width="11" height={up} className="activity-chart__upload-dot" />
              {down + up === 0 && <circle cx={index * 15 + 7.5} cy="96" r="2" className="activity-chart__download-dot" />}
            </>}
          </g>
        })}
      </svg>
      <div className="activity-chart__scale"><span>24 hours earlier</span><span>Latest completed hour</span></div>
      <p className="traffic-caption">{view.completeHours}/24 complete hours. Faded bars are partial; dashed marks are missing. Missing traffic is not counted as zero.</p>
    </div>
    <div className="traffic-breakdown">
      <span><ArrowDownToLine size={18} /> Download <strong>{formatTrafficBytes(Number(history.totals.rxBytes24h))}</strong></span>
      <span><ArrowUpFromLine size={18} /> Upload <strong>{formatTrafficBytes(Number(history.totals.txBytes24h))}</strong></span>
    </div>
    <details className="history-details"><summary>Hourly details</summary>
      <div className="history-table-scroll" tabIndex={0} role="region" aria-label="Hourly VPN traffic details">
        <table><caption>Recorded bytes per completed hour (local time)</caption>
          <thead><tr><th scope="col">Hour starting</th><th scope="col">Download</th><th scope="col">Upload</th><th scope="col">Coverage</th></tr></thead>
          <tbody>{view.hours.map((hour) => <tr key={hour.at}>
            <th scope="row">{historyHourLabel(hour.at)}</th>
            <td>{hour.download === null ? '—' : formatTrafficBytes(hour.download)}</td>
            <td>{hour.upload === null ? '—' : formatTrafficBytes(hour.upload)}</td>
            <td>{hour.coverage}{hour.coverage === 'Partial' ? ` (${(hour.observedSeconds / 60).toLocaleString(undefined, { maximumFractionDigits: 1 })} min)` : ''}</td>
          </tr>)}</tbody>
        </table>
      </div>
    </details>
  </div>
}
