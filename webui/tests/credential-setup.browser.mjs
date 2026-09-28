import assert from 'node:assert/strict'
import { writeFile } from 'node:fs/promises'

export async function checkCredentialSetup({evaluate, send, waitFor, click, fill, pause}) {
  const open = () => waitFor("Boolean(document.querySelector('.credential-setup-dialog[open]'))")
  const manual = () => waitFor("document.querySelectorAll('.credential-setup-checklist li').length === 5")
  const closed = () => waitFor("!document.querySelector('dialog[open]')")
  const escape = async () => {
    await send('Input.dispatchKeyEvent', {type:'keyDown',key:'Escape',code:'Escape',windowsVirtualKeyCode:27})
    await send('Input.dispatchKeyEvent', {type:'keyUp',key:'Escape',code:'Escape',windowsVirtualKeyCode:27})
  }
  const refresh = async () => {
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
    await click('Refresh status')
    await waitFor("document.querySelector('.topbar__actions button')?.disabled === false")
  }
  const screenshot = async name => {
    if (!process.env.CREDENTIAL_SETUP_SCREENSHOT_DIR) return
    await pause(300)
    const result = await send('Page.captureScreenshot', {format:'png'})
    await writeFile(`${process.env.CREDENTIAL_SETUP_SCREENSHOT_DIR}/${name}.png`, Buffer.from(result.data,'base64'))
  }
  // Completion hands off an open sign-in dialog, including when approval is pending.
  await click('Sign in to Tailscale')
  await waitFor("Boolean(document.querySelector('.renewal-dialog[open]'))")
  const writes = await evaluate('window.apiWrites.length')
  await evaluate("window.signInFixture=false;window.renewalState='SIGNED_IN';window.dispatchEvent(new Event('focus'))")
  await open()
  assert.equal(await evaluate("document.querySelectorAll('dialog[open]').length"), 1)
  await waitFor("Boolean(document.querySelector('.credential-setup-dialog [name=client_secret]'))")
  assert.equal(await evaluate("document.querySelector('.credential-setup-dialog a[href=\"#/tailscale-setup/oauth-credentials\"]').target"), '_blank')
  assert.equal(await evaluate("document.querySelector('.credential-setup-dialog').scrollWidth <= document.querySelector('.credential-setup-dialog').clientWidth"), true, 'mobile credentials overflow')
  await screenshot('credentials-mobile')
  await click('Skip for now'); await manual()
  const items = await evaluate("[...document.querySelectorAll('.credential-setup-checklist li')].map(li=>li.textContent)")
  for (const [i, phrase] of ['approve this device','exit node','subnet router','peer relay','Settings'].entries()) assert.ok(items[i].includes(phrase), phrase)
  assert.equal(await evaluate("document.activeElement.id"), 'credential-setup-title')
  assert.equal(await evaluate("document.querySelector('.credential-setup-dialog').scrollWidth <= document.querySelector('.credential-setup-dialog').clientWidth"), true, 'mobile reminder overflows')
  await screenshot('manual-mobile')
  await click('Add credentials now')
  await waitFor("Boolean(document.querySelector('.credential-setup-dialog [name=client_secret]'))")
  await escape(); await manual()
  await click('Continue without credentials'); await closed()
  assert.equal(await evaluate('window.apiWrites.length'), writes, 'skip changed device settings')
  await refresh()
  assert.equal(await evaluate("Boolean(document.querySelector('dialog[open]'))"), false, 'refresh reopened dismissed setup')
  await send('Page.reload', {ignoreCache:true})
  await waitFor("Boolean(document.querySelector('.status-grid')) && document.querySelector('.topbar__actions button')?.disabled === false")
  assert.equal(await evaluate("Boolean(document.querySelector('dialog[open]'))"), false, 'reload reopened dismissed setup')

  // Intercept API responses after reload for a new account, read failure, and save failure.
  await evaluate(`(() => {
    window.beforeCredentialSetupFetch=window.fetch;
    window.setupSaveCalls=0;
    window.setupReadFailure=true;
    window.setupStatus={backendState:'NeedsMachineAuth',haveNodeKey:true,tailscaleIPs:['100.64.0.3'],currentTailnet:{name:'New tailnet'},self:{id:'new-device',online:false,keyExpiry:null},peers:[]};
    window.fetch=async (url,options) => {
      const body=JSON.parse(options?.body||'{}');
      if(body.operationName==='TailscaleStatus') return Response.json({data:{tailscaleStatus:structuredClone(window.setupStatus)}});
      if(body.operationName==='TailscaleClient' && window.setupReadFailure) throw new Error('Unable to load credential settings.');
      if(body.operationName==='SetTailscaleCredential') {
        window.setupSaveCalls++;
        if(window.setupSaveFailure) return Response.json({errors:[{message:'Unable to save credentials.'}]});
        if(window.setupHoldSave) await new Promise(resolve => {window.releaseSetupSave=resolve});
      }
      return window.beforeCredentialSetupFetch(url,options);
    };
    location.hash='#/network';
  })()`)
  await waitFor("Boolean(document.getElementById('tailnet-ack'))")
  await refresh(); await open()
  await waitFor("document.querySelector('.credential-setup-dialog [role=alert]')?.textContent.includes('Unable to load')")
  assert.equal(await evaluate("Boolean(document.querySelector('.credential-setup-dialog form'))"), false)
  await evaluate('window.setupReadFailure=false')
  await click('Retry loading')
  await waitFor("Boolean(document.querySelector('.credential-setup-dialog [name=client_secret]'))")
  await evaluate("document.querySelector('[aria-label=\"Skip client credentials\"]').click()")
  await manual()
  await click('Add credentials now')
  await fill(await evaluate("document.querySelector('.credential-setup-dialog [name=client_id]').id"),'new-tailnet-client')
  await fill(await evaluate("document.querySelector('.credential-setup-dialog [name=client_secret]').id"),'new-tailnet-secret')
  await evaluate('window.setupSaveFailure=true')
  await click('Save credentials')
  await waitFor("document.querySelector('.credential-setup-dialog [role=alert]')?.textContent.includes('Unable to save')")
  assert.equal(await evaluate("document.querySelector('.credential-setup-dialog [name=client_secret]').value"), 'new-tailnet-secret')
  await evaluate('window.setupSaveFailure=false;window.setupHoldSave=true')
  await click('Save credentials')
  await waitFor('Boolean(window.releaseSetupSave)')
  assert.equal(await evaluate("document.querySelector('.credential-setup-dialog button[aria-label=\"Skip client credentials\"]').disabled"), true)
  await escape()
  assert.equal(await evaluate("Boolean(document.querySelector('.credential-setup-checklist'))"), false, 'Escape dismissed an active save')
  await evaluate("document.querySelector('.credential-setup-dialog form').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}))")
  assert.equal(await evaluate('window.setupSaveCalls'), 2, 'duplicate save')
  await evaluate('window.setupHoldSave=false;window.releaseSetupSave()')
  await closed()
  assert.deepEqual(await evaluate('window.credentialWrites'), [{clientId:'new-tailnet-client',clientSecret:'new-tailnet-secret'}])
  assert.equal(await evaluate("JSON.stringify({...localStorage,...sessionStorage}).includes('new-tailnet-secret')"), false)
  await refresh()
  assert.equal(await evaluate("Boolean(document.querySelector('dialog[open]'))"), false, 'save did not complete setup')

  // A further account change prompts again; Settings remains the deferred entry point.
  await evaluate("window.setupStatus.currentTailnet.name='Another tailnet'")
  await refresh(); await open()
  await send('Emulation.setDeviceMetricsOverride', {width:1440,height:1100,deviceScaleFactor:1,mobile:false})
  await screenshot('credentials-desktop')
  await click('Skip for now'); await manual()
  await evaluate("document.querySelector('.credential-setup-checklist a[href=\"#/settings\"]').click()")
  await closed()
  await waitFor("location.hash==='#/settings' && Boolean(document.querySelector('.credential-settings'))")
  assert.equal(await evaluate("document.querySelector('.credential-settings [name=client_id]').value"), 'new-tailnet-client')
  // Restore the original enrollment before the harness reloads its other fixtures.
  await evaluate("window.setupStatus.currentTailnet.name='Test tailnet';window.setupStatus.self.id='test-device';window.setupStatus.backendState='Running';location.hash='#/network'")
  await waitFor("Boolean(document.getElementById('tailnet-ack'))")
  await refresh(); await open()
  await click('Skip for now'); await manual()
  await click('Continue without credentials'); await closed()
  await evaluate('window.fetch=window.beforeCredentialSetupFetch')
  console.log('PASS: enrollment handoff, credential prompt, all five skip reminders, Escape/close, deferred Settings, reload persistence, new accounts, read/save errors, duplicate guards and mobile layout')
}
