import assert from 'node:assert/strict'
import test from 'node:test'
import {
  ApiError,
  changePasswordRequest,
  isSessionError,
  loginRequest,
  tokenExpiry,
  tailscaleStatusRequest,
  tailscaleClientRequest,
  setTailscaleCredentialRequest,
  clearTailscaleCredentialRequest,
  shouldPromptForTailscale,
  isTailscaleConnected,
  tailscaleStatusLabel,
  deviceStatusRequest,
  formatDeviceUptime,
  deviceIPTypeLabel,
  validateDeviceIP,
  setDeviceIPRequest,
  deviceReconnectURL,
  networkActivityRequest,
} from '../src/api.ts'

function trafficSample(overrides = {}) {
  return { interfaceName: 'tailscale0', rxBytes: '18446744073709551615', txBytes: '0', sampledAt: '2026-09-24T12:00:00Z', counterEpoch: 'boot:3', ...overrides }
}

test('VPN traffic query uses bearer authentication, preserves uint64 precision, and supports cancellation', async (t) => {
  const sample = trafficSample(), token = jwt(), controller = new AbortController()
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { networkActivity: sample } }))
  assert.deepEqual(await networkActivityRequest(token, controller.signal), sample)
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query')
  assert.equal(options.method, 'POST')
  assert.equal(options.headers.Authorization, `Bearer ${token}`)
  assert.equal(options.signal, controller.signal)
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'NetworkActivity')
  assert.match(body.query, /networkActivity \{ interfaceName rxBytes txBytes sampledAt counterEpoch \}/)
  fetch.mock.mockImplementation(async (_url, options) => { options.signal.throwIfAborted() })
  await assert.rejects(networkActivityRequest(token, AbortSignal.abort()), { name: 'AbortError' })
})

test('VPN traffic rejects malformed or non-VPN samples', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  const invalid = [null, {}, [], trafficSample({ interfaceName: 'eth0' }), trafficSample({ sampledAt: 'invalid' }), trafficSample({ counterEpoch: ' ' })]
  for (const counter of [null, 123, -1, '', '-1', '+1', '01', '1.2', '1e3', 'NaN', '18446744073709551616', '9'.repeat(100)]) {
    invalid.push(trafficSample({ rxBytes: counter }), trafficSample({ txBytes: counter }))
  }
  for (const field of Object.keys(trafficSample())) {
    const sample = trafficSample(); delete sample[field]; invalid.push(sample)
  }
  for (const sample of invalid) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { networkActivity: sample } }))
    await assert.rejects(networkActivityRequest(jwt()), /valid VPN traffic counters/)
  }
})

test('VPN traffic preserves GraphQL and session errors without fabricating zero activity', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { networkActivity: trafficSample() }, errors: [{ message: 'Unable to read VPN traffic' }] }))
  await assert.rejects(networkActivityRequest(jwt()), { name: 'ApiError', status: 200, message: 'Unable to read VPN traffic' })
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(networkActivityRequest(jwt()), error => isSessionError(error))
  fetch.mock.mockImplementation(async () => new Response('Bad gateway', { status: 502 }))
  await assert.rejects(networkActivityRequest(jwt()), /VPN traffic request/)
  fetch.mock.mockImplementation(async () => { throw new TypeError('Failed to fetch') })
  await assert.rejects(networkActivityRequest(jwt()), /Failed to fetch/)
})

function staticDeviceIP(overrides = {}) {
  return { type: 'static', ip: '192.0.2.20/24', gateway: '192.0.2.1', dns: ['1.1.1.1'], ...overrides }
}

test('LAN IPv4 validation normalizes static and DHCP configuration', () => {
  assert.deepEqual(validateDeviceIP(staticDeviceIP({ type: ' STATIC ', ip: ' 192.0.2.20/24 ', gateway: ' 192.0.2.1 ', dns: [' 1.1.1.1 ', '1.1.1.1'] })), staticDeviceIP())
  assert.deepEqual(validateDeviceIP({ type: ' dhcp ', ip: '', gateway: '', dns: [] }), { type: 'DHCP', ip: '', gateway: '', dns: [] })
  for (const ip of ['192.0.2.20/24', '192.0.2.0/31', '192.0.2.20/32']) {
    assert.deepEqual(validateDeviceIP(staticDeviceIP({ ip, gateway: '', dns: [] })), staticDeviceIP({ ip, gateway: '', dns: [] }))
  }
  assert.equal(validateDeviceIP(staticDeviceIP({ ip: '192.0.2.0/31' })).gateway, '192.0.2.1')
})

test('invalid LAN settings never reach the API', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  const invalid = [{ type: 'unknown' }, { type: 'DHCP' }]
  for (const ip of ['', '192.0.2.20', '192.0.2.20/0', '192.0.2.20/33', '192.0.2.0/24', '192.0.2.255/24',
    '127.0.0.1/8', '0.1.2.3/24', '224.0.0.1/24', '255.255.255.255/32', '::ffff:192.0.2.20/120', 'fd00::2/64',
    '192.0.2.20/24;reboot', '192.000.2.20/24']) invalid.push({ ip })
  for (const gateway of ['192.0.3.1', '192.0.2.20', '192.0.2.0', '192.0.2.255', '::1', '192.0.2.1/24', '192.0.2.1;reboot']) invalid.push({ gateway })
  for (const dns of [[''], ['::1'], ['127.0.0.1'], ['224.0.0.1'], ['1.1.1.1;reboot'], Array(9).fill('1.1.1.1')]) invalid.push({ dns })
  for (const overrides of invalid) await assert.rejects(setDeviceIPRequest(jwt(), staticDeviceIP(overrides)))
  assert.equal(fetch.mock.callCount(), 0)
})

test('LAN mutation uses authenticated GraphQL variables and explicit success', async (t) => {
  const token = jwt()
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { setDeviceIP: true } }))
  await setDeviceIPRequest(token, staticDeviceIP())
  const [url, options] = fetch.mock.calls[0].arguments
  const body = JSON.parse(options.body)
  assert.equal(url, '/api/v1/query')
  assert.equal(options.method, 'POST')
  assert.equal(options.headers.Authorization, `Bearer ${token}`)
  assert.equal(body.operationName, 'SetDeviceIP')
  assert.match(body.query, /\$deviceIP: DeviceIP!/)
  assert.equal(body.query.includes('192.0.2.20'), false)
  assert.deepEqual(body.variables, { deviceIP: staticDeviceIP() })
  assert.ok(options.signal instanceof AbortSignal)
  await setDeviceIPRequest(token, { type: 'DHCP', ip: '', gateway: '', dns: [] })
  assert.deepEqual(JSON.parse(fetch.mock.calls[1].arguments[1].body).variables, { deviceIP: { type: 'DHCP', ip: '', gateway: '', dns: [] } })
  for (const payload of [{ data: { setDeviceIP: false } }, { data: { setDeviceIP: 'true' } }, { data: {} }, { data: null }, null]) {
    fetch.mock.mockImplementation(async () => Response.json(payload))
    await assert.rejects(setDeviceIPRequest(token, staticDeviceIP()))
  }
  fetch.mock.mockImplementation(async () => Response.json({ data: { setDeviceIP: true }, errors: [{ message: 'Only portal administrators can change LAN settings' }] }))
  await assert.rejects(setDeviceIPRequest(token, staticDeviceIP()), { name: 'ApiError', status: 200, isGraphQLError: true, message: 'Only portal administrators can change LAN settings' })
  fetch.mock.mockImplementation(async () => new Response('{"data":', { status: 200 }))
  await assert.rejects(setDeviceIPRequest(token, staticDeviceIP()), { name: 'ApiError', status: 200, isGraphQLError: false })
  fetch.mock.mockImplementation(async () => new Response('Bad gateway', { status: 502 }))
  await assert.rejects(setDeviceIPRequest(token, staticDeviceIP()), { name: 'ApiError', status: 502, isGraphQLError: false })
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(setDeviceIPRequest(token, staticDeviceIP()), (error) => isSessionError(error))
})

test('LAN requests time out, clean up the timer, and do not retry on disconnection', async (t) => {
  let timeout
  const handle = {}
  t.mock.method(globalThis, 'setTimeout', (callback, milliseconds) => { assert.equal(milliseconds, 60_000); timeout = callback; return handle })
  const clear = t.mock.method(globalThis, 'clearTimeout', (timer) => assert.equal(timer, handle))
  const fetch = t.mock.method(globalThis, 'fetch', async (_url, options) => { timeout(); options.signal.throwIfAborted() })
  await assert.rejects(setDeviceIPRequest(jwt(), staticDeviceIP()), { name: 'AbortError' })
  assert.equal(clear.mock.callCount(), 1)
  fetch.mock.mockImplementation(async () => { throw new TypeError('Failed to fetch') })
  await assert.rejects(setDeviceIPRequest(jwt(), staticDeviceIP()), /Failed to fetch/)
  assert.equal(fetch.mock.callCount(), 2)
  assert.equal(clear.mock.callCount(), 2)
})

test('reconnect link keeps protocol/port/path without carrying tokens or credentials', () => {
  assert.equal(deviceReconnectURL('https://user:password@old.test:8443/portal/?token=secret#/settings', '192.0.2.20/24'), 'https://192.0.2.20:8443/portal/#/login')
  assert.equal(deviceReconnectURL('http://[fd00::2]:8080/#/settings', '192.0.2.20/24'), 'http://192.0.2.20:8080/#/login')
  for (const ip of ['192.0.2.20@evil.test', 'javascript:alert(1)', '::1', '127.0.0.1', '999.0.0.1']) {
    assert.equal(deviceReconnectURL('https://portal.test', ip), null)
  }
  assert.equal(deviceReconnectURL('file:///tmp/portal.html', '192.0.2.20/24'), null)
})

function jwt(payload = {}) {
  const header = Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url')
  const claims = Buffer.from(JSON.stringify({
    pid: 1, exp: Math.floor(Date.now() / 1000) + 3600, ...payload,
  })).toString('base64url')
  return `${header}.${claims}.signature`
}

function makeStatus(overrides = {}) {
  return { backendState: 'NeedsLogin', haveNodeKey: false, tailscaleIPs: [], currentTailnet: null, self: null, peers: [], ...overrides }
}

function makePeer(overrides = {}) {
  return {
    id: 'peer-a', hostName: 'desktop', dnsName: 'desktop.example.ts.net.', os: 'linux',
    tailscaleIPs: ['100.64.0.2', 'fd7a:115c:a1e0::2'], online: true, ...overrides,
  }
}

function makeDeviceStatus(overrides = {}) {
  return {
    hostname: 'nanotail', lanIPType: 'DHCP', lanIP: '192.0.2.2/24', gateway: '192.0.2.1',
    dns: ['192.0.2.53', '2001:db8::53'], lanIPv6Type: 'auto', lanIPv6: 'fd00::2/64', gateway6: 'fe80::1',
    ethAddr: '02:00:00:00:00:01', cpuload: 42, memory: 65,
    lastRestart: '2026-09-23T10:20:30Z', uptime: 90061, health: 'healthy', ...overrides,
  }
}

test('device status selects every field with bearer authentication and an abort signal', async (t) => {
  const status = makeDeviceStatus()
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { deviceStatus: status } }))
  const token = jwt()
  const controller = new AbortController()
  assert.deepEqual(await deviceStatusRequest(token, controller.signal), status)
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query')
  assert.equal(options.method, 'POST')
  assert.equal(options.headers.Authorization, `Bearer ${token}`)
  assert.equal(options.headers['Content-Type'], 'application/json')
  assert.equal(options.signal, controller.signal)
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'DeviceStatus')
  assert.deepEqual(body.variables, {})
  assert.equal(body.query.replace(/\s+/g, ' ').trim(),
    'query DeviceStatus { deviceStatus { hostname lanIPType lanIP gateway dns lanIPv6Type lanIPv6 gateway6 ethAddr cpuload memory lastRestart uptime health } }')
})

test('device status accepts empty networks, zero usage, and 64-bit uptime', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const overrides of [
    { lanIP: '', gateway: '', lanIPv6: '', gateway6: '', dns: [], lanIPType: 'unknown', lanIPv6Type: 'disabled', cpuload: 0, memory: 100, uptime: 0 },
    { cpuload: 100, memory: 0, uptime: 5_000_000_000 },
  ]) {
    const status = makeDeviceStatus(overrides)
    fetch.mock.mockImplementation(async () => Response.json({ data: { deviceStatus: status } }))
    assert.deepEqual(await deviceStatusRequest(jwt()), status)
  }
})

test('device status rejects missing and malformed measurements', async (t) => {
  const invalid = [null, [], {}, ...Object.keys(makeDeviceStatus()).map((field) => makeDeviceStatus({ [field]: undefined })),
    ...['lanIPType', 'lanIP', 'gateway', 'lanIPv6Type', 'lanIPv6', 'gateway6'].flatMap((field) =>
      [null, [], 42].map((value) => makeDeviceStatus({ [field]: value }))),
    makeDeviceStatus({ dns: null }), makeDeviceStatus({ dns: [null] }), makeDeviceStatus({ dns: '192.0.2.53' }),
    { lanIPs: ['192.0.2.2/24'], gatewayIP: ['192.0.2.1'] },
    makeDeviceStatus({ hostname: {} }), makeDeviceStatus({ ethAddr: 42 }), makeDeviceStatus({ health: true }),
    makeDeviceStatus({ lastRestart: 'not a date' }), makeDeviceStatus({ lastRestart: 123 }),
    ...['cpuload', 'memory'].flatMap((field) => [-1, 101, 1.5, '42', null].map((value) => makeDeviceStatus({ [field]: value }))),
    ...[-1, 1.5, '90061', Number.MAX_SAFE_INTEGER + 1].map((uptime) => makeDeviceStatus({ uptime })),
  ]
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const status of invalid) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { deviceStatus: status } }))
    await assert.rejects(deviceStatusRequest(jwt()), /valid device status/)
  }
})

test('device errors override data, preserve session errors, and use device-specific fallbacks', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({
    data: { deviceStatus: makeDeviceStatus() }, errors: [{ message: 'Unable to read device status' }],
  }))
  await assert.rejects(deviceStatusRequest(jwt()), { name: 'ApiError', status: 200, message: 'Unable to read device status' })
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(deviceStatusRequest(jwt()), (error) => isSessionError(error))
  fetch.mock.mockImplementation(async () => new Response('Bad gateway', { status: 502 }))
  await assert.rejects(deviceStatusRequest(jwt()), { name: 'ApiError', status: 502, message: 'Unable to complete the device request. Please retry.' })
  for (const payload of [null, { data: null }, { data: {}, errors: {} }]) {
    fetch.mock.mockImplementation(async () => Response.json(payload))
    await assert.rejects(deviceStatusRequest(jwt()), /invalid device response/)
  }
  fetch.mock.mockImplementation(async () => { throw new TypeError('Failed to fetch') })
  await assert.rejects(deviceStatusRequest(jwt()), /Failed to fetch/)
  fetch.mock.mockImplementation(async (_url, options) => { options.signal.throwIfAborted() })
  await assert.rejects(deviceStatusRequest(jwt(), AbortSignal.abort()), { name: 'AbortError' })
})

test('device uptime formatting handles seconds, minutes, days, and long-running systems', () => {
  for (const [seconds, expected] of [[0, '0s'], [59, '59s'], [60, '1m'], [3599, '59m'],
    [3600, '1h 0m'], [86399, '23h 59m'], [86400, '1d 0h 0m'], [90061, '1d 1h 1m'],
    [5_000_000_000, '57870d 8h 53m']]) {
    assert.equal(formatDeviceUptime(seconds), expected)
  }
  for (const seconds of [-1, NaN, Infinity, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
    assert.equal(formatDeviceUptime(seconds), 'Unavailable')
  }
})

test('device address modes distinguish static, DHCP, IPv6 autoconfiguration, and unknown', () => {
  assert.equal(deviceIPTypeLabel('static'), 'Static')
  assert.equal(deviceIPTypeLabel('DHCP'), 'DHCP')
  assert.equal(deviceIPTypeLabel('DHCP', true), 'DHCPv6')
  assert.equal(deviceIPTypeLabel('auto', true), 'Automatic (SLAAC / DHCPv6)')
  assert.equal(deviceIPTypeLabel('unknown'), 'Unknown')
  assert.equal(deviceIPTypeLabel(''), 'Unknown')
  assert.equal(deviceIPTypeLabel('disabled', true), 'Disabled')
  assert.equal(deviceIPTypeLabel('link-local', true), 'Link-local only')
  assert.equal(deviceIPTypeLabel('ignore', true), 'Not managed')
  assert.equal(deviceIPTypeLabel('shared'), 'Shared connection')
  assert.equal(deviceIPTypeLabel('future'), 'future')
})

test('JWT expiry supports current tokens and rejects malformed, expired, or legacy sessions', () => {
  assert.equal(tokenExpiry(jwt({ exp: 100 }), 99_000), 100_000)
  for (const token of [
    '', 'legacy-opaque-token', 'a.b.c', 'a.b', 'a..c',
    jwt({ exp: 100 }), jwt({ exp: '1000' }), jwt({ exp: null }),
    jwt({ pid: 0 }), jwt({ pid: -1 }), jwt({ pid: '1' }),
  ]) {
    assert.equal(tokenExpiry(token, 100_000), null)
  }
})

test('credential query never requests or retains the saved secret', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: {
    tailscaleClient: { clientId: 'client', hasClientSecret: true, updateTime: '2026-09-23T00:00:00Z', clientSecret: 'must-not-retain' },
  } }))
  const client = await tailscaleClientRequest(jwt())
  assert.deepEqual(client, { clientId: 'client', hasClientSecret: true, updateTime: '2026-09-23T00:00:00Z' })
  assert.equal(JSON.parse(fetch.mock.calls[0].arguments[1].body).query.includes('clientSecret'), false)
  fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleClient: null } }))
  assert.equal(await tailscaleClientRequest(jwt()), null)
})

test('credentials use GraphQL variables; retaining a secret omits it', async (t) => {
  const token = jwt()
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { setTailscaleCredential: true } }))
  await setTailscaleCredentialRequest(token, { clientId: ' client ', clientSecret: ' test-secret ' })
  const [url, options] = fetch.mock.calls[0].arguments
  const body = JSON.parse(options.body)
  assert.equal(url, '/api/v1/query')
  assert.equal(options.headers.Authorization, `Bearer ${token}`)
  assert.equal(body.operationName, 'SetTailscaleCredential')
  assert.deepEqual(body.variables, { credential: { clientId: 'client', clientSecret: 'test-secret' } })
  assert.equal(body.query.includes('test-secret'), false)
  await setTailscaleCredentialRequest(token, { clientId: 'client', clientSecret: '' })
  assert.deepEqual(JSON.parse(fetch.mock.calls[1].arguments[1].body).variables, { credential: { clientId: 'client' } })
})

test('credential input validation avoids sending invalid data', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const credential of [
    { clientId: ' ' }, { clientId: 'client id' }, { clientId: 'x'.repeat(513) },
    { clientId: 'client', clientSecret: 'line\nbreak' }, { clientId: 'client', clientSecret: 'x'.repeat(4097) },
  ]) await assert.rejects(setTailscaleCredentialRequest(jwt(), credential), /valid client ID/)
  assert.equal(fetch.mock.callCount(), 0)
})

test('credential mutations require explicit success and preserve GraphQL errors', async (t) => {
  for (const [field, request] of [
    ['setTailscaleCredential', () => setTailscaleCredentialRequest(jwt(), { clientId: 'client', clientSecret: 'secret' })],
    ['clearTailscaleCredential', () => clearTailscaleCredentialRequest(jwt())],
  ]) {
    await t.test(field, async (t) => {
      const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { [field]: true } }))
      await request()
      for (const payload of [{ data: { [field]: false } }, { data: {} }, null, { data: { [field]: true }, errors: {} }]) {
        fetch.mock.mockImplementation(async () => Response.json(payload))
        await assert.rejects(request())
      }
      fetch.mock.mockImplementation(async () => Response.json({ data: { [field]: true }, errors: [{ message: 'Internal Server Error' }] }))
      await assert.rejects(request(), { name: 'ApiError', status: 200, message: 'Internal Server Error' })
      fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
      await assert.rejects(request(), (error) => isSessionError(error))
    })
  }
})

test('status query uses the new schema, bearer authentication, and abort signal', async (t) => {
  const status = makeStatus({
    backendState: 'Running', tailscaleIPs: ['100.64.0.1'], currentTailnet: { name: 'example.test' },
    haveNodeKey: true, self: { online: true, keyExpiry: '2027-01-01T00:00:00Z' }, peers: [makePeer(), makePeer({ id: 'peer-b', online: false })],
  })
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { tailscaleStatus: status } }))
  const token = jwt()
  const controller = new AbortController()
  assert.deepEqual(await tailscaleStatusRequest(token, controller.signal), status)
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query')
  assert.equal(options.method, 'POST')
  assert.equal(options.headers.Authorization, `Bearer ${token}`)
  assert.equal(options.headers['Content-Type'], 'application/json')
  assert.equal(options.signal, controller.signal)
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'TailscaleStatus')
  assert.deepEqual(body.variables, {})
  assert.equal(body.query.replace(/\s+/g, ' ').trim(),
    'query TailscaleStatus { tailscaleStatus { backendState haveNodeKey tailscaleIPs currentTailnet { name } self { online keyExpiry } peers { id hostName dnsName os tailscaleIPs online } } }')
  assert.doesNotMatch(body.query, /\b(connected|needsLogin|tailnet|ips|authURL)\b/)
})

test('status accepts null tailnet/self and empty lists before login', async (t) => {
  const status = makeStatus()
  t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { tailscaleStatus: status } }))
  assert.deepEqual(await tailscaleStatusRequest(jwt()), status)
})

test('status accepts a configured key with no expiry and preserves nullable expiry metadata', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const keyExpiry of [null, '2026-09-25T12:00:00Z', '2020-01-01T00:00:00Z']) {
    const status = makeStatus({ haveNodeKey: true, self: { online: true, keyExpiry } })
    fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleStatus: status } }))
    assert.deepEqual(await tailscaleStatusRequest(jwt()), status)
  }
})

test('status rejects legacy responses and malformed nested fields', async (t) => {
  const invalid = [
    null, [], {}, { backendState: 'Running', connected: true, needsLogin: false, tailnet: 'old', ips: [] },
    makeStatus({ backendState: null }), makeStatus({ tailscaleIPs: null }), makeStatus({ tailscaleIPs: [42] }),
    makeStatus({ haveNodeKey: undefined }), makeStatus({ haveNodeKey: 'true' }),
    makeStatus({ currentTailnet: undefined }), makeStatus({ currentTailnet: [] }),
    makeStatus({ currentTailnet: 'example.test' }), makeStatus({ currentTailnet: { name: null } }),
    makeStatus({ self: undefined }), makeStatus({ self: [] }), makeStatus({ self: {} }),
    makeStatus({ self: { online: 'false' } }), makeStatus({ peers: null }), makeStatus({ peers: {} }),
    ...[undefined, '', 123, 'not-a-date'].map(keyExpiry => makeStatus({ self: { online: true, keyExpiry } })),
    makeStatus({ peers: [null] }), makeStatus({ peers: [{}] }),
    ...['id', 'hostName', 'dnsName', 'os', 'tailscaleIPs', 'online'].map((field) =>
      makeStatus({ peers: [makePeer({ [field]: undefined })] })),
    makeStatus({ peers: [makePeer({ online: 'true' })] }),
    makeStatus({ peers: [makePeer({ tailscaleIPs: [42] })] }),
  ]
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const status of invalid) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleStatus: status } }))
    await assert.rejects(tailscaleStatusRequest(jwt()), /valid Tailscale status/)
  }
})

test('status errors take precedence over data and preserve session/network handling', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({
    data: { tailscaleStatus: makeStatus() }, errors: [{ message: 'Unable to read Tailscale status' }],
  }))
  await assert.rejects(tailscaleStatusRequest(jwt()), { name: 'ApiError', status: 200, message: 'Unable to read Tailscale status' })
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(tailscaleStatusRequest(jwt()), (error) => isSessionError(error))
  fetch.mock.mockImplementation(async () => { throw new TypeError('Failed to fetch') })
  await assert.rejects(tailscaleStatusRequest(jwt()), /Failed to fetch/)
})

test('status failures and stopped/offline devices do not trigger a credential prompt', () => {
  const status = makeStatus()
  assert.equal(shouldPromptForTailscale(status, ''), true)
  assert.equal(shouldPromptForTailscale(status, 'Status unavailable'), false)
  assert.equal(shouldPromptForTailscale(null, ''), false)
  for (const backendState of ['Running', 'Stopped', 'Starting', 'NeedsMachineAuth', 'NoState', 'FutureState']) {
    for (const self of [null, { online: false }, { online: true }]) {
      assert.equal(shouldPromptForTailscale(makeStatus({ backendState, self }), ''), false)
    }
  }
})

test('connection labels distinguish backend state, offline/missing self, and stale status', () => {
  assert.equal(isTailscaleConnected(null), false)
  assert.equal(tailscaleStatusLabel(null, ''), 'Checking…')
  assert.equal(tailscaleStatusLabel(null, 'Status unavailable'), 'Unavailable')
  for (const [backendState, self, connected, label] of [
    ['Running', { online: true }, true, 'Connected'],
    ['Running', { online: false }, false, 'Not connected'],
    ['Running', null, false, 'Not connected'],
    ['NeedsLogin', null, false, 'Needs setup'],
    ['Stopped', { online: true }, false, 'Stopped'],
    ['Starting', null, false, 'Starting'],
    ['NeedsMachineAuth', null, false, 'Awaiting approval'],
    ['NoState', null, false, 'Not connected'],
    ['FutureState', null, false, 'Not connected'],
  ]) {
    const status = makeStatus({ backendState, self })
    assert.equal(isTailscaleConnected(status), connected)
    assert.equal(tailscaleStatusLabel(status, ''), label)
    assert.equal(isTailscaleConnected(status, 'Failed to refresh'), false)
    assert.equal(tailscaleStatusLabel(status, 'Failed to refresh'), 'Unavailable')
  }
})

test('login posts credentials to /api/login and reads token', async (t) => {
  const token = jwt()
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ token }))
  const credentials = { username: 'admin', password: 'admin' }
  assert.equal(await loginRequest(credentials), token)
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/login')
  assert.equal(options.method, 'POST')
  assert.equal(options.headers['Content-Type'], 'application/json')
  assert.deepEqual(JSON.parse(options.body), credentials)
})

test('login rejects missing, old-format, invalid, and expired tokens', async (t) => {
  for (const payload of [null, {}, { access_token: jwt() }, { token: 42 }, { token: 'invalid' }, { token: jwt({ exp: 1 }) }]) {
    await t.test(JSON.stringify(payload), async (t) => {
      t.mock.method(globalThis, 'fetch', async () => Response.json(payload))
      await assert.rejects(loginRequest({ username: 'admin', password: 'admin' }), /valid login token/)
    })
  }
})

test('login surfaces backend errors and tolerates non-JSON errors', async (t) => {
  await t.test('backend message', async (t) => {
    t.mock.method(globalThis, 'fetch', async () => Response.json({ message: 'Unauthorized error' }, { status: 401 }))
    await assert.rejects(loginRequest({ username: 'admin', password: 'wrong' }), {
      name: 'ApiError', status: 401, message: 'Unauthorized error',
    })
  })
  await t.test('proxy error', async (t) => {
    t.mock.method(globalThis, 'fetch', async () => new Response('Bad gateway', { status: 502 }))
    await assert.rejects(loginRequest({ username: 'admin', password: 'admin' }), {
      name: 'ApiError', status: 502, message: 'Unable to sign in. Check your credentials.',
    })
  })
})

test('password change sends a named GraphQL mutation with bearer auth and variables', async (t) => {
  const token = jwt()
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { changePassword: true } }))
  const passwords = { oldpassword: 'old"\\password', newpassword: 'new"\\password\n界' }
  await changePasswordRequest(token, passwords)
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query')
  assert.equal(options.method, 'POST')
  assert.equal(options.headers.Authorization, `Bearer ${token}`)
  assert.equal(options.headers['Content-Type'], 'application/json')
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'ChangePassword')
  assert.equal(body.query.replace(/\s+/g, ' ').trim(),
    'mutation ChangePassword($passwords: ChangePassword!) { changePassword(passwords: $passwords) }')
  assert.deepEqual(body.variables, { passwords })
  assert.equal(body.query.includes(passwords.oldpassword), false)
  assert.equal(body.query.includes(passwords.newpassword), false)
})

test('password validation uses UTF-8 bytes and permits both boundaries', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { changePassword: true } }))
  for (const password of ['', 'a'.repeat(7), 'a'.repeat(73), '界'.repeat(25)]) {
    await assert.rejects(changePasswordRequest(jwt(), { oldpassword: 'admin', newpassword: password }), /8 and 72 bytes/)
  }
  assert.equal(fetch.mock.callCount(), 0)
  for (const password of ['a'.repeat(8), 'a'.repeat(72), '界'.repeat(3), '界'.repeat(24)]) {
    await changePasswordRequest(jwt(), { oldpassword: 'admin', newpassword: password })
  }
  assert.equal(fetch.mock.callCount(), 4)
})

test('GraphQL resolver errors keep the session usable for retry', async (t) => {
  for (const message of [
    'Unauthorized error',
    'New password must contain between 8 and 72 bytes',
    'Internal Server Error',
  ]) {
    await t.test(message, async (t) => {
      const token = jwt()
      const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({
        data: null, errors: [{ message, path: ['changePassword'] }],
      }))
      await assert.rejects(
        changePasswordRequest(token, { oldpassword: 'admin', newpassword: 'new-password' }),
        (error) => {
          assert.equal(error.name, 'ApiError')
          assert.equal(error.status, 200)
          assert.equal(error.message, message)
          assert.equal(isSessionError(error), false)
          return true
        },
      )

      fetch.mock.mockImplementation(async () => Response.json({ data: { changePassword: true } }))
      await changePasswordRequest(token, { oldpassword: 'admin', newpassword: 'new-password' })
      assert.equal(fetch.mock.callCount(), 2)
      for (const call of fetch.mock.calls) {
        assert.equal(call.arguments[1].headers.Authorization, `Bearer ${token}`)
      }
    })
  }
})

test('GraphQL errors take precedence over data, including non-200 validation errors', async (t) => {
  for (const status of [200, 422]) {
    await t.test(String(status), async (t) => {
      t.mock.method(globalThis, 'fetch', async () => Response.json({
        data: { changePassword: true },
        errors: [{ message: 'Password rejected' }, { message: 'Please retry' }],
      }, { status }))
      await assert.rejects(
        changePasswordRequest(jwt(), { oldpassword: 'admin', newpassword: 'new-password' }),
        { name: 'ApiError', status, message: 'Password rejected\nPlease retry' },
      )
    })
  }
})

test('password change rejects false, missing, malformed, and legacy success responses', async (t) => {
  const responses = [
    ['false result', () => Response.json({ data: { changePassword: false } })],
    ['null result', () => Response.json({ data: { changePassword: null } })],
    ['string result', () => Response.json({ data: { changePassword: 'true' } })],
    ['missing result', () => Response.json({ data: {} })],
    ['missing data', () => Response.json({})],
    ['null data', () => Response.json({ data: null })],
    ['null response', () => Response.json(null)],
    ['invalid JSON', () => new Response('not JSON')],
    ['legacy 204', () => new Response(null, { status: 204 })],
    ['invalid errors shape', () => Response.json({ data: { changePassword: true }, errors: {} })],
  ]
  for (const [name, response] of responses) {
    await t.test(name, async (t) => {
      t.mock.method(globalThis, 'fetch', async () => response())
      await assert.rejects(
        changePasswordRequest(jwt(), { oldpassword: 'admin', newpassword: 'new-password' }),
        /did not confirm the password change/,
      )
    })
  }
})

test('malformed GraphQL errors still prevent success and use a safe fallback', async (t) => {
  t.mock.method(globalThis, 'fetch', async () => Response.json({
    data: { changePassword: true }, errors: [null, {}, { message: 42 }, { message: '' }],
  }))
  await assert.rejects(
    changePasswordRequest(jwt(), { oldpassword: 'admin', newpassword: 'new-password' }),
    { name: 'ApiError', status: 200, message: 'Unable to change your password. Please try again.' },
  )
})

test('HTTP 401 from JWT middleware is a session failure; HTTP 500 is not', async (t) => {
  for (const [status, message] of [[401, 'invalid or expired jwt'], [500, 'Internal Server Error']]) {
    await t.test(String(status), async (t) => {
      t.mock.method(globalThis, 'fetch', async () => Response.json({ message }, { status }))
      await assert.rejects(
        changePasswordRequest(jwt(), { oldpassword: 'admin', newpassword: 'new-password' }),
        (error) => {
          assert.equal(error.status, status)
          assert.equal(error.message, message)
          assert.equal(isSessionError(error), status === 401)
          return true
        },
      )
    })
  }
  assert.equal(isSessionError(new ApiError('Unauthorized error', 401)), true)
  assert.equal(isSessionError(new Error('Network error')), false)
})

test('password change handles non-JSON HTTP failures and network failures', async (t) => {
  await t.test('proxy error', async (t) => {
    t.mock.method(globalThis, 'fetch', async () => new Response('Bad gateway', { status: 502 }))
    await assert.rejects(
      changePasswordRequest(jwt(), { oldpassword: 'admin', newpassword: 'new-password' }),
      { name: 'ApiError', status: 502, message: 'Unable to change your password. Please try again.' },
    )
  })
  await t.test('network error', async (t) => {
    t.mock.method(globalThis, 'fetch', async () => { throw new TypeError('Failed to fetch') })
    await assert.rejects(
      changePasswordRequest(jwt(), { oldpassword: 'admin', newpassword: 'new-password' }),
      { name: 'TypeError', message: 'Failed to fetch' },
    )
  })
})
