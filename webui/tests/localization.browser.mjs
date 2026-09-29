import assert from 'node:assert/strict'
import { writeFile } from 'node:fs/promises'

// Runs inside the routing harness: all device APIs remain mocked.
export async function checkLocalization({evaluate, send, waitFor, click, fill, pause}) {
  const choose = async language => {
    await evaluate(`(() => {const select=document.querySelector('.language-selector select');select.value=${JSON.stringify(language)};select.dispatchEvent(new Event('change',{bubbles:true}))})()`)
    await waitFor(`document.documentElement.lang === ${JSON.stringify(language)}`)
  }
  const noOverflow = async context => assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth'), true, context + ' overflows')
  const screenshot = async (path, full = false) => {
    if (!path) return
    await pause(350)
    const result = await send('Page.captureScreenshot', {format:'png',captureBeyondViewport:full})
    await writeFile(path, Buffer.from(result.data,'base64'))
  }
  await evaluate("location.hash='#/access-control'")
  await waitFor("Boolean(document.getElementById('routing-subnets'))")
  await fill('routing-subnets', '192.168.77.0/24')
  await evaluate("window.draftNode=document.getElementById('routing-subnets');void 0")
  const mutations = await evaluate("window.apiWrites.length")
  const jwt = await evaluate("localStorage.getItem('nanotail_access_token')")
  await choose('zh-CN')
  assert.equal(await evaluate("document.querySelector('h1').textContent"), '访问控制')
  assert.equal(await evaluate("localStorage.getItem('nanotail_language')"), 'zh-CN')
  assert.equal(await evaluate("document.querySelector('.language-selector select').getAttribute('aria-label')"), '语言 / Language')
  assert.equal(await evaluate("document.getElementById('routing-subnets') === window.draftNode"), true, 'language switch remounted draft')
  assert.equal(await evaluate("document.getElementById('routing-subnets').value"), '192.168.77.0/24')
  await fill('routing-subnets', '192.168.77.8/24')
  assert.ok(await evaluate("document.querySelector('.routing-settings').textContent.includes('最多输入 64 个不重复的子网 CIDR')"))
  await choose('en')
  assert.ok(await evaluate("document.querySelector('.routing-settings').textContent.includes('Enter up to 64 unique subnet CIDRs')"))
  assert.equal(await evaluate("document.getElementById('routing-subnets').value"), '192.168.77.8/24')
  await choose('zh-CN')
  await noOverflow('Chinese access control on mobile')
  await send('Emulation.setDeviceMetricsOverride', {width:1440,height:1100,deviceScaleFactor:1,mobile:false})
  await screenshot(process.env.CHINESE_ACCESS_SCREENSHOT)

  await evaluate("location.hash='#/settings'")
  await waitFor("Boolean(document.querySelector('.credential-actions'))")
  await choose('en')
  const clientId = await evaluate("document.querySelector('[name=client_id]').id")
  const clientSecret = await evaluate("document.querySelector('[name=client_secret]').id")
  await fill(clientId, 'Settings')
  await fill(clientSecret, 'do-not-translate-this-secret')
  await choose('zh-CN')
  assert.equal(await evaluate("document.querySelector('[name=client_id]').value"), 'Settings')
  assert.equal(await evaluate("document.querySelector('[name=client_secret]').value"), 'do-not-translate-this-secret')
  assert.equal(await evaluate("document.body.innerText.includes('do-not-translate-this-secret')"), false)

  // Exercise translated data, charts and calendar labels, not just navigation.
  await evaluate(`(() => {
    const previousFetch=window.fetch;
    window.fetch=async (url,options) => {
      if(String(url)==='/api/login') return Response.json({message:'Unauthorized error'},{status:401});
      const body=JSON.parse(options?.body||'{}');
      if(body.operationName==='NetworkActivityHistory'){
        const end=Math.floor(Date.now()/3600000)*3600000,start=end-86400000;
        return Response.json({data:{networkActivityHistory:{
          windowStart:new Date(start).toISOString(),windowEnd:new Date(end).toISOString(),
          hours:[{startedAt:new Date(start).toISOString(),rxBytes:'1024',txBytes:'512',observedSeconds:3600}],
          totals:{rxBytes24h:'1024',txBytes24h:'512',observedSeconds24h:3600,totalRxBytes:'1024',totalTxBytes:'512',totalObservedSeconds:3600,recordedSince:new Date(start).toISOString()}
        }}});
      }
      if(body.operationName==='NetworkActivity')return Response.json({data:{networkActivity:{interfaceName:'tailscale0',rxBytes:String(Date.now()),txBytes:String(Date.now()),sampledAt:new Date().toISOString(),counterEpoch:'browser-test'}}});
      return previousFetch(url,options);
    };
    window.nodeKeyExpiry=new Date(Date.now()+2*86400000+60000).toISOString();
    location.hash='#/';
  })()`)
  await waitFor("Boolean(document.querySelector('.node-key-card'))")
  await click('刷新状态')
  await waitFor("document.querySelector('.node-key-card')?.textContent.includes('剩余 2 天')")
  await waitFor("document.querySelector('.saved-traffic')?.textContent.includes('累计流量')")
  assert.ok(await evaluate("document.querySelector('.device-panel').textContent.includes('1 小时 0 分钟')"))
  assert.ok(await evaluate("document.querySelector('.device-panel time').textContent.includes('年')"))
  await click('最近 24 小时')
  await waitFor("document.querySelector('.history-details')?.textContent.includes('完整记录')")
  assert.ok(await evaluate("document.querySelector('.history-details').textContent.includes('缺少数据')"))
  assert.ok(await evaluate("document.querySelector('.history-details').textContent.includes('1 KiB')"))
  await screenshot(process.env.CHINESE_OVERVIEW_SCREENSHOT)
  await send('Emulation.setDeviceMetricsOverride', {width:390,height:844,deviceScaleFactor:1,mobile:true})
  await noOverflow('Chinese overview on mobile')
  await screenshot(process.env.CHINESE_MOBILE_SCREENSHOT, true)

  // A pending sign-in translates without restarting or sending any auth action.
  await evaluate("window.signInFixture=true;window.renewalState='IDLE'")
  await click('刷新状态')
  await waitFor("Boolean(document.querySelector('dialog[open] .signin-steps'))")
  assert.ok(await evaluate("document.querySelector('.signin-steps').textContent.includes('设备批准指南')"))
  assert.equal(await evaluate("document.querySelector('.renewal-submit').textContent.trim()"), '准备登录')
  assert.equal(await evaluate("document.querySelector('.renewal-submit').disabled"), true)
  await evaluate("document.querySelector('[aria-label=\"关闭登录对话框\"]').click()")
  await waitFor("!document.querySelector('dialog[open]')")
  await evaluate("window.renewalState='AWAITING_APPROVAL'")
  await click('登录 Tailscale')
  await waitFor("document.querySelector('.renewal-status')?.textContent.includes('正在等待设备批准')")
  await evaluate("document.querySelector('[aria-label=\"关闭登录对话框\"]').click()")
  await waitFor("!document.querySelector('dialog[open]')")
  await evaluate("window.signInFixture=false;window.renewalState='IDLE'")

  await evaluate("location.hash='#/network'")
  await waitFor("document.querySelector('.tailnet-settings h2')?.textContent === 'Tailnet 连接'")
  await waitFor("document.querySelector('.tailnet-settings').textContent.includes('关闭 tailnet 连接')")
  await click('刷新状态')
  await waitFor("document.querySelector('.credential-setup-dialog[open] h2')?.textContent === '添加客户端凭据'")
  await click('暂时跳过')
  await waitFor("document.querySelectorAll('.credential-setup-checklist li').length === 5")
  assert.ok(await evaluate("document.querySelector('.credential-setup-checklist').textContent.includes('设备中继')"))
  assert.ok(await evaluate("document.querySelector('.credential-setup-checklist').textContent.includes('子网路由器')"))
  await noOverflow('Chinese credential reminder on mobile')
  await click('暂不添加凭据并继续')
  await waitFor("!document.querySelector('dialog[open]')")
  await noOverflow('Chinese Network on mobile')

  // Every guide is translated; command/scope strings and safe links survive.
  const titles = ['登录并批准本设备','批准子网路由','批准此出口节点','创建客户端凭据']
  for (const [index, slug] of ['device-approval','subnet-routes','exit-node','oauth-credentials'].entries()) {
    await evaluate(`location.hash='#/tailscale-setup/${slug}'`)
    await waitFor(`document.querySelector('h1')?.textContent === ${JSON.stringify(titles[index])}`)
    assert.equal(await evaluate("document.querySelector('.topbar__title strong').textContent"), '设置指南')
    assert.ok(await evaluate("document.querySelector('.setup-guide').textContent.includes('设备')"))
    if(slug==='subnet-routes') assert.equal(await evaluate("document.querySelector('.guide-steps code').textContent"), 'sudo tailscale set --accept-routes')
    if(slug==='oauth-credentials') assert.ok(await evaluate("document.querySelector('.setup-guide').textContent.includes('devices:routes')"))
    await noOverflow('Chinese ' + slug)
  }

  // Reset requires both confirmations and the exact untranslated RESET token.
  await evaluate("location.hash='#/settings'")
  await waitFor("document.querySelector('h1')?.textContent === '设置'")
  await fill('current-password','existing-password')
  await fill('new-password','new-password')
  await fill('confirm-password','different-password')
  await click('更新密码')
  await waitFor("document.querySelector('.password-change-form [role=alert]')?.textContent.includes('两次输入的新密码不一致')")
  await click('恢复出厂设置')
  await waitFor("Boolean(document.querySelector('.factory-reset-dialog[open]'))")
  const resetText = await evaluate("document.querySelector('#reset-description').textContent")
  for (const value of ['nanotail-portal.yml','nanotail-portal.sqlite3','logs','nanotail-portal.key']) assert.ok(resetText.includes(value))
  assert.equal(await evaluate("document.querySelector('.factory-reset-dialog .danger-button').disabled"), true)
  await evaluate("document.querySelector('.reset-acknowledgement input').click()")
  await click('继续进行最终确认')
  await waitFor("Boolean(document.getElementById('reset-confirmation'))")
  await fill('reset-confirmation','重置')
  await fill('reset-password','existing-password')
  assert.equal(await evaluate("document.querySelector('.reset-form button[type=submit]').disabled"), true)
  await fill('reset-confirmation','RESET')
  assert.equal(await evaluate("document.querySelector('.reset-form button[type=submit]').disabled"), false)
  // A real cross-tab language event also preserves the active confirmation.
  await evaluate("localStorage.setItem('nanotail_language','en');window.dispatchEvent(new StorageEvent('storage',{key:'nanotail_language',newValue:'en'}))")
  await waitFor("document.querySelector('#reset-title')?.textContent === 'Confirm factory reset'")
  assert.equal(await evaluate("document.getElementById('reset-confirmation').value"), 'RESET')
  assert.equal(await evaluate("document.getElementById('reset-password').value"), 'existing-password')
  await evaluate("document.querySelector('[aria-label=\"Close factory reset dialog\"]').click()")
  await waitFor("!document.querySelector('dialog[open]')")
  assert.equal(await evaluate("window.apiWrites.length"), mutations, 'language/help/validation triggered a mutation')
  assert.equal(await evaluate("localStorage.getItem('nanotail_access_token')"), jwt, 'language switch changed the JWT')

  await choose('zh-CN')
  await click('退出登录')
  await waitFor("location.hash === '#/login' && Boolean(document.getElementById('username'))")
  assert.equal(await evaluate("document.querySelector('.login-card h2').textContent"), '欢迎回来')
  await fill('username', 'Settings')
  await fill('password', 'keep-this-password')
  await choose('en')
  assert.equal(await evaluate("document.getElementById('username').value"), 'Settings')
  assert.equal(await evaluate("document.getElementById('password').value"), 'keep-this-password')
  await choose('zh-CN')
  await click('登录')
  await waitFor("document.querySelector('.login-form [role=alert]')?.textContent.includes('未授权')")
  await noOverflow('Chinese login on mobile')
  await screenshot(process.env.CHINESE_LOGIN_SCREENSHOT)

  // Fresh-page browser detection, explicit override, persistence, blocked storage.
  await send('Page.addScriptToEvaluateOnNewDocument', {source:"Object.defineProperty(navigator,'languages',{value:['zh-CN','en-US'],configurable:true});"})
  await evaluate("localStorage.removeItem('nanotail_language');window.beforeLanguageReload=true")
  await send('Page.reload', {ignoreCache:true})
  await waitFor("!window.beforeLanguageReload && document.documentElement.lang === 'zh-CN' && Boolean(document.querySelector('.language-selector select'))")
  await choose('en')
  await evaluate("window.beforeLanguageReload=true")
  await send('Page.reload', {ignoreCache:true})
  await waitFor("!window.beforeLanguageReload && document.documentElement.lang === 'en' && Boolean(document.querySelector('.language-selector select'))")
  assert.equal(await evaluate("localStorage.getItem('nanotail_language')"), 'en')
  await send('Page.addScriptToEvaluateOnNewDocument', {source:`(() => {
    const get=Storage.prototype.getItem,set=Storage.prototype.setItem;
    Storage.prototype.getItem=function(key){if(key==='nanotail_language')throw new Error('blocked');return get.call(this,key)};
    Storage.prototype.setItem=function(key,value){if(key==='nanotail_language')throw new Error('blocked');return set.call(this,key,value)};
  })()`})
  await evaluate("window.beforeLanguageReload=true")
  await send('Page.reload', {ignoreCache:true})
  await waitFor("!window.beforeLanguageReload && document.documentElement.lang === 'zh-CN' && Boolean(document.querySelector('.language-selector select'))")
  await choose('en')
  assert.equal(await evaluate("document.querySelector('.language-selector select').value"), 'en', 'blocked storage disabled the selector')
  assert.equal(await evaluate("window.apiWrites.length"), 0, 'fresh language selection triggered a mutation')
  console.log('PASS: Chinese/English switching, browser detection and persistence, blocked storage, safe interpolation, unchanged drafts/credentials, translated status/history/guides/login, reset confirmations, no action side effects, and responsive layout')
}
