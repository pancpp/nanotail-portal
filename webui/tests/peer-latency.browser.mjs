import assert from 'node:assert/strict'
import { writeFile } from 'node:fs/promises'

export async function checkPeerLatencies({ evaluate, send, waitFor, click, pause }) {
  await evaluate(`(() => {
    window.beforeLatencyFetch = window.fetch;
    window.latencyCalls = 0; window.latencyAborts = 0;
    window.latencyHold = false; window.latencyFailure = false; window.latencyHidden = false;
    window.connectionStopped = false; window.connectionDirectAddress = '203.0.113.10:41641';
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => window.latencyHidden });
    const peers = [
      { id: 'a', hostName: 'laptop', dnsName: '', os: 'linux', tailscaleIPs: ['100.64.0.1', 'fd7a:115c:a1e0::1'], online: true, active: true, curAddr: '203.0.113.10:41641', peerRelay: '203.0.113.11:40000:7', relay: 'sea' },
      { id: 'b', hostName: 'offline', dnsName: '', os: 'android', tailscaleIPs: ['100.64.0.2'], online: false, active: true, curAddr: '203.0.113.12:41641', peerRelay: '', relay: 'sea' },
      { id: 'c', hostName: 'unreachable', dnsName: '', os: 'windows', tailscaleIPs: ['100.64.0.3'], online: true, active: true, curAddr: '', peerRelay: '[2001:db8:1234:5678::1]:40000:4294967295', relay: 'sea' },
      { id: 'd', hostName: 'nearby', dnsName: '', os: 'linux', tailscaleIPs: ['100.64.0.4'], online: true, active: true, curAddr: '', peerRelay: '', relay: 'sea' },
      { id: 'e', hostName: 'idle', dnsName: '', os: 'linux', tailscaleIPs: ['100.64.0.5'], online: true, active: false, curAddr: '', peerRelay: '', relay: 'sea' },
      { id: 'f', hostName: 'unknown', dnsName: '', os: 'linux', tailscaleIPs: ['100.64.0.6'], online: true, active: true, curAddr: '', peerRelay: '', relay: '' },
    ];
    window.fetch = async (url, options) => {
      const operation = JSON.parse(options?.body || '{}').operationName;
      if (operation === 'TailscaleStatus') {
        const response = await window.beforeLatencyFetch(url, options);
        const body = await response.json(); body.data.tailscaleStatus.peers = structuredClone(peers);
        body.data.tailscaleStatus.peers[0].curAddr = window.connectionDirectAddress;
        if (window.connectionStopped) body.data.tailscaleStatus.backendState = 'Stopped';
        return Response.json(body);
      }
      if (operation === 'TailscalePeerLatencies') {
        window.latencyCalls++;
        if (window.latencyHold) await new Promise((resolve, reject) => {
          const abort = () => {window.latencyAborts++; reject(new DOMException('Aborted', 'AbortError'))};
          options.signal.addEventListener('abort', abort, {once: true});
          window.releaseLatency = () => {options.signal.removeEventListener('abort', abort); resolve()};
        });
        if (window.latencyFailure) return Response.json({errors: [{message: 'Probe request failed'}]});
        return Response.json({data: {tailscaleStatus: {peers: peers.map((peer, index) => ({id: peer.id, latencyMs: [12, null, null, 0, null, null][index]}))}}});
      }
      return window.beforeLatencyFetch(url, options);
    };
  })()`)
  const values = () => evaluate("[...document.querySelectorAll('.peer-latency')].map(el => el.textContent)")
  const connections = () => evaluate("[...document.querySelectorAll('.peer-connection__type')].map(el => el.textContent)")
  try {
    await click('Refresh status')
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
    assert.equal(await evaluate('window.latencyCalls'), 0, 'another page measured latency')
    await evaluate("location.hash='#/'")
    await waitFor("document.querySelector('.peer-latency')?.textContent === '12 ms'")
    assert.deepEqual(await values(), ['12 ms', '—', 'Unavailable', '0 ms', 'Unavailable', 'Unavailable'])
    assert.deepEqual(await connections(), ['Direct', '—', 'Peer relay', 'DERP', 'Idle', 'Unknown'])
    assert.deepEqual(await evaluate("[...document.querySelectorAll('.peer-connection__detail')].map(el => el.textContent)"),
      ['[2001:db8:1234:5678::1]:40000:4294967295', 'sea'])
    assert.equal(await evaluate('window.latencyCalls'), 1)

    for (const width of [1440, 1280, 1101, 800, 390, 320]) {
      await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width < 600 })
      await pause(100)
      assert.equal(await evaluate(`(() => {
        const row = document.querySelector('.peer-row').getBoundingClientRect();
        const latency = document.querySelector('.peer-latency').getBoundingClientRect();
        const status = document.querySelector('.peer-status').getBoundingClientRect();
        const connectionFits = [...document.querySelectorAll('.peer-connection')].every(el => {
          const box = el.getBoundingClientRect(), row = el.closest('.peer-row').getBoundingClientRect();
          return box.width > 0 && box.right <= row.right + 1 && el.scrollWidth <= el.clientWidth + 1;
        });
        return connectionFits && latency.right <= row.right + 1 && status.right <= latency.left && latency.width > 0 && document.documentElement.scrollWidth <= innerWidth;
      })()`), true, 'latency overflow at ' + width)
      if (process.env.PEER_TABLE_SCREENSHOT_DIR && [1440, 390].includes(width)) {
        const screenshot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true })
        await writeFile(`${process.env.PEER_TABLE_SCREENSHOT_DIR}/peers-${width}.png`, Buffer.from(screenshot.data, 'base64'))
      }
    }
    await send('Emulation.clearDeviceMetricsOverride')

    await evaluate("(() => {const el=document.querySelector('.language-selector select');el.value='zh-CN';el.dispatchEvent(new Event('change',{bubbles:true}))})()")
    await waitFor("document.querySelector('.peer-connection__type')?.textContent === '直连'")
    assert.deepEqual(await connections(), ['直连', '—', '设备中继', 'DERP', '空闲', '未知'])
    assert.equal(await evaluate('window.latencyCalls'), 1, 'changing language triggered probes')
    await evaluate("(() => {const el=document.querySelector('.language-selector select');el.value='en';el.dispatchEvent(new Event('change',{bubbles:true}))})()")
    await waitFor("document.querySelector('.peer-connection__type')?.textContent === 'Direct'")

    await evaluate('window.latencyHold = true')
    await click('Refresh status')
    await waitFor("document.querySelector('.peer-latency')?.textContent === 'Checking…'")
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
    assert.equal(await evaluate("document.querySelectorAll('.peer-row').length"), 6, 'slow probes hid peer status')
    assert.deepEqual(await connections(), ['Direct', '—', 'Peer relay', 'DERP', 'Idle', 'Unknown'])
    await evaluate("window.latencyHidden = true; document.dispatchEvent(new Event('visibilitychange'))")
    await waitFor('window.latencyAborts === 1')
    await click('Refresh status')
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
    assert.equal(await evaluate('window.latencyCalls'), 2, 'hidden page measured latency')

    await evaluate("window.latencyHidden = false; document.dispatchEvent(new Event('visibilitychange'))")
    await waitFor('window.latencyCalls === 3')
    await evaluate("location.hash='#/network'")
    await waitFor('window.latencyAborts === 2')
    await evaluate('window.latencyHold = false; window.latencyFailure = true')
    await evaluate("location.hash='#/'")
    await waitFor("document.querySelector('.peer-latency')?.textContent === 'Unavailable'")
    assert.deepEqual(await values(), ['Unavailable', '—', 'Unavailable', 'Unavailable', 'Unavailable', 'Unavailable'])
    assert.deepEqual(await connections(), ['Direct', '—', 'Peer relay', 'DERP', 'Idle', 'Unknown'], 'failed probes hid connection types')
    await evaluate('window.latencyFailure = false')
    await click('Refresh status')
    await waitFor("document.querySelector('.peer-latency')?.textContent === '12 ms'")

    await evaluate("window.connectionDirectAddress = ''")
    await click('Refresh status')
    await waitFor("document.querySelector('.peer-connection__type')?.textContent === 'Peer relay'")
    assert.equal(await evaluate("document.querySelector('.peer-connection__detail').textContent"), '203.0.113.11:40000:7')
    await evaluate('window.connectionStopped = true')
    await click('Refresh status')
    await waitFor("document.querySelector('.peer-connection__type')?.textContent === 'Unavailable'")
    assert.deepEqual(await connections(), Array(6).fill('Unavailable'))
  } finally {
    await send('Emulation.clearDeviceMetricsOverride')
    await evaluate("window.fetch = window.beforeLatencyFetch; delete document.hidden; location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets'))")
    await click('Refresh status')
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
  }
  console.log('PASS: peer connection types and route changes, English/Chinese, relay endpoints, idle/offline/unknown states, latency on demand, slow/failed probes, hidden tabs, cancellation, recovery, and responsive layout')
}
