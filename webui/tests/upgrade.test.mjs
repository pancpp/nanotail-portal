import assert from 'node:assert/strict'
import test from 'node:test'
import { ApiError, isSessionError } from '../src/api.ts'
import {
  MAX_UPGRADE_PACKAGE_BYTES, checkUpgradeRequest, downloadUpgradeRequest,
  upgradeStatusRequest, versionComparable, installUpgradeRequest, installationPending, UpgradeInstallOutcomeUnknown,
} from '../src/upgrade.ts'

const release = {version:'1.2.0',notes:'Fixes and improvements',publishedAt:'2026-10-01T12:00:00Z',packageName:'portal-1.2.0.tar.gz',size:1234}
const staged = {version:'1.2.0',notes:'Fixes and improvements',verifiedAt:'2026-10-01T12:01:00Z',sha256:'ab'.repeat(32),size:1234}
const installation = overrides => ({id:'INSTALLATION123',version:'1.2.0',phase:'prepared',error:'',startedAt:'2026-10-01T12:02:00Z',completedAt:null,...overrides})
const status = overrides => ({phase:'idle',installation:null,currentVersion:'1.1.0',latestRelease:null,updateAvailable:false,checkedAt:null,stagedPackage:null,installationSupported:false,onlineCheckSupported:true,...overrides})
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

test('status selects and validates the service and complete installation lifecycle', async t => {
  const fetch=t.mock.method(globalThis,'fetch')
  for (const phase of ['idle','checking','downloading','verifying','staged']) {
    const expected=status({phase})
    fetch.mock.mockImplementation(async () => response('upgradeStatus',expected))
    assert.deepEqual(await upgradeStatusRequest('session'),expected)
  }
  for (const phase of ['preparing','prepared','activating','awaiting-ready','rolling-back','rollback-failed','complete','aborted','rolled-back']) {
    const terminal=['complete','aborted','rolled-back'].includes(phase)
    const expected=status({installation:installation({phase,error:phase==='rollback-failed'?'Recovery will retry.':'',completedAt:terminal?'2026-10-01T12:03:00Z':null})})
    fetch.mock.mockImplementation(async () => response('upgradeStatus',expected))
    assert.deepEqual(await upgradeStatusRequest('session'),expected)
    assert.equal(installationPending(expected.installation),!terminal,phase)
  }
  const body=JSON.parse(fetch.mock.calls[0].arguments[1].body)
  assert.match(body.query,/installation\s*\{\s*id version phase error startedAt completedAt\s*\}/)
  assert.match(body.query,/onlineCheckSupported\s+phase/)
  assert.equal(installationPending(null),false)
  assert.equal(installationPending(undefined),false)
  assert.equal(installationPending({phase:'future-active-phase'}),true,'unknown states must not permit another installation')
})

test('status rejects malformed installation progress and inconsistent completion state', async t => {
  const fetch=t.mock.method(globalThis,'fetch')
  const invalid=[
    status({phase:undefined}),status({phase:'installed'}),status({installation:undefined}),status({installation:{}}),
    ...[
      {id:''},{id:' '},{id:123},{version:'latest'},{version:'1.2'},{phase:'unknown'},{error:null},
      {startedAt:'invalid'},{completedAt:undefined},{completedAt:'invalid'},
      {phase:'complete'},{phase:'aborted'},{phase:'rolled-back',completedAt:'invalid'},
    ].map(value=>status({installation:installation(value)})),
  ]
  for (const value of invalid) {
    fetch.mock.mockImplementation(async () => response('upgradeStatus',value))
    await assert.rejects(upgradeStatusRequest('session'),/invalid upgrade status/)
  }
})

test('recovery remains pending when failed finalization retained a completion timestamp', async t => {
  const fetch=t.mock.method(globalThis,'fetch')
  for (const phase of ['rollback-failed','rolling-back']) {
    const expected=status({installation:installation({phase,error:'Recovery will retry.',completedAt:'2026-10-01T12:03:00Z'})})
    fetch.mock.mockImplementation(async ()=>response('upgradeStatus',expected))
    assert.deepEqual(await upgradeStatusRequest('session'),expected)
    assert.equal(installationPending(expected.installation),true)
  }
})

test('installation submits only the exact selected version and digest in a standalone mutation', async t => {
  const accepted={accepted:true,installation:installation()}
  const fetch=t.mock.method(globalThis,'fetch',async ()=>response('installUpgrade',accepted))
  assert.deepEqual(await installUpgradeRequest('session',staged.version,staged.sha256),accepted)
  assert.equal(fetch.mock.callCount(),1)
  const [url,options]=fetch.mock.calls[0].arguments
  assert.equal(url,'/api/v1/query')
  assert.equal(options.method,'POST')
  assert.equal(options.cache,'no-store')
  assert.equal(options.headers.Authorization,'Bearer session')
  assert.equal(options.headers['Content-Type'],'application/json')
  const body=JSON.parse(options.body)
  assert.equal(body.operationName,'InstallUpgrade')
  assert.deepEqual(body.variables,{version:staged.version,sha256:staged.sha256})
  assert.match(body.query,/mutation InstallUpgrade\(\$version: String!, \$sha256: String!\)/)
  assert.match(body.query,/installUpgrade\(version: \$version, sha256: \$sha256\)/)
  assert.equal((body.query.match(/installUpgrade\(/g)||[]).length,1)
  assert.equal(/downloadUpgrade|checkForUpdates|uploadUpgrade/.test(body.query),false)
  assert.equal(body.query.includes(staged.version),false)
  assert.equal(body.query.includes(staged.sha256),false)
})

test('invalid installation selections and cancellation before dispatch never fetch', async t => {
  const fetch=t.mock.method(globalThis,'fetch')
  for (const [version,digest] of [
    ['',staged.sha256],[' ',staged.sha256],['latest',staged.sha256],['1.2',staged.sha256],
    ['1.2.3-01',staged.sha256],['../1.2.0',staged.sha256],[' 1.2.0',staged.sha256],
    [null,staged.sha256],[staged.version,null],[staged.version,''],[staged.version,'ab'.repeat(31)],
    [staged.version,'ab'.repeat(33)],[staged.version,'AB'.repeat(32)],[staged.version,'zz'.repeat(32)],
    [staged.version,' '+staged.sha256],
  ]) {
    await assert.rejects(installUpgradeRequest('session',version,digest),error=>
      !(error instanceof UpgradeInstallOutcomeUnknown)&&/verified package version and SHA-256 digest/.test(error.message))
  }
  await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256,AbortSignal.abort()),{name:'AbortError'})
  assert.equal(fetch.mock.callCount(),0)
})

test('malformed or mismatched installation acceptance has an unknown outcome and is never retried', async t => {
  const fetch=t.mock.method(globalThis,'fetch')
  const invalid=[null,{},[],{accepted:false,installation:installation()},
    {accepted:'true',installation:installation()},{accepted:true,installation:null},
    ...[
      {id:''},{version:'v1.2.0'},{version:'2.0.0'},{phase:'preparing'},{phase:'activating'},
      {phase:'complete',completedAt:'2026-10-01T12:03:00Z'},{startedAt:'invalid'},
      {error:'Installation preparation failed.'},{completedAt:undefined},{completedAt:'2026-10-01T12:03:00Z'},
    ].map(value=>({accepted:true,installation:installation(value)})),
  ]
  for (const value of invalid) {
    fetch.mock.mockImplementation(async ()=>response('installUpgrade',value))
    await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),UpgradeInstallOutcomeUnknown)
  }
  for (const payload of [null,{},[],{data:{}},{data:{downloadUpgrade:status()}},
    {data:{installUpgrade:{accepted:true,installation:installation()}},errors:'broken'},
    {errors:[{}]},
  ]) {
    fetch.mock.mockImplementation(async ()=>Response.json(payload))
    await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),UpgradeInstallOutcomeUnknown)
  }
  assert.equal(fetch.mock.callCount(),invalid.length+7)
})

test('installation preserves explicit GraphQL rejection and authentication errors', async t => {
  const fetch=t.mock.method(globalThis,'fetch')
  for (const code of ['UPGRADE_INVALID_SELECTION','UPGRADE_UNSUPPORTED','UPGRADE_INSTALL_FAILED','MAINTENANCE']) {
    fetch.mock.mockImplementation(async ()=>Response.json({errors:[{message:'Installation was rejected.',extensions:{code}}]}))
    await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),error=>
      error instanceof ApiError&&error.isGraphQLError&&error.status===200&&error.message==='Installation was rejected.')
  }
  for (const status of [401,403]) {
    fetch.mock.mockImplementation(async ()=>Response.json({message:'Authorization required.'},{status}))
    await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),error=>
      error instanceof ApiError&&error.status===status&&isSessionError(error)===(status===401))
  }
  assert.equal(fetch.mock.callCount(),6)
})

test('installation treats network loss, broken response bodies and server errors as uncertain', async t => {
  const fetch=t.mock.method(globalThis,'fetch')
  for (const status of [500,502,503,504]) {
    fetch.mock.mockImplementation(async ()=>new Response('Gateway could not confirm the result',{status}))
    await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),UpgradeInstallOutcomeUnknown)
  }
  fetch.mock.mockImplementation(async ()=>Response.json({errors:[{message:'Upstream failed'}]},{status:502}))
  await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),UpgradeInstallOutcomeUnknown)
  fetch.mock.mockImplementation(async ()=>{throw new TypeError('Failed to fetch')})
  await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),UpgradeInstallOutcomeUnknown)
  fetch.mock.mockImplementation(async ()=>new Response('not JSON'))
  await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),UpgradeInstallOutcomeUnknown)
  fetch.mock.mockImplementation(async ()=>new Response(new ReadableStream({start(controller){controller.error(new TypeError('Connection closed'))}})))
  await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),UpgradeInstallOutcomeUnknown)
  assert.equal(fetch.mock.callCount(),8)
})

test('installation timeout and cancellation after dispatch require reconciliation and clear timers', async t => {
  let expire
  const durations=[]
  t.mock.method(globalThis,'setTimeout',(callback,duration)=>{expire=callback;durations.push(duration);return 7})
  const clear=t.mock.method(globalThis,'clearTimeout')
  const fetch=t.mock.method(globalThis,'fetch',async (_url,options)=>{expire();options.signal.throwIfAborted()})
  await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256),UpgradeInstallOutcomeUnknown)
  const controller=new AbortController()
  fetch.mock.mockImplementation(async (_url,options)=>{controller.abort();options.signal.throwIfAborted()})
  await assert.rejects(installUpgradeRequest('session',staged.version,staged.sha256,controller.signal),UpgradeInstallOutcomeUnknown)
  fetch.mock.mockImplementation(async ()=>response('installUpgrade',{accepted:true,installation:installation()}))
  await installUpgradeRequest('session',staged.version,staged.sha256)
  assert.deepEqual(durations,[300_000,300_000,300_000])
  assert.equal(clear.mock.callCount(),3)
  assert.equal(fetch.mock.callCount(),3)
})
