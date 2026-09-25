import assert from 'node:assert/strict'
import test from 'node:test'
import { tailscaleConnectionRequest, setTailscaleEnabledRequest, isSessionError } from '../src/api.ts'

const connection = { enabled: false, backendState: 'Stopped', canEnable: true }

test('connection query authenticates, validates actual enabled preference and supports cancellation', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { tailscaleConnection: connection } }))
  const controller = new AbortController()
  assert.deepEqual(await tailscaleConnectionRequest('token', controller.signal), connection)
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query'); assert.equal(options.headers.Authorization, 'Bearer token')
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'TailscaleConnection'); assert.match(body.query, /enabled backendState canEnable/)
  controller.abort(); assert.equal(options.signal.aborted, true)
  const invalid = [null, {}, [], { ...connection, enabled: 'false' }, { ...connection, canEnable: 1 }, { ...connection, backendState: '' }]
  for (const field of Object.keys(connection)) { const value = { ...connection }; delete value[field]; invalid.push(value) }
  for (const value of invalid) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleConnection: value } }))
    await assert.rejects(tailscaleConnectionRequest('token'), /valid tailnet connection/)
  }
  fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleConnection: connection }, errors: [{ message: 'Daemon unavailable' }] }))
  await assert.rejects(tailscaleConnectionRequest('token'), /Daemon unavailable/)
})

test('connection mutation sends explicit boolean variables and requires confirmed success', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { setTailscaleEnabled: true } }))
  for (const enabled of [false, true]) {
    await setTailscaleEnabledRequest('token', enabled)
    const [url, options] = fetch.mock.calls.at(-1).arguments
    assert.equal(url, '/api/v1/query'); assert.equal(options.headers.Authorization, 'Bearer token')
    const body = JSON.parse(options.body)
    assert.equal(body.operationName, 'SetTailscaleEnabled'); assert.deepEqual(body.variables, { enabled })
    assert.match(body.query, /\$enabled: Boolean!/); assert.ok(options.signal instanceof AbortSignal)
  }
  for (const value of [false, null, 'true', undefined]) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { setTailscaleEnabled: value } }))
    await assert.rejects(setTailscaleEnabledRequest('token', false), /did not confirm/)
  }
  fetch.mock.mockImplementation(async () => Response.json({ data: { setTailscaleEnabled: true }, errors: [{ message: 'Only administrators' }] }))
  await assert.rejects(setTailscaleEnabledRequest('token', false), { message: 'Only administrators', isGraphQLError: true })
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(setTailscaleEnabledRequest('token', false), error => isSessionError(error))
  fetch.mock.mockImplementation(async () => new Response('Bad gateway', { status: 502 }))
  await assert.rejects(setTailscaleEnabledRequest('token', false), /tailnet connection request/)
})

test('invalid connection input never reaches the API', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const enabled of [undefined, null, 0, 1, 'true', 'false', {}]) await assert.rejects(setTailscaleEnabledRequest('token', enabled), /Choose whether/)
  assert.equal(fetch.mock.callCount(), 0)
})

test('connection writes time out without retries and release their timeout', async (t) => {
  let triggerTimeout, cleared = 0
  t.mock.method(globalThis, 'setTimeout', (callback, delay) => { assert.equal(delay, 60_000); triggerTimeout = callback; return 17 })
  t.mock.method(globalThis, 'clearTimeout', id => { assert.equal(id, 17); cleared++ })
  const fetch = t.mock.method(globalThis, 'fetch', async (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
  }))
  const request = setTailscaleEnabledRequest('token', false)
  triggerTimeout()
  await assert.rejects(request, { name: 'AbortError' })
  assert.equal(fetch.mock.callCount(), 1); assert.equal(cleared, 1)
})
