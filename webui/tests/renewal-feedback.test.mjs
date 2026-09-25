import assert from 'node:assert/strict'
import test from 'node:test'
import { renewalFeedback } from '../src/renewalFeedback.ts'

const idle = { value: null, pending: false, busy: false, starting: false, preparing: false, cancelling: false, error: '' }
const status = state => ({ state, authURL: '', canRenew: false, attemptID: state === 'IDLE' ? '' : 'attempt-1' })

test('opening the dialog or checking an idle device does not imply renewal started', () => {
  assert.equal(renewalFeedback(idle), null)
  assert.equal(renewalFeedback({ ...idle, busy: true }), null)
  assert.equal(renewalFeedback({ ...idle, value: status('IDLE') }), null)
})

test('prepared and cancelled requests do not imply Tailscale started or succeeded', () => {
  assert.equal(renewalFeedback({ ...idle, preparing: true }).state, 'waiting')
  assert.equal(renewalFeedback({ ...idle, cancelling: true }).state, 'waiting')
  const ready = renewalFeedback({ ...idle, value: status('READY'), pending: true })
  assert.equal(ready.state, 'ready')
  assert.match(ready.description, /close the panel to cancel/)
  const cancelled = renewalFeedback({ ...idle, value: status('CANCELLED') })
  assert.equal(cancelled.state, 'cancelled')
  assert.match(cancelled.title, /cancelled/)
})

test('all pending renewal stages show waiting and explain the next step', () => {
  const states = [
    [{ ...idle, pending: true, starting: true, busy: true }, /Starting renewal/],
    [{ ...idle, pending: true, value: status('STARTING') }, /Preparing your sign-in link/],
    [{ ...idle, pending: true, value: status('AWAITING_LOGIN') }, /Complete Tailscale sign-in/],
    [{ ...idle, pending: true, value: status('AWAITING_APPROVAL') }, /device approval/],
    [{ ...idle, pending: true, busy: true }, /Checking renewal status/],
  ]
  for (const [snapshot, description] of states) {
    const feedback = renewalFeedback(snapshot)
    assert.equal(feedback.state, 'waiting')
    assert.equal(feedback.title, 'Waiting for renewal…')
    assert.match(feedback.description, description)
  }
})

test('only a verified COMPLETE result shows a success check and message', () => {
  const snapshot = { ...idle, value: status('COMPLETE') }
  const feedback = renewalFeedback(snapshot)
  assert.equal(feedback.state, 'success')
  assert.equal(feedback.title, 'Node key renewed successfully')
  // Checking the same completed attempt does not flash away the success state.
  assert.deepEqual(renewalFeedback({ ...snapshot, busy: true }), feedback)
  // A new attempt immediately replaces any previous success with waiting.
  assert.equal(renewalFeedback({ ...snapshot, pending: true, starting: true }).state, 'waiting')
})

test('failed or uncertain requests never display success or spin forever', () => {
  for (const value of [null, status('AWAITING_LOGIN'), status('COMPLETE'), status('SIGNED_IN')]) {
    const feedback = renewalFeedback({ ...idle, value, pending: true, error: 'private diagnostic' })
    assert.equal(feedback.state, 'error')
    assert.equal(feedback.title, 'Renewal not confirmed')
    assert.match(feedback.description, /check renewal status/)
    assert.ok(!JSON.stringify(feedback).includes('private diagnostic'))
  }
})

test('first sign-in has its own waiting, cancelled and verified success feedback', () => {
  for (const [snapshot, title] of [
    [{ ...idle, preparing: true }, 'Preparing sign-in…'],
    [{ ...idle, pending: true, starting: true }, 'Waiting for sign-in…'],
    [{ ...idle, pending: true, value: status('AWAITING_LOGIN') }, 'Waiting for sign-in…'],
    [{ ...idle, value: status('CANCELLED') }, 'Sign-in cancelled'],
    [{ ...idle, error: 'unavailable' }, 'Sign-in not confirmed'],
  ]) assert.equal(renewalFeedback(snapshot, true).title, title)
  for (const signIn of [false, true]) {
    const feedback = renewalFeedback({ ...idle, value: status('SIGNED_IN') }, signIn)
    assert.equal(feedback.state, 'success')
    assert.equal(feedback.title, 'Signed in to Tailscale successfully')
    assert.ok(!JSON.stringify(feedback).includes('renewed'))
  }
})
