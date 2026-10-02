import { ApiError, graphQLRequest } from './api.ts'

export const MAX_UPGRADE_PACKAGE_BYTES = 128 * 1024 * 1024

export interface UpgradeRelease {
  version: string
  notes: string
  publishedAt: string
  packageName: string
  size: number
}

export interface StagedUpgradePackage {
  version: string
  sha256: string
  size: number
  verifiedAt: string
  notes: string
}

export type UpgradeInstallationPhase = 'preparing' | 'prepared' | 'activating' | 'awaiting-ready' |
  'rolling-back' | 'rollback-failed' | 'complete' | 'aborted' | 'rolled-back'

export interface UpgradeInstallation {
  id: string
  version: string
  phase: UpgradeInstallationPhase
  error: string
  startedAt: string
  completedAt: string | null
}

export interface UpgradeInstallResult {
  accepted: true
  installation: UpgradeInstallation
}

export class UpgradeInstallOutcomeUnknown extends Error {
  constructor() {
    super('The installation request may have been accepted. Check upgrade status before trying again.')
    this.name = 'UpgradeInstallOutcomeUnknown'
  }
}

export function installationPending(installation: UpgradeInstallation | null | undefined): boolean {
  return !!installation && !['complete', 'aborted', 'rolled-back'].includes(installation.phase)
}

export interface UpgradeStatus {
  currentVersion: string
  latestRelease: UpgradeRelease | null
  updateAvailable: boolean
  checkedAt: string | null
  stagedPackage: StagedUpgradePackage | null
  installationSupported: boolean
  installation: UpgradeInstallation | null
  onlineCheckSupported: boolean
  phase: 'idle' | 'checking' | 'downloading' | 'verifying' | 'staged'
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isDate(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value))
}

function isVersion(value: unknown): value is string {
  return typeof value === 'string' && !!value.trim()
}

// Match the backend's full SemVer contract, including optional leading v.
// Availability still comes from the server; this only detects development builds.
export function versionComparable(version: string): boolean {
  if (version.length > 128) return false
  const match = /^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.exec(version)
  return !!match && (!match[4] || match[4].split('.').every(part => !/^0[0-9]+$/.test(part)))
}

function isSize(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 && value <= MAX_UPGRADE_PACKAGE_BYTES
}

function validInstallation(value: unknown): value is UpgradeInstallation {
  if (!isRecord(value) || typeof value.id !== 'string' || !value.id.trim() ||
    typeof value.version !== 'string' || !versionComparable(value.version) ||
    typeof value.phase !== 'string' || !['preparing', 'prepared', 'activating', 'awaiting-ready',
      'rolling-back', 'rollback-failed', 'complete', 'aborted', 'rolled-back'].includes(value.phase) ||
    typeof value.error !== 'string' || !isDate(value.startedAt)) return false
  const terminal = ['complete', 'aborted', 'rolled-back'].includes(value.phase)
  // A failed finalization can retain its completion timestamp while recovery
  // retries. The phase, not this timestamp, determines whether work is pending.
  return terminal ? isDate(value.completedAt) : value.completedAt === null || isDate(value.completedAt)
}

function validStatus(value: unknown): value is UpgradeStatus {
  if (!isRecord(value) || typeof value.currentVersion !== 'string' || typeof value.updateAvailable !== 'boolean' ||
    typeof value.installationSupported !== 'boolean' || typeof value.onlineCheckSupported !== 'boolean' ||
    (value.checkedAt !== null && !isDate(value.checkedAt)) ||
    typeof value.phase !== 'string' || !['idle', 'checking', 'downloading', 'verifying', 'staged'].includes(value.phase) ||
    (value.installation !== null && !validInstallation(value.installation))) return false
  const release = value.latestRelease
  if (release !== null && (!isRecord(release) || !isVersion(release.version) || typeof release.notes !== 'string' ||
    !isDate(release.publishedAt) || typeof release.packageName !== 'string' || !release.packageName.trim() || !isSize(release.size))) return false
  if (value.updateAvailable && (release === null || value.checkedAt === null)) return false
  const staged = value.stagedPackage
  return staged === null || (isRecord(staged) && isVersion(staged.version) && typeof staged.notes === 'string' &&
    isDate(staged.verifiedAt) && isSize(staged.size) && typeof staged.sha256 === 'string' && /^[a-f0-9]{64}$/.test(staged.sha256))
}

const INSTALLATION_FIELDS = 'id version phase error startedAt completedAt'

const UPGRADE_STATUS_FIELDS = `
  currentVersion
  latestRelease { version notes publishedAt packageName size }
  updateAvailable
  checkedAt
  stagedPackage { version sha256 size verifiedAt notes }
  installationSupported
  installation { ${INSTALLATION_FIELDS} }
  onlineCheckSupported
  phase
`

const statusOperations = {
  UpgradeStatus: {
    field: 'upgradeStatus',
    query: `query UpgradeStatus { upgradeStatus { ${UPGRADE_STATUS_FIELDS} } }`,
  },
  CheckForUpdates: {
    field: 'checkForUpdates',
    query: `mutation CheckForUpdates { checkForUpdates { ${UPGRADE_STATUS_FIELDS} } }`,
  },
  DownloadUpgrade: {
    field: 'downloadUpgrade',
    query: `mutation DownloadUpgrade($version: String!) {
      downloadUpgrade(version: $version) { ${UPGRADE_STATUS_FIELDS} }
    }`,
  },
}

async function upgradeRequest(token: string, operation: keyof typeof statusOperations,
  variables: object = {}, signal?: AbortSignal): Promise<UpgradeStatus> {
  const timeout = new AbortController()
  const timer = setTimeout(() => timeout.abort(), operation === 'DownloadUpgrade' ? 300_000 : 30_000)
  try {
    const { field, query } = statusOperations[operation]
    const data = await graphQLRequest(token, operation, query, variables,
      signal ? AbortSignal.any([signal, timeout.signal]) : timeout.signal, 'upgrade', {
        failure: 'Unable to complete the upgrade request. Please try again.',
        invalid: 'The server returned invalid upgrade status.',
      })
    const status: unknown = data[field]
    if (!validStatus(status)) throw new ApiError('The server returned invalid upgrade status.', 200)
    return status
  } catch (error) {
    if (timeout.signal.aborted && !signal?.aborted) throw new Error('The upgrade request timed out. Reload upgrade status before trying again.')
    throw error
  } finally {
    clearTimeout(timer)
  }
}

export function upgradeStatusRequest(token: string, signal?: AbortSignal): Promise<UpgradeStatus> {
  return upgradeRequest(token, 'UpgradeStatus', {}, signal)
}

export function checkUpgradeRequest(token: string, signal?: AbortSignal): Promise<UpgradeStatus> {
  return upgradeRequest(token, 'CheckForUpdates', {}, signal)
}

export async function downloadUpgradeRequest(token: string, version: string, signal?: AbortSignal): Promise<UpgradeStatus> {
  if (!isVersion(version)) throw new Error('Choose an available release first.')
  const status = await upgradeRequest(token, 'DownloadUpgrade', { version }, signal)
  if (status.stagedPackage?.version !== version) throw new Error('The server did not confirm a verified upgrade package.')
  return status
}

const INSTALL_UPGRADE_MUTATION = `
  mutation InstallUpgrade($version: String!, $sha256: String!) {
    installUpgrade(version: $version, sha256: $sha256) {
      accepted
      installation { ${INSTALLATION_FIELDS} }
    }
  }
`

// An interrupted response cannot tell us whether durable installation was
// accepted. Callers must reconcile status before offering another installation.
export async function installUpgradeRequest(token: string, version: string, sha256: string,
  signal?: AbortSignal): Promise<UpgradeInstallResult> {
  if (typeof version !== 'string' || !versionComparable(version) ||
    typeof sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(sha256)) {
    throw new Error('Select the verified package version and SHA-256 digest.')
  }
  // Cancellation before dispatch is unambiguous: nothing has been submitted.
  signal?.throwIfAborted()
  const timeout = new AbortController()
  const timer = setTimeout(() => timeout.abort(), 300_000)
  try {
    const data = await graphQLRequest(token, 'InstallUpgrade', INSTALL_UPGRADE_MUTATION, { version, sha256 },
      signal ? AbortSignal.any([signal, timeout.signal]) : timeout.signal, 'upgrade installation', {
        failure: 'Unable to request upgrade installation.',
        invalid: 'The server did not confirm upgrade installation.',
      })
    const result: unknown = data.installUpgrade
    if (!isRecord(result) || result.accepted !== true || !validInstallation(result.installation) ||
      result.installation.version !== version || result.installation.phase !== 'prepared' ||
      result.installation.completedAt !== null || result.installation.error !== '') {
      throw new UpgradeInstallOutcomeUnknown()
    }
    return { accepted: true, installation: result.installation }
  } catch (error) {
    if (error instanceof ApiError && (error.status === 401 || error.status === 403 ||
      (error.isGraphQLError && error.status < 500))) throw error
    throw new UpgradeInstallOutcomeUnknown()
  } finally {
    clearTimeout(timer)
  }
}
