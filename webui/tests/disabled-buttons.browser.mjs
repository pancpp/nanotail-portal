import assert from 'node:assert/strict'
import { writeFile } from 'node:fs/promises'

export async function checkDisabledButtons({evaluate, send, waitFor, click, fill, pause}) {
  const bubble = '.button-tooltip__reason:popover-open'
  const peerSave = '.peer-relay-settings button[type=submit]'
  const resetSave = '.factory-reset-dialog button[type=submit]'
  const visible = message => waitFor(`document.querySelector(${JSON.stringify(bubble)})?.textContent.trim() === ${JSON.stringify(message)}`)
  const focus = selector => evaluate(`(() => {
    const button=document.querySelector(${JSON.stringify(selector)});
    button.scrollIntoView({block:'center'}); button.parentElement.focus();
  })()`)
  const pointerTarget = async selector => {
    await evaluate(`document.querySelector(${JSON.stringify(selector)}).scrollIntoView({block:'center'})`)
    // Responsive navigation can still cover the target while sliding away.
    // Wait for hit testing to reach it before sending real pointer input.
    await waitFor(`(() => {
      const element=document.querySelector(${JSON.stringify(selector)});
      const r=element.getBoundingClientRect();
      const target=element.matches('button:disabled') ? element.parentElement : element;
      return target.contains(document.elementFromPoint(r.left+r.width/2,r.top+r.height/2));
    })()`)
    return evaluate(`(() => {
      const element=document.querySelector(${JSON.stringify(selector)});
      const r=element.getBoundingClientRect(); return {x:r.left+r.width/2,y:r.top+r.height/2};
    })()`)
  }
  const moveTo = async selector => {
    const point = await pointerTarget(selector)
    await send('Input.dispatchMouseEvent', {type:'mouseMoved', ...point})
  }
  const press = async key => {
    const virtual = {Escape:27,Enter:13,' ':32,Tab:9}[key]
    await send('Input.dispatchKeyEvent',{type:'keyDown',key,code:key === ' ' ? 'Space' : key,windowsVirtualKeyCode:virtual})
    await send('Input.dispatchKeyEvent',{type:'keyUp',key,code:key === ' ' ? 'Space' : key,windowsVirtualKeyCode:virtual})
  }
  const covered = async () => assert.deepEqual(await evaluate(`Array.from(document.querySelectorAll('button:disabled')).filter(button => {
    const anchor=button.parentElement;
    return anchor.getAttribute('aria-disabled') !== 'true' || anchor.tabIndex !== 0 ||
      !document.getElementById(anchor.getAttribute('aria-describedby'))?.textContent.trim();
  }).map(button => button.textContent.trim())`), [], 'disabled button without an accessible reason')
  const chooseLanguage = language => evaluate(`(() => {
    const select=document.querySelector('.language-selector select'); select.value=${JSON.stringify(language)};
    select.dispatchEvent(new Event('change',{bubbles:true}));
  })()`)

  const writes = await evaluate('window.apiWrites.length')
  try {
    await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1100,deviceScaleFactor:1,mobile:false})
    await evaluate("location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('peer-relay-enabled')) && !document.getElementById('peer-relay-enabled').checked")
    await covered()
    await moveTo(peerSave)
    await visible('No changes to save.')
    const accessibility = await send('Accessibility.getFullAXTree')
    const saveNode = accessibility.nodes.find(node => !node.ignored && node.role?.value === 'button' && node.name?.value === 'Save peer relay')
    assert.equal(saveNode?.description?.value, 'No changes to save.', 'screen reader is missing the reason')
    assert.equal(saveNode?.properties.find(property => property.name === 'disabled')?.value.value, true)
    await moveTo(bubble); await pause(200)
    await visible('No changes to save.') // Hovering the explanation keeps it open.
    await press('Escape')
    await waitFor(`!document.querySelector(${JSON.stringify(bubble)})`)

    // Native disabled buttons skip Tab; the wrapper supplies a keyboard stop.
    await evaluate("document.querySelector('.peer-relay-settings .routing-guide').focus()")
    await press('Tab')
    assert.equal(await evaluate(`document.activeElement === document.querySelector(${JSON.stringify(peerSave)}).parentElement`), true)
    await visible('No changes to save.')
    await press('Enter'); await press(' ')
    await evaluate(`document.querySelector(${JSON.stringify(peerSave)}).click()`)
    assert.equal(await evaluate('window.apiWrites.length'),writes,'disabled activation sent a mutation')

    await evaluate("document.getElementById('peer-relay-enabled').click()")
    await waitFor(`!document.querySelector(${JSON.stringify(peerSave)}).disabled && !document.querySelector(${JSON.stringify(bubble)})`)
    await fill('peer-relay-port','0'); await focus(peerSave)
    await visible('Enter a UDP port from 1 to 65535')
    await chooseLanguage('zh-CN')
    await visible('请输入 1 至 65535 之间的 UDP 端口')
    await chooseLanguage('en')
    await fill('peer-relay-port','40001')
    await evaluate("document.getElementById('peer-relay-enabled').click()")
    await evaluate('document.activeElement.blur()')

    // Touch users can tap the disabled button; explanations stay within the viewport.
    await send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true})
    await send('Emulation.setTouchEmulationEnabled',{enabled:true})
    const point=await pointerTarget(peerSave)
    await send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[point]})
    await send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]})
    await visible('No changes to save.')
    assert.ok(await evaluate(`(() => {const r=document.querySelector(${JSON.stringify(bubble)}).getBoundingClientRect();return r.left>=0 && r.right<=innerWidth && r.top>=0 && r.bottom<=innerHeight})()`), 'mobile tooltip outside viewport')
    if (process.env.DISABLED_BUTTON_SCREENSHOT) {
      await pause(300) // Let the navigation's responsive transition finish.
      const screenshot=await send('Page.captureScreenshot',{format:'png'})
      await writeFile(process.env.DISABLED_BUTTON_SCREENSHOT,Buffer.from(screenshot.data,'base64'))
    }
    await send('Emulation.setTouchEmulationEnabled',{enabled:false})
    await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1100,deviceScaleFactor:1,mobile:false})

    await evaluate("location.hash='#/settings'")
    await waitFor("Boolean(document.querySelector('.credential-actions'))")
    await focus('.credential-actions .danger-action')
    await visible('No saved credentials to remove.')

    // A loading button explains itself, without turning a read into a write.
    await evaluate("location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('peer-relay-enabled'))")
    await evaluate(`(() => {
      window.tooltipOriginalFetch=window.fetch;
      window.fetch=async (url,options) => {
        if (JSON.parse(options?.body||'{}').operationName==='TailscaleRouting') {
          await new Promise(resolve => {window.releaseTooltipRead=resolve});
        }
        return window.tooltipOriginalFetch(url,options);
      };
    })()`)
    await click('Reload peer relay settings')
    await waitFor("Boolean(window.releaseTooltipRead)")
    await focus('.peer-relay-settings .text-action')
    await visible('Loading peer relay settings…')
    await evaluate('window.fetch=window.tooltipOriginalFetch; window.releaseTooltipRead()')
    await waitFor("!document.querySelector('.peer-relay-settings .text-action').disabled")

    await evaluate("location.hash='#/network'")
    await waitFor("Boolean(document.getElementById('tailnet-ack'))")
    await focus('.tailnet-settings button[type=submit]')
    await visible('Confirm the connection warning before applying.')
    await covered()

    await evaluate("location.hash='#/'")
    await waitFor("Boolean(document.querySelector('.node-key-renew'))")
    await focus('.node-key-renew')
    await visible('Renewal is disabled because node-key expiry is disabled.')
    await covered()

    // Tooltips remain above native modal dialogs; Escape dismisses only the tooltip.
    await evaluate("location.hash='#/settings'")
    await waitFor("Boolean(document.querySelector('.factory-reset-settings button'))")
    await click('Factory reset')
    await waitFor("Boolean(document.querySelector('.factory-reset-dialog[open]'))")
    // Opening a modal makes the old hover target inert. Chrome can send
    // pointerover from that target without first delivering pointerout to it.
    await evaluate(`document.querySelector('.factory-reset-dialog .reset-form button').parentElement.dispatchEvent(
      new PointerEvent('pointerover',{bubbles:true,pointerType:'mouse',relatedTarget:document.querySelector('main')})
    )`)
    await visible('Confirm that you understand the data loss and have local access.')
    await press('Escape')
    await waitFor(`!document.querySelector(${JSON.stringify(bubble)})`)
    await moveTo('.factory-reset-dialog .reset-form button')
    await visible('Confirm that you understand the data loss and have local access.')
    assert.ok(await evaluate(`(() => {const el=document.querySelector(${JSON.stringify(bubble)}),r=el.getBoundingClientRect();return document.elementFromPoint(r.left+r.width/2,r.top+r.height/2)===el})()`), 'tooltip hidden behind dialog')
    await press('Escape')
    assert.equal(await evaluate("Boolean(document.querySelector('.factory-reset-dialog[open]'))"),true,'tooltip Escape closed its dialog')
    await evaluate("document.querySelector('.reset-acknowledgement input').click()")
    await click('Continue to final confirmation')
    await focus(resetSave)
    await visible('Type RESET exactly to confirm.')
    await fill('reset-confirmation','RESET'); await focus(resetSave)
    await visible('Enter your current portal password.')
    await fill('reset-password','fixture-only-password')
    await waitFor(`!document.querySelector(${JSON.stringify(resetSave)}).disabled && !document.querySelector(${JSON.stringify(bubble)})`)
    await evaluate("document.querySelector('button[aria-label=\"Close factory reset dialog\"]').click()")
    assert.equal(await evaluate('window.apiWrites.length'),writes,'tooltip interactions sent a mutation')
  } finally {
    await evaluate("if(window.tooltipOriginalFetch){window.fetch=window.tooltipOriginalFetch;window.releaseTooltipRead?.()}")
    await send('Emulation.setTouchEmulationEnabled',{enabled:false})
    await chooseLanguage('en')
    await evaluate("location.hash='#/access-control'")
    await waitFor("Boolean(document.getElementById('routing-subnets'))")
  }
  console.log('PASS: disabled-button reasons, hover/focus/touch, keyboard guards, live translation, modal layering, Escape, loading and mobile bounds')
}
