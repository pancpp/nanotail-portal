import assert from 'node:assert/strict'

export const portalVersionFixture = `(() => {
  window.portalVersionFixture = 'v0.4.2-17-gabc1234-dirty';
  window.portalVersionReads = 0;
  const previousFetch = window.fetch;
  window.fetch = async (url, options) => {
    if (JSON.parse(options?.body || '{}').operationName === 'PortalVersion') {
      window.portalVersionReads++;
      if (window.portalVersionFailure) return Response.json({errors:[{message:'Version unavailable'}]});
      return Response.json({data:{portalVersion:window.portalVersionFixture}});
    }
    return previousFetch(url, options);
  };
})()`

export async function checkPortalVersion({evaluate, waitFor, click}) {
  const defaultVersion = 'v0.4.2-17-gabc1234-dirty'
  const label = value => waitFor(`document.querySelector('.sidebar__version strong')?.textContent === ${JSON.stringify(value)}`)
  const choose = async value => {
    await evaluate(`(() => {const el=document.querySelector('.language-selector select');el.value=${JSON.stringify(value)};el.dispatchEvent(new Event('change',{bubbles:true}))})()`)
    await waitFor(`document.documentElement.lang === ${JSON.stringify(value)}`)
  }
  const token = await evaluate("localStorage.getItem('nanotail_access_token')")
  const mutations = await evaluate('window.apiWrites.length')
  const remount = async (value, failure = false) => {
    await click('Sign out')
    await waitFor("Boolean(document.getElementById('username'))")
    await evaluate(`window.portalVersionFixture=${JSON.stringify(value)};window.portalVersionFailure=${failure};localStorage.setItem('nanotail_access_token',${JSON.stringify(token)});window.dispatchEvent(new StorageEvent('storage',{key:'nanotail_access_token',newValue:${JSON.stringify(token)}}))`)
    await waitFor("Boolean(document.querySelector('.dashboard-shell'))")
    await evaluate("location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets')) && document.querySelector('.topbar__actions button')?.disabled === false")
  }
  await label(defaultVersion)
  assert.equal(await evaluate("document.querySelector('.sidebar__version strong').title"), defaultVersion)
  assert.equal(await evaluate("document.querySelector('.sidebar__version span').textContent"), 'Portal version')
  const requests = await evaluate('window.portalVersionReads')
  await choose('zh-CN')
  await label(defaultVersion)
  assert.equal(await evaluate("document.querySelector('.sidebar__version span').textContent"), '门户版本')
  await choose('en')
  assert.equal(await evaluate('window.portalVersionReads'), requests, 'language change refetched build metadata')
  await remount('')
  await label('Unavailable')
  await remount(42)
  await label('Unavailable')
  await remount(defaultVersion, true)
  await label('Unavailable')
  const longVersion = 'v0.4.2-' + 'long-build-tag-'.repeat(8) + '17-gabc1234-dirty'
  await remount(longVersion)
  await label(longVersion)
  assert.equal(await evaluate(`(() => {
    const sidebar=document.querySelector('.sidebar').getBoundingClientRect();
    const version=document.querySelector('.sidebar__version strong');
    return version.getBoundingClientRect().right <= sidebar.right && version.scrollWidth <= version.clientWidth;
  })()`), true, 'full Git version overflows the sidebar')
  await remount('<b>build-metadata</b>')
  await label('<b>build-metadata</b>')
  assert.equal(await evaluate("document.querySelector('.sidebar__version strong').childElementCount"), 0, 'version was rendered as HTML')
  await remount(defaultVersion)
  await label(defaultVersion)
  assert.equal(await evaluate('window.apiWrites.length'), mutations, 'version display sent a mutation')
  console.log('PASS: runtime portal version, unchanged metadata in both languages, blank/error fallback, long versions and safe text rendering')
}
