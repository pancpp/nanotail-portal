import assert from 'node:assert/strict'
import test from 'node:test'
import { DEFAULT_PEER_RELAY_PORT, setPeerRelayRequest, isSessionError } from '../src/api.ts'

test('relay settings use an authenticated mutation with explicit port and confirmed success', async t => {
  assert.equal(DEFAULT_PEER_RELAY_PORT, 40001)
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { setPeerRelay: true } }))
  for (const input of [{ enabled: true, port: 40001 }, { enabled: true, port: 65535 }, { enabled: false, port: 40001 }]) {
    await setPeerRelayRequest('token', input)
    const [url, options] = fetch.mock.calls.at(-1).arguments
    assert.equal(url, '/api/v1/query'); assert.equal(options.headers.Authorization, 'Bearer token')
    const body = JSON.parse(options.body)
    assert.equal(body.operationName, 'SetPeerRelay'); assert.deepEqual(body.variables, { input })
    assert.match(body.query, /\$input: PeerRelayInput!/)
  }
  for (const value of [false, null, undefined, 'true']) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { setPeerRelay: value } }))
    await assert.rejects(setPeerRelayRequest('token', { enabled: true, port: 40001 }), /did not confirm/)
  }
  fetch.mock.mockImplementation(async () => Response.json({ data: { setPeerRelay: true }, errors: [{ message: 'Only administrators' }] }))
  await assert.rejects(setPeerRelayRequest('token', { enabled: true, port: 40001 }), /Only administrators/)
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(setPeerRelayRequest('token', { enabled: false, port: 40001 }), isSessionError)
})

test('invalid ports or enabled values never issue writes', async t => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const input of [null, {}, { enabled: 'true', port: 40001 }, ...[0, -1, 65536, 1.5, NaN, '40001'].map(port => ({ enabled: true, port }))]) {
    await assert.rejects(setPeerRelayRequest('token', input), /UDP port/)
  }
  assert.equal(fetch.mock.callCount(), 0)
})

test('timed out writes clear timers and do not retry', async t => {
  let timeout, cleared = 0
  t.mock.method(globalThis, 'setTimeout', (callback, delay) => { assert.equal(delay, 60000); timeout = callback; return 3 })
  t.mock.method(globalThis, 'clearTimeout', id => { assert.equal(id, 3); cleared++ })
  const fetch = t.mock.method(globalThis, 'fetch', async (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
  }))
  const request = setPeerRelayRequest('token', { enabled: true, port: 40001 })
  timeout()
  await assert.rejects(request, { name: 'AbortError' })
  assert.equal(fetch.mock.callCount(), 1); assert.equal(cleared, 1)
})
