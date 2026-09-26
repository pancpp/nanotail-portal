import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFile, readdir } from 'node:fs/promises'
import { parseSync } from 'rolldown/utils'
import { zhCN } from '../src/locales/zh-CN.ts'
import { chooseLanguage, readLanguage, translate, LANGUAGE_STORAGE_KEY } from '../src/localization.ts'
import { nodeKeyStatus } from '../src/nodeKey.ts'
import { formatDeviceUptime } from '../src/api.ts'
import { historyHourLabel } from '../src/trafficHistory.ts'
import { formatTrafficBytes } from '../src/traffic.ts'
import { forwardingWarnings } from '../src/routing.ts'

test('language preference is explicit, browser-aware, and resilient to blocked storage', () => {
  assert.equal(chooseLanguage('en', ['zh-CN']), 'en')
  assert.equal(chooseLanguage('zh-CN', ['en-US']), 'zh-CN')
  assert.equal(chooseLanguage(null, ['zh-CN', 'en-US']), 'zh-CN')
  assert.equal(chooseLanguage(null, ['zh-TW']), 'zh-CN')
  assert.equal(chooseLanguage(null, ['en-US', 'zh-CN']), 'en')
  assert.equal(chooseLanguage('invalid', ['fr', 'zh-Hans']), 'zh-CN')
  assert.equal(chooseLanguage(null, ['fr']), 'en')
  assert.equal(chooseLanguage(null), 'en')
  assert.equal(readLanguage({getItem(key) {assert.equal(key, LANGUAGE_STORAGE_KEY); return 'zh-CN'}}, ['en']), 'zh-CN')
  assert.equal(readLanguage({getItem() {throw new Error('blocked')}}, ['zh']), 'zh-CN')
  assert.equal(readLanguage(null, ['en']), 'en')
})

test('translations preserve interpolation values and safely fall back for unknown diagnostics', () => {
  assert.equal(translate('Settings', 'zh-CN'), '设置')
  assert.equal(translate('Settings', 'en'), 'Settings')
  assert.equal(translate('{count} devices', 'zh-CN', {count: 5}), '5 台设备')
  assert.equal(translate('IPv4: {0} · IPv6: {1}', 'zh-CN', {0:'192.168.1.1',1:'fd00::1'}), 'IPv4：192.168.1.1 · IPv6：fd00::1')
  assert.equal(translate('server detail: <script>alert(1)</script>', 'zh-CN'), 'server detail: <script>alert(1)</script>')
  assert.equal(translate('{0}', 'zh-CN', {0:'$& {1} <b>not HTML</b>'}), '$& {1} <b>not HTML</b>')
  assert.equal(translate('{missing}', 'zh-CN'), '{missing}')
  assert.equal(translate('__proto__', 'zh-CN'), '__proto__')
  assert.equal(translate(undefined, 'zh-CN'), '')
  assert.equal(translate('New passwords do not match.\nserver detail', 'zh-CN'), '两次输入的新密码不一致。\nserver detail')
})

test('every Chinese catalog entry preserves all placeholders', () => {
  assert.ok(Object.keys(zhCN).length > 500)
  const placeholders = text => [...text.matchAll(/\{(\w+)\}/g)].map(m => m[1]).sort()
  for (const [source, translated] of Object.entries(zhCN)) {
    assert.ok(translated.trim(), 'Empty translation: ' + source)
    assert.deepEqual(placeholders(translated), placeholders(source), 'Placeholder mismatch: ' + source)
  }
})

test('all static UI translation calls and rich messages have Chinese entries', async () => {
  const base = new URL('../src/', import.meta.url)
  const paths = ['useNodeKeyRenewal.ts', ...await Promise.all(['pages', 'components'].map(async directory =>
    (await readdir(new URL(directory + '/', base))).filter(name => name.endsWith('.tsx')).map(name => directory + '/' + name))).then(groups => groups.flat())]
  const missing = new Set()
  function check(message) { if (!Object.hasOwn(zhCN, message)) missing.add(message) }
  function visit(node) {
    if (!node || typeof node !== 'object') return
    if (node.type === 'CallExpression' && node.callee.name === 't' && typeof node.arguments[0]?.value === 'string') check(node.arguments[0].value)
    if (node.type === 'JSXAttribute' && node.name.name === 'message' && typeof node.value?.value === 'string') check(node.value.value)
    for (const value of Object.values(node)) {
      if (Array.isArray(value)) value.forEach(visit)
      else if (value && typeof value === 'object') visit(value)
    }
  }
  for (const path of paths) visit(parseSync(path, await readFile(new URL(path, base), 'utf8')).program)
  assert.deepEqual([...missing], [])
})

test('Chinese dates, counts and durations use the selected language without altering data', () => {
  const now = Date.parse('2026-09-25T12:00:00Z')
  const status = {haveNodeKey:true, self:{keyExpiry:new Date(now + 2 * 86400000).toISOString()}}
  assert.equal(nodeKeyStatus(status, '', now, 'zh-CN').label, '剩余 2 天')
  assert.equal(nodeKeyStatus(status, '', now, 'en').label, '2 days remaining')
  assert.equal(formatDeviceUptime(90061, 'zh-CN'), '1 天 1 小时 1 分钟')
  assert.equal(formatDeviceUptime(90061, 'en'), '1d 1h 1m')
  assert.equal(formatTrafficBytes(1536, 'zh-CN'), '1.5 KiB')
  assert.equal(historyHourLabel(now, 'zh-CN'), new Date(now).toLocaleString('zh-CN', {month:'short',day:'numeric',hour:'numeric',minute:'2-digit',timeZoneName:'short'}))
  assert.deepEqual(forwardingWarnings({ipv4Forwarding:false,ipv6Forwarding:null}, true, [], 'zh-CN'), [
    'IPv4 转发已禁用。请在操作系统中启用，路由才能正常工作。',
    '无法检查 IPv6 转发状态。使用路由之前，请在操作系统中确认。',
  ])
})

test('dangerous actions keep the RESET literal, filenames, and scope identifiers intact', () => {
  assert.match(translate('Type RESET to confirm', 'zh-CN'), /RESET/)
  assert.match(translate('Select route write permission ({0}). This permits approving exit nodes and subnet routes. {1} alone is not sufficient; avoid granting “All” access.', 'zh-CN', {0:'devices:routes',1:'auth_keys'}), /devices:routes/)
  assert.match(translate('This permanently clears {0}, {1}, and the {2} folder, deletes {3}, logs out of Tailscale, signs this browser out, and restarts the portal. The new signing key invalidates all existing portal sessions.', 'zh-CN', {0:'nanotail.yml',1:'nanotail.sqlite3',2:'logs',3:'nanotail.key'}), /nanotail\.key/)
})
