import assert from 'node:assert/strict'
import test from 'node:test'
import { portalVersionRequest, isSessionError } from '../src/api.ts'

test('portal version is authenticated, uncached, cancellable and preserves exact build metadata', async t => {
  const controller = new AbortController()
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const version of ['0.2.0', 'v0.2.0-12-gabc1234-dirty', 'b2f51fd', '', '  ']) {
    fetch.mock.mockImplementation(async () => Response.json({data:{portalVersion:version}}))
    assert.equal(await portalVersionRequest('test-token', controller.signal), version)
  }
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query')
  assert.equal(options.headers.Authorization, 'Bearer test-token')
  assert.equal(options.cache, 'no-store')
  assert.equal(options.signal, controller.signal)
  assert.deepEqual(JSON.parse(options.body), {
    operationName:'PortalVersion', query:'query PortalVersion { portalVersion }', variables:{},
  })
})

test('portal version rejects malformed values and preserves server, session and cancellation errors', async t => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const version of [undefined, null, 123, false, {}, ['0.1.0']]) {
    fetch.mock.mockImplementation(async () => Response.json({data:{portalVersion:version}}))
    await assert.rejects(portalVersionRequest('test-token'), /valid portal version/)
  }
  fetch.mock.mockImplementation(async () => Response.json({data:{portalVersion:'stale'},errors:[{message:'Version unavailable'}]}))
  await assert.rejects(portalVersionRequest('test-token'), /Version unavailable/)
  fetch.mock.mockImplementation(async () => new Response('Proxy error', {status:502}))
  await assert.rejects(portalVersionRequest('test-token'), /portal version request/)
  fetch.mock.mockImplementation(async () => Response.json({message:'Invalid token'}, {status:401}))
  await assert.rejects(portalVersionRequest('test-token'), isSessionError)
  fetch.mock.mockImplementation(async (_url, options) => {options.signal.throwIfAborted()})
  await assert.rejects(portalVersionRequest('test-token', AbortSignal.abort()), {name:'AbortError'})
})
