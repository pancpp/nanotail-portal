import assert from 'node:assert/strict'
import test from 'node:test'
import { logoutTailscaleRequest, isSessionError } from '../src/api.ts'

test('Tailscale logout is authenticated and only accepts confirmed success', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { logoutTailscale: true } }))
  await logoutTailscaleRequest('token')
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query')
  assert.equal(options.headers.Authorization, 'Bearer token')
  assert.equal(options.method, 'POST')
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'LogoutTailscale')
  assert.match(body.query, /mutation LogoutTailscale\s*\{\s*logoutTailscale\s*\}/)
  assert.deepEqual(body.variables, {})
  assert.ok(options.signal instanceof AbortSignal)
  assert.equal(fetch.mock.callCount(), 1)
  for (const value of [false, null, 'true', undefined]) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { logoutTailscale: value } }))
    await assert.rejects(logoutTailscaleRequest('token'), /did not confirm Tailscale logout/)
  }
})

test('Tailscale logout propagates authorization errors and rejects partial success', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { logoutTailscale: true }, errors: [{ message: 'Only portal administrators' }] }))
  await assert.rejects(logoutTailscaleRequest('token'), { message: 'Only portal administrators', isGraphQLError: true })
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(logoutTailscaleRequest('token'), error => isSessionError(error))
  fetch.mock.mockImplementation(async () => new Response('private daemon output', { status: 502 }))
  await assert.rejects(logoutTailscaleRequest('token'), /Tailscale logout request/)
})

test('Tailscale logout times out without retrying and releases its timer', async (t) => {
  let triggerTimeout, cleared = 0
  t.mock.method(globalThis, 'setTimeout', (callback, delay) => { assert.equal(delay, 60_000); triggerTimeout = callback; return 17 })
  t.mock.method(globalThis, 'clearTimeout', id => { assert.equal(id, 17); cleared++ })
  const fetch = t.mock.method(globalThis, 'fetch', async (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
  }))
  const request = logoutTailscaleRequest('token')
  triggerTimeout()
  await assert.rejects(request, { name: 'AbortError' })
  assert.equal(fetch.mock.callCount(), 1)
  assert.equal(cleared, 1)
})
