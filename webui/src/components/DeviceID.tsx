import { useEffect, useState } from 'react'
import { deviceIDRequest, isSessionError } from '../api'
import { useAuth } from '../auth'
import { useI18n } from '../i18n'

export default function DeviceID() {
  const { accessToken, logout } = useAuth()
  const { t } = useI18n()
  const [deviceID, setDeviceID] = useState<string | null>()

  useEffect(() => {
    if (!accessToken) { setDeviceID(null); return }
    const controller = new AbortController()
    setDeviceID(undefined)
    void deviceIDRequest(accessToken, controller.signal).then(value => {
      if (!controller.signal.aborted) setDeviceID(value)
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return
      setDeviceID(null)
      if (isSessionError(error)) logout()
    })
    return () => controller.abort()
  }, [accessToken, logout])

  return <dl className="status-card__device-id">
    <dt>{t("Device ID")}</dt>
    <dd>{deviceID ? <code>{deviceID}</code> : deviceID === undefined ? t("Loading…") : t("Unavailable")}</dd>
  </dl>
}
