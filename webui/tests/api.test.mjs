import assert from 'node:assert/strict'
import test from 'node:test'
import {
  ApiError,
  changePasswordRequest,
  isSessionError,
  loginRequest,
  tokenExpiry,
} from '../src/api.ts'

function jwt(payload = {}) {
  const header = Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url')
  const claims = Buffer.from(JSON.stringify({
    pid: 1, exp: Math.floor(Date.now() / 1000) + 3600, ...payload,
  })).toString('base64url')
  return `${header}.${claims}.signature`
}

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

test('password change uses bearer auth and accepts 204 without decoding JSON', async (t) => {
  const token = jwt()
  const fetch = t.mock.method(globalThis, 'fetch', async () => new Response(null, { status: 204 }))
  const passwords = { current_password: 'admin', new_password: 'new-password' }
  await changePasswordRequest(token, passwords)
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/change-password')
  assert.equal(options.method, 'POST')
  assert.equal(options.headers.Authorization, `Bearer ${token}`)
  assert.equal(options.headers['Content-Type'], 'application/json')
  assert.deepEqual(JSON.parse(options.body), passwords)
})

test('password validation uses UTF-8 bytes and permits both boundaries', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => new Response(null, { status: 204 }))
  for (const password of ['', 'a'.repeat(7), 'a'.repeat(73), '界'.repeat(25)]) {
    await assert.rejects(changePasswordRequest(jwt(), { current_password: 'admin', new_password: password }), /8 and 72 bytes/)
  }
  assert.equal(fetch.mock.callCount(), 0)
  for (const password of ['a'.repeat(8), 'a'.repeat(72), '界'.repeat(3), '界'.repeat(24)]) {
    await changePasswordRequest(jwt(), { current_password: 'admin', new_password: password })
  }
  assert.equal(fetch.mock.callCount(), 4)
})

test('wrong current password is a retryable 401, not a session failure', async (t) => {
  t.mock.method(globalThis, 'fetch', async () => Response.json({ message: 'Current password is incorrect' }, { status: 401 }))
  await assert.rejects(
    changePasswordRequest(jwt(), { current_password: 'wrong', new_password: 'new-password' }),
    (error) => {
      assert.equal(error.status, 401)
      assert.equal(isSessionError(error), false)
      return true
    },
  )
  assert.equal(isSessionError(new ApiError('invalid or expired jwt', 401)), true)
  assert.equal(isSessionError(new ApiError('Unauthorized error', 401)), true)
  assert.equal(isSessionError(new ApiError('Internal Server Error', 500)), false)
  assert.equal(isSessionError(new Error('Network error')), false)
})
