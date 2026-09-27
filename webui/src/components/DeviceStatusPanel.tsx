import Button from './Button'
import { useI18n, T } from '../i18n'
import { Activity, Clock3, Cpu, HeartPulse, MemoryStick, Zap } from 'lucide-react'
import { deviceIPTypeLabel, formatDeviceUptime } from '../api'
import { useDevice } from '../device'

export default function DeviceStatusPanel() {
  const { t, locale } = useI18n()
  const { status, error, refreshing, refresh } = useDevice()

  return (
    <section className="device-panel" id="device" aria-labelledby="device-heading">
      <div className="device-panel__header">
        <div className="device-panel__intro">
          <span className="device-panel__icon"><Zap size={22} /></span>
          <div>
            <span className="panel__eyebrow">{t("THIS DEVICE")}</span>
            <h2 id="device-heading">{status?.hostname || t("Device status")}</h2>
            <p>{t("Edge gateway · nanotail portal")}</p>
          </div>
        </div>
        <span className="quiet-label" role="status">
          {refreshing ? t("Updating…") : error ? t("Unavailable") : status ? t("Live status") : t("Loading…")}
        </span>
      </div>

      {error ? (
        <div className="device-panel__error" role="alert">
          <strong>{t("Device status unavailable")}</strong>
          <p>{t(error)}</p>
          <Button className="secondary-button" type="button" disabledReason={refreshing ? t("Refreshing device status…") : ""} onClick={() => { void refresh() }}>{t("Retry device status")}</Button>
        </div>
      ) : !status ? <p role="status">{t("Loading device status…")}</p> : (
        <>
          <dl className="device-network">
            <div>
              <dt>{t("LAN IPv4 · eth0")}</dt>
              <dd>{status.lanIP ? <code>{status.lanIP}</code> : t("No IPv4 address assigned")}</dd>
            </div>
            <div>
              <dt>{t("IPv4 gateway · eth0")}</dt>
              <dd>{status.gateway ? <code>{status.gateway}</code> : t("No IPv4 gateway configured")}</dd>
            </div>
            <div>
              <dt>{t("IPv4 configuration")}</dt>
              <dd>{t(deviceIPTypeLabel(status.lanIPType))}</dd>
            </div>
            <div>
              <dt>{t("LAN IPv6 · eth0")}</dt>
              <dd>{status.lanIPv6 ? <code>{status.lanIPv6}</code> : t("No IPv6 address assigned")}</dd>
            </div>
            <div>
              <dt>{t("IPv6 gateway · eth0")}</dt>
              <dd>{status.gateway6 ? <code>{status.gateway6}</code> : t("No IPv6 gateway configured")}</dd>
            </div>
            <div>
              <dt>{t("IPv6 configuration")}</dt>
              <dd>{t(deviceIPTypeLabel(status.lanIPv6Type, true))}</dd>
            </div>
            <div className="device-network__dns">
              <dt>{t("DNS servers · eth0")}</dt>
              <dd>{status.dns.length ? status.dns.map((ip) => <code key={ip}>{ip}</code>) : t("No DNS servers configured")}</dd>
            </div>
            <div>
              <dt>{t("MAC address · eth0")}</dt>
              <dd><code>{status.ethAddr || t('Not available')}</code></dd>
            </div>
          </dl>

          <dl className="device-metrics">
            <div><dt><T message="{0}Uptime" values={{ 0: <Activity size={17} /> }} /></dt><dd title={t("{0} seconds", { 0: status.uptime })}>{formatDeviceUptime(status.uptime, locale)}</dd></div>
            <div><dt><T message="{0}CPU load" values={{ 0: <Cpu size={17} /> }} /></dt><dd>{status.cpuload}%</dd></div>
            <div><dt><T message="{0}Memory usage" values={{ 0: <MemoryStick size={17} /> }} /></dt><dd>{status.memory}%</dd></div>
            <div className="device-metrics__restart">
              <dt><T message="{0}Last restart · local time" values={{ 0: <Clock3 size={17} /> }} /></dt>
              <dd><time dateTime={status.lastRestart}>{new Date(status.lastRestart).toLocaleString(locale, { dateStyle: 'medium', timeStyle: 'short' })}</time></dd>
            </div>
            <div><dt><T message="{0}Health" values={{ 0: <HeartPulse size={17} /> }} /></dt><dd>{status.health === 'healthy' ? t("Healthy") : t(status.health) || t("Not available")}</dd></div>
          </dl>
        </>
      )}
    </section>
  )
}
