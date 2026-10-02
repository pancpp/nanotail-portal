import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError, isSessionError } from '../src/api.ts'
import {
  MAX_UPGRADE_PACKAGE_BYTES, checkUpgradeRequest, downloadUpgradeRequest,
  upgradeStatusRequest, versionComparable,
} from '../src/upgrade.ts'

const release = {version:'1.2.0',notes:'Fixes and improvements',publishedAt:'2026-10-01T12:00:00Z',packageName:'portal-1.2.0.tar.gz',size:1234}
const staged = {version:'1.2.0',notes:'Fixes and improvements',verifiedAt:'2026-10-01T12:01:00Z',sha256:'ab'.repeat(32),size:1234}
const status = overrides => ({currentVersion:'1.1.0',latestRelease:null,updateAvailable:false,checkedAt:null,stagedPackage:null,installationSupported:false,onlineCheckSupported:true,...overrides})
const response = (field, value) => Response.json({data:{[field]:value}})

test('upgrade status and checking authenticate, honor cancellation, and never manufacture a release', async t => {
  const fetch = t.mock.method(globalThis,'fetch',async () => response('upgradeStatus',status()))
  const controller = new AbortController()
  assert.deepEqual(await upgradeStatusRequest('session',controller.signal), status())
  let [url,options] = fetch.mock.calls[0].arguments
  assert.equal(url,'/api/v1/query')
  assert.equal(options.method,'POST')
  assert.equal(options.cache,'no-store')
  assert.equal(options.headers.Authorization,'Bearer session')
  assert.equal(options.headers['Content-Type'],'application/json')
  assert.equal(JSON.parse(options.body).operationName,'UpgradeStatus')
  assert.match(JSON.parse(options.body).query,/query UpgradeStatus\s*\{\s*upgradeStatus\s*\{/)
  assert.deepEqual(JSON.parse(options.body).variables,{})
  assert.ok(options.signal instanceof AbortSignal)
  controller.abort()
  assert.equal(options.signal.aborted,true)
  fetch.mock.mockImplementation(async () => response('upgradeStatus',status({currentVersion:''})))
  assert.deepEqual(await upgradeStatusRequest('session'),status({currentVersion:''}))
  const checked = status({onlineCheckSupported:true,checkedAt:release.publishedAt,latestRelease:release,updateAvailable:true})
  fetch.mock.mockImplementation(async () => response('checkForUpdates',checked))
  assert.deepEqual(await checkUpgradeRequest('session'),checked)
  ;[url,options] = fetch.mock.calls[2].arguments
  assert.equal(url,'/api/v1/query')
  assert.equal(options.method,'POST')
  assert.equal(options.headers.Authorization,'Bearer session')
  assert.equal(JSON.parse(options.body).operationName,'CheckForUpdates')
  assert.match(JSON.parse(options.body).query,/mutation CheckForUpdates\s*\{\s*checkForUpdates\s*\{/)
  assert.deepEqual(JSON.parse(options.body).variables,{})
})

test('download selects only a version, and requires verification of that exact release', async t => {
  const fetch = t.mock.method(globalThis,'fetch',async () => response('downloadUpgrade',status({stagedPackage:staged})))
  assert.deepEqual(await downloadUpgradeRequest('session',release.version),status({stagedPackage:staged}))
  const [url,options] = fetch.mock.calls[0].arguments
  assert.equal(url,'/api/v1/query')
  assert.equal(options.method,'POST')
  assert.equal(options.headers.Authorization,'Bearer session')
  assert.equal(options.headers['Content-Type'],'application/json')
  const body = JSON.parse(options.body)
  assert.equal(body.operationName,'DownloadUpgrade')
  assert.match(body.query,/mutation DownloadUpgrade\(\$version: String!\)/)
  assert.match(body.query,/downloadUpgrade\(version: \$version\)/)
  assert.deepEqual(body.variables,{version:release.version})
  assert.equal(body.query.includes(release.version),false,'release must be passed as a variable')
  for (const stagedPackage of [null,{...staged,version:'0.9.0'}]) {
    fetch.mock.mockImplementation(async () => response('downloadUpgrade',status({stagedPackage})))
    await assert.rejects(downloadUpgradeRequest('session',release.version),/did not confirm/)
  }
})

test('checking preserves current and no-compatible-release results', async t => {
  const fetch = t.mock.method(globalThis,'fetch')
  for (const checked of [status({checkedAt:release.publishedAt}), status({checkedAt:release.publishedAt,currentVersion:release.version,latestRelease:release})]) {
    fetch.mock.mockImplementation(async () => response('checkForUpdates',checked))
    assert.deepEqual(await checkUpgradeRequest('session'),checked)
  }
})

test('unselected versions never send a request', async t => {
  const fetch = t.mock.method(globalThis,'fetch')
  await assert.rejects(downloadUpgradeRequest('session',' '))
  assert.equal(fetch.mock.callCount(),0)
})

test('development versions are not mistaken for comparable releases', () => {
  for (const version of ['v1.2.3','0.0.0','1.2.3-beta.1','1.2.3+build.01','v1.2.3-beta.1+build.2']) assert.equal(versionComparable(version),true,version)
  for (const version of ['','development','abc1234','1.2','1.2.3.4','01.2.3','v1.2.3-01','1.2.3-alpha..beta','1.2.3+','1.2.3-','1.2.3+'+'a'.repeat(128)]) assert.equal(versionComparable(version),false,version)
})

test('upgrade status rejects malformed, unverified, and inconsistent response data', async t => {
  const fetch = t.mock.method(globalThis,'fetch')
  const invalid = [null,{},[],status({currentVersion:null}),status({installationSupported:'false'}),status({onlineCheckSupported:undefined}),
    status({checkedAt:'yesterday'}),status({updateAvailable:true}),status({stagedPackage:{...staged,sha256:'not a digest'}}),
    status({stagedPackage:{...staged,verifiedAt:'invalid'}}),status({stagedPackage:{...staged,size:0}}),
    status({latestRelease:{...release,publishedAt:'invalid'}}),status({latestRelease:{...release,size:MAX_UPGRADE_PACKAGE_BYTES+1}}),
    status({latestRelease:{...release,notes:null}})]
  for (const data of invalid) {
    fetch.mock.mockImplementation(async () => response('upgradeStatus',data))
    await assert.rejects(upgradeStatusRequest('session'),/invalid upgrade status/)
  }
})

test('upgrade requests reject missing and malformed GraphQL response envelopes', async t => {
  const fetch = t.mock.method(globalThis,'fetch')
  for (const payload of [null,{},[],status(),{data:null},{data:{}},{data:[]},
    {data:{checkForUpdates:status()}},{data:{upgradeStatus:status()},errors:'failed'},
    {data:{upgradeStatus:status()},errors:{message:'failed'}}]) {
    fetch.mock.mockImplementation(async () => Response.json(payload))
    await assert.rejects(upgradeStatusRequest('session'),/invalid upgrade status/)
  }
  fetch.mock.mockImplementation(async () => new Response('not JSON'))
  await assert.rejects(upgradeStatusRequest('session'),/invalid upgrade status/)
})

test('HTTP 200 GraphQL errors reject even plausible successful data, without retries', async t => {
  const fetch = t.mock.method(globalThis,'fetch')
  const requests = [
    ['upgradeStatus',status(),() => upgradeStatusRequest('session')],
    ['checkForUpdates',status({latestRelease:release,checkedAt:release.publishedAt,updateAvailable:true}),() => checkUpgradeRequest('session')],
    ['downloadUpgrade',status({stagedPackage:staged}),() => downloadUpgradeRequest('session',release.version)],
  ]
  for (const [field,data,request] of requests) {
    fetch.mock.mockImplementation(async () => Response.json({data:{[field]:data},errors:[{message:'Upgrade operation failed.'}]}))
    await assert.rejects(request(),error => error instanceof ApiError && error.status===200 &&
      error.isGraphQLError && error.message==='Upgrade operation failed.' && !isSessionError(error))
  }
  fetch.mock.mockImplementation(async () => Response.json({data:{upgradeStatus:status()},errors:[{}]}))
  await assert.rejects(upgradeStatusRequest('session'),/Unable to complete the upgrade request/)
  assert.equal(fetch.mock.callCount(),4)
})

test('upgrade failures preserve auth and verification errors without retrying', async t => {
  const fetch = t.mock.method(globalThis,'fetch',async () => Response.json({message:'invalid jwt'},{status:401}))
  await assert.rejects(upgradeStatusRequest('session'),isSessionError)
  fetch.mock.mockImplementation(async () => Response.json({errors:[{message:'Package signature is invalid.'}]}))
  await assert.rejects(downloadUpgradeRequest('session',release.version),/Package signature is invalid/)
  fetch.mock.mockImplementation(async () => Response.json({errors:[{message:'Unable to contact GitHub Releases. Try again later.'}]}))
  await assert.rejects(checkUpgradeRequest('session'),/Unable to contact GitHub Releases/)
  fetch.mock.mockImplementation(async () => new Response('Bad gateway',{status:502}))
  await assert.rejects(checkUpgradeRequest('session'),/Unable to complete/)
  fetch.mock.mockImplementation(async () => {throw new TypeError('Failed to fetch')})
  await assert.rejects(downloadUpgradeRequest('session',release.version),/Failed to fetch/)
  assert.equal(fetch.mock.callCount(),5)
})

test('upgrade requests clear timeouts and preserve caller cancellation', async t => {
  let expire
  const durations=[]
  t.mock.method(globalThis,'setTimeout',(callback,duration) => {expire=callback;durations.push(duration);return 7})
  const clear=t.mock.method(globalThis,'clearTimeout')
  const fetch=t.mock.method(globalThis,'fetch',async (_url,options) => {expire();options.signal.throwIfAborted()})
  await assert.rejects(checkUpgradeRequest('session'),/timed out.*Reload upgrade status/)
  await assert.rejects(downloadUpgradeRequest('session',release.version),/timed out/)
  assert.deepEqual(durations,[30_000,300_000])
  assert.equal(clear.mock.callCount(),2)
  fetch.mock.mockImplementation(async (_url,options) => {options.signal.throwIfAborted()})
  await assert.rejects(upgradeStatusRequest('session',AbortSignal.abort()),{name:'AbortError'})
  assert.equal(clear.mock.callCount(),3)
})
