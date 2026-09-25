import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile, mkdtemp, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, extname } from 'node:path'
import { spawn } from 'node:child_process'

const dist = fileURLToPath(new URL('../dist', import.meta.url))
const server = createServer(async (req, res) => {
  try {
    const pathname = new URL(req.url, 'http://localhost').pathname
    const file = join(dist, pathname === '/' ? 'index.html' : pathname)
    if (!file.startsWith(dist + '/')) throw new Error('invalid path')
    res.setHeader('Content-Type', ({ '.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html', '.svg': 'image/svg+xml' })[extname(file)] || 'application/octet-stream')
    res.end(await readFile(file))
  } catch { res.writeHead(404).end() }
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const url = `http://127.0.0.1:${server.address().port}/#/access-control`
const profile = await mkdtemp(join(tmpdir(), 'nanotail-router-chrome-'))
const chrome = spawn(process.env.CHROME_BIN || '/usr/bin/google-chrome', ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-background-networking', '--no-first-run', '--disable-extensions', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: ['ignore', 'ignore', 'ignore'] })
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
let socket
try {
  let port
  for (let n = 0; n < 100; n++) {
    try { port = (await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; break } catch { await pause(50) }
  }
  assert.ok(port, 'Chrome did not start')
  const page = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' }).then(r => r.json())
  socket = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject })
  let sequence = 0
  const pending = new Map()
  socket.onmessage = event => {
    const message = JSON.parse(event.data)
    if (message.id && pending.has(message.id)) {
      const { resolve, reject, timer } = pending.get(message.id)
      pending.delete(message.id); clearTimeout(timer)
      if (message.error) reject(new Error(JSON.stringify(message.error))); else resolve(message.result)
    }
  }
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)) }, 5000)
    pending.set(id, { resolve, reject, timer })
    socket.send(JSON.stringify({ id, method, params }))
  })
  const evaluate = async expression => {
    const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails))
    return result.result.value
  }
  const waitFor = async expression => {
    for (let n = 0; n < 100; n++) { if (await evaluate(expression)) return; await pause(30) }
    throw new Error(`Condition not met: ${expression}\n${await evaluate('document.body.innerText')}`)
  }
  const click = text => evaluate(`[...document.querySelectorAll('button')].find(b => b.textContent.trim() === ${JSON.stringify(text)}).click()`)
  const buttonStyle = text => evaluate(`(() => {
    const button = [...document.querySelectorAll('button')].find(b => b.textContent.trim() === ${JSON.stringify(text)});
    const style = getComputedStyle(button);
    // Flex parents blockify inline-flex; compare visible styling, not that layout distinction.
    return Object.fromEntries(['color', 'backgroundColor', 'borderWidth', 'borderRadius', 'padding', 'fontSize', 'fontWeight', 'boxShadow', 'marginTop', 'alignSelf'].map(key => [key, style[key]]));
  })()`)
  const assertTailnetButtonLayout = async () => {
    const layout = await evaluate(`(() => {
      const button = document.querySelector('.tailnet-settings button[type=submit]');
      const box = button.getBoundingClientRect(), text = button.querySelector('span').getBoundingClientRect(), icon = button.querySelector('svg').getBoundingClientRect();
      return { leftInset: text.left - box.left, rightInset: box.right - icon.right, gap: icon.left - text.right };
    })()`)
    assert.equal(layout.leftInset, 17, 'tailnet label must be on the left')
    assert.equal(layout.rightInset, 17, 'tailnet icon must be on the right')
    assert.ok(layout.gap > 0, 'tailnet icon overlaps the label')
  }
  const fill = (id, value) => evaluate(`(() => { const el = document.getElementById(${JSON.stringify(id)}); Object.getOwnPropertyDescriptor(el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value').set.call(el, ${JSON.stringify(value)}); el.dispatchEvent(new Event('input', { bubbles: true })); })()`)

  await send('Page.enable')
  await send('Page.addScriptToEvaluateOnNewDocument', { source: "(() => {\n localStorage.setItem('nanotail_access_token', 'header.' + btoa(JSON.stringify({pid:1,exp:Math.floor(Date.now()/1000)+3600})) + '.signature');\n window.routingFixture = {\n  backendState:'Running',advertiseExitNode:true,subnetDefaultsPending:true,subnetRoutes:[],usingExitNode:false,snatEnabled:true,health:[],\n  lanInterface:'eth0',defaultSubnetRoutes:['192.168.42.0/24','fd00:1234::/64'],lanWarning:'',ipv4Forwarding:true,ipv6Forwarding:true\n };\n window.routingWrites = []; window.nodeKeyExpiry = null; window.connectionEnabled = true; window.connectionWrites = []; window.logoutCalls = 0;\n window.routingWriteFailure = false;\n window.credentialFixture = null; window.credentialWrites = []; window.credentialRemovals = 0;\n window.credentialRemoveFailure = false; window.holdCredentialRemoval = false;\n const realFetch = window.fetch.bind(window);\n window.fetch = async (url, options) => {\n  if (!String(url).startsWith('/api/')) return realFetch(url, options);\n  const body = JSON.parse(options.body || '{}');\n  switch(body.operationName) {\n   case 'TailscaleRouting':return Response.json({data:{tailscaleRouting:structuredClone(window.routingFixture)}});\n   case 'SetRouting':\n    window.routingWrites.push(body.variables.input);\n    Object.assign(window.routingFixture,body.variables.input,{usingExitNode:false,advertiseExitNode:true,subnetDefaultsPending:false});\n    if(window.routingWriteFailure) throw new TypeError('Simulated connection interrupted after apply');\n    return Response.json({data:{setRouting:true}});\n   case 'TailscaleStatus':return Response.json({data:{tailscaleStatus:{backendState:'Running',haveNodeKey:true,tailscaleIPs:['100.64.0.2'],currentTailnet:{name:'Test tailnet'},self:{online:true,keyExpiry:window.nodeKeyExpiry},peers:[]}}});\n   case 'TailscaleKeyRenewal':return Response.json({data:{tailscaleKeyRenewal:{state:'IDLE',authURL:'',canRenew:window.nodeKeyExpiry!==null,attemptID:''}}});\n   case 'TailscaleConnection':return Response.json({data:{tailscaleConnection:{enabled:window.connectionEnabled,backendState:window.connectionEnabled?'Running':'Stopped',canEnable:true}}});\n   case 'SetTailscaleEnabled':window.connectionWrites.push(body.variables.enabled);window.connectionEnabled=body.variables.enabled;return Response.json({data:{setTailscaleEnabled:true}});\n   case 'LogoutTailscale':window.logoutCalls++;throw new Error('Network must not log out');\n   case 'DeviceStatus':return Response.json({data:{deviceStatus:{hostname:'nanotail',lanIPType:'DHCP',lanIP:'192.168.42.8/24',gateway:'192.168.42.1',dns:['192.168.42.1'],lanIPv6Type:'auto',lanIPv6:'fd00:1234::8/64',gateway6:'',ethAddr:'02:00:00:00:00:01',cpuload:0,memory:20,lastRestart:'2026-09-24T00:00:00Z',uptime:3600,health:'Healthy'}}});\n   case 'TailscaleClient':return Response.json({data:{tailscaleClient:structuredClone(window.credentialFixture)}});\n   case 'SetTailscaleCredential':\n    window.credentialWrites.push(body.variables.credential);\n    window.credentialFixture={clientId:body.variables.credential.clientId,hasClientSecret:true,updateTime:new Date().toISOString()};\n    return Response.json({data:{setTailscaleCredential:true}});\n   case 'ClearTailscaleCredential':\n    window.credentialRemovals++;\n    if(window.credentialRemoveFailure) return Response.json({errors:[{message:'Unable to remove credentials.'}]});\n    if(window.holdCredentialRemoval) await new Promise(resolve => {window.releaseCredentialRemoval=resolve});\n    window.credentialFixture=null;\n    return Response.json({data:{clearTailscaleCredential:true}});\n   default:return Response.json({errors:[{message:'Read-only browser fixture'}]});\n  }\n };\n})()" })
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false })
  await send('Page.navigate', { url })
  await waitFor("Boolean(document.getElementById('routing-subnets'))")
  const reloadSettingsStyle = await buttonStyle('Reload settings')
  const localLANStyle = await buttonStyle('Use local LAN')
  assert.equal(await evaluate("document.getElementById('routing-subnets').value"), '192.168.42.0/24\nfd00:1234::/64')
  assert.equal(await evaluate("document.getElementById('routing-subnet-enabled').checked"), true)
  assert.ok(await evaluate("document.body.innerText.includes('Local LAN advertisement is pending')"))
  assert.equal(await evaluate("window.routingWrites.length"), 0, 'opening settings changed routes')
  assert.equal(await evaluate("document.querySelector('.routing-settings button[type=submit]').disabled"), true)
  assert.equal(await evaluate("document.getElementById('routing-exit-node')"), null)
  assert.ok(await evaluate("document.body.innerText.includes('Always enabled')"))
  await fill('routing-subnets', '192.168.42.8/24')
  await evaluate("document.getElementById('routing-ack').click()")
  assert.equal(await evaluate("document.querySelector('.routing-settings button[type=submit]').disabled"), true, 'invalid subnet accepted')
  await click('Use local LAN')
  assert.equal(await evaluate("document.getElementById('routing-ack').checked"), false, 'edits retained acknowledgement')
  await evaluate("document.getElementById('routing-ack').click()")
  await click('Save and apply routing')
  await waitFor("document.querySelector('.routing-settings .form-success') && !document.querySelector('.routing-settings').innerText.includes('Loading routing settings')")
  assert.deepEqual(await evaluate("window.routingWrites[0]"), {subnetRoutes:['192.168.42.0/24','fd00:1234::/64']})

  // Removing subnet advertisements must preserve the mandatory exit-node role.
  await evaluate("document.getElementById('routing-subnet-enabled').click();document.getElementById('routing-ack').click()")
  await click('Save and apply routing')
  await waitFor("window.routingWrites.length === 2 && !document.querySelector('.routing-settings').innerText.includes('Loading routing settings')")
  assert.deepEqual(await evaluate("window.routingWrites[1]"), {subnetRoutes:[]})
  assert.equal(await evaluate("window.routingFixture.advertiseExitNode"), true)
  await click('Reload settings')
  await waitFor("!document.querySelector('.routing-settings').innerText.includes('Loading routing settings')")
  assert.equal(await evaluate("document.getElementById('routing-subnet-enabled').checked"),false,'reload re-enabled saved off choice')
  await evaluate("document.getElementById('routing-subnet-enabled').click()")
  await fill('routing-subnets', '10.20.0.0/16')
  await evaluate("document.getElementById('routing-ack').click()")
  await click('Save and apply routing')
  await waitFor("window.routingWrites.length === 3 && document.getElementById('routing-subnets')?.value === '10.20.0.0/16'")
  await click('Reload settings')
  await waitFor("document.getElementById('routing-subnets')?.value === '10.20.0.0/16'")

  // An uncertain mutation may have applied. Disable edits until explicit reload,
  // then use fresh readback, never automatically retry.
  await fill('routing-subnets', '10.30.0.0/16')
  await evaluate("window.routingWriteFailure=true;document.getElementById('routing-ack').click()")
  await click('Save and apply routing')
  await waitFor("document.querySelector('.routing-settings [role=alert]')?.textContent.includes('interrupted')")
  assert.equal(await evaluate("document.getElementById('routing-subnet-enabled').matches(':disabled')"), true)
  await pause(250)
  assert.equal(await evaluate("window.routingWrites.length"),4)
  await evaluate("window.routingWriteFailure=false")
  await click('Reload settings')
  await waitFor("document.getElementById('routing-subnet-enabled')?.checked && !document.getElementById('routing-subnet-enabled').matches(':disabled')")
  assert.equal(await evaluate("document.getElementById('routing-subnets').value"),'10.30.0.0/16')

  if (process.env.ROUTING_SCREENSHOT) {
    const result = await send('Page.captureScreenshot', {format:'png',captureBeyondViewport:true})
    await writeFile(process.env.ROUTING_SCREENSHOT, Buffer.from(result.data,'base64'))
  }
  await evaluate("location.hash='#/'")
  await waitFor("document.querySelectorAll('.status-grid .status-card').length === 4")
  assert.equal(await evaluate("[...document.querySelectorAll('.routing-card strong')].map(el=>el.textContent).join(',')"),'Advertised,Advertised')
  assert.ok(await evaluate("document.querySelector('.route-list').textContent.includes('10.30.0.0/16')"))
  assert.equal(await evaluate("document.documentElement.scrollWidth <= innerWidth"),true,'desktop layout overflows')
  // Expiry-disabled keys cannot start Renew, including in an already-open dialog.
  await waitFor("document.querySelector('.node-key-card strong')?.textContent === 'Expiry disabled'")
  assert.equal(await evaluate("document.querySelector('.node-key-renew').disabled"),true)
  await evaluate("document.querySelector('.node-key-renew').click()")
  assert.equal(await evaluate("Boolean(document.querySelector('dialog[open]'))"),false)
  await evaluate("window.nodeKeyExpiry='2099-01-01T00:00:00Z'")
  await click('Refresh status')
  await waitFor("document.querySelector('.node-key-renew')?.disabled === false")
  await evaluate("document.querySelector('.node-key-renew').click()")
  await waitFor("Boolean(document.querySelector('dialog[open] .renewal-submit'))")
  await evaluate("window.nodeKeyExpiry=null")
  // Trigger the same refresh used by periodic polling while the dialog is open.
  await click('Refresh status')
  await waitFor("document.querySelector('dialog[open]')?.innerText.includes('so renewal is disabled')")
  assert.equal(await evaluate("document.querySelector('.node-key-renew').disabled"),true)
  assert.equal(await evaluate("Boolean(document.querySelector('.renewal-submit'))"),false)
  await evaluate("document.querySelector('[aria-label=\"Close renewal dialog\"]').click()")
  await waitFor("!document.querySelector('dialog[open]')")

  await send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true})
  assert.equal(await evaluate("document.documentElement.scrollWidth <= innerWidth"),true,'mobile overview overflows')
  await evaluate("location.hash='#/access-control'")
  await waitFor("Boolean(document.getElementById('routing-subnets'))")
  assert.equal(await evaluate("document.documentElement.scrollWidth <= innerWidth"),true,'mobile form overflows')

  // Stopped Tailscale still permits subnet withdrawal, never disabling exit service.
  await evaluate("window.routingFixture.backendState='Stopped'")
  await click('Reload settings')
  await waitFor("document.querySelector('.routing-settings').innerText.includes('Tailscale is not running')")
  await evaluate("document.getElementById('routing-subnet-enabled').click();document.getElementById('routing-ack').click()")
  await click('Save and apply routing')
  await waitFor("window.routingWrites.length===5 && !document.querySelector('.routing-settings').innerText.includes('Loading routing settings')")
  assert.deepEqual(await evaluate("window.routingWrites[4]"),{subnetRoutes:[]})
  assert.equal(await evaluate("window.routingFixture.advertiseExitNode"),true)

  // Missing LAN/forwarding information remains editable, and unknown isn't true.
  await evaluate("Object.assign(window.routingFixture,{backendState:'Running',defaultSubnetRoutes:[],lanWarning:'LAN unavailable; enter routes manually',ipv4Forwarding:null,ipv6Forwarding:false})")
  await click('Reload settings')
  await waitFor("document.querySelector('.routing-settings').innerText.includes('LAN unavailable')")
  assert.equal(await evaluate("document.getElementById('routing-subnets').value"),'')
  await evaluate("document.getElementById('routing-subnet-enabled').click()")
  await fill('routing-subnets','192.168.99.0/24')
  await waitFor("document.querySelector('.routing-settings').innerText.includes('IPv4 forwarding could not be checked')")
  assert.equal(await evaluate("window.routingWrites.length"),5)
  // A pending default is still an enabled preference, even with no LAN while
  // disconnected. An explicit off must be savable despite the empty readback.
  await evaluate("Object.assign(window.routingFixture,{backendState:'Stopped',subnetDefaultsPending:true,subnetRoutes:[]})")
  await click('Reload settings')
  await waitFor("document.querySelector('.routing-settings').innerText.includes('Local LAN advertisement is pending')")
  assert.equal(await evaluate("document.getElementById('routing-subnet-enabled').checked"),true)
  await evaluate("document.getElementById('routing-subnet-enabled').click();document.getElementById('routing-ack').click()")
  await click('Save and apply routing')
  await waitFor("window.routingWrites.length===6 && !document.querySelector('.routing-settings').innerText.includes('Loading routing settings')")
  assert.deepEqual(await evaluate("window.routingWrites[5]"),{subnetRoutes:[]})
  assert.equal(await evaluate("document.getElementById('routing-subnet-enabled').checked"),false)
  assert.equal(await evaluate("window.routingFixture.subnetDefaultsPending"),false)

  // Network retains pause/resume only; signing out is covered by factory reset.
  await evaluate("location.hash='#/network'")
  await waitFor("Boolean(document.getElementById('tailnet-ack')) && Boolean(document.querySelector('.lan-settings .text-action'))")
  const discardStyle = await buttonStyle('Discard edits and use current values')
  assert.deepEqual(await buttonStyle('Reload connection'),discardStyle,'reload connection style differs from discard')
  assert.deepEqual(reloadSettingsStyle,discardStyle,'reload settings style differs from discard')
  assert.deepEqual(localLANStyle,discardStyle,'local LAN style differs from discard')
  await assertTailnetButtonLayout()
  assert.equal(await evaluate("document.documentElement.scrollWidth <= innerWidth"),true,'mobile Network overflows')
  await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1100,deviceScaleFactor:1,mobile:false})
  await assertTailnetButtonLayout()
  if (process.env.NETWORK_SCREENSHOT) {
    const result = await send('Page.captureScreenshot', {format:'png',captureBeyondViewport:true})
    await writeFile(process.env.NETWORK_SCREENSHOT, Buffer.from(result.data,'base64'))
  }
  assert.equal(await evaluate("document.querySelector('.tailnet-settings').innerText.includes('Log out of Tailscale')"),false)
  assert.equal(await evaluate("Boolean(document.getElementById('tailnet-logout-help'))"),false)
  assert.equal(await evaluate("document.querySelector('.tailnet-settings button[type=submit]').disabled"),true)
  await evaluate("document.getElementById('tailnet-ack').click()")
  await click('Turn tailnet off')
  await waitFor("document.querySelector('.tailnet-settings button[type=submit]')?.textContent.includes('Turn tailnet on')")
  assert.equal(await evaluate("document.querySelector('.tailnet-settings button[type=submit]').disabled"),true)
  await evaluate("document.getElementById('tailnet-ack').click()")
  await click('Turn tailnet on')
  await waitFor("document.querySelector('.tailnet-settings button[type=submit]')?.textContent.includes('Turn tailnet off')")
  await assertTailnetButtonLayout()
  assert.deepEqual(await evaluate("window.connectionWrites"),[false,true])
  assert.equal(await evaluate("window.logoutCalls"),0)


  // Credential removal is discoverable, confirmed, local-only, and safe to cancel.
  await evaluate("location.hash='#/access-control'")
  await waitFor("Boolean(document.querySelector('.credential-actions'))")
  assert.equal(await evaluate("document.querySelector('.credential-actions .danger-action').disabled"),true,'remove enabled without saved credentials')
  await click('Remove credentials')
  assert.equal(await evaluate("window.credentialRemovals"),0)
  assert.equal(await evaluate("Boolean(document.querySelector('.remove-confirmation'))"),false)
  const credentialId = await evaluate("document.querySelector('[name=client_id]').id")
  const credentialSecret = await evaluate("document.querySelector('[name=client_secret]').id")
  await fill(credentialId,'browser-test-client')
  await fill(credentialSecret,'browser-test-secret')
  await click('Save credentials')
  await waitFor("document.querySelector('.credential-settings .credential-state')?.textContent === 'Secret saved' && !document.querySelector('.credential-actions .danger-action').disabled")
  assert.deepEqual(await evaluate("window.credentialWrites"),[{clientId:'browser-test-client',clientSecret:'browser-test-secret'}])
  assert.equal(await evaluate("document.querySelector('[name=client_secret]').value"),'')
  assert.equal(await evaluate("document.body.innerText.includes('browser-test-secret')"),false)
  assert.equal(await evaluate(`(() => {
    const save = document.querySelector('.credential-actions .login-submit').getBoundingClientRect();
    const remove = document.querySelector('.credential-actions .danger-action').getBoundingClientRect();
    return remove.left > save.right && remove.top >= save.top && remove.bottom <= save.bottom;
  })()`),true,'desktop removal control is not beside Save')
  await click('Remove credentials')
  await waitFor("Boolean(document.querySelector('.remove-confirmation'))")
  assert.equal(await evaluate("window.credentialRemovals"),0,'opening confirmation removed credentials')
  assert.ok(await evaluate("document.querySelector('.remove-confirmation').textContent.includes('does not disconnect Tailscale')"))
  assert.equal(await evaluate("document.querySelector('.credential-actions .login-submit').disabled"),true)
  await click('Cancel')
  await waitFor("!document.querySelector('.remove-confirmation')")
  assert.equal(await evaluate("window.credentialRemovals"),0,'cancel removed credentials')
  assert.equal(await evaluate("document.querySelector('[name=client_id]').value"),'browser-test-client')

  // A failed removal preserves the saved state and is never automatically retried.
  await evaluate("window.credentialRemoveFailure=true")
  await click('Remove credentials')
  await click('Confirm removal')
  await waitFor("document.querySelector('.credential-settings .credential-form [role=alert]')?.textContent.includes('Unable to remove credentials')")
  assert.equal(await evaluate("document.querySelector('.credential-settings .credential-state').textContent"),'Secret saved')
  assert.equal(await evaluate("document.querySelector('[name=client_id]').value"),'browser-test-client')
  assert.ok(await evaluate("Boolean(document.querySelector('.remove-confirmation'))"))
  await pause(250)
  assert.equal(await evaluate("window.credentialRemovals"),1,'failed removal was retried automatically')
  await click('Cancel')

  // Pending removal blocks duplicate actions and uses removal-specific feedback.
  await fill(credentialSecret,'unsaved-replacement-secret')
  await evaluate("window.credentialRemoveFailure=false;window.holdCredentialRemoval=true")
  await send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true})
  await click('Remove credentials')
  assert.equal(await evaluate("document.documentElement.scrollWidth <= innerWidth"),true,'mobile credential confirmation overflows')
  if (process.env.CREDENTIAL_SCREENSHOT) {
    await pause(250)
    await evaluate("document.querySelector('.credential-actions').scrollIntoView({block:'center'})")
    const result = await send('Page.captureScreenshot', {format:'png'})
    await writeFile(process.env.CREDENTIAL_SCREENSHOT, Buffer.from(result.data,'base64'))
  }
  await click('Confirm removal')
  await waitFor("document.querySelector('.remove-confirmation').textContent.includes('Removing credentials…')")
  assert.equal(await evaluate("document.querySelector('.credential-settings .credential-form').getAttribute('aria-busy')"),'true')
  assert.equal(await evaluate("[...document.querySelectorAll('.credential-settings .credential-form button')].every(button => button.disabled)"),true)
  assert.equal(await evaluate("document.querySelector('.credential-settings .credential-form').textContent.includes('Saving changes…')"),false)
  await click('Removing credentials…')
  assert.equal(await evaluate("window.credentialRemovals"),2,'pending removal was sent twice')
  await evaluate("window.releaseCredentialRemoval()")
  await waitFor("document.querySelector('.credential-settings .credential-state')?.textContent === 'Not configured' && document.querySelector('.credential-settings .credential-form [role=status]')?.textContent.includes('Saved credentials removed')")
  assert.equal(await evaluate("document.querySelector('[name=client_id]').value"),'')
  assert.equal(await evaluate("document.querySelector('[name=client_secret]').value"),'')
  assert.equal(await evaluate("document.querySelector('[name=client_secret]').type"),'password')
  assert.equal(await evaluate("document.querySelector('[name=client_secret]').required"),true)
  assert.equal(await evaluate("document.querySelector('.credential-actions .danger-action').disabled"),true)
  assert.equal(await evaluate("Boolean(document.querySelector('.remove-confirmation'))"),false)
  assert.equal(await evaluate("window.credentialRemovals"),2)
  assert.equal(await evaluate("window.credentialWrites.length"),1,'removal submitted the credential form')
  assert.equal(await evaluate("window.logoutCalls"),0,'removal disconnected Tailscale')
  assert.deepEqual(await evaluate("window.connectionWrites"),[false,true])
  assert.equal(await evaluate("window.routingWrites.length"),6,'removal changed routing')
  assert.equal(await evaluate("Boolean(localStorage.getItem('nanotail_access_token'))"),true,'removal signed out the WebUI')
  await evaluate("location.hash='#/network'")
  await waitFor("Boolean(document.getElementById('tailnet-ack'))")
  await evaluate("location.hash='#/access-control'")
  await waitFor("document.querySelector('.credential-settings .credential-state')?.textContent === 'Not configured'")
  assert.equal(await evaluate("document.querySelector('[name=client_id]').value"),'')
  assert.equal(await evaluate("document.querySelector('.credential-actions .danger-action').disabled"),true)

  console.log('PASS: confirmed credential removal, cancellation, failure recovery, pending-state protection, local-only removal, consistent text-action styles and tailnet button layout, Network pause/resume without logout, expiry-disabled Renew, enabled LAN defaults, persistent explicit off, validation, acknowledgement, always-on exit role, subnet withdrawal and preservation, readback, uncertain-save recovery, no retries, stopped withdrawal, missing LAN, forwarding warnings, Overview, and responsive layout')
} finally {
  socket?.close()
  chrome.kill('SIGTERM')
  await new Promise(resolve => server.close(resolve))
}
