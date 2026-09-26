import { useI18n, T } from '../i18n'
import { ArrowDownToLine, ArrowUpFromLine } from 'lucide-react'
import { useState } from 'react'
import { formatTrafficRate, TRAFFIC_WINDOW_MS } from '../traffic'
import { useNetworkActivity } from '../useNetworkActivity'
import NetworkActivityHistory from './NetworkActivityHistory'
import NetworkActivityTotals from './NetworkActivityTotals'
import { useNetworkActivityHistory } from '../useNetworkActivityHistory'

export default function NetworkActivityPanel() {
  const { t, locale } = useI18n()
  const [view, setView] = useState<'live' | 'history'>('live')
  const { latest, rate, history, error, paused, retry } = useNetworkActivity()
  const saved = useNetworkActivityHistory()
  const peak = Math.max(1, ...history.flatMap((point) => [point.download, point.upload]))
  const end = rate?.at ?? 0
  const x = (at: number) => 4 + 352 * (1 - (end - at) / TRAFFIC_WINDOW_MS)
  const y = (bytes: number) => 96 - 88 * bytes / peak
  const points = (direction: 'download' | 'upload') => history.map((point) => `${x(point.at)},${y(point[direction])}`).join(' ')

  return <article className="panel activity-panel" aria-labelledby="network-activity-heading">
    <div className="panel__header">
      <div><span className="panel__eyebrow"><T message="TAILSCALE VPN · {0}" values={{ 0: view === 'live' ? t("LAST MINUTE") : t("24-HOUR HISTORY") }} /></span><h2 id="network-activity-heading">{t("Network activity")}</h2></div>
      <span className={`activity-live${!rate || error || paused ? ' activity-live--muted' : ''}`}>
        {view === 'history' ? t("Saved hourly") : paused ? t("Paused") : error ? t("Unavailable") : rate ? t("Live · 2s") : latest ? t("Measuring…") : t("Loading…")}
      </span>
    </div>
    <NetworkActivityTotals {...saved} />
    <div className="activity-views" role="group" aria-label={t("Network activity period")}>
      <button type="button" aria-pressed={view === 'live'} onClick={() => setView('live')}>{t("Live")}</button>
      <button type="button" aria-pressed={view === 'history'} onClick={() => setView('history')}>{t("Last 24 hours")}</button>
    </div>
    {view === 'history' ? saved.history && <NetworkActivityHistory history={saved.history} /> : paused ? <p className="activity-message" role="status">{t("Updates are paused while this page is hidden.")}</p> :
      error ? <div className="activity-message activity-message--error" role="alert"><p>{t(error)}</p>
        <button className="secondary-button" type="button" onClick={retry}>{t("Retry network activity")}</button></div> :
      !latest ? <p className="activity-message" role="status">{t("Loading VPN traffic…")}</p> : <>
        {rate ? <div className="activity-chart">
          <div className="activity-chart__scale"><span>{formatTrafficRate(peak, locale)}</span><span>{t("Download / upload")}</span></div>
          <svg viewBox="0 0 360 100" role="img" aria-label={t("VPN download and upload rates over the last 60 seconds")}>
            <path d="M4 96H356 M4 52H356 M4 8H356" className="activity-chart__grid" />
            <polyline points={points('download')} className="activity-chart__download" />
            <polyline points={points('upload')} className="activity-chart__upload" />
            {history.map((point) => <g key={point.at}>
              <title><T message="{0}: download {1}, upload {2}" values={{ 0: new Date(point.at).toLocaleTimeString(locale), 1: formatTrafficRate(point.download, locale), 2: formatTrafficRate(point.upload, locale) }} /></title>
              <circle cx={x(point.at)} cy={y(point.download)} r="2" className="activity-chart__download-dot" />
              <circle cx={x(point.at)} cy={y(point.upload)} r="2" className="activity-chart__upload-dot" />
            </g>)}
          </svg>
          <div className="activity-chart__scale"><span>{t("60 seconds ago")}</span><span>{t("Now")}</span></div>
        </div> : <p className="activity-message" role="status">{t("Collecting samples to measure speed…")}</p>}
        <div className="traffic-breakdown">
          <span><T message="{0} Download {1}" values={{ 0: <ArrowDownToLine size={18} />, 1: <strong>{rate ? formatTrafficRate(rate.download, locale) : t("Measuring…")}</strong> }} /></span>
          <span><T message="{0} Upload {1}" values={{ 0: <ArrowUpFromLine size={18} />, 1: <strong>{rate ? formatTrafficRate(rate.upload, locale) : t("Measuring…")}</strong> }} /></span>
        </div>
      </>}
  </article>
}
