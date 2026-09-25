import { ArrowDownToLine, ArrowUpFromLine } from 'lucide-react'
import { useState } from 'react'
import { formatTrafficRate, TRAFFIC_WINDOW_MS } from '../traffic'
import { useNetworkActivity } from '../useNetworkActivity'
import NetworkActivityHistory from './NetworkActivityHistory'
import NetworkActivityTotals from './NetworkActivityTotals'
import { useNetworkActivityHistory } from '../useNetworkActivityHistory'

export default function NetworkActivityPanel() {
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
      <div><span className="panel__eyebrow">TAILSCALE VPN · {view === 'live' ? 'LAST MINUTE' : '24-HOUR HISTORY'}</span><h2 id="network-activity-heading">Network activity</h2></div>
      <span className={`activity-live${!rate || error || paused ? ' activity-live--muted' : ''}`}>
        {view === 'history' ? 'Saved hourly' : paused ? 'Paused' : error ? 'Unavailable' : rate ? 'Live · 2s' : latest ? 'Measuring…' : 'Loading…'}
      </span>
    </div>
    <NetworkActivityTotals {...saved} />
    <div className="activity-views" role="group" aria-label="Network activity period">
      <button type="button" aria-pressed={view === 'live'} onClick={() => setView('live')}>Live</button>
      <button type="button" aria-pressed={view === 'history'} onClick={() => setView('history')}>Last 24 hours</button>
    </div>
    {view === 'history' ? saved.history && <NetworkActivityHistory history={saved.history} /> : paused ? <p className="activity-message" role="status">Updates are paused while this page is hidden.</p> :
      error ? <div className="activity-message activity-message--error" role="alert"><p>{error}</p>
        <button className="secondary-button" type="button" onClick={retry}>Retry network activity</button></div> :
      !latest ? <p className="activity-message" role="status">Loading VPN traffic…</p> : <>
        {rate ? <div className="activity-chart">
          <div className="activity-chart__scale"><span>{formatTrafficRate(peak)}</span><span>Download / upload</span></div>
          <svg viewBox="0 0 360 100" role="img" aria-label="VPN download and upload rates over the last 60 seconds">
            <path d="M4 96H356 M4 52H356 M4 8H356" className="activity-chart__grid" />
            <polyline points={points('download')} className="activity-chart__download" />
            <polyline points={points('upload')} className="activity-chart__upload" />
            {history.map((point) => <g key={point.at}>
              <title>{new Date(point.at).toLocaleTimeString()}: download {formatTrafficRate(point.download)}, upload {formatTrafficRate(point.upload)}</title>
              <circle cx={x(point.at)} cy={y(point.download)} r="2" className="activity-chart__download-dot" />
              <circle cx={x(point.at)} cy={y(point.upload)} r="2" className="activity-chart__upload-dot" />
            </g>)}
          </svg>
          <div className="activity-chart__scale"><span>60 seconds ago</span><span>Now</span></div>
        </div> : <p className="activity-message" role="status">Collecting samples to measure speed…</p>}
        <div className="traffic-breakdown">
          <span><ArrowDownToLine size={18} /> Download <strong>{rate ? formatTrafficRate(rate.download) : 'Measuring…'}</strong>
          </span>
          <span><ArrowUpFromLine size={18} /> Upload <strong>{rate ? formatTrafficRate(rate.upload) : 'Measuring…'}</strong>
          </span>
        </div>
      </>}
  </article>
}
