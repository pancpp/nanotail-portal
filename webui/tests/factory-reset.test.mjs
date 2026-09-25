import assert from 'node:assert/strict'
import test from 'node:test'
import { canConfirmReset, factoryResetRequest, ResetOutcomeUnknown } from '../src/factoryReset.ts'
import { isSessionError } from '../src/api.ts'

test('reset requires acknowledgement, exact phrase, password, and idle state', () => {
  assert.equal(canConfirmReset(true, 'RESET', 'password', false), true)
  for (const args of [[false, 'RESET', 'password', false], [true, 'reset', 'password', false], [true, 'RESET', '', false], [true, 'RESET', 'password', true]]) {
    assert.equal(canConfirmReset(...args), false)
  }
})

test('reset is authenticated, explicitly confirmed, and accepts only 202', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ accepted: true }, { status: 202 }))
  await factoryResetRequest('token', 'RESET', 'password')
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/factory-reset')
  assert.equal(options.method, 'POST')
  assert.equal(options.headers.Authorization, 'Bearer token')
  assert.deepEqual(JSON.parse(options.body), { confirmed: true, confirmation: 'RESET', password: 'password' })
  assert.ok(options.signal instanceof AbortSignal)
  assert.equal(fetch.mock.callCount(), 1)
  for (const [body, status] of [[{ accepted: false }, 202], [{ accepted: true }, 200], [{ accepted: 'true' }, 202], [{}, 202]]) {
    fetch.mock.mockImplementation(async () => Response.json(body, { status }))
    await assert.rejects(factoryResetRequest('token', 'RESET', 'password'), ResetOutcomeUnknown)
  }
})

test('unconfirmed reset never reaches server; rejected reset preserves the error', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ message: 'Only administrators can reset' }, { status: 403 }))
  await assert.rejects(factoryResetRequest('token', 'reset', 'password'), /Type RESET/)
  assert.equal(fetch.mock.callCount(), 0)
  await assert.rejects(factoryResetRequest('token', 'RESET', 'password'), { status: 403, message: 'Only administrators can reset' })
  fetch.mock.mockImplementation(async () => Response.json({ message: 'expired token' }, { status: 401 }))
  await assert.rejects(factoryResetRequest('token', 'RESET', 'password'), isSessionError)
})

test('lost responses and timeouts are unknown outcomes and never retried', async t => {
  let trigger, cleared = 0
  t.mock.method(globalThis, 'setTimeout', (callback, delay) => { assert.equal(delay, 20_000); trigger = callback; return 7 })
  t.mock.method(globalThis, 'clearTimeout', id => { assert.equal(id, 7); cleared++ })
  const fetch = t.mock.method(globalThis, 'fetch', async (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(new Error('network ended')))
  }))
  const request = factoryResetRequest('token', 'RESET', 'password')
  trigger()
  await assert.rejects(request, ResetOutcomeUnknown)
  assert.equal(fetch.mock.callCount(), 1)
  assert.equal(cleared, 1)
})

test('proxy failures are uncertain even with an HTTP response', async t => {
  t.mock.method(globalThis, 'fetch', async () => new Response('upstream disconnected', { status: 502 }))
  await assert.rejects(factoryResetRequest('token', 'RESET', 'password'), ResetOutcomeUnknown)
})
