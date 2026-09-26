import assert from 'node:assert/strict'
import test from 'node:test'
import { tailscalePeerLatenciesRequest, tailscaleStatusRequest, isSessionError } from '../src/api.ts'

test('latency probes use a separate authenticated, cancellable request', async t => {
  const peers = [{ id: 'online', latencyMs: 12 }, { id: 'offline', latencyMs: null }, { id: 'fast', latencyMs: 0 }]
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { tailscaleStatus: { peers } } }))
  const controller = new AbortController()
  assert.deepEqual(await tailscalePeerLatenciesRequest('token', controller.signal), peers)
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query')
  assert.equal(options.headers.Authorization, 'Bearer token')
  assert.equal(options.cache, 'no-store')
  assert.equal(JSON.parse(options.body).operationName, 'TailscalePeerLatencies')
  assert.match(JSON.parse(options.body).query, /latencyMs/)
  controller.abort()
  assert.equal(options.signal.aborted, true)

  fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleStatus: {
    backendState: 'Running', haveNodeKey: true, tailscaleIPs: [], currentTailnet: null, self: null, peers: [],
  } } }))
  await tailscaleStatusRequest('token')
  assert.doesNotMatch(JSON.parse(fetch.mock.calls.at(-1).arguments[1].body).query, /latencyMs/)
})

test('latency responses reject malformed values and propagate request failures', async t => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const peers of [null, {}, [null], [{}], [{ id: 1, latencyMs: 1 }], [{ id: 'x' }],
    [{ id: 'x', latencyMs: -1 }], [{ id: 'x', latencyMs: '12' }]]) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleStatus: { peers } } }))
    await assert.rejects(tailscalePeerLatenciesRequest('token'), /valid peer latencies/)
  }
  fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleStatus: { peers: [] } } }))
  assert.deepEqual(await tailscalePeerLatenciesRequest('token'), [])
  fetch.mock.mockImplementation(async () => Response.json({ errors: [{ message: 'Unavailable' }] }))
  await assert.rejects(tailscalePeerLatenciesRequest('token'), /Unavailable/)
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(tailscalePeerLatenciesRequest('token'), isSessionError)
})
