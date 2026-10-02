import { useEffect, useRef, useState } from 'react'
import { Download, PackageCheck, RefreshCw } from 'lucide-react'
import Button from './Button'
import { useAuth } from '../auth'
import { isSessionError } from '../api'
import { useI18n } from '../i18n'
import { formatTrafficBytes } from '../traffic'
import { checkUpgradeRequest, downloadUpgradeRequest, upgradeStatusRequest, versionComparable, type UpgradeStatus } from '../upgrade'

type Action = 'loading' | 'checking' | 'downloading'

export default function UpgradeSettingsCard() {
  const { t, locale } = useI18n()
  const { accessToken, logout } = useAuth()
  const [status, setStatus] = useState<UpgradeStatus | null>(null)
  const [busy, setBusy] = useState<Action | null>('loading')
  const [error, setError] = useState('')
  const request = useRef<AbortController | null>(null)

  useEffect(() => {
    if (!accessToken) return
    const controller = new AbortController()
    request.current = controller
    setBusy('loading')
    void upgradeStatusRequest(accessToken, controller.signal).then(setStatus).catch(error => {
      if (controller.signal.aborted) return
      if (isSessionError(error)) logout()
      else setError(error instanceof Error ? error.message : 'Unable to load upgrade status.')
    }).finally(() => {
      if (!controller.signal.aborted) { request.current = null; setBusy(null) }
    })
    return () => { controller.abort(); request.current?.abort() }
  }, [accessToken, logout])

  async function run(action: Action) {
    if (!accessToken || request.current) return
    const release = status?.latestRelease
    if (action === 'downloading' && (!release || (!status?.updateAvailable && versionComparable(status?.currentVersion ?? '')))) return
    const controller = new AbortController()
    request.current = controller
    setBusy(action)
    setError('')
    try {
      const next = await (action === 'checking' ? checkUpgradeRequest(accessToken, controller.signal) :
        action === 'downloading' ? downloadUpgradeRequest(accessToken, release!.version, controller.signal) : upgradeStatusRequest(accessToken, controller.signal))
      if (controller.signal.aborted) return
      setStatus(next)
    } catch (error) {
      if (controller.signal.aborted) return
      if (isSessionError(error)) logout()
      else setError(error instanceof Error ? error.message : 'Unable to complete the upgrade request. Please try again.')
    } finally {
      if (!controller.signal.aborted) { request.current = null; setBusy(null) }
    }
  }

  const busyReason = busy ? t("Wait for the upgrade request to finish.") : ''
  const release = status?.latestRelease
  const staged = status?.stagedPackage
  const comparable = versionComparable(status?.currentVersion ?? '')
  const formatDate = (value: string) => new Date(value).toLocaleString(locale)

  return <section className="panel upgrade-settings" aria-labelledby="upgrade-heading">
    <div className="panel__header">
      <div><span className="panel__eyebrow">{t("PORTAL SOFTWARE")}</span><h2 id="upgrade-heading">{t("Software upgrade")}</h2></div>
      <Download size={22} aria-hidden="true" />
    </div>
    <div className="credential-form upgrade-content" aria-busy={!!busy}>
      <p className="credential-intro">{t("Check GitHub Releases for a new version. Downloaded packages are verified with a trusted release key before they are saved.")}</p>
      {status && <p className="upgrade-current">{t("Installed version")} <strong>{status.currentVersion || t("Unavailable")}</strong></p>}
      <div className="upgrade-online">
        <h3>{t("Online updates")}</h3>
        <p className="password-help upgrade-source">{t("Release source:")} <a href="https://github.com/pancpp/nanotail-portal/releases" target="_blank" rel="noopener noreferrer">pancpp/nanotail-portal</a></p>
        {status?.checkedAt && <p className="password-help">{t("Last checked: {time}", { time: formatDate(status.checkedAt) })}</p>}
        {status?.checkedAt && !release && <p role="status">{t("No compatible release is available yet.")}</p>}
        {status?.checkedAt && release && !status.updateAvailable && <p role="status">{comparable ? t("No newer version is available.") : t("The installed version cannot be compared with the latest release.")}</p>}
        {release && <div className="upgrade-release">
          <p className="upgrade-release__version">{status?.updateAvailable ? t("Version {version} is available", { version: release.version }) : t("Latest release: {version}", { version: release.version })}</p>
          <p className="password-help">{t("Published {date} · {size}", { date: formatDate(release.publishedAt), size: formatTrafficBytes(release.size, locale) })}</p>
          {release.notes && <details><summary>{t("Release notes")}</summary><p className="upgrade-notes">{release.notes}</p></details>}
          {(status?.updateAvailable || !comparable) && <Button type="button" className="secondary-button" disabledReason={busyReason} onClick={() => { void run('downloading') }}>
            <Download size={17} aria-hidden="true" />{t("Prepare upgrade")}
          </Button>}
        </div>}
        <Button type="button" className="secondary-button" disabledReason={busyReason} onClick={() => { void run('checking') }}>
          <RefreshCw size={17} aria-hidden="true" />{t("Check for updates")}
        </Button>
      </div>
      {busy && <p className="upgrade-progress" role="status">{busy === 'loading' ? t("Loading upgrade status…") : busy === 'checking' ? t("Checking for updates…") : t("Downloading and verifying the package…")}</p>}
      {error && <div className="upgrade-error">
        <p className="form-error" role="alert">{t(error)}</p>
        <Button type="button" className="text-action" disabledReason={busyReason} onClick={() => { void run('loading') }}>{t("Reload upgrade status")}</Button>
      </div>}
      {staged && <div className="upgrade-verified" role="status">
        <h3><PackageCheck size={20} aria-hidden="true" />{t("Package verified")}</h3>
        <p>{t("Version {version} · {size}", { version: staged.version, size: formatTrafficBytes(staged.size, locale) })}</p>
        <p className="password-help">{t("Verified {time}", { time: formatDate(staged.verifiedAt) })}</p>
        <p>{t("The verified package is saved. Installation is not available yet; the installed version has not changed.")}</p>
        {staged.notes && <details><summary>{t("Package release notes")}</summary><p className="upgrade-notes">{staged.notes}</p></details>}
      </div>}
      {!staged && <p className="password-help upgrade-staging-note">{t("Packages can be verified and saved now. Installing an upgrade will be available in a later release.")}</p>}
    </div>
  </section>
}
