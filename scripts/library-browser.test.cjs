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

test('Chromium uses observed model and firmware commands to explain disabled actions', { skip: !binary, timeout: 60000 }, async t => {
  const net = require('node:net'), read = require('node:fs');
  const catalog = JSON.parse(read.readFileSync(path.join(__dirname,'../internal/catalog/upstream.json'),'utf8')).commands;
  let model=0, capabilities=[1033,1035], mode=0;
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
        else if(command===1035){payload=Buffer.alloc(capabilities.length*2);capabilities.forEach((c,i)=>payload.writeUInt16BE(c,i*2));}
        else if(command===1002)payload=Buffer.from([mode]);
        else if(command===1001)mode=frame[9];
        else if(command===2000)status=1;
        else status=103;
        const reply=Buffer.alloc(payload.length+10);reply[0]=0x11;reply[1]=0xef;reply.writeUInt16BE(command,2);reply.writeUInt16BE(status,4);reply.writeUInt16BE(payload.length,6);
        reply[8]=(-reply.subarray(0,8).reduce((sum,v)=>sum+v,0))&255;payload.copy(reply,9);reply[reply.length-1]=(-payload.reduce((sum,v)=>sum+v,0))&255;socket.write(reply);
      }
    });
  });
  await new Promise(resolve=>simulator.listen(0,'127.0.0.1',resolve));
  let server, browser;
  t.after(async()=>{if(browser)await browser.close();if(server)await server.close();for(const socket of sockets)socket.destroy();await new Promise(resolve=>simulator.close(resolve));});
  server=await serve(binary,'127.0.0.1:0',['--port','tcp:127.0.0.1:'+simulator.address().port]);
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
  assert.deepEqual(errors,[]);
});
