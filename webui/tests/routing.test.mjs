import assert from 'node:assert/strict'
import test from 'node:test'
import { tailscaleRoutingRequest, setRoutingRequest, isSessionError } from '../src/api.ts'
import { routingSummary, routingDraft, forwardingWarnings } from '../src/routing.ts'
import { canonicalSubnetRoutes, parseSubnetRouteText } from '../src/subnetRoutes.ts'

const settings = (overrides = {}) => ({
  backendState: 'Running', advertiseExitNode: false, subnetDefaultsPending: false, subnetRoutes: [], usingExitNode: false,
  snatEnabled: true, health: [], lanInterface: 'eth0', defaultSubnetRoutes: ['192.168.42.0/24'],
  lanWarning: '', ipv4Forwarding: true, ipv6Forwarding: true, ...overrides,
})

test('routing query is authenticated, cancellable, and validates the response including unknown readiness', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { tailscaleRouting: settings() } }))
  const controller = new AbortController()
  assert.deepEqual(await tailscaleRoutingRequest('token', controller.signal), settings())
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query'); assert.equal(options.headers.Authorization, 'Bearer token')
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'TailscaleRouting')
  assert.match(body.query, /defaultSubnetRoutes/); assert.doesNotMatch(body.query, /exitNodes/)
  controller.abort(); assert.equal(options.signal.aborted, true)
  const invalid = [null, {}, settings({ subnetRoutes: [null] }), settings({ advertiseExitNode: 'true' }),
    settings({ ipv4Forwarding: 1 }), settings({ defaultSubnetRoutes: '192.168.0.0/24' }), settings({ health: [1] })]
  for (const field of Object.keys(settings())) { const value = settings(); delete value[field]; invalid.push(value) }
  for (const value of invalid) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleRouting: value } }))
    await assert.rejects(tailscaleRoutingRequest('token'), /valid routing settings/)
  }
  fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleRouting: settings({ ipv4Forwarding: null, ipv6Forwarding: false }) } }))
  assert.equal((await tailscaleRoutingRequest('token')).ipv4Forwarding, null)
})

test('router mutation sends explicit advertisements and handles errors without trusting partial success', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { setRouting: true } }))
  for (const input of [
    { subnetRoutes: ['192.168.42.0/24'] },
    { subnetRoutes: ['fd00::/64'] },
    { subnetRoutes: [] },
  ]) {
    await setRoutingRequest('token', input)
    const [url, options] = fetch.mock.calls.at(-1).arguments
    assert.equal(url, '/api/v1/query'); assert.equal(options.headers.Authorization, 'Bearer token')
    const body = JSON.parse(options.body)
    assert.equal(body.operationName, 'SetRouting'); assert.deepEqual(body.variables, { input })
    assert.match(body.query, /\$input: RoutingInput!/); assert.ok(options.signal instanceof AbortSignal)
  }
  const input = { subnetRoutes: [] }
  for (const value of [false, null, 'true', undefined]) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { setRouting: value } }))
    await assert.rejects(setRoutingRequest('token', input), /did not confirm/)
  }
  fetch.mock.mockImplementation(async () => Response.json({ data: { setRouting: true }, errors: [{ message: 'Only administrators' }] }))
  await assert.rejects(setRoutingRequest('token', input), { message: 'Only administrators', isGraphQLError: true })
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(setRoutingRequest('token', input), error => isSessionError(error))
  fetch.mock.mockImplementation(async () => new Response('Bad gateway', { status: 502 }))
  await assert.rejects(tailscaleRoutingRequest('token'), /routing request/)
})

test('subnet validation supports canonical IPv4 and IPv6 and rejects dangerous/invalid ranges', () => {
  assert.deepEqual(parseSubnetRouteText('192.168.42.0/24,\nFD00:1234:0:0::/64\n10.0.0.1/32'), ['10.0.0.1/32', '192.168.42.0/24', 'fd00:1234::/64'])
  assert.deepEqual(canonicalSubnetRoutes([]), [])
  for (const route of ['192.168.1.1/24', '::/0', '0.0.0.0/0', 'invalid', '--reset', '192.168.1.0/33',
    '01.0.0.0/8', 'fd00::1/64', 'fd00::/129', 'fd00::/00', 'fe80::/64', 'ff00::/8', '::1/128', '::/128',
    '::ffff:c0a8:100/120', '127.0.0.0/8', '169.254.0.0/16', '100.64.0.0/10', '100.0.0.0/8',
    'fd7a:115c:a1e0::/64', '224.0.0.0/4', '240.0.0.0/4', '0.1.0.0/16', 'fd00:::0/64']) {
    assert.throws(() => canonicalSubnetRoutes([route]), /subnet CIDRs/, route)
  }
  for (const value of [null, '192.168.1.0/24', [null], Array(65).fill('10.0.0.0/24'), ['FD00::/64', 'fd00::/64']]) {
    assert.throws(() => canonicalSubnetRoutes(value), /subnet CIDRs/)
  }
})

test('invalid mutation never calls the server', async t => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const input of [null, {}, { advertiseExitNode: 'true', subnetRoutes: [] },
    { advertiseExitNode: true }, { advertiseExitNode: false, subnetRoutes: [] },
    { advertiseExitNode: true, subnetRoutes: [] }, { subnetRoutes: ['192.168.1.1/24'] }]) {
    await assert.rejects(setRoutingRequest('token', input))
  }
  assert.equal(fetch.mock.callCount(), 0)
})

test('routing writes time out, clear timers, and never retry automatically', async t => {
  let triggerTimeout, cleared = 0
  t.mock.method(globalThis, 'setTimeout', (callback, delay) => { assert.equal(delay, 60_000); triggerTimeout = callback; return 17 })
  t.mock.method(globalThis, 'clearTimeout', id => { assert.equal(id, 17); cleared++ })
  const fetch = t.mock.method(globalThis, 'fetch', async (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
  }))
  const request = setRoutingRequest('token', { subnetRoutes: [] })
  triggerTimeout()
  await assert.rejects(request, { name: 'AbortError' })
  assert.equal(fetch.mock.callCount(), 1); assert.equal(cleared, 1)
})

test('subnet defaults are enabled while pending, while saved custom routes and explicit off are preserved', () => {
  assert.deepEqual(routingDraft(settings({ subnetDefaultsPending: true })), { subnetEnabled: true, routeText: '192.168.42.0/24' })
  assert.deepEqual(routingDraft(settings({ subnetDefaultsPending: true, defaultSubnetRoutes: [] })), { subnetEnabled: true, routeText: '' })
  assert.equal(routingSummary(settings({ subnetDefaultsPending: true }), '', 'subnet').title, 'Pending')
  assert.deepEqual(routingDraft(settings()), { subnetEnabled: false, routeText: '192.168.42.0/24' })
  assert.deepEqual(routingDraft(settings({ subnetRoutes: ['10.20.0.0/16'] })), { subnetEnabled: true, routeText: '10.20.0.0/16' })
  assert.equal(routingDraft(settings({ defaultSubnetRoutes: [] })).routeText, '')
})

test('overview describes advertisements, not approval, and distinguishes disabled/stopped/unknown state', () => {
  for (const kind of ['exit', 'subnet']) {
    assert.equal(routingSummary(null, '', kind).title, 'Checking…')
    assert.equal(routingSummary(settings(), 'failed', kind).title, 'Unavailable')
    assert.equal(routingSummary(settings(), '', kind).title, kind === 'exit' ? 'Pending' : 'Not advertised')
    const advertised = settings({ advertiseExitNode: true, subnetRoutes: ['192.168.42.0/24'] })
    assert.equal(routingSummary(advertised, '', kind).title, 'Advertised')
    assert.match(routingSummary(advertised, '', kind).detail, /approval/)
    assert.equal(routingSummary({ ...advertised, backendState: 'Stopped' }, '', kind).title, 'Paused')
    assert.equal(routingSummary({ ...advertised, ipv4Forwarding: false }, '', kind).title, 'Needs OS setup')
    assert.equal(routingSummary({ ...advertised, snatEnabled: false }, '', kind).title, 'Check routing')
  }
  assert.match(routingSummary(settings({ usingExitNode: true }), '').detail, /another exit node/)
  assert.equal(forwardingWarnings(settings({ ipv6Forwarding: false }), false, ['192.168.1.0/24']).length, 0)
  assert.equal(forwardingWarnings(settings({ ipv6Forwarding: null }), true, []).length, 1)
})
