import assert from 'node:assert/strict'
import { writeFile } from 'node:fs/promises'

export async function checkUpgrade({evaluate, send, waitFor, click, pause}) {
  const card = 'document.querySelector(".upgrade-settings")'
  const prepare = `[...${card}.querySelectorAll('button')].find(button=>button.textContent.trim()==='Prepare upgrade')`
  const install = `[...${card}.querySelectorAll('button')].find(button=>button.textContent.trim()==='Install and restart')`
  const dialog = 'document.querySelector("dialog[open]")'
  const confirmInstall = `[...${dialog}.querySelectorAll('button')].find(button=>button.textContent.trim()==='Install and restart')`
  const installation = `${card}.querySelector('.upgrade-installation')`
  const installRequests = 'window.upgradeRequests.filter(request=>request.operationName==="InstallUpgrade")'
  const waitForInstallation = async expression => {
    for (let n=0;n<180;n++) { if(await evaluate(expression))return;await pause(50) }
    throw new Error(`Installation condition not met: ${expression}\n${await evaluate('document.body.innerText')}`)
  }
  const assertUpgradeMutationsBlocked = async () => {
    assert.ok(await evaluate(`[...${card}.querySelectorAll('button')].filter(button=>['Check for updates','Prepare upgrade','Install and restart'].includes(button.textContent.trim())).every(button=>button.disabled)`),'upgrade actions remained enabled during installation')
  }
  const remountSettings = async () => {
    await evaluate("location.hash='#/network'")
    await waitFor(`!${card}`)
    await evaluate("location.hash='#/settings'")
    await waitFor(`${card}?.textContent.includes('Installed version')`)
  }
  const openInstallDialog = async () => {
    await waitFor(`${install}?.disabled===false`)
    await evaluate(`${install}.click()`)
    await waitFor(`${dialog}?.textContent.includes('Install version 1.2.0?')`)
  }
  const captureUpgrade = async (suffix='', element=card) => {
    if(!process.env.UPGRADE_SCREENSHOT)return
    if(element===card)await evaluate(`window.scrollTo(0,Math.max(0,${card}.getBoundingClientRect().top+scrollY-80))`)
    await pause(150)
    const clip = await evaluate(`(()=>{const box=${element}.getBoundingClientRect();return {x:box.left+scrollX,y:box.top+scrollY,width:box.width,height:box.height,scale:1}})()`)
    const screenshot=await send('Page.captureScreenshot',{format:'png',captureBeyondViewport:element===card,...(element===card?{clip}:{})})
    const path=process.env.UPGRADE_SCREENSHOT.replace(/\.png$/, '')+suffix+'.png'
    await writeFile(path,Buffer.from(screenshot.data,'base64'))
  }
  await evaluate(`(() => {
    window.upgradeOriginalFetch=window.fetch;
    window.upgradeRequests=[];
    window.upgradeTransportRequests=[];
    window.upgradeFixture={currentVersion:'1.1.0',latestRelease:null,updateAvailable:false,checkedAt:null,stagedPackage:null,installationSupported:false,onlineCheckSupported:true,phase:'idle',installation:null};
    window.upgradeRelease={version:'1.2.0',notes:'A safer portal. <img src=x onerror=alert(1)>',publishedAt:'2026-10-01T12:00:00Z',packageName:'nanotail-portal-1.2.0-linux-arm64.tar.gz',size:1234};
    window.upgradeStaged={version:'1.2.0',notes:'A safer portal.',verifiedAt:'2026-10-01T12:01:00Z',sha256:'ab'.repeat(32),size:1234};
    window.upgradeStatusFailures=[];
    window.upgradeInstallSequence=0;
    window.upgradeInstallFixture=(phase='prepared')=>({id:'installation-'+window.upgradeInstallSequence,version:'1.2.0',phase,error:'',startedAt:window.upgradeStartedAt||new Date().toISOString(),completedAt:['complete','aborted','rolled-back'].includes(phase)?new Date().toISOString():null});
    window.fetch=async (url,options) => {
      const body=url==='/api/v1/query'?JSON.parse(options?.body||'{}'):{};
      window.upgradeTransportRequests.push({url,operationName:body.operationName});
      const field={UpgradeStatus:'upgradeStatus',CheckForUpdates:'checkForUpdates',DownloadUpgrade:'downloadUpgrade',InstallUpgrade:'installUpgrade'}[body.operationName];
      if(url!=='/api/v1/query'||!field)return window.upgradeOriginalFetch(url,options);
      window.upgradeRequests.push({url,method:options.method,authorization:new Headers(options.headers).get('Authorization'),operationName:body.operationName,variables:body.variables});
      if(window.upgradeUnauthorized)return Response.json({message:'Unauthorized'},{status:401});
      if(body.operationName==='UpgradeStatus'&&window.holdUpgradeStatus)await new Promise(resolve=>window.releaseUpgradeStatus=resolve);
      if(body.operationName==='UpgradeStatus'&&window.upgradeStatusFailures.length){
        const failure=window.upgradeStatusFailures.shift();
        if(failure==='network')throw new TypeError('Simulated restart disconnected');
        if(failure==='forbidden')return Response.json({errors:[{message:'Only portal administrators can manage upgrades',extensions:{code:'FORBIDDEN'}}]});
        if(failure===403)return Response.json({message:'Only portal administrators can manage upgrades'},{status:403});
        return new Response('Portal is restarting',{status:failure});
      }
      if(body.operationName==='CheckForUpdates'){
        if(window.upgradeCheckFailure)return Response.json({errors:[{message:'Unable to contact GitHub Releases. Try again later.'}]});
        Object.assign(window.upgradeFixture,{latestRelease:window.upgradeNoReleases?null:window.upgradeRelease,updateAvailable:!window.upgradeNoReleases&&(window.upgradeAvailabilityOverride??window.upgradeFixture.currentVersion==='1.1.0'),checkedAt:'2026-10-01T12:02:00Z'});
      }
      if(body.operationName==='DownloadUpgrade'){
        window.upgradeDownloadVersion=body.variables.version;
        if(window.holdUpgrade)await new Promise(resolve=>window.releaseUpgrade=resolve);
        if(window.upgradeBadSignature)return Response.json({errors:[{message:'Package verification failed. The GitHub release package is not trusted or is incompatible with this device.'}]});
        window.upgradeFixture.stagedPackage=window.upgradeStaged;
        window.upgradeFixture.phase='staged';
      }
      if(body.operationName==='InstallUpgrade'){
        window.upgradeInstallSequence++;
        window.upgradeStartedAt=new Date().toISOString();
        if(window.holdUpgradeInstall)await new Promise(resolve=>window.releaseUpgradeInstall=resolve);
        if(window.upgradeInstallOutcome==='rejected')return Response.json({errors:[{message:'Select the verified package version and SHA-256 digest',extensions:{code:'UPGRADE_INVALID_SELECTION'}}]});
        if(window.upgradeInstallOutcome!=='ambiguous-historical')window.upgradeFixture.installation=window.upgradeInstallFixture();
        if(window.upgradeInstallOutcome==='disconnect')throw new TypeError('Simulated accepted installation response lost');
        if(window.upgradeInstallOutcome?.startsWith('ambiguous'))return Response.json({data:{installUpgrade:{accepted:true}}});
        return Response.json({data:{installUpgrade:{accepted:true,installation:structuredClone(window.upgradeFixture.installation)}}});
      }
      return Response.json({data:{[field]:structuredClone(window.upgradeFixture)}});
    };
    location.hash='#/settings';
  })()`)
  try {
    await waitFor(`${card}?.textContent.includes('Installed version') && ${card}.querySelector('[aria-busy]').getAttribute('aria-busy')==='false'`)
    assert.equal(await evaluate(`${card}.nextElementSibling === null`),true,'upgrade card is not last in Settings')
    assert.ok(await evaluate(`${card}.textContent.includes('GitHub Releases')`))
    assert.equal(await evaluate(`${card}.querySelector('.upgrade-source a').href`),'https://github.com/pancpp/nanotail-portal/releases')
    assert.equal(await evaluate(`${card}.querySelectorAll('input[type=file],form').length`),0,'manual upgrade controls remain')
    assert.equal(await evaluate('window.upgradeRequests.length'),1,'mount performed an upgrade operation')
    assert.equal(await evaluate('window.upgradeRequests[0].operationName'),'UpgradeStatus','mount performed an upgrade mutation')
    assert.equal(await evaluate(`${card}.textContent.includes('No newer version is available.')`),false)

    await evaluate('window.upgradeCheckFailure=true')
    await click('Check for updates')
    await waitFor(`${card}.querySelector('[role=alert]')?.textContent.includes('Unable to contact GitHub Releases')`)
    assert.equal(await evaluate(`${card}.textContent.includes('No newer version is available.')`),false,'network failure claimed up-to-date')
    await evaluate('window.upgradeCheckFailure=false;window.upgradeNoReleases=true')
    await click('Check for updates')
    await waitFor(`${card}.textContent.includes('No compatible release is available yet.')`)
    assert.equal(await evaluate(`Boolean(${prepare})`),false,'no-release state has a download button')

    await evaluate("window.upgradeNoReleases=false;window.upgradeFixture.currentVersion='1.2.0'")
    await click('Check for updates')
    await waitFor(`${card}.textContent.includes('No newer version is available.')`)
    assert.ok(await evaluate(`${card}.textContent.includes('Latest release: 1.2.0')`))
    assert.equal(await evaluate(`Boolean(${prepare})`),false,'current release offers an unnecessary upgrade')

    await evaluate("window.upgradeFixture.currentVersion='development'")
    await click('Check for updates')
    await waitFor(`${card}.textContent.includes('The installed version cannot be compared')`)
    assert.equal(await evaluate(`Boolean(${prepare})`),true,'development build cannot prepare a release')
    assert.equal(await evaluate(`${card}.textContent.includes('No newer version is available.')`),false,'development version claimed up-to-date')

    await evaluate("window.upgradeFixture.currentVersion='1.1.0'")
    await click('Check for updates')
    await waitFor(`${card}.textContent.includes('Version 1.2.0 is available')`)
    assert.equal(await evaluate(`Boolean(${card}.querySelector('img'))`),false,'release notes were interpreted as HTML')
    await evaluate('window.upgradeBadSignature=true')
    await click('Prepare upgrade')
    await waitFor(`${card}.querySelector('[role=alert]')?.textContent.includes('verification failed')`)
    assert.equal(await evaluate(`Boolean(${card}.querySelector('.upgrade-verified'))`),false)

    await evaluate('window.upgradeBadSignature=false;window.holdUpgrade=true')
    await click('Prepare upgrade')
    await waitFor('Boolean(window.releaseUpgrade)')
    assert.equal(await evaluate(`[...${card}.querySelectorAll('button')].every(button=>button.disabled)`),true)
    await evaluate(`${prepare}.click()`)
    assert.equal(await evaluate('window.upgradeRequests.filter(r=>r.operationName==="DownloadUpgrade").length'),2,'duplicate download request')
    await evaluate('window.holdUpgrade=false;window.releaseUpgrade()')
    await waitFor(`${card}.querySelector('.upgrade-verified')?.textContent.includes('Package verified') && !${prepare}.disabled`)
    assert.equal(await evaluate(`${card}.querySelector('.upgrade-current strong').textContent`),'1.1.0')
    assert.ok(await evaluate(`!${install}||${install}.disabled`),'unsupported device offered installation')
    assert.equal(await evaluate('window.upgradeDownloadVersion'),'1.2.0')
    assert.ok(await evaluate('window.upgradeRequests.every(r=>r.authorization.startsWith("Bearer "))'))
    assert.ok(await evaluate('window.upgradeRequests.every(r=>r.url==="/api/v1/query"&&r.method==="POST")'),'upgrade requests did not use GraphQL')
    assert.ok(await evaluate('window.upgradeRequests.filter(r=>r.operationName==="DownloadUpgrade").every(r=>r.variables.version==="1.2.0")'),'download did not use the selected version variable')

    // Dirty builds may select the latest release even when its SemVer is equal
    // or older. The backend owns availability; both rendering and click handling
    // must honor it without trying to compare versions in the browser.
    for (const currentVersion of ['1.2.0+build.42.dirty','2.0.0+build.42.dirty','v2.0.0-3-gabcdef-dirty','abcdef-dirty']) {
      await evaluate(`window.upgradeFixture.currentVersion=${JSON.stringify(currentVersion)};window.upgradeAvailabilityOverride=true`)
      await click('Check for updates')
      await waitFor(`${card}.querySelector('.upgrade-current strong').textContent===${JSON.stringify(currentVersion)} && ${prepare}?.disabled===false`)
      assert.ok(await evaluate(`${card}.textContent.includes('Version 1.2.0 is available')`),`${currentVersion}: backend availability was ignored`)
      assert.equal(await evaluate(`${card}.textContent.includes('No newer version is available.')`),false,`${currentVersion}: dirty build claimed up-to-date`)
      const downloads = await evaluate('window.upgradeRequests.filter(r=>r.operationName==="DownloadUpgrade").length')
      await click('Prepare upgrade')
      await waitFor(`window.upgradeRequests.filter(r=>r.operationName==='DownloadUpgrade').length===${downloads+1} && ${prepare}?.disabled===false`)
      assert.equal(await evaluate(`Boolean(${card}.querySelector('[role=alert]'))`),false,`${currentVersion}: download failed`)
      assert.equal(await evaluate('window.upgradeRequests.filter(r=>r.operationName==="DownloadUpgrade").at(-1).variables.version'),'1.2.0',`${currentVersion}: wrong release selected`)
      assert.ok(await evaluate(`${card}.querySelector('.upgrade-verified').textContent.includes('Version 1.2.0')`),`${currentVersion}: package was not staged`)
    }
    await evaluate("delete window.upgradeAvailabilityOverride;window.upgradeFixture.currentVersion='1.1.0'")

    await evaluate("location.hash='#/network'")
    await waitFor(`!${card}`)
    await evaluate("location.hash='#/settings'")
    await waitFor(`Boolean(${card}?.querySelector('.upgrade-verified'))`)
    assert.ok(await evaluate(`${card}.textContent.includes('Version 1.2.0')`),'returning to Settings lost staged package')

    await send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true})
    await evaluate(`(() => {const select=document.querySelector('.language-selector select');select.value='zh-CN';select.dispatchEvent(new Event('change',{bubbles:true}))})()`)
    await waitFor(`${card}.querySelector('h2').textContent==='软件升级'`)
    assert.ok(await evaluate(`${card}.textContent.includes('升级包已验证')`))
    assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth'),true,'upgrade settings overflow on mobile')
    await captureUpgrade('-zh-CN')
    await evaluate(`(() => {const select=document.querySelector('.language-selector select');select.value='en';select.dispatchEvent(new Event('change',{bubbles:true}))})()`)
    await waitFor(`${card}.querySelector('h2').textContent==='Software upgrade'`)

    assert.equal(await evaluate(`${installRequests}.length`),0,'checking or staging installed a package')
    await evaluate("window.upgradeFixture.installationSupported=true;window.upgradeFixture.installation=null;window.upgradeFixture.currentVersion='1.1.0';window.upgradeFixture.phase='checking'")
    await remountSettings()

    // Another tab can be checking/downloading before an installation exists.
    // Its busy state must retain a usable way to read back current status.
    await waitForInstallation(`${card}.textContent.includes('Another upgrade operation is in progress')`)
    await assertUpgradeMutationsBlocked()
    assert.ok(await evaluate(`[...${card}.querySelectorAll('button')].some(button=>button.textContent.trim()==='Reload upgrade status'&&!button.disabled)`),'server-only busy state has no reload action')
    await evaluate("window.upgradeStatusFailures=['network']")
    await click('Reload upgrade status')
    await waitForInstallation(`${card}.textContent.includes('Waiting for the portal to reconnect')`)
    await evaluate("window.upgradeFixture.phase='staged'")
    await click('Reload upgrade status')
    await waitForInstallation(`${install}?.disabled===false`)
    await captureUpgrade()

    // Opening or cancelling confirmation must never send the install mutation.
    await openInstallDialog()
    assert.equal(await evaluate(`${installRequests}.length`),0,'opening confirmation installed a package')
    assert.ok(await evaluate(`${dialog}.textContent.toLowerCase().includes('restart')`),'confirmation omitted restart notice')
    assert.ok(await evaluate(`(()=>{const box=${dialog}.getBoundingClientRect();return box.left>=0&&box.right<=innerWidth})()`),'install confirmation overflowed the mobile viewport')
    await captureUpgrade('-confirmation',dialog)
    await evaluate(`[...${dialog}.querySelectorAll('button')].find(button=>button.textContent.trim()==='Cancel').click()`)
    await waitFor(`!${dialog}`)
    assert.equal(await evaluate(`${installRequests}.length`),0,'cancelling confirmation installed a package')

    // Even a definite rejection is reconciled with fresh status before retry.
    await evaluate("window.upgradeInstallOutcome='rejected';window.holdUpgradeStatus=true;delete window.releaseUpgradeStatus")
    await openInstallDialog()
    await evaluate(`${confirmInstall}.click()`)
    await waitForInstallation('Boolean(window.releaseUpgradeStatus)')
    await assertUpgradeMutationsBlocked()
    assert.equal(await evaluate(`${installRequests}.length`),1)
    await evaluate('window.holdUpgradeStatus=false;window.releaseUpgradeStatus()')
    await waitForInstallation(`${install}?.disabled===false`)
    assert.ok(await evaluate(`${card}.textContent.includes('Select the verified package version and SHA-256 digest')`))
    assert.equal(await evaluate(`${installRequests}.length`),1,'rejected install was automatically retried')

    // Acceptance precedes restart. A slow reply must not permit duplicate submits.
    await evaluate("window.upgradeInstallOutcome='accepted';window.holdUpgradeInstall=true;delete window.releaseUpgradeInstall")
    await openInstallDialog()
    await evaluate(`${confirmInstall}.click()`)
    await waitFor('Boolean(window.releaseUpgradeInstall)')
    await assertUpgradeMutationsBlocked()
    await evaluate(`${install}?.click();if(${dialog})${confirmInstall}?.click()`)
    assert.equal(await evaluate(`${installRequests}.length`),2,'duplicate install request')
    assert.deepEqual(await evaluate(`${installRequests}.at(-1).variables`),{version:'1.2.0',sha256:'ab'.repeat(32)})
    await evaluate("window.upgradeStatusFailures=['network',502,503];window.holdUpgradeInstall=false;window.releaseUpgradeInstall()")
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='prepared'`)
    await waitForInstallation('window.upgradeStatusFailures.length===0')
    await assertUpgradeMutationsBlocked()
    assert.equal(await evaluate('location.hash'),'#/settings','restart interruption signed the user out')
    assert.equal(await evaluate(`${installRequests}.length`),2,'restart interruption retried installation')

    // Administrative rejection is actionable, not a transient restart. Pause
    // polling and keep controls blocked until the user explicitly reads status.
    for(const failure of ['forbidden',403]){
      await evaluate(`window.upgradeStatusFailures=[${JSON.stringify(failure)}]`)
      await waitForInstallation(`${card}.textContent.includes('Only portal administrators can manage upgrades')`)
      const reads=await evaluate('window.upgradeRequests.filter(request=>request.operationName==="UpgradeStatus").length')
      await pause(2200)
      assert.equal(await evaluate('window.upgradeRequests.filter(request=>request.operationName==="UpgradeStatus").length'),reads,'authorization error did not pause polling')
      await assertUpgradeMutationsBlocked()
      await click('Reload upgrade status')
      await waitForInstallation(`!${card}.textContent.includes('Only portal administrators can manage upgrades')`)
    }

    // Running the target version is insufficient until recovery confirms health.
    await evaluate("window.upgradeFixture.installation.phase='awaiting-ready';Object.assign(window.upgradeFixture,{currentVersion:'1.2.0',latestRelease:null,checkedAt:null,updateAvailable:false})")
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='awaiting-ready'`)
    assert.equal(await evaluate(`${card}.textContent.includes('Upgrade complete')`),false,'target version was mistaken for completed recovery')
    await assertUpgradeMutationsBlocked()
    await remountSettings()
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='awaiting-ready'`)
    await assertUpgradeMutationsBlocked()
    assert.equal(await evaluate(`${installRequests}.length`),2,'remount retried a pending installation')

    // Completion must also match the version actually served by the portal.
    await evaluate("window.upgradeFixture.installation.phase='complete';window.upgradeFixture.installation.completedAt=new Date().toISOString();window.upgradeFixture.currentVersion='1.1.0'")
    await waitForInstallation(`${card}.textContent.includes('Checking the installed version')`)
    assert.equal(await evaluate(`${card}.textContent.includes('Upgrade complete')`),false,'mismatched installed version claimed success')
    await assertUpgradeMutationsBlocked()
    const timeOrigin = await evaluate('performance.timeOrigin')
    await evaluate("window.upgradeFixture.currentVersion='1.2.0'")
    await waitForInstallation(`${card}.textContent.includes('Upgrade complete')`)
    assert.ok(await evaluate(`[...${card}.querySelectorAll('button')].some(button=>button.textContent.trim()==='Refresh portal'&&!button.disabled)`),'completed upgrade lacks refresh action')
    await captureUpgrade('-complete')
    await pause(250)
    assert.equal(await evaluate('performance.timeOrigin'),timeOrigin,'upgrade automatically reloaded the portal')
    assert.equal(await evaluate(`${installRequests}.length`),2)

    // An older completed journal must not confirm an uncertain new attempt.
    await evaluate("window.upgradeFixture.currentVersion='1.1.0';window.upgradeFixture.installation={...window.upgradeInstallFixture('complete'),id:'historical-installation',version:'1.1.0'};window.upgradeInstallOutcome='ambiguous-historical'")
    await remountSettings()
    await openInstallDialog()
    await evaluate(`${confirmInstall}.click()`)
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='unknown'`)
    await pause(2200)
    await assertUpgradeMutationsBlocked()
    assert.equal(await evaluate(`${card}.textContent.includes('Upgrade complete')`),false,'historical result confirmed an unknown attempt')
    assert.equal(await evaluate(`${installRequests}.length`),3,'ambiguous response retried installation')
    assert.ok(await evaluate("Boolean(sessionStorage.getItem('nanotail_upgrade_attempt'))"),'unknown attempt was not persisted for remount')
    await remountSettings()
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='unknown'`)
    await assertUpgradeMutationsBlocked()
    assert.equal(await evaluate(`${installRequests}.length`),3,'remount retried an unknown installation')
    await evaluate("window.upgradeFixture.installation=window.upgradeInstallFixture('prepared')")
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='prepared'`)
    await evaluate("window.upgradeFixture.installation=window.upgradeInstallFixture('aborted');window.upgradeFixture.installation.error='Installation was canceled before changing the installed release.'")
    await waitForInstallation(`${card}.textContent.includes('Installation canceled')`)
    assert.equal(await evaluate(`${card}.textContent.includes('Upgrade complete')`),false,'aborted installation claimed success')
    assert.equal(await evaluate(`${installRequests}.length`),3)

    // A lost acceptance response is reconciled without another mutation. Failed
    // recovery remains blocked; only a completed rollback restores controls.
    await evaluate("window.upgradeInstallOutcome='disconnect';window.upgradeStatusFailures=[502,'network']")
    await openInstallDialog()
    await evaluate(`${confirmInstall}.click()`)
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='unknown'`)
    await assertUpgradeMutationsBlocked()
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='prepared'`)
    assert.equal(await evaluate(`${installRequests}.length`),4,'lost acceptance retried installation')
    await evaluate("window.upgradeFixture.installation.phase='rolling-back'")
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='rolling-back'`)
    await assertUpgradeMutationsBlocked()
    await evaluate("window.upgradeFixture.installation.phase='rollback-failed';window.upgradeFixture.installation.completedAt=new Date().toISOString();window.upgradeFixture.installation.error='Recovery could not finish. <img src=x onerror=alert(1)>'")
    await waitForInstallation(`${card}.textContent.includes('Recovery needs attention')`)
    await assertUpgradeMutationsBlocked()
    assert.equal(await evaluate(`Boolean(${card}.querySelector('img'))`),false,'recovery error was interpreted as HTML')
    const recoveryReads = await evaluate('window.upgradeRequests.filter(request=>request.operationName==="UpgradeStatus").length')
    await waitForInstallation(`window.upgradeRequests.filter(request=>request.operationName==='UpgradeStatus').length>${recoveryReads}`)
    await evaluate("window.upgradeFixture.installation=window.upgradeInstallFixture('rolled-back');window.upgradeFixture.installation.error='The previous version was restored.';window.upgradeFixture.currentVersion='1.1.0'")
    await waitForInstallation(`${card}.textContent.includes('Previous version restored')`)
    assert.equal(await evaluate(`${card}.textContent.includes('Upgrade complete')`),false,'rollback claimed upgrade success')
    await waitForInstallation(`${install}?.disabled===false`)
    assert.equal(await evaluate(`${installRequests}.length`),4,'recovery retried installation')

    // A known accepted attempt may be superseded while this tab is away. A newer
    // journal ID/timestamp must replace it even when that version differs.
    await evaluate("window.upgradeInstallOutcome='accepted'")
    await openInstallDialog()
    await evaluate(`${confirmInstall}.click()`)
    await waitForInstallation(`${installation}?.getAttribute('data-phase')==='prepared'`)
    assert.equal(await evaluate(`${installRequests}.length`),5)
    await evaluate("location.hash='#/network'")
    await waitFor(`!${card}`)
    await evaluate("(()=>{const later=Date.parse(window.upgradeFixture.installation.startedAt)+1000;window.upgradeFixture.installation={...window.upgradeInstallFixture('complete'),id:'newer-installation',version:'1.3.0',startedAt:new Date(later).toISOString(),completedAt:new Date(later+1000).toISOString()};window.upgradeFixture.currentVersion='1.3.0';location.hash='#/settings'})()")
    await waitForInstallation(`${card}?.textContent.includes('A later installation replaced this attempt. Showing the latest result.')`)
    assert.ok(await evaluate(`${installation}.textContent.includes('Upgrade complete')&&${installation}.textContent.includes('1.3.0')`),'newer installation result was not displayed')
    assert.equal(await evaluate("sessionStorage.getItem('nanotail_upgrade_attempt')"),null,'superseded accepted marker remained stuck')
    assert.equal(await evaluate(`${installRequests}.length`),5,'superseded attempt was retried')

    await evaluate('window.upgradeSession=localStorage.getItem("nanotail_access_token");window.upgradeUnauthorized=true')
    await click('Check for updates')
    await waitFor("location.hash==='#/login'")
    assert.equal(await evaluate('localStorage.getItem("nanotail_access_token")'),null,'401 did not clear session')
    await evaluate(`window.upgradeUnauthorized=false;localStorage.setItem('nanotail_access_token',window.upgradeSession);dispatchEvent(new StorageEvent('storage',{key:'nanotail_access_token'}))`)
    await waitFor(`Boolean(${card})`)
    await evaluate("location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets'))")
    assert.equal(await evaluate('window.upgradeTransportRequests.some(r=>String(r.url).startsWith("/api/v1/upgrade")||/upload/i.test(r.operationName||""))'),false,'upgrade used a REST or upload request')
  } finally {
    await evaluate('window.fetch=window.upgradeOriginalFetch')
    await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1100,deviceScaleFactor:1,mobile:false})
    await evaluate("location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets'))")
  }
  console.log('PASS: GraphQL GitHub checks, dirty-build downloads of equal/older releases, verified staging, install confirmation/cancellation, unsupported devices, exact package selection, duplicate guards, status reconciliation, restart interruptions, authorization polling pauses, server-only busy recovery, pending/unknown remount recovery, historical-result isolation and superseding attempts, confirmed completion without automatic reload, aborted/rollback outcomes, safe errors, translations, mobile layout, and expired sessions')
}
