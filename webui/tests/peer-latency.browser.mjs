import assert from 'node:assert/strict'

export async function checkPeerLatencies({ evaluate, send, waitFor, click, pause }) {
  await evaluate(`(() => {
    window.beforeLatencyFetch = window.fetch;
    window.latencyCalls = 0; window.latencyAborts = 0;
    window.latencyHold = false; window.latencyFailure = false; window.latencyHidden = false;
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => window.latencyHidden });
    const peers = [
      { id: 'a', hostName: 'laptop', dnsName: '', os: 'linux', tailscaleIPs: ['100.64.0.1', 'fd7a:115c:a1e0::1'], online: true },
      { id: 'b', hostName: 'offline', dnsName: '', os: 'android', tailscaleIPs: ['100.64.0.2'], online: false },
      { id: 'c', hostName: 'unreachable', dnsName: '', os: 'windows', tailscaleIPs: ['100.64.0.3'], online: true },
      { id: 'd', hostName: 'nearby', dnsName: '', os: 'linux', tailscaleIPs: ['100.64.0.4'], online: true },
    ];
    window.fetch = async (url, options) => {
      const operation = JSON.parse(options?.body || '{}').operationName;
      if (operation === 'TailscaleStatus') {
        const response = await window.beforeLatencyFetch(url, options);
        const body = await response.json(); body.data.tailscaleStatus.peers = peers;
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
        return Response.json({data: {tailscaleStatus: {peers: peers.map((peer, index) => ({id: peer.id, latencyMs: [12, null, null, 0][index]}))}}});
      }
      return window.beforeLatencyFetch(url, options);
    };
  })()`)
  const values = () => evaluate("[...document.querySelectorAll('.peer-latency')].map(el => el.textContent)")
  try {
    await click('Refresh status')
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
    assert.equal(await evaluate('window.latencyCalls'), 0, 'another page measured latency')
    await evaluate("location.hash='#/'")
    await waitFor("document.querySelector('.peer-latency')?.textContent === '12 ms'")
    assert.deepEqual(await values(), ['12 ms', '—', 'Unavailable', '0 ms'])
    assert.equal(await evaluate('window.latencyCalls'), 1)

    for (const width of [1280, 390, 320]) {
      await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width < 600 })
      await pause(100)
      assert.equal(await evaluate(`(() => {
        const row = document.querySelector('.peer-row').getBoundingClientRect();
        const latency = document.querySelector('.peer-latency').getBoundingClientRect();
        const status = document.querySelector('.peer-status').getBoundingClientRect();
        return latency.right <= row.right + 1 && status.right <= latency.left && latency.width > 0 && document.documentElement.scrollWidth <= innerWidth;
      })()`), true, 'latency overflow at ' + width)
    }
    await send('Emulation.clearDeviceMetricsOverride')

    await evaluate('window.latencyHold = true')
    await click('Refresh status')
    await waitFor("document.querySelector('.peer-latency')?.textContent === 'Checking…'")
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
    assert.equal(await evaluate("document.querySelectorAll('.peer-row').length"), 4, 'slow probes hid peer status')
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
    assert.deepEqual(await values(), ['Unavailable', '—', 'Unavailable', 'Unavailable'])
    await evaluate('window.latencyFailure = false')
    await click('Refresh status')
    await waitFor("document.querySelector('.peer-latency')?.textContent === '12 ms'")
  } finally {
    await send('Emulation.clearDeviceMetricsOverride')
    await evaluate("window.fetch = window.beforeLatencyFetch; delete document.hidden; location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets'))")
    await click('Refresh status')
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
  }
  console.log('PASS: peer latency on demand, zero/offline/unavailable values, slow probes, hidden tabs, cancellation, recovery, and responsive layout')
}
