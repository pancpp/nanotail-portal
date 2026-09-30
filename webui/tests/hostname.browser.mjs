import assert from 'node:assert/strict'
import { writeFile } from 'node:fs/promises'

export async function checkHostname({evaluate, send, waitFor, click, fill, pause}) {
  const field = 'document.querySelector("input[name=device_hostname]")'
  const save = 'document.querySelector(".hostname-settings button[type=submit]")'
  await evaluate(`(() => {
    window.hostnameOriginalFetch = window.fetch;
    window.hostnameFixture = 'nanotail'; window.hostnameWrites = [];
    window.fetch = async (url, options) => {
      const body = JSON.parse(options?.body || '{}');
      if (body.operationName === 'SetDeviceHostname') {
        window.hostnameWrites.push(body.variables.hostname);
        if (window.holdHostname) await new Promise(resolve => { window.releaseHostname = resolve });
        if (window.hostnameFailure) throw new TypeError('Simulated lost response');
        window.hostnameFixture = body.variables.hostname;
        return Response.json({data:{setDeviceHostname:true}});
      }
      const response = await window.hostnameOriginalFetch(url, options);
      if (body.operationName === 'DeviceStatus') {
        if (window.hostnameStatusFailure) return Response.json({errors:[{message:'Device status unavailable'}]});
        const payload = await response.json();
        payload.data.deviceStatus.hostname = window.hostnameFixture;
        return Response.json(payload);
      }
      return response;
    };
    location.hash = '#/network';
  })()`)
  try {
    await waitFor(`${field}?.value === 'nanotail'`)
    assert.equal(await evaluate(`${save}.disabled`), true)
    assert.equal(await evaluate('window.hostnameWrites.length'), 0, 'opening the card changed the hostname')
    const inputID = await evaluate(`${field}.id`)
    await fill(inputID, 'bad.name')
    assert.equal(await evaluate(`${save}.disabled`), true)
    assert.equal(await evaluate(`${field}.getAttribute('aria-invalid')`), 'true')
    await fill(inputID, 'office-router')
    await click('Refresh device status')
    await waitFor("!document.querySelector('.hostname-settings .text-action').disabled")
    assert.equal(await evaluate(`${field}.value`), 'office-router', 'refresh overwrote the draft')
    await evaluate('window.holdHostname = true')
    await click('Save hostname')
    await waitFor('Boolean(window.releaseHostname)')
    assert.equal(await evaluate(`${field}.disabled && ${save}.disabled`), true)
    await evaluate("document.querySelector('.hostname-settings form').dispatchEvent(new Event('submit', {bubbles:true,cancelable:true}))")
    assert.equal(await evaluate('window.hostnameWrites.length'), 1, 'duplicate submission')
    await evaluate('window.holdHostname = false; window.releaseHostname()')
    await waitFor("document.querySelector('.hostname-settings .form-success')?.textContent === 'Hostname saved.' && !document.querySelector('.hostname-settings input').disabled")
    assert.deepEqual(await evaluate('window.hostnameWrites'), ['office-router'])
    assert.equal(await evaluate(`${save}.disabled`), true, 'saved value should not be resubmitted')
    assert.equal(await evaluate("document.querySelector('.sidebar strong[title]')?.title || document.querySelector('strong[title=\"office-router\"]')?.title"), 'office-router')

    await fill(inputID, 'another-router')
    await evaluate('window.hostnameFailure = true')
    await click('Save hostname')
    await waitFor("document.querySelector('.hostname-settings .form-error')?.textContent.includes('did not confirm')")
    assert.equal(await evaluate(`${field}.value`), 'another-router', 'failed save lost the draft')
    assert.equal(await evaluate("document.querySelector('.hostname-settings .form-success')"), null)
    await pause(250)
    assert.equal(await evaluate('window.hostnameWrites.length'), 2, 'failed save was automatically retried')
    await evaluate("[...document.querySelectorAll('.hostname-settings button')].find(b => b.textContent === 'Discard edits and use current values').click()")
    assert.equal(await evaluate(`${field}.value`), 'office-router')

    await evaluate('window.hostnameStatusFailure = true')
    await click('Refresh device status')
    await waitFor("document.querySelector('.hostname-settings').textContent.includes('Device status unavailable')")
    await evaluate("location.hash = '#/access-control'")
    await waitFor("!document.querySelector('.hostname-settings')")
    await evaluate("location.hash = '#/network'")
    await waitFor("document.querySelector('.hostname-settings').textContent.includes('Load device status before editing the hostname.')")
    assert.equal(await evaluate(field), null)
    await evaluate('window.hostnameStatusFailure = false')
    await click('Refresh device status')
    await waitFor(`${field}?.value === 'office-router'`)

    await send('Emulation.setDeviceMetricsOverride', {width:390,height:844,deviceScaleFactor:1,mobile:true})
    await pause(300)
    assert.ok(await evaluate("document.documentElement.scrollWidth <= innerWidth"), 'Network page overflows on mobile')
    if (process.env.HOSTNAME_SCREENSHOT) {
      await evaluate("document.querySelector('.hostname-settings').scrollIntoView({block:'start'})")
      const screenshot = await send('Page.captureScreenshot', {format:'png'})
      await writeFile(process.env.HOSTNAME_SCREENSHOT, Buffer.from(screenshot.data, 'base64'))
    }
  } finally {
    await send('Emulation.setDeviceMetricsOverride', {width:1440,height:1100,deviceScaleFactor:1,mobile:false})
    await evaluate('window.fetch = window.hostnameOriginalFetch')
    await click('Refresh device status')
    await evaluate("location.hash = '#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets'))")
  }
  console.log('PASS: hostname loading, validation, draft preservation, authenticated save, duplicate prevention, header refresh, failure recovery, no automatic retries, and mobile layout')
}
