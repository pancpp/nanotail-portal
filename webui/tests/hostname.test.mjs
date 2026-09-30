import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ApiError, setDeviceHostnameRequest, validateDeviceHostname } from '../src/api.ts'

test('hostname validation normalizes DNS labels and rejects malformed or reserved names', () => {
  assert.equal(validateDeviceHostname(' NanoTail-2 '), 'nanotail-2')
  for (const input of ['a', '1-device', 'a'.repeat(63)]) assert.equal(validateDeviceHostname(input), input)
  for (const input of ['', ' ', '-device', 'device-', '--help', 'device.local', 'device_name', 'two names', 'a\nb', 'a;b', '$(reboot)', '设备', 'a'.repeat(64)]) {
    assert.throws(() => validateDeviceHostname(input), /1–63/)
  }
  for (const input of ['localhost', ' LOCALHOST6 ']) assert.throws(() => validateDeviceHostname(input), /other than localhost/)
})

test('hostname mutation sends authenticated variables and requires explicit success', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({data: {setDeviceHostname: true}}))
  await setDeviceHostnameRequest('token', ' NanoTail-2 ')
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query')
  assert.equal(options.method, 'POST')
  assert.equal(options.headers.Authorization, 'Bearer token')
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'SetDeviceHostname')
  assert.deepEqual(body.variables, {hostname: 'nanotail-2'})
  assert.match(body.query, /setDeviceHostname\(hostname: \$hostname\)/)
  assert.ok(options.signal instanceof AbortSignal)

  for (const data of [{}, {setDeviceHostname:false}, {setDeviceHostname:'true'}]) {
    fetch.mock.mockImplementation(async () => Response.json({data}))
    await assert.rejects(setDeviceHostnameRequest('token', 'nanotail'), /did not confirm/)
  }
  fetch.mock.mockImplementation(async () => Response.json({data:{setDeviceHostname:true}, errors:[{message:'Only portal administrators can change the device hostname'}]}))
  await assert.rejects(setDeviceHostnameRequest('token', 'nanotail'), error => error instanceof ApiError && error.isGraphQLError && /administrators/.test(error.message))
  fetch.mock.mockImplementation(async () => new Response('', {status:401}))
  await assert.rejects(setDeviceHostnameRequest('token', 'nanotail'), error => error instanceof ApiError && error.status === 401)
})

test('invalid hostname never sends a mutation and failed writes are not retried', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => { throw new TypeError('Network error') })
  await assert.rejects(setDeviceHostnameRequest('token', 'bad;name'), /1–63/)
  assert.equal(fetch.mock.callCount(), 0)
  await assert.rejects(setDeviceHostnameRequest('token', 'nanotail'), /Network error/)
  assert.equal(fetch.mock.callCount(), 1)
})
