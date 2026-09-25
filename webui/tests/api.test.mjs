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
} from '../src/api.ts'

function jwt(payload = {}) {
  const header = Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url')
  const claims = Buffer.from(JSON.stringify({
    pid: 1, exp: Math.floor(Date.now() / 1000) + 3600, ...payload,
  })).toString('base64url')
  return `${header}.${claims}.signature`
}

function makeStatus(overrides = {}) {
  return { backendState: 'NeedsLogin', tailscaleIPs: [], currentTailnet: null, self: null, peers: [], ...overrides }
}

function makePeer(overrides = {}) {
  return {
    id: 'peer-a', hostName: 'desktop', dnsName: 'desktop.example.ts.net.', os: 'linux',
    tailscaleIPs: ['100.64.0.2', 'fd7a:115c:a1e0::2'], online: true, ...overrides,
  }
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
    self: { online: true }, peers: [makePeer(), makePeer({ id: 'peer-b', online: false })],
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
    'query TailscaleStatus { tailscaleStatus { backendState tailscaleIPs currentTailnet { name } self { online } peers { id hostName dnsName os tailscaleIPs online } } }')
  assert.doesNotMatch(body.query, /\b(connected|needsLogin|tailnet|ips|authURL)\b/)
})

test('status accepts null tailnet/self and empty lists before login', async (t) => {
  const status = makeStatus()
  t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { tailscaleStatus: status } }))
  assert.deepEqual(await tailscaleStatusRequest(jwt()), status)
})

test('status rejects legacy responses and malformed nested fields', async (t) => {
  const invalid = [
    null, [], {}, { backendState: 'Running', connected: true, needsLogin: false, tailnet: 'old', ips: [] },
    makeStatus({ backendState: null }), makeStatus({ tailscaleIPs: null }), makeStatus({ tailscaleIPs: [42] }),
    makeStatus({ currentTailnet: undefined }), makeStatus({ currentTailnet: [] }),
    makeStatus({ currentTailnet: 'example.test' }), makeStatus({ currentTailnet: { name: null } }),
    makeStatus({ self: undefined }), makeStatus({ self: [] }), makeStatus({ self: {} }),
    makeStatus({ self: { online: 'false' } }), makeStatus({ peers: null }), makeStatus({ peers: {} }),
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
