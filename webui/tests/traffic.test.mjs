import assert from 'node:assert/strict'
import test from 'node:test'
import { appendTrafficSample, emptyTrafficWindow, formatTrafficBytes, formatTrafficRate } from '../src/traffic.ts'

const start = Date.parse('2026-09-24T12:00:00Z')
function sample(milliseconds = 0, rx = 1000n, tx = 2000n, overrides = {}) {
  return { interfaceName: 'tailscale0', rxBytes: String(rx), txBytes: String(tx), sampledAt: new Date(start + milliseconds).toISOString(), counterEpoch: 'boot:3', ...overrides }
}

test('traffic needs two samples and computes byte rates from actual elapsed time', () => {
  const first = appendTrafficSample(emptyTrafficWindow(), sample())
  assert.equal(first.rate, null)
  assert.deepEqual(first.history, [])
  const next = appendTrafficSample(first, sample(2500, 6000n, 4500n))
  assert.deepEqual(next.rate, { at: start + 2500, download: 2000, upload: 1000 })
  assert.deepEqual(next.history, [next.rate])
  assert.equal(first.latest.rxBytes, '1000', 'previous state mutated')
})

test('idle traffic is measured as zero, not missing data', () => {
  const first = appendTrafficSample(emptyTrafficWindow(), sample())
  assert.deepEqual(appendTrafficSample(first, sample(2000)).rate, { at: start + 2000, download: 0, upload: 0 })
})

test('large cumulative counters preserve small deltas exactly', () => {
  const large = 18446744073709550000n
  const first = appendTrafficSample(emptyTrafficWindow(), sample(0, large, large))
  const next = appendTrafficSample(first, sample(2000, large + 10n, large + 20n))
  assert.equal(next.rate.download, 5)
  assert.equal(next.rate.upload, 10)
})

test('counter resets, interface recreation, clock changes and long gaps rebase the chart', () => {
  let state = appendTrafficSample(emptyTrafficWindow(), sample())
  state = appendTrafficSample(state, sample(2000, 3000n, 4000n))
  for (const next of [
    sample(4000, 2999n, 5000n), sample(4000, 5000n, 3999n),
    sample(4000, 5000n, 6000n, { counterEpoch: 'boot:4' }),
    sample(4000, 5000n, 6000n, { counterEpoch: 'new-boot:3' }),
    sample(2000, 5000n, 6000n), sample(1000, 5000n, 6000n), sample(12001, 5000n, 6000n),
  ]) {
    const reset = appendTrafficSample(state, next)
    assert.equal(reset.rate, null)
    assert.deepEqual(reset.history, [])
    assert.deepEqual(reset.latest, next)
  }
})

test('traffic history is bounded to thirty points and the last minute', () => {
  let state = emptyTrafficWindow()
  for (let index = 0; index < 100; index++) state = appendTrafficSample(state, sample(index * 2000, BigInt(index * 1000), BigInt(index * 2000)))
  assert.equal(state.history.length, 30)
  assert.ok(state.history.every(point => point.at > start + 198000 - 60000))
  for (let index = 100; index < 120; index++) state = appendTrafficSample(state, sample(198000 + (index - 99) * 9000, BigInt(index * 1000), BigInt(index * 2000)))
  assert.equal(state.history.length, 7)
})

test('traffic formatting uses clear binary byte units and rates', () => {
  assert.equal(formatTrafficBytes(0), '0 B')
  assert.equal(formatTrafficBytes(1024), '1 KiB')
  assert.equal(formatTrafficBytes(1536), '1.5 KiB')
  assert.equal(formatTrafficBytes(1024 ** 3), '1 GiB')
  assert.equal(formatTrafficRate(1024 ** 2), '1 MiB/s')
  assert.equal(formatTrafficRate(0), '0 B/s')
  for (const invalid of [NaN, Infinity, -1]) assert.equal(formatTrafficBytes(invalid), 'Unavailable')
})
