import { useEffect, useRef, useState } from 'react'
import { CheckCircle2, Download, PackageCheck, RefreshCw, RotateCw } from 'lucide-react'
import Button from './Button'
import { useAuth } from '../auth'
import { useI18n } from '../i18n'
import { formatTrafficBytes } from '../traffic'
import { versionComparable, type StagedUpgradePackage } from '../upgrade'
import { useUpgradeFlow } from '../useUpgradeFlow'

const phaseMessages: Record<string, string> = {
  preparing: 'Preparing the verified package for installation…',
  prepared: 'Installation accepted. Waiting for the portal to restart…',
  activating: 'Activating the new release…',
  'awaiting-ready': 'The portal is restarting. Checking the new release’s health…',
  'rolling-back': 'Restoring the previous release…',
  'rollback-failed': 'Automatic recovery needs attention. Check the device’s upgrade recovery service logs.',
  aborted: 'Installation stopped before activation. The installed release was not changed.',
  'rolled-back': 'The new release did not start reliably. The previous release was restored.',
}

export default function UpgradeSettingsCard() {
  const { t, locale } = useI18n()
  const { accessToken, logout } = useAuth()
  const { status, busy, error, attempt, installation, monitoring, connectionLost, pollExpired, pollPaused, run, install } = useUpgradeFlow(accessToken, logout)
  const [confirmation, setConfirmation] = useState<StagedUpgradePackage | null>(null)
  const release = status?.latestRelease
  const staged = status?.stagedPackage
  const comparable = versionComparable(status?.currentVersion ?? '')
  const operationReason = busy ? t('Wait for the upgrade request to finish.') : monitoring ? t('Wait for installation or recovery to finish.') : ''
  const onlineReason = operationReason || (status && !status.onlineCheckSupported ? t('Online update checking is not supported on this device.') : '')
  const installReason = operationReason || (!status?.installationSupported ? t('Upgrade installation is not supported on this device') : '')
  const installed = !!staged && status?.currentVersion === staged.version
  const complete = installation?.phase === 'complete' && status?.currentVersion === installation.version
  const historical = installation?.phase === 'complete' && !complete && !attempt
  const failed = installation && ['aborted', 'rolled-back', 'rollback-failed'].includes(installation.phase)
  const phase = installation?.phase ?? (busy === 'installing' ? 'submitting' : 'unknown')
  const formatDate = (value: string) => new Date(value).toLocaleString(locale)
  let installationTitle = t('Installing upgrade')
  if (complete) installationTitle = t('Upgrade complete')
  else if (historical) installationTitle = t('Previous upgrade completed')
  else if (installation?.phase === 'aborted') installationTitle = t('Installation canceled')
  else if (installation?.phase === 'rolled-back') installationTitle = t('Previous version restored')
  else if (installation?.phase === 'rollback-failed') installationTitle = t('Recovery needs attention')
  else if (!installation && busy !== 'installing') installationTitle = t('Checking installation status')

  return <section className="panel upgrade-settings" aria-labelledby="upgrade-heading">
    <div className="panel__header">
      <div><span className="panel__eyebrow">{t('PORTAL SOFTWARE')}</span><h2 id="upgrade-heading">{t('Software upgrade')}</h2></div>
      <Download size={22} aria-hidden="true" />
    </div>
    <div className="credential-form upgrade-content" aria-busy={!!busy}>
      <p className="credential-intro">{t('Check GitHub Releases for a new version. Downloaded packages are verified with a trusted release key before they are saved.')}</p>
      {status && <p className="upgrade-current">{t('Installed version')} <strong>{status.currentVersion || t('Unavailable')}</strong></p>}
      {(installation || attempt) && <div className={`upgrade-installation${failed ? ' upgrade-installation--error' : ''}`} data-phase={phase} role={failed ? 'alert' : 'status'} aria-live="polite">
        <h3>{complete ? <CheckCircle2 size={20} aria-hidden="true" /> : <RotateCw size={20} aria-hidden="true" />}{installationTitle}</h3>
        <p>{t('Upgrade version: {version}', { version: installation?.version ?? attempt!.version })}</p>
        {installation?.phase === 'complete' ? <p>{t(complete ? 'The new release is running. Refresh the portal to load its WebUI.' : historical ? 'This is the result of a previous installation.' : 'Checking the installed version…')}</p> :
          <p>{t(phaseMessages[phase] ?? (phase === 'submitting' ? 'Requesting installation…' : 'The request may have been accepted. Checking status before another installation can start.'))}</p>}
        {installation?.error && <p>{t(installation.error)}</p>}
        {monitoring && !pollExpired && !pollPaused && <p className="password-help">{t(connectionLost ? 'Waiting for the portal to reconnect…' : 'Installation status refreshes automatically. You can return to Settings to check progress.')}</p>}
        {pollExpired && monitoring && <p>{t('Installation is taking longer than expected. Check status again before taking further action.')}</p>}
        {complete && <Button type="button" className="secondary-button" onClick={() => window.location.reload()}><RefreshCw size={17} aria-hidden="true" />{t('Refresh portal')}</Button>}
        {(monitoring || failed) && <Button type="button" className="text-action" disabledReason={busy ? t('Wait for the upgrade request to finish.') : ''} onClick={() => { void run('loading') }}>{t('Reload upgrade status')}</Button>}
      </div>}
      <div className="upgrade-online">
        <h3>{t('Online updates')}</h3>
        <p className="password-help upgrade-source">{t('Release source:')} <a href="https://github.com/pancpp/nanotail-portal/releases" target="_blank" rel="noopener noreferrer">pancpp/nanotail-portal</a></p>
        {status?.checkedAt && <p className="password-help">{t('Last checked: {time}', { time: formatDate(status.checkedAt) })}</p>}
        {status?.checkedAt && !release && <p role="status">{t('No compatible release is available yet.')}</p>}
        {status?.checkedAt && release && !status.updateAvailable && <p role="status">{comparable ? t('No newer version is available.') : t('The installed version cannot be compared with the latest release.')}</p>}
        {release && <div className="upgrade-release">
          <p className="upgrade-release__version">{status?.updateAvailable ? t('Version {version} is available', { version: release.version }) : t('Latest release: {version}', { version: release.version })}</p>
          <p className="password-help">{t('Published {date} · {size}', { date: formatDate(release.publishedAt), size: formatTrafficBytes(release.size, locale) })}</p>
          {release.notes && <details><summary>{t('Release notes')}</summary><p className="upgrade-notes">{release.notes}</p></details>}
          {(status?.updateAvailable || !comparable) && <Button type="button" className="secondary-button" disabledReason={onlineReason} onClick={() => { void run('downloading') }}>
            <Download size={17} aria-hidden="true" />{t('Prepare upgrade')}
          </Button>}
        </div>}
        <Button type="button" className="secondary-button" disabledReason={onlineReason} onClick={() => { void run('checking') }}>
          <RefreshCw size={17} aria-hidden="true" />{t('Check for updates')}
        </Button>
        {status && !status.onlineCheckSupported && <p className="password-help">{t('Online update checking is not supported on this device.')}</p>}
      </div>
      {busy && busy !== 'installing' && <p className="upgrade-progress" role="status">{busy === 'loading' ? t('Loading upgrade status…') : busy === 'checking' ? t('Checking for updates…') : t('Downloading and verifying the package…')}</p>}
      {monitoring && !attempt && !installation && <div className="upgrade-progress" role="status">
        <p>{t('Another upgrade operation is in progress')}</p>
        {connectionLost && <p>{t('Waiting for the portal to reconnect…')}</p>}
        {pollExpired && <p>{t('Installation is taking longer than expected. Check status again before taking further action.')}</p>}
        <Button type="button" className="text-action" disabledReason={busy ? t('Wait for the upgrade request to finish.') : ''} onClick={() => { void run('loading') }}>{t('Reload upgrade status')}</Button>
      </div>}
      {error && <div className="upgrade-error">
        <p className="form-error" role="alert">{t(error)}</p>
        {!monitoring && !failed && <Button type="button" className="text-action" disabledReason={busy ? t('Wait for the upgrade request to finish.') : ''} onClick={() => { void run('loading') }}>{t('Reload upgrade status')}</Button>}
      </div>}
      {staged && <div className="upgrade-verified">
        <h3><PackageCheck size={20} aria-hidden="true" />{t('Package verified')}</h3>
        <p>{t('Version {version} · {size}', { version: staged.version, size: formatTrafficBytes(staged.size, locale) })}</p>
        <p className="password-help">{t('Verified {time}', { time: formatDate(staged.verifiedAt) })}</p>
        {installed ? <p>{t('This version is already installed.')}</p> : <>
          <p>{t('The verified package is ready to install. The portal will restart during installation.')}</p>
          <Button type="button" className="secondary-button" disabledReason={installReason} onClick={() => setConfirmation({ ...staged })}>
            <RotateCw size={17} aria-hidden="true" />{t('Install and restart')}
          </Button>
          {!status?.installationSupported && <p className="password-help">{t('Upgrade installation is not supported on this device')}</p>}
        </>}
        {staged.notes && <details><summary>{t('Package release notes')}</summary><p className="upgrade-notes">{staged.notes}</p></details>}
      </div>}
      {!staged && <p className="password-help upgrade-staging-note">{t('Prepare a release to verify its package, then install it from here.')}</p>}
    </div>
    {confirmation && <InstallUpgradeDialog selected={confirmation} disabledReason={installReason}
      onClose={() => setConfirmation(null)} onInstall={() => { setConfirmation(null); void install(confirmation) }} />}
  </section>
}

function InstallUpgradeDialog({ selected, disabledReason, onClose, onInstall }: {
  selected: StagedUpgradePackage
  disabledReason: string
  onClose: () => void
  onInstall: () => void
}) {
  const { t } = useI18n()
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const element = dialog.current!
    const previousFocus = document.activeElement as HTMLElement | null
    const previousOverflow = document.body.style.overflow
    element.showModal()
    document.body.style.overflow = 'hidden'
    return () => { element.close(); document.body.style.overflow = previousOverflow; previousFocus?.focus() }
  }, [])
  return <dialog ref={dialog} className="setup-dialog upgrade-install-dialog" aria-labelledby="upgrade-install-title" aria-describedby="upgrade-install-description" onCancel={event => { event.preventDefault(); onClose() }}>
    <div className="panel__header"><h2 id="upgrade-install-title">{t('Install version {version}?', { version: selected.version })}</h2></div>
    <p id="upgrade-install-description" className="setup-dialog__description">{t('Install the verified package and restart the portal. The connection will be briefly interrupted; this page will reconnect and check the result.')}</p>
    <div className="setup-dialog__footer upgrade-install-actions">
      <Button type="button" className="secondary-button" autoFocus onClick={onClose}>{t('Cancel')}</Button>
      <Button type="button" className="secondary-button" disabledReason={disabledReason} onClick={onInstall}><RotateCw size={17} aria-hidden="true" />{t('Install and restart')}</Button>
    </div>
  </dialog>
}
