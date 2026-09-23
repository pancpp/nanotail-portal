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
