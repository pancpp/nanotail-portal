import assert from 'node:assert/strict'
import test from 'node:test'
import { isSessionError, networkActivityHistoryRequest } from '../src/api.ts'
import { trafficHistoryView, historyHourLabel, trafficTotalsView } from '../src/trafficHistory.ts'

const hour = (overrides = {}) => ({ startedAt: '2026-09-24T11:00:00Z', rxBytes: '36893488147419103230', txBytes: '10', observedSeconds: 3600, ...overrides })
const history = (overrides = {}) => {
  const hours = overrides.hours ?? [hour()]
  // Allow malformed-hour fixtures to reach API validation instead of failing here.
  const sum = key => hours.reduce((sum, row) => sum + (typeof row[key] === 'string' && /^\d+$/.test(row[key]) ? BigInt(row[key]) : 0n), 0n).toString()
  const observed = hours.reduce((sum, row) => sum + (typeof row.observedSeconds === 'number' ? row.observedSeconds : 0), 0)
  return { windowStart: '2026-09-23T12:00:00Z', windowEnd: '2026-09-24T12:00:00Z', hours,
    totals: { rxBytes24h: sum('rxBytes'), txBytes24h: sum('txBytes'), observedSeconds24h: observed,
      totalRxBytes: sum('rxBytes'), totalTxBytes: sum('txBytes'), totalObservedSeconds: observed,
      recordedSince: observed > 0 ? '2026-09-23T12:00:00Z' : null }, ...overrides }
}

test('history uses a cancellable authenticated read and preserves large hourly totals', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { networkActivityHistory: history() } }))
  const controller = new AbortController()
  assert.deepEqual(await networkActivityHistoryRequest('token', controller.signal), history())
  const [url, options] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/query'); assert.equal(options.headers.Authorization, 'Bearer token'); assert.equal(options.signal, controller.signal)
  const body = JSON.parse(options.body)
  assert.equal(body.operationName, 'NetworkActivityHistory')
  assert.match(body.query, /hours \{ startedAt rxBytes txBytes observedSeconds \}/)
  assert.match(body.query, /totals \{ rxBytes24h txBytes24h observedSeconds24h totalRxBytes totalTxBytes totalObservedSeconds recordedSince \}/)
  assert.deepEqual(body.variables, {})
  fetch.mock.mockImplementation(async () => Response.json({ data: { networkActivityHistory: history({ hours: [] }) } }))
  assert.deepEqual((await networkActivityHistoryRequest('token')).hours, [])
})

test('history rejects missing, inconsistent and malformed saved totals', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  const valid = history().totals
  const invalid = [undefined, null, {}, [], { ...valid, rxBytes24h: '0' }, { ...valid, txBytes24h: '11' },
    { ...valid, totalRxBytes: '1' }, { ...valid, totalTxBytes: '0' }, { ...valid, observedSeconds24h: 3599 },
    { ...valid, totalObservedSeconds: 3599 }]
  for (const field of Object.keys(valid)) { const value = { ...valid }; delete value[field]; invalid.push(value) }
  for (const field of ['rxBytes24h', 'txBytes24h', 'totalRxBytes', 'totalTxBytes']) {
    for (const value of [null, 1, '-1', '01', '1.5', '1e4', '', '9'.repeat(41)]) invalid.push({ ...valid, [field]: value })
  }
  for (const field of ['observedSeconds24h', 'totalObservedSeconds']) {
    for (const value of [-1, -0.000001, '3600', null, Infinity]) invalid.push({ ...valid, [field]: value })
  }
  for (const recordedSince of [null, 'bad', '2026-09-24T12:00:00Z', '2026-09-23T12:00:01Z']) invalid.push({ ...valid, recordedSince })
  for (const totals of invalid) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { networkActivityHistory: history({ totals }) } }))
    await assert.rejects(networkActivityHistoryRequest('token'), /valid VPN history/)
  }
  const empty = history({ hours: [] })
  for (const bad of [{ totalRxBytes: '1' }, { totalTxBytes: '1' }, { recordedSince: '2026-09-23T12:00:00Z' }, { observedSeconds24h: -0.000001 }]) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { networkActivityHistory: { ...empty, totals: { ...empty.totals, ...bad } } } }))
    await assert.rejects(networkActivityHistoryRequest('token'), /valid VPN history/)
  }
})

test('saved lifetime totals survive an empty 24-hour window without precision loss', async (t) => {
  const saved = history({ hours: [] })
  Object.assign(saved.totals, { totalRxBytes: '36893488147419103230', totalTxBytes: '10', totalObservedSeconds: 3600, recordedSince: '2026-09-01T12:00:00Z' })
  t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { networkActivityHistory: saved } }))
  assert.deepEqual(await networkActivityHistoryRequest('token'), saved)
  assert.deepEqual(trafficTotalsView(saved), { last24Bytes: 0, totalBytes: Number(36893488147419103240n) })
})

test('saved total display shows zero without measurements and uses cached totals', () => {
  assert.deepEqual(trafficTotalsView(history({ hours: [] })), { last24Bytes: 0, totalBytes: 0 })
  const unmeasured = history({ hours: [hour({ rxBytes: '0', txBytes: '0', observedSeconds: 0 })] })
  assert.deepEqual(trafficTotalsView(unmeasured), { last24Bytes: 0, totalBytes: 0 })
  assert.ok(trafficHistoryView(unmeasured).hours.every(hour => hour.coverage === 'Missing'))
  const zero = history({ hours: [hour({ rxBytes: '0', txBytes: '0' })] })
  assert.deepEqual(trafficTotalsView(zero), { last24Bytes: 0, totalBytes: 0 })
  const saved = history()
  saved.totals.rxBytes24h = '100'; saved.totals.txBytes24h = '50'
  saved.totals.totalRxBytes = '200'; saved.totals.totalTxBytes = '75'
  assert.deepEqual(trafficTotalsView(saved), { last24Bytes: 150, totalBytes: 275 })
})

test('history rejects malformed dates, duplicate/out-of-window hours and invalid data', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch')
  const invalid = [null, {}, [], history({ windowEnd: 'bad' }), history({ windowStart: '2026-09-23T12:00:01Z' }),
    history({ windowEnd: '2026-09-24T13:00:00Z' }), history({ hours: [hour(), hour()] }), history({ hours: Array(25).fill(hour()) })]
  for (const field of Object.keys(history())) { const value = history(); delete value[field]; invalid.push(value) }
  for (const field of Object.keys(hour())) { const value = hour(); delete value[field]; invalid.push(history({ hours: [value] })) }
  for (const value of [null, 1, '-1', '01', '1.5', '1e4', '', '9'.repeat(41)]) {
    invalid.push(history({ hours: [hour({ rxBytes: value })] }), history({ hours: [hour({ txBytes: value })] }))
  }
  for (const observedSeconds of [-1, 3601, '60', null, Infinity]) invalid.push(history({ hours: [hour({ observedSeconds })] }))
  for (const startedAt of ['bad', '2026-09-23T11:00:00Z', '2026-09-24T12:00:00Z', '2026-09-24T11:30:00Z']) invalid.push(history({ hours: [hour({ startedAt })] }))
  invalid.push(history({ hours: [hour(), hour({ startedAt: '2026-09-24T10:00:00Z' })] }))
  for (const value of invalid) {
    fetch.mock.mockImplementation(async () => Response.json({ data: { networkActivityHistory: value } }))
    await assert.rejects(networkActivityHistoryRequest('token'), /valid VPN history/)
  }
})

test('history preserves auth/errors and does not turn failed reads into zero traffic', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ data: { networkActivityHistory: history() }, errors: [{ message: 'Apply database migrations' }] }))
  await assert.rejects(networkActivityHistoryRequest('token'), /Apply database migrations/)
  fetch.mock.mockImplementation(async () => Response.json({ message: 'invalid jwt' }, { status: 401 }))
  await assert.rejects(networkActivityHistoryRequest('token'), error => isSessionError(error))
  fetch.mock.mockImplementation(async () => new Response('Bad gateway', { status: 502 }))
  await assert.rejects(networkActivityHistoryRequest('token'), /VPN history request/)
  fetch.mock.mockImplementation(async (_url, options) => options.signal.throwIfAborted())
  await assert.rejects(networkActivityHistoryRequest('token', AbortSignal.abort()), { name: 'AbortError' })
})

test('hour slots distinguish missing, unmeasured, partial and measured zero traffic', () => {
  const view = trafficHistoryView(history({ hours: [
    hour({ startedAt: '2026-09-24T08:00:00Z', rxBytes: '0', txBytes: '0', observedSeconds: 0 }),
    hour({ startedAt: '2026-09-24T09:00:00Z', rxBytes: '0', txBytes: '0' }),
    hour({ startedAt: '2026-09-24T10:00:00Z', rxBytes: '12', txBytes: '3', observedSeconds: 1200.5 }),
    hour(),
  ] }))
  assert.equal(view.hours.length, 24)
  assert.equal(view.hours[0].coverage, 'Missing'); assert.equal(view.hours[0].download, null)
  assert.equal(view.hours[20].coverage, 'Missing'); assert.equal(view.hours[20].download, null)
  assert.equal(view.hours[21].coverage, 'Complete'); assert.equal(view.hours[21].download, 0)
  assert.equal(view.hours[22].coverage, 'Partial'); assert.equal(view.hours[22].download, 12)
  assert.equal(view.completeHours, 2); assert.equal(view.observedSeconds, 8400.5)
  assert.equal(view.received, 36893488147419103242n); assert.equal(view.sent, 13n)
  assert.equal(historyHourLabel(view.hours[23].at), new Date('2026-09-24T11:00:00Z').toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', timeZoneName: 'short' }))
})

test('empty history has 24 unknown slots, not 24 zero-traffic hours', () => {
  const view = trafficHistoryView(history({ hours: [] }))
  assert.equal(view.completeHours, 0); assert.equal(view.observedSeconds, 0)
  assert.equal(view.received, 0n); assert.equal(view.sent, 0n)
  assert.ok(view.hours.every(hour => hour.download === null && hour.upload === null))
})
