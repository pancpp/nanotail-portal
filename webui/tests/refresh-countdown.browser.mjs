import assert from 'node:assert/strict'

// Runs against the built WebUI with the routing harness's mocked APIs.
export async function checkRefreshCountdown({evaluate, waitFor, click, pause}) {
  await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
  const mutations = await evaluate('window.apiWrites.length')
  await evaluate(`(() => {
    window.countdownOriginalNow = Date.now;
    window.countdownNow = Date.now();
    Date.now = () => window.countdownNow;
    window.countdownOriginalFetch = window.fetch;
    window.countdownReads = {TailscaleStatus:0, TailscaleRouting:0, TailscaleClient:0, DeviceStatus:0};
    window.fetch = async (url, options) => {
      const operation = JSON.parse(options?.body || '{}').operationName;
      if (Object.hasOwn(window.countdownReads, operation)) {
        window.countdownReads[operation]++;
        if (window.countdownGate) await window.countdownGate;
        if (window.countdownFailure) return Response.json({errors:[{message:'Countdown test unavailable'}]});
      }
      return window.countdownOriginalFetch(url, options);
    };
  })()`)
  const advance = ms => evaluate(`window.countdownNow += ${ms}`)
  const label = value => waitFor(`document.querySelector('.updated-at')?.textContent === ${JSON.stringify(value)}`)
  const reads = () => evaluate('window.countdownReads')
  try {
    await click('Refresh status')
    await label('Auto-refresh in 30s')
    assert.deepEqual(await reads(), {TailscaleStatus:1,TailscaleRouting:1,TailscaleClient:1,DeviceStatus:1})
    assert.equal(await evaluate("document.querySelector('.updated-at').getAttribute('aria-live')"), 'off')
    await advance(1000)
    await label('Auto-refresh in 29s')
    await advance(4000)
    await label('Auto-refresh in 25s')
    assert.equal((await reads()).DeviceStatus, 1, 'countdown tick triggered a refresh')

    await evaluate("(() => {const el=document.querySelector('.language-selector select');el.value='zh-CN';el.dispatchEvent(new Event('change',{bubbles:true}))})()")
    await label('25 秒后自动刷新')
    await evaluate("(() => {const el=document.querySelector('.language-selector select');el.value='en';el.dispatchEvent(new Event('change',{bubbles:true}))})()")
    await label('Auto-refresh in 25s')

    // A slow refresh shows progress and cannot trigger duplicate requests.
    await evaluate('window.countdownGate = new Promise(resolve => {window.releaseCountdown = resolve});void 0')
    await advance(25000)
    await label('Updating…')
    assert.deepEqual(await reads(), {TailscaleStatus:2,TailscaleRouting:2,TailscaleClient:2,DeviceStatus:2})
    assert.equal(await evaluate("document.querySelector('.topbar__actions button').disabled"), true)
    await advance(90000)
    await pause(600)
    assert.equal((await reads()).DeviceStatus, 2, 'overlapping automatic refresh')
    await evaluate('window.countdownGate = null;window.releaseCountdown()')
    await label('Auto-refresh in 30s')
    await advance(11000)
    await label('Auto-refresh in 19s')
    await click('Refresh status')
    await label('Auto-refresh in 30s')
    assert.deepEqual(await reads(), {TailscaleStatus:3,TailscaleRouting:3,TailscaleClient:3,DeviceStatus:3})

    // Page changes keep the same deadline, even while the header is hidden.
    await advance(7000)
    await label('Auto-refresh in 23s')
    await evaluate("location.hash='#/network'")
    await waitFor("Boolean(document.querySelector('.tailnet-settings'))")
    await label('Auto-refresh in 23s')
    await evaluate("location.hash='#/settings'")
    await waitFor("Boolean(document.querySelector('.password-change-form'))")
    assert.equal(await evaluate("Boolean(document.querySelector('.updated-at'))"), false)
    await advance(23000)
    await waitFor('window.countdownReads.DeviceStatus === 4')
    await evaluate("location.hash='#/'")
    await waitFor("Boolean(document.querySelector('.node-key-card'))")
    await label('Auto-refresh in 30s')

    // A throttled/background timer catches up once, never as a request burst.
    await advance(90000)
    await waitFor('window.countdownReads.DeviceStatus === 5')
    await label('Auto-refresh in 30s')
    await pause(600)
    assert.equal((await reads()).DeviceStatus, 5)

    // Failures still schedule the next refresh, which can recover normally.
    await evaluate('window.countdownFailure = true')
    await advance(30000)
    await waitFor("document.querySelector('.connection-notice--error')?.textContent.includes('Countdown test unavailable')")
    await label('Auto-refresh in 30s')
    assert.equal((await reads()).DeviceStatus, 6)
    await evaluate('window.countdownFailure = false')
    await advance(30000)
    await waitFor('window.countdownReads.DeviceStatus === 7')
    await label('Auto-refresh in 30s')
    await waitFor("!document.querySelector('.connection-notice--error')")

    // Signing out unmounts the scheduler. Restore the mocked session afterward.
    const token = await evaluate("localStorage.getItem('nanotail_access_token')")
    await click('Sign out')
    await waitFor("Boolean(document.getElementById('username'))")
    await advance(90000)
    await pause(600)
    assert.equal((await reads()).DeviceStatus, 7, 'refresh continued after sign-out')
    await evaluate(`localStorage.setItem('nanotail_access_token', ${JSON.stringify(token)});window.dispatchEvent(new StorageEvent('storage',{key:'nanotail_access_token',newValue:${JSON.stringify(token)}}))`)
    await waitFor("Boolean(document.querySelector('.dashboard-shell'))")
    await evaluate("location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets')) && document.querySelector('.topbar__actions button')?.disabled === false")
    assert.equal(await evaluate('window.apiWrites.length'), mutations, 'countdown sent a mutation')
  } finally {
    await evaluate('Date.now = window.countdownOriginalNow;window.fetch = window.countdownOriginalFetch;window.countdownFailure = false;window.countdownGate = null;window.releaseCountdown?.()')
    await click('Refresh status')
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
  }
  console.log('PASS: refresh countdown, English/Chinese, shared polling, manual reset, slow requests, background catch-up, navigation, failure recovery and sign-out cleanup')
}
