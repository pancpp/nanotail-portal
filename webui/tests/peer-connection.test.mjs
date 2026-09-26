import assert from 'node:assert/strict'
import test from 'node:test'
import { peerConnection } from '../src/peerConnection.ts'
import { translate } from '../src/localization.ts'

const peer = (overrides = {}) => ({ online: true, active: true, curAddr: '', peerRelay: '', relay: 'sea', ...overrides })

test('connection uses the active path, not the always-present home DERP region', () => {
  assert.deepEqual(peerConnection(peer({ curAddr: '203.0.113.10:41641', peerRelay: '203.0.113.11:40000:7' }), true),
    { kind: 'direct', label: 'Direct', detail: '' })
  for (const endpoint of ['203.0.113.11:40000:7', '[2001:db8:1234:5678::1]:40000:4294967295']) {
    assert.deepEqual(peerConnection(peer({ peerRelay: endpoint }), true),
      { kind: 'peer-relay', label: 'Peer relay', detail: endpoint })
  }
  assert.deepEqual(peerConnection(peer(), true), { kind: 'derp', label: 'DERP', detail: 'sea' })
})

test('idle, offline, disconnected, and unknown paths do not claim a live connection', () => {
  const cached = peer({ curAddr: '203.0.113.10:41641', peerRelay: '203.0.113.11:40000:7' })
  assert.deepEqual(peerConnection({ ...cached, active: false }, true), { kind: 'idle', label: 'Idle', detail: '' })
  assert.deepEqual(peerConnection({ ...cached, online: false }, true), { kind: 'offline', label: '—', detail: '' })
  assert.deepEqual(peerConnection(cached, false), { kind: 'unavailable', label: 'Unavailable', detail: '' })
  assert.deepEqual(peerConnection(peer({ relay: '' }), true), { kind: 'unknown', label: 'Unknown', detail: '' })
})

test('connection labels translate while relay details retain their original values', () => {
  for (const [value, label, detail] of [
    [peer({ curAddr: '203.0.113.10:41641' }), '直连', ''],
    [peer({ peerRelay: '203.0.113.11:40000:7' }), '设备中继', '203.0.113.11:40000:7'],
    [peer(), 'DERP', 'sea'],
    [peer({ active: false }), '空闲', ''],
    [peer({ relay: '' }), '未知', ''],
  ]) {
    const connection = peerConnection(value, true)
    assert.equal(translate(connection.label, 'zh-CN'), label)
    assert.equal(connection.detail, detail)
  }
})
