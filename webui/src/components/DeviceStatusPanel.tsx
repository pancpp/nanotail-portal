import { Activity, Clock3, Cpu, HeartPulse, MemoryStick, Zap } from 'lucide-react'
import { deviceIPTypeLabel, formatDeviceUptime } from '../api'
import { useDevice } from '../device'

export default function DeviceStatusPanel() {
  const { status, error, refreshing, refresh } = useDevice()

  return (
    <section className="device-panel" id="device" aria-labelledby="device-heading">
      <div className="device-panel__header">
        <div className="device-panel__intro">
          <span className="device-panel__icon"><Zap size={22} /></span>
          <div>
            <span className="panel__eyebrow">THIS DEVICE</span>
            <h2 id="device-heading">{status?.hostname || 'Device status'}</h2>
            <p>Edge gateway · nanotail portal</p>
          </div>
        </div>
        <span className="quiet-label" role="status">
          {refreshing ? 'Updating…' : error ? 'Unavailable' : status ? 'Live status' : 'Loading…'}
        </span>
      </div>

      {error ? (
        <div className="device-panel__error" role="alert">
          <strong>Device status unavailable</strong>
          <p>{error}</p>
          <button className="secondary-button" type="button" disabled={refreshing} onClick={() => { void refresh() }}>
            Retry device status
          </button>
        </div>
      ) : !status ? <p role="status">Loading device status…</p> : (
        <>
          <dl className="device-network">
            <div>
              <dt>LAN IPv4 · eth0</dt>
              <dd>{status.lanIP ? <code>{status.lanIP}</code> : 'No IPv4 address assigned'}</dd>
            </div>
            <div>
              <dt>IPv4 gateway · eth0</dt>
              <dd>{status.gateway ? <code>{status.gateway}</code> : 'No IPv4 gateway configured'}</dd>
            </div>
            <div>
              <dt>IPv4 configuration</dt>
              <dd>{deviceIPTypeLabel(status.lanIPType)}</dd>
            </div>
            <div>
              <dt>LAN IPv6 · eth0</dt>
              <dd>{status.lanIPv6 ? <code>{status.lanIPv6}</code> : 'No IPv6 address assigned'}</dd>
            </div>
            <div>
              <dt>IPv6 gateway · eth0</dt>
              <dd>{status.gateway6 ? <code>{status.gateway6}</code> : 'No IPv6 gateway configured'}</dd>
            </div>
            <div>
              <dt>IPv6 configuration</dt>
              <dd>{deviceIPTypeLabel(status.lanIPv6Type, true)}</dd>
            </div>
            <div className="device-network__dns">
              <dt>DNS servers · eth0</dt>
              <dd>{status.dns.length ? status.dns.map((ip) => <code key={ip}>{ip}</code>) : 'No DNS servers configured'}</dd>
            </div>
            <div>
              <dt>MAC address · eth0</dt>
              <dd><code>{status.ethAddr || 'Not available'}</code></dd>
            </div>
          </dl>

          <dl className="device-metrics">
            <div><dt><Activity size={17} />Uptime</dt><dd title={`${status.uptime} seconds`}>{formatDeviceUptime(status.uptime)}</dd></div>
            <div><dt><Cpu size={17} />CPU load</dt><dd>{status.cpuload}%</dd></div>
            <div><dt><MemoryStick size={17} />Memory usage</dt><dd>{status.memory}%</dd></div>
            <div className="device-metrics__restart">
              <dt><Clock3 size={17} />Last restart · local time</dt>
              <dd><time dateTime={status.lastRestart}>{new Date(status.lastRestart).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}</time></dd>
            </div>
            <div><dt><HeartPulse size={17} />Health</dt><dd>{status.health === 'healthy' ? 'Healthy' : status.health || 'Not available'}</dd></div>
          </dl>
        </>
      )}
    </section>
  )
}
