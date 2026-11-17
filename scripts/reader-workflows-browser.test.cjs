const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { createInterface } = require('node:readline');
const { chromium } = require('playwright');
const binary = process.env.CHAMELEON_WEBUI_TEST_BINARY;

async function serve(extra = []) {
  const taskDir=await fs.mkdtemp(path.join(os.tmpdir(),'chameleon-task-fixture-'));
  const child = spawn(path.resolve(binary), ['serve','--task-journal',path.join(taskDir,'tasks.jsonl'), '--addr', '127.0.0.1:0', ...extra], { stdio: ['ignore', 'pipe', 'pipe'] });
  let errors = ''; child.stderr.on('data', data => { errors += data; });
  const lines = createInterface({ input: child.stdout });
  const base = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => { child.kill(); reject(Error('server startup timeout: ' + errors)); }, 15000);
    lines.on('line', line => { const match = line.match(/http:\/\/([^/]+)\//); if (match) { clearTimeout(timer); resolve('http://' + match[1]); } });
    child.once('error', error => { clearTimeout(timer); reject(error); });
    child.once('exit', () => { clearTimeout(timer); reject(Error('server exited: ' + errors)); });
  });
  return { base, async close(signal="SIGTERM") { lines.close(); if (child.exitCode === null && child.signalCode === null) { const exit = once(child, 'exit'); child.kill(signal); await exit; } await fs.rm(taskDir,{recursive:true,force:true}); } };
}
const launch = () => chromium.launch({ headless: true, executablePath: process.env.CHAMELEON_TEST_CHROMIUM || undefined });
const library = page => page.locator('#db-json').inputValue().then(JSON.parse);
const cards = database => Object.entries(database).filter(([key]) => key === 'cards' || key.endsWith('.cards')).flatMap(([, values]) => values.map(value => typeof value === 'string' ? JSON.parse(value) : value));
const saved = page => page.waitForFunction(() => document.querySelector('#db-save-state').textContent.includes('已自动保存'));
async function selectCard(page, id) {
  await page.locator('.dock [data-open="cards"]').click();
  await page.locator('#db-id').fill(id); await page.locator('#db-show').click();
  await page.waitForFunction(id => document.querySelector('#db-id').value === id && document.querySelector('#card-edit-name').value !== '' && !document.querySelector('#cards').hasAttribute('aria-busy'), id);
}
async function type2Simulator({writable=false,failPage=null}={}) {
  const uid = Buffer.from('04112233445566', 'hex'), memory = Buffer.alloc(45 * 4, 0x22), commands = [], sockets = new Set(), rawReads = [], rawWrites = [];
  uid.copy(memory, 0, 0, 3); memory[3] = 0x88 ^ uid[0] ^ uid[1] ^ uid[2]; uid.copy(memory, 4, 3);
  memory[8] = uid[3] ^ uid[4] ^ uid[5] ^ uid[6]; memory[9] = 0x48; memory[10] = memory[11] = 0;
  Buffer.from([0xe1, 0x10, 0x12, 0]).copy(memory, 12); memory.fill(0, 41 * 4);
  Buffer.from('DEADBEEFBEEFFACE', 'hex').copy(memory, 43 * 4);
  const server = net.createServer(socket => {
    sockets.add(socket); socket.on('close', () => sockets.delete(socket)); socket.on('error', () => {}); let buffer = Buffer.alloc(0);
    socket.on('data', chunk => {
      buffer = Buffer.concat([buffer, chunk]);
      while (buffer.length >= 10) {
        const size = buffer.readUInt16BE(6) + 10; if (buffer.length < size) break;
        const frame = buffer.subarray(0, size); buffer = buffer.subarray(size); const command = frame.readUInt16BE(2), input = frame.subarray(9, size - 1); commands.push(command);
        let status = 0x68, data = Buffer.alloc(0);
        if (command === 1033) data = Buffer.from([0]);
        else if (command === 1035) { const list = [1001, 1002, 2000, 2010, 2101]; data = Buffer.alloc(list.length * 2); list.forEach((value, index) => data.writeUInt16BE(value, index * 2)); }
        else if (command === 1002) data = Buffer.from([1]);
        else if (command === 2000) { status = 0; data = Buffer.concat([Buffer.from([7]), uid, Buffer.from([0x44, 0, 0, 0])]); }
        else if (command === 2010) {
          status = 0;
          if (input[5] === 0x60) data = Buffer.from([0, 4, 4, 2, 1, 0, 0x0f, 3]);
          else if(input[5]===0x3c) data=Buffer.alloc(32,0xa5);
          else if(input[5]===0xa2&&writable){rawWrites.push(input[6]);if(input[6]===failPage)data=Buffer.from([0]);else{input.subarray(7,11).copy(memory,input[6]*4);data=Buffer.from([10]);}}
          else if (input[5] === 0x3a) { rawReads.push([input[6], input[7]]); data = memory.subarray(input[6] * 4, (input[7] + 1) * 4); }
          else { status = 0x67; }
        } else if (command !== 2101 && command !== 1001) status = 0x67;
        const reply = Buffer.alloc(data.length + 10); reply[0] = 0x11; reply[1] = 0xef; reply.writeUInt16BE(command, 2); reply.writeUInt16BE(status, 4); reply.writeUInt16BE(data.length, 6);
        reply[8] = (-reply.subarray(0, 8).reduce((sum, v) => sum + v, 0)) & 255; data.copy(reply, 9); reply[reply.length - 1] = (-data.reduce((sum, v) => sum + v, 0)) & 255; socket.write(reply);
      }
    });
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { port: 'tcp:127.0.0.1:' + server.address().port, commands, rawReads, rawWrites, memory, async close() { for (const socket of sockets) socket.destroy(); await new Promise(resolve => server.close(resolve)); } };
}

test('Chromium reads modeled Type2, masks credentials, promotes explicitly and restores the standard-card handoff', { skip: !binary, timeout: 90000 }, async t => {
  const simulator = await type2Simulator(); let server, browser; const errors = [];
  t.after(async () => { if (browser) await browser.close(); if (server) await server.close(); await simulator.close(); });
  server = await serve(['--port', simulator.port]); browser = await launch(); const page = await browser.newPage(); page.on('pageerror', error => errors.push(error.message)); page.on('dialog', dialog => dialog.accept());
  await page.goto(server.base); await page.evaluate(() => window.libraryReady);
  await page.locator('.dock [data-open="device"]').click(); await page.locator('#refresh-features').click(); await page.waitForFunction(() => !document.querySelector('#device').hasAttribute('aria-busy'));
  await page.locator('.dock [data-open="read"]').click(); await page.locator('#reader-workflows > summary').click();
  await page.locator('#workflow-kind').selectOption('type2'); await page.locator('#workflow-json').fill(JSON.stringify({ uid: '04112233445566', full: true }));
  await page.locator('#workflow-check').click(); await page.waitForFunction(() => !document.querySelector('#workflow-read').disabled); await page.locator('#workflow-read').click();
  await page.waitForFunction(() => !document.querySelector('#workflow-import').disabled);
  await page.locator('#workflow-import').click(); await page.locator('.dock [data-open="cards"]').click(); await page.waitForFunction(() => document.querySelector('#card-edit-rows').value.includes('?? ?? ?? ??'),null,{timeout:15000}).catch(async error=>{const state=await page.evaluate(()=>({rows:document.querySelector('#card-edit-rows').value,state:document.querySelector('#card-edit-state').textContent,workflow:document.querySelector('#workflow-result').textContent,toast:document.querySelector('#toast')?.textContent,db:document.querySelector('#db-json').value}));throw Error(error.message+'; '+JSON.stringify(state));});
  let database = await library(page), source = cards(database)[0]; assert.equal(source.desk_read.read_result.type2.model, 'NTAG213'); assert.equal(source.desk_read.rows[43], '????????'); assert.equal(source.desk_read.rows[44], '????????');
  assert.doesNotMatch(JSON.stringify(database), /DEADBEEF|BEEFFACE/); assert(await page.locator('#card-promote-classic').isDisabled());
  await page.locator('#card-type2-options > summary').click(); await page.locator('#card-type2-password').fill('11223344'); await page.locator('#card-type2-pack').fill('1234'); await page.locator('#card-type2-confirm').check();
  await page.waitForFunction(() => !document.querySelector('#card-promote-classic').disabled); await page.locator('#card-promote-classic').click();
  await page.waitForFunction(() => document.querySelector('#card-edit-limit').textContent.includes('只修改文件')); await saved(page);
  database = await library(page); const standard = cards(database).find(card => !card.desk_read); assert(standard); assert.notEqual(standard.id, source.id); assert.equal(standard.tag, 1100); assert.deepEqual(standard.data[43], [0x11, 0x22, 0x33, 0x44]); assert.deepEqual(standard.data[44], [0x12, 0x34, 0, 0]);
  assert.equal(cards(database).find(card => card.id === source.id).desk_read.rows[43], '????????');
  await page.reload(); await page.evaluate(() => window.libraryReady); assert.deepEqual(await library(page), database); await selectCard(page, standard.id);
  await page.locator('#cards button').filter({ hasText: '在槽位加载器中打开' }).click(); assert.equal(await page.locator('#slots').isVisible(), true); assert.equal(JSON.parse(await page.locator('#load-card-json').inputValue()).id, standard.id);
  assert.deepEqual(simulator.rawReads, [[0, 14], [15, 29], [30, 44]]); assert(simulator.commands.every(command => [1033, 1035, 1001, 1002, 2000, 2010, 2101].includes(command))); assert.deepEqual(errors, []);
});

test('Chromium converts LF offline, restores an unsaved editor snapshot and applies addressed Classic ACL edits through Go', { skip: !binary, timeout: 90000 }, async t => {
  let server, browser; const errors = [], requests = [];
  t.after(async () => { if (browser) await browser.close(); if (server) await server.close(); });
  server = await serve(); browser = await launch(); const page = await browser.newPage(); page.on('pageerror', error => errors.push(error.message)); page.on('dialog', dialog => dialog.accept()); page.on('request', request => requests.push(new URL(request.url()).pathname));
  await page.goto(server.base); await page.evaluate(() => window.libraryReady); await page.locator('.dock [data-open="cards"]').click();
  await page.locator('.read-result-import > summary').click(); await page.locator('#read-import-json').fill(JSON.stringify({ schema: 1, read_result: { schema: 1, kind: 'lf', complete: true, requested_units: 1, units_read: 1, present: true, record: { protocol: 'pac', id: '3132333435363738', pac: '12345678' } }, error: null })); await page.locator('#read-import-go').click();
  await page.waitForFunction(() => !document.querySelector('#card-promote-classic').disabled); await page.locator('#card-promote-classic').click(); await page.waitForFunction(() => document.querySelector('#card-lf-editor').hidden === false); await saved(page);
  const standard = cards(await library(page)).find(card => card.tag === 150); assert(standard); assert.equal(standard.uid, '3132333435363738');
  await page.locator('#card-edit-name').fill('Recovered PAC draft'); const downloadPromise = page.waitForEvent('download'); await page.locator('#card-edit-export').click(); const download = await downloadPromise, recovery = JSON.parse(await fs.readFile(await download.path(), 'utf8'));
  assert.equal(recovery.card.id, standard.id); assert.equal(recovery.name, 'Recovered PAC draft'); await page.locator('#card-edit-discard').click();
  await page.locator('#card-edit-recovery-json').locator('..').locator('summary').click(); await page.locator('#card-edit-recovery-json').fill(JSON.stringify(recovery)); await page.locator('#card-edit-recovery-go').click();
  await page.waitForFunction(() => document.querySelector('#card-edit-name').value === 'Recovered PAC draft'); assert.equal(cards(await library(page)).find(card => card.id === standard.id).name, standard.name); assert(await page.locator('#card-edit-save').isEnabled()); await page.locator('#card-edit-save').click();await page.waitForFunction(()=>!document.querySelector('#cards').hasAttribute('aria-busy')&&document.querySelector('#card-edit-state').textContent.includes('编辑已加入卡库')); await saved(page);
  const data = Array.from({ length: 20 }, () => Array(16).fill(0)); for (let block = 3; block < 20; block += 4) { data[block].fill(0xff, 0, 6); data[block].splice(6, 4, 0xff, 7, 0x80, 0x69); data[block].fill(0xff, 10); } data[0].splice(0, 8, 1, 2, 3, 4, 4, 9, 4, 0);
  const classic = { id: 'classic-browser', name: 'Classic test', uid: '01020304', tag: 1000, sak: 9, atqa: [0, 4], ats: [], data };
  const addDetails=page.locator('#db-card').locator('xpath=ancestor::details[1]');if(await addDetails.count()&&await addDetails.getAttribute('open')===null)await addDetails.locator('summary').click();await page.locator('#db-card').fill(JSON.stringify(classic)); await page.locator('#db-add').click(); await saved(page); await selectCard(page, classic.id);
  await page.locator('#card-edit-address').locator('..').locator('..').locator('summary').click(); await page.locator('#card-edit-address').fill('1'); await page.locator('#card-edit-unit-hex').fill('AA ' + '00 '.repeat(15)); await page.locator('#card-edit-unit-apply').click();
  await page.locator('#card-edit-acl-read').click(); await page.waitForFunction(() => document.querySelector('#card-edit-acl-values').value === '0,0,0,4'); await page.locator('#card-edit-acl-values').fill('0,0,0,3'); await page.locator('#card-edit-acl-apply').click(); await page.waitForFunction(() => document.querySelector('#card-edit-unit-status').textContent.includes('已修改访问位'));
  assert.match(await page.locator('#card-edit-diff-summary').textContent(), /2 行有修改/); await page.locator('#card-edit-save').click();await page.waitForFunction(()=>!document.querySelector('#cards').hasAttribute('aria-busy')&&document.querySelector('#card-edit-state').textContent.includes('编辑已加入卡库')); await saved(page);
  const edited = cards(await library(page)).find(card => card.id === classic.id); assert.equal(edited.data[1][0], 0xaa); assert.deepEqual(edited.data[3].slice(0, 6), Array(6).fill(0xff)); assert.equal(edited.data[3][9], 0x69); assert.deepEqual(edited.data[3].slice(10), Array(6).fill(0xff)); assert.notDeepEqual(edited.data[3].slice(6, 9), classic.data[3].slice(6, 9));
  assert(requests.includes('/api/acl/decode')); assert(requests.includes('/api/acl/encode')); assert(!requests.some(url => url.startsWith('/api/reader/') || url.startsWith('/api/slot/'))); assert.deepEqual(errors, []);
});

async function slotSnapshotSimulator(){
 const commands=[],sockets=new Set(),memory=Buffer.alloc(1024,0x42);let failSecond=true;
 Buffer.from([1,2,3,4]).copy(memory);for(let block=3;block<64;block+=4)Buffer.from([0xff,7,0x80]).copy(memory,block*16+6);
 const types=Buffer.alloc(32),flags=Buffer.alloc(16);types.writeUInt16BE(1001,0);types.writeUInt16BE(100,2);flags[0]=flags[1]=1;
 const capabilities=[1000,1033,1017,1035,1002,1025,1019,1023,1018,1008,4018,4008,5001];
 const server=net.createServer(socket=>{sockets.add(socket);socket.on('error',()=>{});socket.on('close',()=>sockets.delete(socket));let buffer=Buffer.alloc(0);
  socket.on('data',chunk=>{buffer=Buffer.concat([buffer,chunk]);while(buffer.length>=10){const size=buffer.readUInt16BE(6)+10;if(buffer.length<size)break;const frame=buffer.subarray(0,size),command=frame.readUInt16BE(2),input=frame.subarray(9,size-1);buffer=buffer.subarray(size);commands.push(command);let status=0x68,data=Buffer.alloc(0);
   if(command===1000)data=Buffer.from([2,2]);else if(command===1033||command===1002||command===1018)data=Buffer.from([0]);else if(command===1017)data=Buffer.from('slot-fixture');else if(command===1035){data=Buffer.alloc(capabilities.length*2);capabilities.forEach((value,i)=>data.writeUInt16BE(value,i*2));}
   else if(command===1025)data=Buffer.from([0x0e,0x74,80]);else if(command===1019)data=types;else if(command===1023)data=flags;else if(command===1008)data=Buffer.from('Current slot');else if(command===4018)data=Buffer.from([4,1,2,3,4,4,0,8,0]);
   else if(command===4008){if(input[0]===32&&failSecond)status=103;else data=memory.subarray(input[0]*16,(input[0]+input[1])*16);}
   else if(command===5001)data=Buffer.from([0,100,1,2,3,4,5]);else status=103;
   const reply=Buffer.alloc(data.length+10);reply[0]=0x11;reply[1]=0xef;reply.writeUInt16BE(command,2);reply.writeUInt16BE(status,4);reply.writeUInt16BE(data.length,6);reply[8]=(-reply.subarray(0,8).reduce((sum,n)=>sum+n,0))&255;data.copy(reply,9);reply[reply.length-1]=(-data.reduce((sum,n)=>sum+n,0))&255;socket.write(reply);
  }});
 });await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 return {commands,port:'tcp:127.0.0.1:'+server.address().port,complete(){failSecond=false},async close(){for(const socket of sockets)socket.destroy();await new Promise(resolve=>server.close(resolve))}};
}
test('Chromium reads current slot, previews unknown data and persists an imported snapshot without device writes',{skip:!binary,timeout:90000},async t=>{
 const simulator=await slotSnapshotSimulator();let server,browser;const errors=[];
 t.after(async()=>{if(browser)await browser.close();if(server)await server.close();await simulator.close()});
 server=await serve(['--port',simulator.port]);browser=await launch();const page=await browser.newPage();page.on('pageerror',error=>errors.push(error.message));page.on('dialog',dialog=>dialog.accept());
 await page.goto(server.base);await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="slots"]').click();
 assert(await page.locator('#slot-read-go').isDisabled());await page.locator('#refresh-slots').click();await page.waitForFunction(()=>!document.querySelector('#slot-read-go').disabled);
 await page.locator('#slot-read-go').click();await page.waitForFunction(()=>document.querySelector('#slot-read-result').textContent.includes('32 / 64'));
 assert.match(await page.locator('#slot-read-result').textContent(),/32: \?\?/);assert.match(await page.locator('#slot-read-result').textContent(),/UID 01020304/);assert(await page.locator('#slot-read-import').isEnabled());
 await page.locator('#slot-read-name').fill('Slot snapshot partial');await page.locator('#slot-read-import').click();await saved(page);
 const imported=cards(await library(page)).find(c=>c.name==='Slot snapshot partial');assert(imported);assert.equal(imported.tag,0);assert.equal(imported.desk_read.read_result.kind,'slot');assert.equal(imported.desk_read.rows.length,64);assert.equal(imported.desk_read.rows[32],'??'.repeat(16));assert.equal(imported.desk_read.read_result.slot_snapshot.identity.atqa,'0004');assert.equal(imported.desk_read.read_result.slot_snapshot.identity.ats,'');
 const count=simulator.commands.filter(c=>c===4008).length;assert.equal(count,2);
 await page.reload();await page.evaluate(()=>window.libraryReady);assert.equal(simulator.commands.filter(c=>c===4008).length,count);
 await selectCard(page,imported.id);assert.match(await page.locator('#card-edit-limit').textContent(),/槽位快照草稿/);assert.match(await page.locator('#card-edit-rows').inputValue(),/\?\?/);assert(await page.locator('#card-promote-classic').isDisabled());
 const pending=page.waitForEvent('download');await page.locator('[data-export-card]').click();const download=await pending;const exported=JSON.parse(await fs.readFile(await download.path(),'utf8'));assert.equal(exported.desk_read.read_result.slot_snapshot.sha256,imported.desk_read.read_result.slot_snapshot.sha256);assert.equal(exported.desk_read.rows[32],'??'.repeat(16));
 assert.equal(simulator.commands.some(c=>[1001,1003,1009,4000,4022].includes(c)),false);assert.deepEqual(errors,[]);
});

test('Chromium explicitly converts a complete slot snapshot and retains both records and provenance after reload',{skip:!binary,timeout:90000},async t=>{
 const simulator=await slotSnapshotSimulator();simulator.complete();let server,browser;const errors=[],requests=[];
 t.after(async()=>{if(browser)await browser.close();if(server)await server.close();await simulator.close()});
 server=await serve(['--port',simulator.port]);browser=await launch();const page=await browser.newPage();page.on('pageerror',error=>errors.push(error.message));page.on('dialog',dialog=>dialog.accept());page.on('request',request=>requests.push(new URL(request.url()).pathname));
 await page.goto(server.base);await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="slots"]').click();await page.locator('#refresh-slots').click();await page.waitForFunction(()=>!document.querySelector('#slot-read-go').disabled);
 await page.locator('#slot-read-go').click();await page.waitForFunction(()=>document.querySelector('#slot-read-result').textContent.includes('64 / 64'));
 await page.locator('#slot-read-import').click();await page.locator('.dock [data-open="cards"]').click();await page.waitForFunction(()=>!document.querySelector('#card-promote-classic').disabled);await saved(page);
 const before=await library(page),source=cards(before).find(c=>c.desk_read?.read_result.kind==='slot');assert(source);assert.equal(source.desk_read.read_result.complete,true);assert.equal(source.desk_read.read_result.slot_snapshot.unknown_ranges.length,0);
 await page.locator('#card-promote-classic').click();await page.waitForFunction(()=>!document.querySelector('#card-slot-origin').hidden);await saved(page);
 const database=await library(page),standard=cards(database).find(c=>c.desk_read_origin?.source==='emulator_slot');assert(standard);assert.notEqual(standard.id,source.id);assert.equal(standard.tag,1001);assert.equal(standard.uid,'01020304');assert.deepEqual(standard.atqa,[0,4]);assert.equal(standard.sak,8);assert.equal(standard.desk_read_origin.source_card_id,source.id);assert.equal(standard.desk_read_origin.physical_write_allowed,false);assert.deepEqual(standard.desk_read_origin.draft,source.desk_read);assert.deepEqual(cards(database).find(c=>c.id===source.id),source);
 assert.match(await page.locator('#card-slot-origin').textContent(),/模拟器槽位 1.*实体卡写入未开放/);assert(requests.includes('/api/features/check'));assert(requests.includes('/api/database'));
 await page.reload();await page.evaluate(()=>window.libraryReady);assert.deepEqual(await library(page),database);await selectCard(page,standard.id);assert.match(await page.locator('#card-slot-origin').textContent(),/模拟器槽位 1/);
 const pending=page.waitForEvent('download');await page.locator('[data-export-card]').click();const download=await pending,exported=JSON.parse(await fs.readFile(await download.path(),'utf8'));assert.deepEqual(exported.desk_read_origin,standard.desk_read_origin);
 assert.equal(simulator.commands.filter(c=>c===4008).length,2);assert.equal(simulator.commands.some(c=>[1001,1003,1009,4000,4022].includes(c)),false);assert.equal(requests.some(p=>p.startsWith('/api/physical/')||p.includes('write-plan')),false);assert.deepEqual(errors,[]);
});

test('Chromium manages folders and dictionaries and previews external files without losing unknown data',{skip:!binary,timeout:120000},async t=>{
 let server,browser;const errors=[];t.after(async()=>{if(browser)await browser.close();if(server)await server.close()});server=await serve();browser=await launch();const page=await browser.newPage();page.on('pageerror',error=>errors.push(error.message));page.on('dialog',dialog=>dialog.accept());await page.goto(server.base);await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="cards"]').click();const root=page.locator('#library-manager');await root.locator('summary').first().click();await page.waitForFunction(()=>!document.querySelector('#cards').hasAttribute('aria-busy'));
 const input=(label,tag='input')=>root.locator('label').filter({hasText:label}).first().locator(tag);await input('名称').fill('Work');await root.getByRole('button',{name:'新建目录',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#db-json').value.includes('Work')&&!document.querySelector('#cards').hasAttribute('aria-busy'));await saved(page);
 let database=await library(page);assert(Object.keys(database).some(k=>k.endsWith('card_folders')));await input('资料类型','select').selectOption('字典');await page.waitForFunction(()=>!document.querySelector('#cards').hasAttribute('aria-busy'));await input('名称').fill('Two keys');await input('字典密钥（','textarea').fill('FFFFFFFFFFFF\nA0A1A2A3A4A5\nFFFFFFFFFFFF');await root.getByRole('button',{name:'新建字典',exact:true}).click();await saved(page);await page.waitForFunction(()=>!document.querySelector('#cards').hasAttribute('aria-busy'));database=await library(page);const dict=Object.entries(database).find(([k])=>k.endsWith('dictionaries'))[1].map(JSON.parse)[0];assert.equal(dict.keys.length,2);
 await input('源文件').setInputFiles({name:'partial.mct',mimeType:'text/plain',buffer:Buffer.from('+Sector: 0\n01020304000000000000000000000000\n--------------------------------\n')});await root.getByRole('button',{name:'预览文件',exact:true}).click();await page.waitForFunction(()=>[...document.querySelectorAll('#library-manager button')].some(b=>b.textContent==='将预览添加为来源草稿'&&!b.disabled));await root.getByRole('button',{name:'将预览添加为来源草稿',exact:true}).click();await saved(page);database=await library(page);const imported=cards(database).find(c=>c.desk_read?.read_result.kind==='file');assert(imported);assert.equal(imported.tag,0);assert.equal(imported.desk_read.rows[1],'??'.repeat(16));assert(imported.desk_read.read_result.file_observation.raw.includes('--------------------------------'));
 const edited=structuredClone(imported);edited.desk_read.rows[0]='FF'+edited.desk_read.rows[0].slice(2);await input('左侧 Desk 卡片 JSON').setInputFiles({name:'left.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(imported))});await input('右侧 Desk 卡片 JSON').setInputFiles({name:'right.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(edited))});await root.getByRole('button',{name:'比较两个文件',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#library-manager').textContent.includes('unknown_comparisons'));assert.match(await root.textContent(),/"unknown_comparisons": 16/);assert.match(await root.textContent(),/"state": "changed"/);
 await page.reload();await page.evaluate(()=>window.libraryReady);assert.deepEqual(await library(page),database);await selectCard(page,imported.id);assert.match(await page.locator('#card-edit-limit').textContent(),/外部文件来源草稿/);assert(await page.locator('#card-promote-classic').isDisabled());assert.deepEqual(errors,[]);
});

test('Chromium captures signature metadata and stops a reviewed multi-page write at the first uncertain page',{skip:!binary,timeout:120000},async t=>{
 const simulator=await type2Simulator({writable:true,failPage:5});let server,browser;const errors=[];t.after(async()=>{if(browser)await browser.close();if(server)await server.close();await simulator.close()});server=await serve(['--port',simulator.port,'--allow-writes']);browser=await launch();const page=await browser.newPage();page.on('pageerror',error=>errors.push(error.message));page.on('dialog',dialog=>dialog.accept());await page.goto(server.base);await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="device"]').click();await page.locator('#refresh-features').click();await page.waitForFunction(()=>!document.querySelector('#device').hasAttribute('aria-busy'));await page.locator('.dock [data-open="read"]').click();await page.locator('#reader-workflows > summary').click();
 const fields=page.locator('#workflow-form-fields'),field=(text)=>fields.locator('label').filter({hasText:text}).first().locator('input');await field('预期 UID').fill('04112233445566');await field('采集签名').check();await page.locator('#workflow-check').click();await page.waitForFunction(()=>!document.querySelector('#workflow-read').disabled);await page.locator('#workflow-read').click();await page.waitForFunction(()=>!document.querySelector('#workflow-import').disabled);await page.locator('#workflow-import').click();await saved(page);const source=cards(await library(page)).find(c=>c.desk_read?.read_result.type2?.signature);assert(source);assert.equal(source.desk_read.read_result.type2.signature.status,'observed');assert.equal(source.desk_read.read_result.type2.signature.verified,false);assert.equal(source.desk_read.read_result.type2.signature.data,'A5'.repeat(32));
 await page.locator('#workflow-kind').selectOption('type2-batch');await field('预期 UID').fill('04112233445566');for(let i=0;i<3;i++){await fields.getByRole('button',{name:'添加普通页',exact:true}).click();const row=fields.locator('label').filter({hasText:'页地址 / 备份旧值 / 新值'}).nth(i).locator('input');await row.nth(1).fill('22222222');await row.nth(2).fill(['01020304','05060708','090A0B0C'][i]);}
 await page.locator('#workflow-plan').click();await page.waitForFunction(()=>document.querySelector('#workflow-result').textContent.includes('ordinary_user_pages_only')&&!document.querySelector('#read').hasAttribute('aria-busy'));await page.locator('#workflow-confirm').check();await page.locator('#workflow-write').click();await page.waitForFunction(()=>document.querySelector('#workflow-result').textContent.includes('write_outcome_unknown')&&!document.querySelector('#read').hasAttribute('aria-busy'));assert.match(await page.locator('#workflow-result').textContent(),/"verified_pages": 1/);assert.match(await page.locator('#workflow-result').textContent(),/"outcome": "not_started"/);assert.deepEqual(simulator.rawWrites,[4,5]);assert.equal(simulator.memory.subarray(16,20).toString('hex'),'01020304');assert.equal(simulator.memory.subarray(20,24).toString('hex'),'22222222');assert(await page.locator('#workflow-write').isDisabled());await page.reload();await page.evaluate(()=>window.libraryReady);assert.deepEqual(simulator.rawWrites,[4,5]);assert.deepEqual(errors,[]);
});

test('Chromium diagnoses an offline server and queries its task after reload without repeating the check',{skip:!binary,timeout:90000},async t=>{
 let server,browser;const errors=[],submissions=[];t.after(async()=>{if(browser)await browser.close();if(server)await server.close()});server=await serve();browser=await launch();const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(new URL(r.url()).pathname==='/api/tasks'&&r.method()==='POST')submissions.push(r.postDataJSON())});
 await page.goto(server.base);await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="device"]').click();await page.locator('#environment-panel > summary').click();await page.locator('#environment-check').click();await page.waitForFunction(()=>document.querySelector('#environment-json').textContent.includes('worker_missing'));
 assert.match(await page.locator('#environment-status').textContent(),/发现环境缺项/);assert(await page.locator('#environment-results').isVisible());assert.equal(await page.locator('#environment-results').locator('li').count()>11,true);assert.equal(submissions.filter(s=>s.operation_id==='environment.diagnostics').length,1);
 await page.reload();await page.evaluate(()=>window.libraryReady);const response=await page.request.get(server.base+'/api/capabilities');const caps=await response.json();const tasks=await (await page.request.get(server.base+'/api/tasks',{headers:{'X-Chameleon-Token':caps.csrf_token}})).json();const task=tasks.tasks.find(t=>t.operation_id==='environment.diagnostics');assert(task);assert.equal(task.state,'succeeded');assert.equal(task.commands_attempted,0);assert.equal(task.write,false);const result=await (await page.request.get(server.base+'/api/tasks/'+task.id,{headers:{'X-Chameleon-Token':caps.csrf_token}})).json();assert.equal(result.result.schema_version,1);assert.equal(submissions.filter(s=>s.operation_id==='environment.diagnostics').length,1);assert.deepEqual(errors,[]);
});

test('Chromium imports LF samples offline, survives a device-session change and restores the task after reload',{skip:!binary,timeout:120000},async t=>{
 let server,browser;const errors=[],posts=[];t.after(async()=>{if(browser)await browser.close();if(server)await server.close()});server=await serve();browser=await launch();const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(new URL(r.url()).pathname==='/api/tasks'&&r.method()==='POST')posts.push(r.postDataJSON())});
 await page.goto(server.base);await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="read"]').click();await page.locator('#rf-files > summary').click();await page.locator('#rf-format').selectOption('lf-u8');await page.locator('#rf-rate').fill('125000');const input=Buffer.from(Array.from({length:100},(_,i)=>Math.floor(i/4)%2?200:0));await page.locator('#rf-file').setInputFiles({name:'samples.bin',mimeType:'application/octet-stream',buffer:input});await page.waitForFunction(()=>!document.querySelector('#rf-analyze').disabled);
 const accepted=page.waitForRequest(r=>new URL(r.url()).pathname==='/api/tasks'&&r.method()==='POST'&&r.postDataJSON()?.operation_id==='files.rf-analyze');await page.locator('#rf-analyze').click();await accepted;
 const caps=await (await page.request.get(server.base+'/api/capabilities')).json();await page.request.post(server.base+'/api/session/disconnect',{headers:{'X-Chameleon-Token':caps.csrf_token},data:{}});await page.locator('.dock [data-open="device"]').click();await page.locator('#session-refresh').click();await page.locator('.dock [data-open="read"]').click();await page.waitForFunction(()=>!document.querySelector('#rf-report').disabled);
 assert.match(await page.locator('#rf-summary').textContent(),/100/);assert.equal(await page.locator('#rf-table-body tr').count(),50);await page.locator('#rf-next').click();assert.match(await page.locator('#rf-page').textContent(),/2 \/ 2/);assert(await page.locator('#rf-waveform svg').isVisible());
 const originalEvent=page.waitForEvent('download');await page.locator('#rf-original').click();const original=await originalEvent;assert.deepEqual(await fs.readFile(await original.path()),input);
 const csvEvent=page.waitForEvent('download');await page.locator('#rf-csv').click();const csv=await csvEvent;assert.match(await fs.readFile(await csv.path(),'utf8'),/^index,time_us,value\n0,0.000000,0/);
 const tasks=await (await page.request.get(server.base+'/api/tasks',{headers:{'X-Chameleon-Token':caps.csrf_token}})).json();const task=tasks.tasks.find(t=>t.operation_id==='files.rf-analyze');assert.equal(task.session_id,'offline');assert.equal(task.commands_attempted,0);assert.equal(posts.filter(t=>t.operation_id==='files.rf-analyze').length,2);
 await page.reload();await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="read"]').click();await page.locator('#rf-files > summary').click();await page.locator('#rf-task-id').fill(task.id);await page.locator('#rf-query').click();await page.waitForFunction(()=>!document.querySelector('#rf-report').disabled);assert(await page.locator('#rf-original').isDisabled());assert.equal(posts.filter(t=>t.operation_id==='files.rf-analyze').length,2);assert.deepEqual(errors,[]);
});

test('Chromium retains a truncated HF prefix and declared PM3 timing without acquiring hardware',{skip:!binary,timeout:90000},async t=>{
 let server,browser;const requests=[],errors=[];t.after(async()=>{if(browser)await browser.close();if(server)await server.close()});server=await serve();browser=await launch();const page=await browser.newPage();page.on('request',r=>requests.push(new URL(r.url()).pathname));page.on('pageerror',e=>errors.push(e.message));await page.goto(server.base);await page.evaluate(()=>window.libraryReady);await page.locator('.dock [data-open="read"]').click();await page.locator('#rf-files > summary').click();await page.locator('#rf-format').selectOption('hf-packed');await page.locator('#rf-file').setInputFiles({name:'partial.bin',mimeType:'application/octet-stream',buffer:Buffer.from([0,7,0x26,0x80,8,0x04,0])});await page.waitForFunction(()=>!document.querySelector('#rf-analyze').disabled);await page.locator('#rf-analyze').click();await page.waitForFunction(()=>!document.querySelector('#rf-report').disabled);assert.match(await page.locator('#rf-status').textContent(),/前缀/);assert.match(await page.locator('#rf-notes').textContent(),/truncated_header/);assert.equal(await page.locator('#rf-table-body tr').count(),2);await page.locator('#rf-direction').selectOption('card');assert.equal(await page.locator('#rf-table-body tr').count(),1);assert.match(await page.locator('#rf-table-body').textContent(),/未采集/);
 const trace=Buffer.alloc(10);trace.writeUInt32LE(1234,0);trace.writeUInt16LE(64,4);trace.writeUInt16LE(1,6);trace[8]=0x26;trace[9]=7;await page.locator('#rf-format').selectOption('pm3-trace');await page.locator('#rf-file').setInputFiles({name:'gui.trace',mimeType:'application/octet-stream',buffer:trace});await page.waitForFunction(()=>!document.querySelector('#rf-analyze').disabled);await page.locator('#rf-short-markers').check();await page.locator('#rf-analyze').click();await page.waitForFunction(()=>!document.querySelector('#rf-report').disabled);await page.locator('#rf-direction').selectOption('all');assert.match(await page.locator('#rf-table-body').textContent(),/1234 \+ 64 ticks（未验证）/);assert.match(await page.locator('#rf-table-body').textContent(),/7 数据位/);assert.equal(requests.some(p=>p.startsWith('/api/scan/')||p.startsWith('/api/reader/')||p.startsWith('/api/physical/')),false);assert.deepEqual(errors,[]);
});
