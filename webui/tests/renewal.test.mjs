import assert from 'node:assert/strict'
import test from 'node:test'
import { tailscaleKeyRenewalRequest, renewTailscaleNodeKeyRequest, beginTailscaleNodeKeyRenewalRequest, cancelTailscaleNodeKeyRenewalRequest, isKeyRenewalPending, isSessionError } from '../src/api.ts'

const idle = { state: 'IDLE', authURL: '', canRenew: true, attemptID: '' }
const ready = { state: 'READY', authURL: '', canRenew: false, attemptID: 'attempt-1' }
const pending = { ...ready, state: 'AWAITING_LOGIN', authURL: 'https://login.tailscale.com/a/test' }
const calls = [tailscaleKeyRenewalRequest, renewTailscaleNodeKeyRequest,
  token => beginTailscaleNodeKeyRenewalRequest(token, ready.attemptID),
  token => cancelTailscaleNodeKeyRenewalRequest(token, ready.attemptID)]

test('renewal status is authenticated, uncached, cancelable, and read-only', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { tailscaleKeyRenewal: idle } }))
  const controller = new AbortController()
  assert.deepEqual(await tailscaleKeyRenewalRequest('token', controller.signal), idle)
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query'); assert.equal(options.headers.Authorization, 'Bearer token'); assert.equal(options.cache, 'no-store')
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'TailscaleKeyRenewal'); assert.match(body.query, /query TailscaleKeyRenewal/)
  assert.match(body.query, /state authURL canRenew attemptID/); assert.deepEqual(body.variables, {})
  controller.abort(); assert.equal(options.signal.aborted, true)
  for (const state of ['IDLE', 'READY', 'CANCELLED', 'STARTING', 'AWAITING_LOGIN', 'AWAITING_APPROVAL', 'COMPLETE', 'SIGNED_IN']) {
    const value = { state, authURL: state === 'AWAITING_LOGIN' ? pending.authURL : '', canRenew: ['IDLE', 'COMPLETE'].includes(state), attemptID: state === 'IDLE' ? '' : ready.attemptID }
    fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleKeyRenewal: value } }))
    assert.deepEqual(await tailscaleKeyRenewalRequest('token'), value)
    assert.equal(isKeyRenewalPending(value), ['READY', 'STARTING', 'AWAITING_LOGIN', 'AWAITING_APPROVAL'].includes(state))
  }
})

test('renewal rejects invalid data and unsafe sign-in URLs without exposing them in errors', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  const invalid = [null, {}, [], { ...idle, state: 'SUCCESS' }, { ...idle, canRenew: 'true' }, { ...idle, authURL: pending.authURL }, { ...pending, canRenew: true }]
  for (const attemptID of ['', null, 17, 'invalid id', 'a'.repeat(129), 'private/secret']) invalid.push({ ...ready, attemptID })
  invalid.push({ ...ready, canRenew: true })
  for (const field of Object.keys(idle)) { const value = { ...idle }; delete value[field]; invalid.push(value) }
  for (const authURL of ['', 'javascript:alert(1)', 'http://login.tailscale.com/a/secret', 'https://evil.test/a/secret', 'https://login.tailscale.com.evil.test/a/secret', 'https://login.tailscale.com@evil.test/a/secret', 'https://user@login.tailscale.com/a/secret', 'https://login.tailscale.com:444/a/secret', 'https://login.tailscale.com/a/', 'https://login.tailscale.com/admin', 'https://login.tailscale.com/a/secret#fragment', 'https://login.tailscale.com/a/sec ret', 'https://login.tailscale.com/a/sec\\ret']) invalid.push({ ...pending, authURL })
  for (const value of invalid) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleKeyRenewal: value } }))
    await assert.rejects(tailscaleKeyRenewalRequest('token'), error => /valid node key renewal status/.test(error.message) && !error.message.includes('secret'))
  }
})

test('renewal preparation sends no credentials and never treats ready as complete', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { renewTailscaleNodeKey: ready } }))
  assert.deepEqual(await renewTailscaleNodeKeyRequest('token'), ready)
  const [, options] = fetch.mock.calls[0].arguments
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'RenewTailscaleNodeKey'); assert.match(body.query, /mutation RenewTailscaleNodeKey/)
  assert.deepEqual(body.variables, {}); assert.equal(options.headers.Authorization, 'Bearer token')
  assert.equal(fetch.mock.callCount(), 1)
  for (const value of [true, null, idle, { ...ready, state: 'COMPLETE' }, { ...ready, state: 'SIGNED_IN' }]) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { renewTailscaleNodeKey: value } }))
    await assert.rejects(renewTailscaleNodeKeyRequest('token'), /Check status before retrying/)
  }
})

test('begin and cancel identify exactly one prepared attempt', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const [call, operationName, field, states] of [
    [beginTailscaleNodeKeyRenewalRequest, 'BeginTailscaleNodeKeyRenewal', 'beginTailscaleNodeKeyRenewal', ['STARTING', 'AWAITING_LOGIN', 'AWAITING_APPROVAL', 'COMPLETE', 'SIGNED_IN', 'IDLE']],
    [cancelTailscaleNodeKeyRenewalRequest, 'CancelTailscaleNodeKeyRenewal', 'cancelTailscaleNodeKeyRenewal', ['CANCELLED']],
  ]) {
    for (const state of states) {
      const value = { ...ready, state, authURL: state === 'AWAITING_LOGIN' ? pending.authURL : '' }
      fetch.mock.mockImplementation(async () => Response.json({ data: { [field]: value } }))
      const controller = new AbortController()
      assert.deepEqual(await call('token', ready.attemptID, controller.signal), value)
      const options = fetch.mock.calls.at(-1).arguments[1]
      const body = JSON.parse(options.body)
      assert.equal(body.operationName, operationName)
      assert.match(body.query, /\$attemptID: String!/)
      assert.match(body.query, /state authURL canRenew attemptID/)
      assert.deepEqual(body.variables, { attemptID: ready.attemptID })
      assert.equal(options.headers.Authorization, 'Bearer token')
      controller.abort(); assert.equal(options.signal.aborted, true)
    }
    for (const value of [true, null, ready, { ...ready, state: states[0], attemptID: 'another-attempt' }, { ...ready, state: states[0], attemptID: '' }]) {
      fetch.mock.mockImplementation(async () => Response.json({ data: { [field]: value } }))
      await assert.rejects(call('token', ready.attemptID))
    }
    const before = fetch.mock.callCount()
    for (const id of ['', undefined, null, 17, 'bad id', 'a'.repeat(129)]) await assert.rejects(call('token', id))
    assert.equal(fetch.mock.callCount(), before, 'invalid IDs never reach the backend')
  }
})

test('renewal propagates masked errors, rejects partial success, and handles expired sessions', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  for (const call of calls) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { tailscaleKeyRenewal: pending, renewTailscaleNodeKey: ready, beginTailscaleNodeKeyRenewal: pending, cancelTailscaleNodeKeyRenewal: { ...ready, state: 'CANCELLED' } }, errors: [{ message: 'Only portal administrators' }] }))
    await assert.rejects(call('token'), { message: 'Only portal administrators', isGraphQLError: true })
    fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
    await assert.rejects(call('token'), error => isSessionError(error))
    fetch.mock.mockImplementation(async () => new Response('Bad gateway', { status: 502 }))
    await assert.rejects(call('token'), /renewal/)
  }
})

test('renewal reads and writes time out without retrying and clear timers', async (t) => {
  let triggerTimeout, cleared = 0
  t.mock.method(globalThis, 'setTimeout', (callback, delay) => { assert.equal(delay, 60_000); triggerTimeout = callback; return 17 })
  t.mock.method(globalThis, 'clearTimeout', id => { assert.equal(id, 17); cleared++ })
  const fetch = t.mock.method(globalThis, 'fetch', async (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
  }))
  for (const call of calls) {
    const request = call('token'); triggerTimeout()
    await assert.rejects(request, { name: 'AbortError' })
  }
  assert.equal(fetch.mock.callCount(), 4); assert.equal(cleared, 4)
})
