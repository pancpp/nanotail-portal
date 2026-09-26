import { useEffect, useState } from 'react'
import { isSessionError, portalVersionRequest } from '../api'
import { useAuth } from '../auth'
import { useI18n } from '../i18n'

export default function PortalVersion() {
  const { accessToken, logout } = useAuth()
  const { t } = useI18n()
  const [version, setVersion] = useState<string | null>()

  // Build metadata is independent of Tailscale, the database and OS metrics.
  // Read it once per session; tab/language changes must not restart the request.
  useEffect(() => {
    if (!accessToken) { setVersion(null); return }
    const controller = new AbortController()
    setVersion(undefined)
    void portalVersionRequest(accessToken, controller.signal).then(value => {
      if (!controller.signal.aborted) setVersion(value.trim() ? value : null)
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return
      setVersion(null)
      if (isSessionError(error)) logout()
    })
    return () => controller.abort()
  }, [accessToken, logout])

  return <div className="sidebar__version">
    <span>{t("Portal version")}</span>
    <strong title={version || undefined}>
      {version === undefined ? t("Loading…") : version || t("Unavailable")}
    </strong>
  </div>
}
