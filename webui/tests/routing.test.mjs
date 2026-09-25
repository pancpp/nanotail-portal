import assert from 'node:assert/strict'
import test from 'node:test'
import { tailscaleRoutingRequest, setExitNodeRequest, isSessionError } from '../src/api.ts'
import { routingSummary, selectedExitNode } from '../src/routing.ts'

const peer = { id: 'exit-1', hostName: 'home-router', dnsName: 'home.example.ts.net', os: 'linux', tailscaleIPs: ['100.64.0.2'], online: true }
const settings = (overrides = {}) => ({ backendState: 'Running', exitNodeID: '', exitNodeIP: '', allowLANAccess: false, advertiseExitNode: false, exitNodes: [peer], ...overrides })

test('routing query is authenticated, cancellable, and validates the response', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { tailscaleRouting: settings() } }))
  const controller = new AbortController()
  assert.deepEqual(await tailscaleRoutingRequest('token', controller.signal), settings())
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query'); assert.equal(options.headers.Authorization, 'Bearer token')
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'TailscaleRouting'); assert.match(body.query, /exitNodes \{ id hostName dnsName os tailscaleIPs online \}/)
  controller.abort(); assert.equal(options.signal.aborted, true)
  const invalid = [null, {}, settings({ exitNodes: [null] }), settings({ exitNodes: [{ ...peer, online: 'true' }] }), settings({ allowLANAccess: 'false' })]
  for (const field of Object.keys(settings())) { const value = settings(); delete value[field]; invalid.push(value) }
  for (const value of invalid) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleRouting: value } }))
    await assert.rejects(tailscaleRoutingRequest('token'), /valid routing settings/)
  }
})

test('routing mutation sends explicit variables and only trusts a true, error-free response', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { setExitNode: true } }))
  for (const input of [{ exitNodeID: peer.id, allowLANAccess: true }, { exitNodeID: '', allowLANAccess: false }]) {
    await setExitNodeRequest('token', input)
    const [url, options] = fetch.mock.calls.at(-1).arguments
    assert.equal(url, '/api/v1/query'); assert.equal(options.headers.Authorization, 'Bearer token')
    const body = JSON.parse(options.body)
    assert.equal(body.operationName, 'SetExitNode'); assert.deepEqual(body.variables, { input })
    assert.match(body.query, /\$input: ExitNodeInput!/); assert.ok(options.signal instanceof AbortSignal)
  }
  for (const value of [false, null, 'true', undefined]) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { setExitNode: value } }))
    await assert.rejects(setExitNodeRequest('token', { exitNodeID: '', allowLANAccess: false }), /did not confirm/)
  }
  fetch.mock.mockImplementation(async () => Response.json({ data: { setExitNode: true }, errors: [{ message: 'Only administrators' }] }))
  await assert.rejects(setExitNodeRequest('token', { exitNodeID: '', allowLANAccess: false }), { message: 'Only administrators', isGraphQLError: true })
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(setExitNodeRequest('token', { exitNodeID: '', allowLANAccess: false }), error => isSessionError(error))
  fetch.mock.mockImplementation(async () => new Response('Bad gateway', { status: 502 }))
  await assert.rejects(tailscaleRoutingRequest('token'), /routing request/)
})

test('invalid routing input never reaches the API', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const input of [{}, { exitNodeID: '', allowLANAccess: true }, { exitNodeID: ' exit-1', allowLANAccess: false },
    { exitNodeID: 'x'.repeat(257), allowLANAccess: false }, { exitNodeID: peer.id, allowLANAccess: 'true' }]) {
    await assert.rejects(setExitNodeRequest('token', input), /Choose an exit node/)
  }
  assert.equal(fetch.mock.callCount(), 0)
})

test('routing writes time out, clear their timer, and never retry automatically', async (t) => {
  let triggerTimeout, cleared = 0
  t.mock.method(globalThis, 'setTimeout', (callback, delay) => { assert.equal(delay, 60_000); triggerTimeout = callback; return 17 })
  t.mock.method(globalThis, 'clearTimeout', id => { assert.equal(id, 17); cleared++ })
  const fetch = t.mock.method(globalThis, 'fetch', async (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
  }))
  const request = setExitNodeRequest('token', { exitNodeID: 'exit-1', allowLANAccess: true })
  triggerTimeout()
  await assert.rejects(request, { name: 'AbortError' })
  assert.equal(fetch.mock.callCount(), 1); assert.equal(cleared, 1)
})

test('routing summary never presents unavailable or stale settings as local-gateway success', () => {
  assert.equal(routingSummary(null, '').title, 'Checking…')
  assert.equal(routingSummary(settings(), 'failed').title, 'Unavailable')
  assert.equal(routingSummary(settings(), '').title, 'Local gateway')
  assert.match(routingSummary(settings({ advertiseExitNode: true }), '').detail, /advertises itself/)
  const selected = settings({ exitNodeID: peer.id, allowLANAccess: true })
  assert.equal(routingSummary(selected, '').title, peer.hostName)
  assert.match(routingSummary(selected, '').detail, /LAN access allowed/)
  assert.match(routingSummary({ ...selected, allowLANAccess: false }, '').detail, /LAN access blocked/)
  assert.match(routingSummary({ ...selected, backendState: 'Stopped' }, '').detail, /not running/)
  assert.match(routingSummary({ ...selected, exitNodes: [{ ...peer, online: false }] }, '').detail, /offline/)
  assert.match(routingSummary({ ...selected, exitNodes: [] }, '').detail, /no longer available/)
  assert.equal(routingSummary({ ...selected, exitNodes: [{ ...peer, hostName: '' }] }, '').title, peer.dnsName)
  assert.equal(selectedExitNode(settings({ exitNodeIP: '100.64.0.3' })), 'unavailable:100.64.0.3')
})
