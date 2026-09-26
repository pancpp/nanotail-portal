import assert from 'node:assert/strict'
import { writeFile } from 'node:fs/promises'

export async function checkPeerRelay({ evaluate, send, waitFor, click, fill, pause }) {
  await evaluate(`(() => {
    window.beforePeerRelayFetch = window.fetch;
    window.beforePeerRelayRouting = structuredClone(window.routingFixture);
    window.peerRelayWrites = []; window.peerRelayFailure = ''; window.peerRelayHold = false; window.peerRelayReadFailure = false;
    window.fetch = async (url, options) => {
      const body = JSON.parse(options?.body || '{}');
      if (body.operationName === 'TailscaleRouting' && window.peerRelayReadFailure) throw new TypeError('Peer relay read unavailable');
      if (body.operationName !== 'SetPeerRelay') return window.beforePeerRelayFetch(url, options);
      window.peerRelayWrites.push(body.variables.input); window.apiWrites.push(body.operationName);
      if (window.peerRelayHold) await new Promise(resolve => {window.releasePeerRelay = resolve});
      if (window.peerRelayFailure === 'rejected') return Response.json({errors:[{message:'Only portal administrators can change peer relay settings'}]});
      const input = body.variables.input;
      Object.assign(window.routingFixture, {peerRelayEnabled:input.enabled, peerRelayPort:input.enabled ? input.port : null});
      if (window.peerRelayFailure === 'network') throw new TypeError('Connection interrupted after save');
      return Response.json({data:{setPeerRelay:true}});
    };
  })()`)
  const loaded = () => waitFor("Boolean(document.getElementById('peer-relay-enabled')) && document.querySelector('.peer-relay-settings form').getAttribute('aria-busy') === 'false'")
  const saveDone = () => waitFor("Boolean(document.querySelector('.peer-relay-settings .form-success')) && document.querySelector('.peer-relay-settings form').getAttribute('aria-busy') === 'false'")
  const port = () => evaluate("document.getElementById('peer-relay-port').value")
  const screenshot = async (name, selector) => {
    if (!process.env.PEER_RELAY_SCREENSHOT_DIR) return
    const clip = await evaluate(`(() => {const r=document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();return {x:r.left+scrollX,y:r.top+scrollY,width:r.width,height:r.height,scale:1}})()`)
    const image = await send('Page.captureScreenshot', {format:'png',captureBeyondViewport:true,clip})
    await writeFile(`${process.env.PEER_RELAY_SCREENSHOT_DIR}/${name}.png`, Buffer.from(image.data,'base64'))
  }
  try {
    await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1100,deviceScaleFactor:1,mobile:false})
    await loaded()
    assert.equal(await port(), '40001')
    assert.equal(await evaluate("document.getElementById('peer-relay-enabled').checked"), false)
    assert.equal(await evaluate('window.peerRelayWrites.length'), 0)
    await evaluate("document.getElementById('peer-relay-enabled').click()")
    await click('Save peer relay'); await saveDone()
    assert.deepEqual(await evaluate('window.peerRelayWrites[0]'), {enabled:true,port:40001})
    for (const value of ['0','65536','1.5']) {
      await fill('peer-relay-port',value)
      assert.equal(await evaluate("document.querySelector('.peer-relay-settings button[type=submit]').disabled"),true)
    }
    await fill('peer-relay-port','45678')
    await click('Refresh status')
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
    assert.equal(await port(),'45678','background refresh replaced a draft')
    await evaluate('window.peerRelayHold = true')
    await click('Save peer relay')
    await waitFor('window.peerRelayWrites.length === 2')
    await evaluate("document.querySelector('.peer-relay-settings form').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}))")
    assert.equal(await evaluate('window.peerRelayWrites.length'),2,'duplicate save')
    await evaluate('window.peerRelayHold = false; window.releasePeerRelay()')
    await saveDone()
    assert.equal(await port(),'45678')
    assert.deepEqual(await evaluate('window.routingFixture.subnetRoutes'),await evaluate('window.beforePeerRelayRouting.subnetRoutes'))
    await screenshot('access-control','.peer-relay-settings')

    await evaluate("location.hash='#/'")
    await waitFor("document.querySelector('.peer-relay-card strong')?.textContent === 'Enabled'")
    assert.ok(await evaluate("document.querySelector('.peer-relay-card').textContent.includes('UDP port 45678')"))
    await screenshot('overview','.status-grid')
    await evaluate("document.querySelector('.peer-relay-card a').click()")
    await loaded()
    assert.equal(await evaluate('location.hash'),'#/access-control#peer-relay')
    await evaluate("document.getElementById('peer-relay-enabled').click()")
    await click('Save peer relay'); await saveDone()
    assert.deepEqual(await evaluate('window.peerRelayWrites.at(-1)'),{enabled:false,port:40001})
    assert.equal(await evaluate('window.routingFixture.peerRelayPort'),null)

    await evaluate("Object.assign(window.routingFixture,{backendState:'Stopped',peerRelayEnabled:true,peerRelayPort:0})")
    await click('Reload peer relay settings'); await loaded()
    assert.equal(await port(),'40001')
    assert.ok(await evaluate("document.querySelector('.peer-relay-settings').textContent.includes('currently chooses a port automatically')"))
    await click('Save peer relay'); await saveDone()
    await evaluate("location.hash='#/'")
    await waitFor("document.querySelector('.peer-relay-card strong')?.textContent === 'Paused'")
    await evaluate("location.hash='#/access-control'"); await loaded()
    await evaluate("window.routingFixture.backendState='Running';window.peerRelayFailure='rejected'")
    await fill('peer-relay-port','45679')
    await click('Save peer relay')
    await waitFor("document.querySelector('.peer-relay-settings [role=alert]')?.textContent.includes('Only portal administrators')")
    assert.equal(await evaluate("document.getElementById('peer-relay-port').disabled"),true)
    await evaluate("window.peerRelayFailure=''")
    await click('Reload peer relay settings'); await loaded()
    assert.equal(await port(),'40001')

    await fill('peer-relay-port','45680')
    await evaluate("window.peerRelayFailure='network'")
    await click('Save peer relay')
    await waitFor("document.querySelector('.peer-relay-settings [role=alert]')?.textContent.includes('interrupted')")
    const writes = await evaluate('window.peerRelayWrites.length')
    await pause(250)
    assert.equal(await evaluate('window.peerRelayWrites.length'),writes,'uncertain save retried')
    assert.equal(await evaluate("document.getElementById('peer-relay-enabled').disabled"),true)
    await evaluate("window.peerRelayFailure=''")
    await click('Reload peer relay settings'); await loaded()
    assert.equal(await port(),'45680','readback did not recover uncertain save')

    await evaluate('window.peerRelayReadFailure=true')
    await click('Reload peer relay settings')
    await waitFor("document.querySelector('.peer-relay-settings [role=alert]')?.textContent.includes('unavailable')")
    assert.equal(await evaluate("document.querySelector('.peer-relay-settings button[type=submit]').disabled"),true)
    await evaluate('window.peerRelayReadFailure=false')
    await click('Reload peer relay settings'); await loaded()
    await evaluate("(() => {const el=document.querySelector('.language-selector select');el.value='zh-CN';el.dispatchEvent(new Event('change',{bubbles:true}))})()")
    await waitFor("document.getElementById('peer-relay-heading').textContent === '设备中继'")
    assert.equal(await port(),'45680')
    assert.ok(await evaluate("document.querySelector('.peer-relay-settings').textContent.includes('40001')"))
    await send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true})
    await pause(100)
    assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth'),true,'peer relay form overflow')
    await screenshot('mobile','.peer-relay-settings')
  } finally {
    await evaluate("window.fetch=window.beforePeerRelayFetch;window.routingFixture=window.beforePeerRelayRouting;(() => {const el=document.querySelector('.language-selector select');el.value='en';el.dispatchEvent(new Event('change',{bubbles:true}))})();location.hash='#/access-control'")
    await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1100,deviceScaleFactor:1,mobile:false})
    await waitFor("document.getElementById('peer-relay-heading')?.textContent === 'Peer relay'")
    await click('Reload peer relay settings'); await loaded()
    await click('Reload settings')
    await waitFor("document.querySelector('.routing-settings form')?.getAttribute('aria-busy') === 'false'")
  }
  console.log('PASS: peer relay default/custom port, enable/disable, admin rejection, stopped/automatic settings, readback, drafts, duplicate/uncertain saves, Overview navigation, Chinese and mobile')
}
