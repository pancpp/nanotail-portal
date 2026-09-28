import assert from 'node:assert/strict'
import { test } from 'node:test'
import { emptyCredentialSetup, observeCredentialSetup, readCredentialSetup } from '../src/credentialSetup.ts'

const bound = (overrides = {}) => ({
  backendState: 'Running', haveNodeKey: true, currentTailnet: {name:'example.test'},
  self: {id:'device-a', online:true, keyExpiry:null}, tailscaleIPs:['100.64.0.1'], peers:[], ...overrides,
})
const unbound = bound({backendState:'NeedsLogin', haveNodeKey:false, currentTailnet:null, self:null})

test('initial enrollment prompts even while waiting for device approval', () => {
  const waiting = observeCredentialSetup(emptyCredentialSetup, unbound)
  assert.equal(waiting.pending, false)
  for (const backendState of ['Running', 'Starting', 'Stopped', 'NeedsMachineAuth']) {
    const next = observeCredentialSetup(waiting, bound({backendState}))
    assert.equal(next.pending, true, backendState)
    assert.equal(next.awaitingBinding, false)
  }
})

test('existing enrollment, key renewal, reconnects, and failures do not prompt', () => {
  const baseline = observeCredentialSetup(emptyCredentialSetup, bound())
  assert.equal(baseline.pending, false)
  for (const backendState of ['Running', 'Stopped', 'Starting', 'NeedsLogin', 'NeedsMachineAuth']) {
    const next = observeCredentialSetup(baseline, bound({backendState}))
    assert.equal(next.pending, false)
    assert.equal(observeCredentialSetup(next, bound()).pending, false)
  }
  assert.equal(observeCredentialSetup(baseline, null), baseline)
  assert.equal(observeCredentialSetup(baseline, unbound, 'Status unavailable'), baseline)
  assert.equal(observeCredentialSetup(baseline, bound({self:{id:'new-device'}}), 'Status unavailable'), baseline)
})

test('dismissal survives polling but a different account or enrollment prompts again', () => {
  const pending = observeCredentialSetup(observeCredentialSetup(emptyCredentialSetup, unbound), bound())
  const dismissed = {...pending, pending:false}
  assert.equal(observeCredentialSetup(dismissed, bound()), dismissed)
  assert.equal(observeCredentialSetup(dismissed, bound({currentTailnet:{name:'another.test'}})).pending, true)
  assert.equal(observeCredentialSetup(dismissed, bound({self:{id:'device-b'}})).pending, true)
  assert.equal(observeCredentialSetup(observeCredentialSetup(dismissed, unbound), bound()).pending, true)
})

test('incomplete enrollment metadata defers the prompt and preserves the observation', () => {
  const waiting = observeCredentialSetup(emptyCredentialSetup, unbound)
  for (const status of [null, bound({self:null}), bound({self:{id:''}}), bound({currentTailnet:null}), bound({backendState:'NeedsLogin'})]) {
    assert.equal(observeCredentialSetup(waiting, status), waiting)
  }
  assert.equal(observeCredentialSetup(waiting, bound()).pending, true)
})

test('reloads retain pending/dismissed setup, with safe fallback for blocked or malformed storage', () => {
  const waiting = observeCredentialSetup(emptyCredentialSetup, unbound)
  const restored = readCredentialSetup({getItem:() => JSON.stringify(waiting)})
  assert.equal(observeCredentialSetup(restored, bound()).pending, true)
  for (const pending of [true, false]) {
    const state = {...observeCredentialSetup(restored, bound()), pending}
    assert.deepEqual(readCredentialSetup({getItem:() => JSON.stringify(state)}), state)
  }
  for (const value of [null, 'invalid', '{}', 'null', '{"binding":1,"awaitingBinding":true,"pending":true}']) {
    assert.deepEqual(readCredentialSetup({getItem:() => value}), emptyCredentialSetup)
  }
  assert.deepEqual(readCredentialSetup({getItem() {throw new Error('blocked')}}), emptyCredentialSetup)
})
