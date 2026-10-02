import assert from 'node:assert/strict'
import { writeFile } from 'node:fs/promises'

export async function checkUpgrade({evaluate, send, waitFor, click, pause}) {
  const card = 'document.querySelector(".upgrade-settings")'
  const prepare = `[...${card}.querySelectorAll('button')].find(button=>button.textContent.trim()==='Prepare upgrade')`
  await evaluate(`(() => {
    window.upgradeOriginalFetch=window.fetch;
    window.upgradeRequests=[];
    window.upgradeTransportRequests=[];
    window.upgradeFixture={currentVersion:'1.1.0',latestRelease:null,updateAvailable:false,checkedAt:null,stagedPackage:null,installationSupported:false,onlineCheckSupported:true};
    window.upgradeRelease={version:'1.2.0',notes:'A safer portal. <img src=x onerror=alert(1)>',publishedAt:'2026-10-01T12:00:00Z',packageName:'nanotail-portal-1.2.0-linux-arm64.tar.gz',size:1234};
    window.upgradeStaged={version:'1.2.0',notes:'A safer portal.',verifiedAt:'2026-10-01T12:01:00Z',sha256:'ab'.repeat(32),size:1234};
    window.fetch=async (url,options) => {
      const body=url==='/api/v1/query'?JSON.parse(options?.body||'{}'):{};
      window.upgradeTransportRequests.push({url,operationName:body.operationName});
      const field={UpgradeStatus:'upgradeStatus',CheckForUpdates:'checkForUpdates',DownloadUpgrade:'downloadUpgrade'}[body.operationName];
      if(url!=='/api/v1/query'||!field)return window.upgradeOriginalFetch(url,options);
      window.upgradeRequests.push({url,method:options.method,authorization:new Headers(options.headers).get('Authorization'),operationName:body.operationName,variables:body.variables});
      if(window.upgradeUnauthorized)return Response.json({message:'Unauthorized'},{status:401});
      if(body.operationName==='CheckForUpdates'){
        if(window.upgradeCheckFailure)return Response.json({errors:[{message:'Unable to contact GitHub Releases. Try again later.'}]});
        Object.assign(window.upgradeFixture,{latestRelease:window.upgradeNoReleases?null:window.upgradeRelease,updateAvailable:!window.upgradeNoReleases&&window.upgradeFixture.currentVersion==='1.1.0',checkedAt:'2026-10-01T12:02:00Z'});
      }
      if(body.operationName==='DownloadUpgrade'){
        window.upgradeDownloadVersion=body.variables.version;
        if(window.holdUpgrade)await new Promise(resolve=>window.releaseUpgrade=resolve);
        if(window.upgradeBadSignature)return Response.json({errors:[{message:'Package verification failed. The GitHub release package is not trusted or is incompatible with this device.'}]});
        window.upgradeFixture.stagedPackage=window.upgradeStaged;
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
    assert.ok(await evaluate(`${card}.querySelector('.upgrade-verified').textContent.includes('Installation is not available yet')`))
    assert.equal(await evaluate('window.upgradeDownloadVersion'),'1.2.0')
    assert.ok(await evaluate('window.upgradeRequests.every(r=>r.authorization.startsWith("Bearer "))'))
    assert.ok(await evaluate('window.upgradeRequests.every(r=>r.url==="/api/v1/query"&&r.method==="POST")'),'upgrade requests did not use GraphQL')
    assert.ok(await evaluate('window.upgradeRequests.filter(r=>r.operationName==="DownloadUpgrade").every(r=>r.variables.version==="1.2.0")'),'download did not use the selected version variable')

    await evaluate("location.hash='#/network'")
    await waitFor(`!${card}`)
    await evaluate("location.hash='#/settings'")
    await waitFor(`Boolean(${card}?.querySelector('.upgrade-verified'))`)
    assert.ok(await evaluate(`${card}.textContent.includes('Version 1.2.0')`),'returning to Settings lost staged package')

    await send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true})
    await evaluate(`(() => {const select=document.querySelector('.language-selector select');select.value='zh-CN';select.dispatchEvent(new Event('change',{bubbles:true}))})()`)
    await waitFor(`${card}.querySelector('h2').textContent==='软件升级'`)
    assert.ok(await evaluate(`${card}.textContent.includes('安装功能尚不可用')`))
    assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth'),true,'upgrade settings overflow on mobile')
    if(process.env.UPGRADE_SCREENSHOT){
      await evaluate(`${card}.scrollIntoView({block:'start'})`)
      await pause(150)
      const screenshot=await send('Page.captureScreenshot',{format:'png',captureBeyondViewport:true})
      await writeFile(process.env.UPGRADE_SCREENSHOT,Buffer.from(screenshot.data,'base64'))
    }
    await evaluate(`(() => {const select=document.querySelector('.language-selector select');select.value='en';select.dispatchEvent(new Event('change',{bubbles:true}))})()`)
    await waitFor(`${card}.querySelector('h2').textContent==='Software upgrade'`)
    await evaluate('window.upgradeSession=localStorage.getItem("nanotail_access_token");window.upgradeUnauthorized=true')
    await click('Check for updates')
    await waitFor("location.hash==='#/login'")
    assert.equal(await evaluate('localStorage.getItem("nanotail_access_token")'),null,'401 did not clear session')
    await evaluate(`window.upgradeUnauthorized=false;localStorage.setItem('nanotail_access_token',window.upgradeSession);dispatchEvent(new StorageEvent('storage',{key:'nanotail_access_token'}))`)
    await waitFor(`Boolean(${card})`)
    await evaluate("location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets'))")
    assert.equal(await evaluate('window.upgradeTransportRequests.some(r=>String(r.url).startsWith("/api/v1/upgrade")||/upload|install/i.test(r.operationName||""))'),false,'upgrade used a REST, upload, or installation request')
  } finally {
    await evaluate('window.fetch=window.upgradeOriginalFetch')
    await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1100,deviceScaleFactor:1,mobile:false})
    await evaluate("location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets'))")
  }
  console.log('PASS: GraphQL GitHub upgrade checks, available/current/development/no-release states, upstream and signature errors, authenticated download staging, duplicate prevention, persistence, no REST/upload/install calls, literal release notes, translated mobile layout, and expired sessions')
}
