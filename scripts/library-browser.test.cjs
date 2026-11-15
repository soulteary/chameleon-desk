const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { createInterface } = require('node:readline');
const { chromium } = require('playwright');

async function serve(binary, addr = '127.0.0.1:0', extra = []) {
  const child = spawn(path.resolve(binary), ['serve', '--addr', addr, ...extra], { stdio: ['ignore', 'pipe', 'pipe'] });
  let errors = ''; child.stderr.on('data', data => { errors += data; });
  const lines = createInterface({ input: child.stdout });
  const base = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => { child.kill(); reject(new Error('server startup timeout: ' + errors)); }, 10000);
    lines.on('line', line => {
      const match = line.match(/http:\/\/([^/]+)\//);
      if (match) { clearTimeout(timer); resolve('http://' + match[1]); }
    });
    child.once('error', error => { clearTimeout(timer); reject(error); });
    child.once('exit', () => { clearTimeout(timer); reject(new Error('server exited: ' + errors)); });
  });
  return { base, async close() { lines.close(); if (child.exitCode === null && child.signalCode === null) { const exit = once(child, 'exit'); child.kill(); await exit; } } };
}

const binary = process.env.CHAMELEON_WEBUI_TEST_BINARY;
test('Chromium persists the library through reload, browser restart and Desk restart', { skip: !binary, timeout: 90000 }, async t => {
  const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'chameleon-library-browser-'));
  let server, context;
  const pageErrors = [], dialogs = [];
  t.after(async () => { if (context) await context.close(); if (server) await server.close(); await fs.rm(profile, { recursive: true, force: true }); });
  const launch = async () => {
    context = await chromium.launchPersistentContext(profile, { headless: true, executablePath: process.env.CHAMELEON_TEST_CHROMIUM || undefined });
    const page = context.pages()[0] || await context.newPage();
    page.on('pageerror', error => pageErrors.push(error.message));
    page.on('dialog', dialog => { dialogs.push(dialog.type()); return dialog.type() === 'beforeunload' ? dialog.accept() : dialog.dismiss(); });
    await page.goto(server.base); await page.evaluate(() => window.libraryReady);
    await page.locator('.dock [data-open="cards"]').click();
    await page.locator('#cards details.json-input').first().locator('summary').click();
    return page;
  };
  const saved = async page => {
    await page.waitForFunction(() => document.querySelector('#db-save-state').textContent.includes('已自动保存'));
  };
  const snapshot = JSON.stringify({ 'flutter.cards': [], custom: { error: 'metadata', name: '本地卡库' } });
  server = await serve(binary); let page = await launch();
  await page.locator('#db-json').fill(snapshot); await saved(page);
  await page.reload(); await page.evaluate(() => window.libraryReady);
  assert.equal(await page.locator('#db-json').inputValue(), snapshot);
  await context.close(); context = null;
  page = await launch(); assert.equal(await page.locator('#db-json').inputValue(), snapshot);
  await context.close(); context = null;
  const address = new URL(server.base).host; await server.close(); server = await serve(binary, address);
  page = await launch(); assert.equal(await page.locator('#db-json').inputValue(), snapshot);

  // Backend schema rejection must leave the committed record unchanged.
  await page.locator('#db-json').fill('{"cards":"invalid"}'); await page.locator('#db-save-now').click();
  await page.waitForFunction(() => document.querySelector('#db-save-state').textContent.startsWith('未保存：'));
  assert(await page.locator('#db-save-now').isEnabled());
  assert.equal(await page.locator('#db-json').inputValue(), '{"cards":"invalid"}');
  const warned = await page.evaluate(() => { const event = new Event('beforeunload', { cancelable: true }); dispatchEvent(event); return event.defaultPrevented; });
  assert(warned);
  await page.reload(); await page.evaluate(() => window.libraryReady);
  assert.equal(await page.locator('#db-json').inputValue(), snapshot);
  assert(dialogs.includes('beforeunload'));
  assert.deepEqual(pageErrors, []);
});

test('Chromium verifies capabilities, task reload tracking and configuration readback', { skip: !binary, timeout: 60000 }, async t => {
  const net = require('node:net'), read = require('node:fs');
  const catalog = JSON.parse(read.readFileSync(path.join(__dirname,'../internal/catalog/upstream.json'),'utf8')).commands;
  let model=0, capabilities=[1033,1035], mode=0, delayModel=0, animation=0;const nicknames=new Map();
  let connections=0; const commands=[], sockets=new Set();
  const simulator=net.createServer(socket=>{
    connections++;
    socket.on('error',()=>{});sockets.add(socket);socket.on('close',()=>sockets.delete(socket));let buffer=Buffer.alloc(0);
    socket.on('data',chunk=>{
      buffer=Buffer.concat([buffer,chunk]);
      while(buffer.length>=10){
        const size=buffer.readUInt16BE(6)+10;if(buffer.length<size)break;
        const frame=buffer.subarray(0,size);buffer=buffer.subarray(size);const command=frame.readUInt16BE(2);commands.push(command);
        let payload=Buffer.alloc(0),status=0x68;
        if(command===1033) payload=Buffer.from([model]);
        else if(command===1015){animation=frame[9];}
        else if(command===1016){payload=Buffer.from([animation]);}
        else if(command===1034){payload=Buffer.concat([Buffer.from([6,animation,0,0,0,0,0]),Buffer.from('654321')]);}
        else if(command===1039){payload=Buffer.from([5]);}
        else if(command===1041){payload=Buffer.from([0,200]);}
        else if(command===1007){nicknames.set(frame.subarray(9,11).toString('hex'),frame.subarray(11,frame.length-1));}
        else if(command===1008){payload=nicknames.get(frame.subarray(9,11).toString('hex'))||Buffer.alloc(0);}
        else if(command===1035){payload=Buffer.alloc(capabilities.length*2);capabilities.forEach((c,i)=>payload.writeUInt16BE(c,i*2));}
        else if(command===1002)payload=Buffer.from([mode]);
        else if(command===1001)mode=frame[9];
        else if(command===2000)status=1;
        else status=103;
        const reply=Buffer.alloc(payload.length+10);reply[0]=0x11;reply[1]=0xef;reply.writeUInt16BE(command,2);reply.writeUInt16BE(status,4);reply.writeUInt16BE(payload.length,6);
        reply[8]=(-reply.subarray(0,8).reduce((sum,v)=>sum+v,0))&255;payload.copy(reply,9);reply[reply.length-1]=(-payload.reduce((sum,v)=>sum+v,0))&255;if(command===1033&&delayModel){setTimeout(()=>{if(!socket.destroyed)socket.write(reply)},delayModel);}else socket.write(reply);
      }
    });
  });
  await new Promise(resolve=>simulator.listen(0,'127.0.0.1',resolve));
  let server, browser;
  t.after(async()=>{if(browser)await browser.close();if(server)await server.close();for(const socket of sockets)socket.destroy();await new Promise(resolve=>simulator.close(resolve));});
  server=await serve(binary,'127.0.0.1:0',['--allow-writes','--port','tcp:127.0.0.1:'+simulator.address().port]);
  browser=await chromium.launch({headless:true,executablePath:process.env.CHAMELEON_TEST_CHROMIUM||undefined});
  const page=await browser.newPage();const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(server.base);await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="read"]').click();
  assert(await page.locator('#scan-hf').isDisabled());assert(await page.locator('#scan-lf').isDisabled());assert(await page.locator('#ndef-go').isEnabled());assert.equal(commands.length,0);
  await page.locator('.dock [data-open="device"]').click();
  const probe=async()=>{await page.locator('#refresh-features').click();await page.waitForFunction(()=>!document.querySelector('#device').hasAttribute('aria-busy'));};
  await probe();assert(await page.locator('#scan-hf').isDisabled());
  assert.match(await page.locator('#scan-hf + .feature-disabled-reason').textContent(),/2000/);assert.deepEqual(commands,[1033,1035]);
  model=1;capabilities=catalog.map(c=>c.id);await probe();assert.match(await page.locator('#scan-hf + .feature-disabled-reason').textContent(),/型号/);
  model=99;await probe();assert(await page.locator('#scan-hf').isDisabled());
  model=0;await probe();assert(await page.locator('#scan-hf').isEnabled());
  await page.locator('.dock [data-open="read"]').click();await page.locator('#scan-hf').click();
  await page.waitForFunction(()=>document.querySelector('#scan-out').textContent.includes('未检测到'));assert.equal(mode,0);
  await page.locator('.dock [data-open="device"]').click();
  const oldID=await page.evaluate(()=>deviceSession.session_id);
  assert.equal(connections,1);
  await page.locator('#session-disconnect').click();
  await page.waitForFunction(()=>deviceSession.connected===false);
  assert(await page.locator('#scan-hf').isDisabled());
  await page.locator('#session-tcp').fill('tcp:127.0.0.1:'+simulator.address().port);
  await page.locator('#session-connect').click();
  await page.waitForFunction(()=>deviceSession.connected===true);
  assert.notEqual(await page.evaluate(()=>deviceSession.session_id),oldID);
  await probe();assert.equal(connections,2);
  await page.reload();await page.evaluate(()=>window.libraryReady);
  await page.waitForFunction(()=>typeof deviceSession==='object'&&deviceSession?.connected);
  assert.equal(connections,2); // page reload reuses the server session
  delayModel=1500;
  const before=commands.length;
  const running=await page.evaluate(async()=>{
    const caps=await (await fetch('/api/capabilities')).json();const session=await (await fetch('/api/session/status')).json();
    return (await fetch('/api/tasks',{method:'POST',headers:{'Content-Type':'application/json','X-Chameleon-Token':caps.csrf_token},body:JSON.stringify({request_id:crypto.randomUUID(),session_id:session.session_id,path:'/api/features/device'})})).json();
  });
  await page.reload();await page.evaluate(()=>window.libraryReady);
  await page.waitForFunction(id=>document.querySelector(`[data-task-id="${id}"]`)?.dataset.state==='succeeded',running.id);
  assert.deepEqual(commands.slice(before),[1033,1035]); // refresh queries; it does not submit again
  assert.equal(connections,2);
  delayModel=0;
  await probe(); // task history must not repopulate current-session capability facts
  await page.locator('#settings-read').click();await page.waitForFunction(()=>!document.querySelector('#device').hasAttribute('aria-busy'));
  assert.doesNotMatch(await page.locator('#settings-result').textContent(),/654321/);
  await page.locator('#settings-value').fill('2');await page.waitForFunction(()=>!document.querySelector('#settings-apply').disabled);
  page.once('dialog',dialog=>dialog.accept());await page.locator('#settings-apply').click();
  await page.waitForFunction(()=>document.querySelector('#settings-result').textContent.includes('"readback_verified": true'));
  assert.equal(animation,2);assert.doesNotMatch(await page.locator('#settings-result').textContent(),/654321/);
  await page.waitForFunction(()=>!document.querySelector('#device').hasAttribute('aria-busy'));
  await page.locator('.dock [data-open="slots"]').click();await page.locator('#slot-edit-name').fill('Desk test');
  await page.waitForFunction(()=>!document.querySelector('#slot-edit-apply').disabled);
  page.once('dialog',dialog=>dialog.accept());await page.locator('#slot-edit-apply').click();
  await page.waitForFunction(()=>document.querySelector('#slot-edit-result').textContent.includes('"configuration_verified": true'));
  assert.equal(nicknames.get('0002').toString(),'Desk test');
  assert.deepEqual(errors,[]);
});
