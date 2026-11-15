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
  let model=0, capabilities=[1033,1035], mode=0, delayModel=0, animation=0, readerEnabled=false;const nicknames=new Map();
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
        else if(command===2000){status=readerEnabled?0:1;if(readerEnabled)payload=Buffer.from([4,1,2,3,4,4,0,9,0]);}
        else if(command===2008){status=0;payload=Buffer.alloc(16,frame[10]);if(frame[10]%4===3)Buffer.from([0x7f,0x07,0x88]).copy(payload,6);}
        else if(command===2007){status=0;}
        else if(command===2010){status=0;payload=Buffer.from(Array.from({length:16},(_,i)=>frame[15]*4+i));}
        else if(command===3014){status=0x40;payload=Buffer.from('12345678');}
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
  readerEnabled=true;
  await page.waitForFunction(()=>!document.querySelector('#slots').hasAttribute('aria-busy'));
  await page.locator('.dock [data-open="read"]').click();
  await page.locator('#reader-classic-json').fill(JSON.stringify({uid:'01020304',sectors:Array.from({length:5},(_,sector)=>({sector,a:'010203040506'}))}));
  await page.waitForFunction(()=>!document.querySelector('#reader-classic-go').disabled);await page.locator('#reader-classic-go').click();
  await page.waitForFunction(()=>document.querySelector('#reader-classic-result').textContent.includes('20 / 20'));
  assert.equal(await page.locator('#reader-classic-json').inputValue(),'');
  await page.waitForFunction(()=>!document.querySelector('#read').hasAttribute('aria-busy'));assert(await page.locator('#reader-classic-export').isEnabled());
  assert.match(await page.locator('#reader-classic-result').textContent(),/未知密钥/);
  await page.waitForFunction(()=>!document.querySelector('#read').hasAttribute('aria-busy'));
  await page.locator('#reader-type2-uid').fill('01020304');await page.locator('#reader-type2-start').fill('1');await page.locator('#reader-type2-count').fill('2');
  await page.waitForFunction(()=>!document.querySelector('#reader-type2-go').disabled);await page.locator('#reader-type2-go').click();
  await page.waitForFunction(()=>document.querySelector('#reader-type2-result').textContent.includes('2 / 2'));
  assert.match(await page.locator('#reader-type2-result').textContent(),/1: 04 05 06 07/);
  await page.waitForFunction(()=>!document.querySelector('#read').hasAttribute('aria-busy'));
  await page.locator('#reader-lf-protocol').selectOption('pac');await page.waitForFunction(()=>!document.querySelector('#reader-lf-go').disabled);await page.locator('#reader-lf-go').click();
  await page.waitForFunction(()=>document.querySelector('#reader-lf-result').textContent.includes('LF 读取完成'));
  assert.match(await page.locator('#reader-lf-result').textContent(),/12345678/);
  await page.waitForFunction(()=>!document.querySelector('#read').hasAttribute('aria-busy'));
  const tasks=await page.evaluate(()=>api('/api/tasks'));assert(tasks.tasks.filter(t=>t.path.startsWith('/api/reader/')).every(t=>!t.write));
  model=1;await page.locator('.dock [data-open="device"]').click();await probe();await page.locator('.dock [data-open="read"]').click();
  assert(await page.locator('#reader-type2-go').isDisabled());assert.match(await page.locator('#reader-type2-go').locator('xpath=following-sibling::span[1]').textContent(),/当前设备型号/);
  assert.deepEqual(errors,[]);
});

test('Chromium manages a USB bridge and reuses its device session after reload', {skip:!binary,timeout:60000}, async t => {
  const net=require('node:net'),http=require('node:http');
  const keyDir=await fs.mkdtemp(path.join(os.tmpdir(),'chameleon-usb-browser-'));
  const keyFile=path.join(keyDir,'usb.key'),token='a'.repeat(64);
  await fs.writeFile(keyFile,token,{mode:0o600});
  const transport={connection:'tcp',medium:'usb-serial',managed:true,allows_pairing_changes:false,allows_device_restore:false,supports_dfu:false};
  let state='disconnected',selected=null,connections=0,server,browser;
  const sockets=new Set(),commands=[],errors=[];
  const status=()=>({api_version:1,state,selected_device:selected,session_active:sockets.size>0,transport,data_address:'127.0.0.1:'+data.address().port,error:''});
  const data=net.createServer(socket=>{
    if(state!=='connected'||sockets.size){socket.destroy();return;}
    connections++;sockets.add(socket);socket.on('error',()=>{});socket.on('close',()=>{sockets.delete(socket);state='disconnected';});
    socket.write('CHAMELEON_BRIDGE/1 '+JSON.stringify({api_version:1,transport,device_id:'COM7'})+'\n');
    let buffer=Buffer.alloc(0);
    socket.on('data',chunk=>{
      buffer=Buffer.concat([buffer,chunk]);
      while(buffer.length>=10){
        const size=buffer.readUInt16BE(6)+10;if(buffer.length<size)break;
        const command=buffer.readUInt16BE(2);buffer=buffer.subarray(size);commands.push(command);
        const payloads={1000:Buffer.from([2,0]),1033:Buffer.from([0]),1017:Buffer.from('usb-browser'),1002:Buffer.from([0]),1025:Buffer.from([14,116,80]),1018:Buffer.from([0]),1019:Buffer.from(Array.from({length:8},()=>[3,233,0,100]).flat()),1023:Buffer.from(Array(16).fill(1))};
        const caps=[1000,1033,1017,1035,1002,1025,1018,1019,1023];
        if(command===1035){payloads[1035]=Buffer.alloc(caps.length*2);caps.forEach((c,i)=>payloads[1035].writeUInt16BE(c,i*2));}
        const payload=payloads[command]||Buffer.alloc(0),reply=Buffer.alloc(payload.length+10);
        reply[0]=0x11;reply[1]=0xef;reply.writeUInt16BE(command,2);reply.writeUInt16BE(payloads[command]?0x68:103,4);reply.writeUInt16BE(payload.length,6);
        reply[8]=(-reply.subarray(0,8).reduce((s,v)=>s+v,0))&255;payload.copy(reply,9);reply[reply.length-1]=(-payload.reduce((s,v)=>s+v,0))&255;socket.write(reply);
      }
    });
  });
  const control=http.createServer(async(req,res)=>{
    if(req.headers.authorization!=='Bearer '+token){res.writeHead(401);res.end('{}');return;}
    const chunks=[];for await(const chunk of req)chunks.push(chunk);
    const input=chunks.length?JSON.parse(Buffer.concat(chunks)):{};
    res.setHeader('Content-Type','application/json');res.setHeader('Cache-Control','no-store');
    if(req.url==='/v1/scan'){res.end(JSON.stringify({api_version:1,devices:[{id:'COM7',name:'Application port'}]}));return;}
    if(req.url==='/v1/select'){assert.equal(input.device_id,'COM7');selected={id:'COM7',name:'Application port'};}
    if(req.url==='/v1/connect'){assert(selected);state='connected';}
    if(req.url==='/v1/disconnect'){state='disconnected';for(const socket of sockets)socket.destroy();}
    res.end(JSON.stringify(status()));
  });
  t.after(async()=>{
    if(browser)await browser.close();if(server)await server.close();
    for(const socket of sockets)socket.destroy();
    await Promise.all([new Promise(resolve=>data.close(resolve)),new Promise(resolve=>control.close(resolve))]);
    await fs.rm(keyDir,{recursive:true,force:true});
  });
  await new Promise(resolve=>data.listen(0,'127.0.0.1',resolve));
  await new Promise(resolve=>control.listen(0,'127.0.0.1',resolve));
  server=await serve(binary,'127.0.0.1:0',['--usb-bridge','http://127.0.0.1:'+control.address().port,'--bridge-token-file',keyFile]);
  browser=await chromium.launch({headless:true,executablePath:process.env.CHAMELEON_TEST_CHROMIUM||undefined});
  const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
  const idle=()=>page.waitForFunction(()=>!document.querySelector('#device').hasAttribute('aria-busy'));
  await page.goto(server.base);await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="device"]').click();
  await page.waitForFunction(()=>!document.querySelector('#bridge-scan').disabled);
  assert.match(await page.locator('#bridge-title').textContent(),/USB/);
  assert.equal(await page.locator('#session-panel').isVisible(),false);
  await page.locator('#bridge-scan').click();await idle();
  await page.locator('#bridge-devices').selectOption('COM7');await page.locator('#bridge-select').click();await idle();
  await page.locator('#bridge-connect').click();await idle();
  assert.match(await page.locator('#info-cards').textContent(),/usb-browser/);
  assert.equal(connections,1);
  const session=await page.evaluate(()=>deviceSession.session_id);
  await page.reload();await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="device"]').click();
  await page.waitForFunction(()=>document.querySelector('#bridge-state').textContent.includes('已连接'));
  assert.equal(await page.evaluate(()=>deviceSession.session_id),session);
  await page.locator('#refresh-info').click();await idle();assert.equal(connections,1);
  const snapshot=await (await page.request.get(server.base+'/api/session/status')).json();assert.deepEqual(snapshot.transport,transport);
  await page.locator('#bridge-disconnect').click();await idle();
  assert.match(await page.locator('#bridge-state').textContent(),/未连接/);
  assert.equal(await page.locator('#info-cards').textContent(),'');
  assert.notEqual(await page.evaluate(()=>deviceSession.session_id),session);
  assert(!commands.some(c=>[1010,1014,1020,1030,1032,1037].includes(c)));
  assert.deepEqual(errors,[]);
});
