import assert from 'node:assert/strict'
import test from 'node:test'
import { nodeKeyStatus } from '../src/nodeKey.ts'

const now = Date.parse('2026-09-24T12:00:00Z')
function status(expiry, overrides = {}) {
  return { backendState: 'Running', haveNodeKey: true, tailscaleIPs: ['100.64.0.1'], currentTailnet: { name: 'example.test' },
    self: { online: true, keyExpiry: expiry }, peers: [], ...overrides }
}

test('node-key countdown uses the reported expiry, without assuming a fixed key lifetime', () => {
  for (const [remaining, label] of [
    [400 * 86400000, '400 days remaining'], [2 * 86400000 + 1234, '2 days remaining'],
    [86400000, '1 day remaining'], [86400000 - 1, '23 hours remaining'],
    [3600000, '1 hour remaining'], [3600000 - 1, '59 minutes remaining'],
    [120000, '2 minutes remaining'], [60000, '1 minute remaining'], [59999, 'Less than a minute remaining'], [1, 'Less than a minute remaining'],
  ]) {
    const expiry = new Date(now + remaining).toISOString()
    const result = nodeKeyStatus(status(expiry), '', now)
    assert.equal(result.label, label)
    assert.equal(result.expiresAt, expiry)
    assert.equal(result.description, 'Expires')
    assert.equal(result.state, remaining <= 7 * 86400000 ? 'expiring' : 'active')
  }
})

test('expired keys are detected at the exact deadline and never get negative remaining days', () => {
  for (const time of [now, now - 1, now - 400 * 86400000]) {
    const result = nodeKeyStatus(status(new Date(time).toISOString()), '', now)
    assert.equal(result.state, 'expired')
    assert.equal(result.label, 'Expired')
  }
  const expiry = new Date(now + 1000).toISOString()
  assert.equal(nodeKeyStatus(status(expiry), '', now).state, 'expiring')
  assert.equal(nodeKeyStatus(status(expiry), '', now + 1000).state, 'expired')
})

test('null expiry, absent keys and absent self are distinct and never show a made-up date', () => {
  const noExpiry = nodeKeyStatus(status(null), '', now)
  assert.equal(noExpiry.state, 'no-expiry')
  assert.equal(noExpiry.label, 'No expiry reported')
  assert.equal(noExpiry.expiresAt, null)
  assert.doesNotMatch(noExpiry.label + noExpiry.description, /never expires|disabled|180|178/i)
  assert.equal(nodeKeyStatus(status(null, { haveNodeKey: false }), '', now).state, 'unconfigured')
  assert.equal(nodeKeyStatus(status(null, { self: null }), '', now).state, 'unavailable')
  assert.equal(nodeKeyStatus(status(null, { haveNodeKey: false, self: null, backendState: 'NeedsLogin' }), '', now).state, 'unconfigured')
})

test('loading, failed refreshes and invalid timestamps do not show stale key data', () => {
  assert.equal(nodeKeyStatus(null, '', now).state, 'loading')
  const stale = nodeKeyStatus(status('2027-01-01T00:00:00Z'), 'Tailscale unavailable', now)
  assert.equal(stale.state, 'unavailable')
  assert.equal(stale.expiresAt, null)
  assert.equal(nodeKeyStatus(null, 'Tailscale unavailable', now).state, 'unavailable')
  for (const expiry of [undefined, '', 'bad-date']) assert.equal(nodeKeyStatus(status(expiry), '', now).state, 'unavailable')
  assert.equal(nodeKeyStatus(status('2027-01-01T00:00:00Z'), '', NaN).state, 'unavailable')
})

test('key expiry is independent of connection state and handles timezone offsets', () => {
  for (const backendState of ['Running', 'Stopped', 'NeedsLogin']) {
    const result = nodeKeyStatus(status('2026-09-25T05:00:00-07:00', { backendState, self: { online: false, keyExpiry: '2026-09-25T05:00:00-07:00' } }), '', now)
    assert.equal(result.label, '1 day remaining')
  }
})
