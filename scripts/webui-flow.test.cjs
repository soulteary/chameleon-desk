const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM, VirtualConsole } = require('jsdom');
const assets = path.join(__dirname, '../cmd/chameleon/webui');
const source = (file) => fs.readFileSync(path.join(assets, file), 'utf8');
const tick = () => new Promise((resolve) => setImmediate(resolve));
const deferred = () => { let resolve; const promise = new Promise((r) => { resolve = r; }); return { promise, resolve }; };
const response = (body, status = 200, type = 'application/json') => ({ ok: status < 400, status, headers: { get: () => type }, json: async () => body, text: async () => String(body) });

async function page(t, routes = {}, { width = 1368, height = 900, writes = false, clock = false } = {}) {
  const errors = [], requests = [], downloads = [], imports = [];
  const virtualConsole = new VirtualConsole(); virtualConsole.on('jsdomError', (e) => errors.push(e));
  const dom = new JSDOM(source('index.html'), { url: 'http://127.0.0.1:8099/', runScripts: 'outside-only', virtualConsole });
  const w = dom.window, d = w.document, $ = (s) => d.querySelector(s);
  let now = 0, timerID = 0;
  const timers = new Map();
  if (clock) {
    const clear = w.clearTimeout.bind(w);
    w.setTimeout = (fn, delay) => { const id = ++timerID; timers.set(id, { fn, at: now + delay }); return id; };
    w.clearTimeout = (id) => { if (!timers.delete(id)) clear(id); };
  }
  const advance = async (elapsed) => {
    now += elapsed;
    for (const [id, timer] of [...timers]) if (timer.at <= now) { timers.delete(id); timer.fn(); }
    await tick();
  };
  Object.assign(w, { innerWidth: width, innerHeight: height, Blob: global.Blob });
  w.confirm = () => true;
  w.ResizeObserver = class { observe() {} };
  w.requestAnimationFrame = (callback) => queueMicrotask(callback);
  $('#desktop').getBoundingClientRect = () => ({ top: 30, width: w.innerWidth, height: w.innerHeight - 30 });
  $('.dock').getBoundingClientRect = () => ({ top: w.innerHeight - (w.innerWidth <= 600 ? 70 : 100) });
  for (const win of d.querySelectorAll('.app-window')) {
    const dimension = (key, fallback) => win.hidden ? 0 : parseFloat(win.style[key]) || fallback;
    Object.defineProperties(win, { offsetWidth: { get: () => dimension('width', 870) }, offsetHeight: { get: () => dimension('height', Math.min(640, w.innerHeight - 220)) }, offsetLeft: { get: () => dimension('left', parseFloat(win.style.getPropertyValue('--wx'))) }, offsetTop: { get: () => dimension('top', parseFloat(win.style.getPropertyValue('--wy'))) } });
    const bar = win.querySelector('.titlebar'); let capture = false;
    bar.setPointerCapture = () => { capture = true; }; bar.hasPointerCapture = () => capture; bar.releasePointerCapture = () => { capture = false; };
  }
  w.fetch = async (url, opts) => {
    requests.push({ url, ...opts });
    if (routes[url]) return routes[url](opts);
    if (url === '/api/capabilities') return response({ writes_enabled: writes, csrf_token: 'test-session-token' });
    throw new Error('Unexpected offline request: ' + url);
  };
  w.URL.createObjectURL = (blob) => { downloads.push({ blob }); return 'blob:test'; }; w.URL.revokeObjectURL = () => {};
  w.HTMLAnchorElement.prototype.click = function () { downloads.at(-1).name = this.download; };
  const inputClick = w.HTMLInputElement.prototype.click;
  w.HTMLInputElement.prototype.click = function () { if (this.type === 'file') imports.push(this); else inputClick.call(this); };
  for (const file of ['window-geometry.js', 'desktop.js', 'tool-panels.js', 'app.js']) w.eval(source(file));
  t.after(() => { dom.window.close(); assert.deepEqual(errors, [], 'no uncaught page errors'); });
  await tick();
  return { w, d, $, requests, downloads, imports, advance, timers, click: async (selector) => { $(selector).click(); await tick(); }, input: (selector, value) => { $(selector).value = value; $(selector).dispatchEvent(new w.Event('input', { bubbles: true })); } };
}
const open = (id) => `.dock [data-open="${id}"]`;
const summary = ($, id) => $(id).closest('details').previousElementSibling;
const card = { id: 'sample', name: 'Offline sample', uid: '04112233445566', tag: 1100, sak: 0, atqa: [0, 68], ats: [], data: [[0, 0, 0, 0]] };

test('startup stays offline, offers eight tools, and parses dictionary controls correctly', async (t) => {
  const p = await page(t);
  assert.deepEqual(p.requests.map((r) => r.url), ['/api/capabilities']);
  assert.equal(p.$('#load-card-go').disabled, true);
  assert.equal(p.$('#dict-download').textContent.trim(), '获取字典');
  assert.equal(p.$('#dict-out').closest('button'), null);
  for (const id of ['device', 'slots', 'read', 'cards', 'ndef', 'acl', 'vanity', 'dictionary']) { await p.click(open(id)); assert.equal(p.$('#' + id).hidden, false); assert.equal(p.$('#active-app').textContent, p.$('#' + id).dataset.title); }
});

test('window maximize restores its rectangle, keyboard focus raises windows, hide restores focus', async (t) => {
  const p = await page(t); await p.click(open('acl'));
  const win = p.$('#acl'), rect = () => ['left','top','width','height'].map((key) => win.style[key]), original = rect();
  await p.click('#acl [data-window="maximize"]'); assert.equal(win.classList.contains('maximized'), true);
  await p.click('#acl [data-window="maximize"]'); assert.deepEqual(rect(), original);
  await p.click(open('ndef')); p.$('#acl-hex').focus(); assert.equal(win.classList.contains('front'), true);
  await p.click('#acl [data-window="minimize"]'); assert.equal(win.hidden, true);
  assert.equal(p.d.activeElement.closest('.app-window').id, 'ndef');
  await p.click(open('acl')); assert.equal(win.hidden, false);
  await p.click('#desktop-button'); assert.equal(p.d.querySelectorAll('.app-window:not([hidden])').length, 0);
  assert.equal(p.d.activeElement.id, 'applications-button');
  await p.click(open('acl')); assert.equal(win.hidden, false);
  for (let i = 0; i < 500; i++) { p.$('#acl-hex').focus(); p.$('#ndef-text').focus(); }
  assert.ok(Number(p.$('#ndef').style.zIndex) < 9990);
});

for (const width of [375, 768, 1368]) test(`arranging eight windows and resizing, including maximized windows, stays bounded at ${width}px`, async (t) => {
  const p = await page(t, {}, { width });
  for (const id of ['slots', 'read', 'cards', 'ndef', 'acl', 'vanity', 'dictionary']) await p.click(open(id));
  await p.click('#arrange-windows'); await p.click('#dictionary [data-window="maximize"]');
  p.w.innerWidth = 320; p.w.innerHeight = 480; p.w.dispatchEvent(new p.w.Event('resize'));
  for (const win of p.d.querySelectorAll('.app-window')) {
    assert.ok(win.offsetLeft >= 0 && win.offsetTop >= 0);
    assert.ok(win.offsetLeft + win.offsetWidth <= 320);
    assert.ok(win.offsetTop + win.offsetHeight <= 380, win.id);
  }
});

test('pointer capture loss cleans up drag listeners and prevents ghost movement', async (t) => {
  const p = await page(t), bar = p.$('#device .titlebar'), win = p.$('#device');
  const pointer = (type, x, y) => bar.dispatchEvent(new p.w.MouseEvent(type, { bubbles: true, button: 0, clientX: x, clientY: y }));
  pointer('pointerdown', 200, 100); pointer('pointermove', -1000, -1000);
  assert.equal(win.offsetLeft, 12); assert.equal(win.offsetTop, 12);
  pointer('lostpointercapture', 0, 0); const current = win.style.cssText;
  pointer('pointermove', 800, 600); assert.equal(win.style.cssText, current);
});

test('local JSON validation errors are visible and do not reuse a stale success', async (t) => {
  const p = await page(t, { '/api/acl/encode': () => response({ access_hex: 'FF0780' }) });
  await p.click('#acl-encode-go'); assert.match(summary(p.$, '#acl-out').textContent, /FF0780/);
  p.input('#acl-cond', '0,,0,4'); await p.click('#acl-encode-go');
  assert.match(summary(p.$, '#acl-out').textContent, /四个/); assert.equal(summary(p.$, '#acl-out').hidden, false);
  assert.equal(p.$('#acl-out').dataset.ready, 'false');
  for (const [field, button, output] of [['#ndef-input', '#ndef-write-go', '#ndef-out'], ['#db-json', '#db-list', '#db-out'], ['#db-card', '#db-add', '#db-out'], ['#vanity-uid', '#vanity-go', '#vanity-out']]) {
    if (field === '#db-card') p.input('#db-json', '{}');
    p.input(field, 'invalid'); await p.click(button);
    assert.match(summary(p.$, output).textContent, /操作未完成/); assert.equal(summary(p.$, output).hidden, false);
  }
  assert.equal(p.requests.filter((r) => r.url !== '/api/capabilities').length, 1);
});

test('pending tool requests disable competing actions and recover after HTTP failure', async (t) => {
  const pending = deferred(); let count = 0;
  const p = await page(t, { '/api/acl/decode': () => { count++; return pending.promise; } });
  await p.click('#acl-decode-go'); assert.equal(p.$('#acl-encode-go').disabled, true);
  p.$('#acl-decode-go').dispatchEvent(new p.w.Event('click')); assert.equal(count, 1);
  pending.resolve(response({ error: 'bad access bits' }, 422)); await tick();
  assert.match(summary(p.$, '#acl-out').textContent, /bad access bits/); assert.equal(p.$('#acl-encode-go').disabled, false);
  assert.equal(p.$('#acl').hasAttribute('aria-busy'), false);
});

test('hardware reads serialize across windows and handle no-card and disconnected paths', async (t) => {
  const pending = deferred();
  const p = await page(t, { '/api/scan/hf': () => pending.promise, '/api/scan/lf': () => response({ present: true, card: { uid: '1122334455' } }), '/api/info': () => { throw new Error('device unplugged'); } });
  await p.click('#scan-hf'); await p.click('#refresh-info'); assert.equal(p.requests.filter((r) => r.url === '/api/info').length, 0);
  pending.resolve(response({ present: false })); await tick(); assert.match(p.$('#scan-out').textContent, /未检测到/);
  await p.click('#scan-lf'); assert.match(p.$('#scan-out').textContent, /1122334455/);
  await p.click('#refresh-info'); assert.match(p.$('#info-cards').textContent, /device unplugged/);
  assert.equal(p.$('#menu-connection').classList.contains('connected'), false);
  assert.equal(p.$('#scan-hf').disabled, false);
});

test('capability failure is visible and retry enables only authorized writes', async (t) => {
  let attempts = 0;
  const p = await page(t, { '/api/capabilities': () => ++attempts === 1 ? response({ error: 'service unavailable' }, 503) : response({ writes_enabled: true, csrf_token: 'new-token' }) });
  assert.equal(p.$('#retry-capabilities').hidden, false); assert.equal(p.$('#load-card-go').disabled, true);
  await p.click('#retry-capabilities'); assert.equal(p.$('#load-card-go').disabled, false); assert.equal(p.$('#retry-capabilities').hidden, true);
});

test('slot validation, cancellation, token, activation refresh, and read-only gating', async (t) => {
  const info = { model: 'Mock', slots: [{ number: 1, active: true }, { number: 2, active: false }] };
  const p = await page(t, { '/api/info': () => response(info), '/api/slot/load-card': () => response({ plan: { slot: 2, steps: [] }, verified: true, persisted: true, activated: false, completed_steps: 0 }), '/api/slot/activate': () => response({ ok: true }) }, { writes: true });
  p.input('#load-card-json', '{'); await p.click('#load-card-go'); assert.match(summary(p.$, '#load-card-out').textContent, /有效 JSON/);
  p.input('#load-card-json', JSON.stringify(card));
  for (const value of ['', '2.5', '0', '9']) { p.input('#load-slot', value); await p.click('#load-card-go'); assert.match(summary(p.$, '#load-card-out').textContent, /1 至 8/); }
  p.input('#load-slot', '2'); p.w.confirm = () => false; await p.click('#load-card-go'); assert.equal(p.requests.filter((r) => r.url.startsWith('/api/slot/')).length, 0);
  p.w.confirm = () => true; await p.click('#load-card-go');
  const request = p.requests.find((r) => r.url === '/api/slot/load-card'); assert.equal(request.headers['X-Chameleon-Token'], 'test-session-token'); assert.equal(JSON.parse(request.body).slot, 2);
  const buttons = p.d.querySelectorAll('[data-slot-active]'); assert.equal(buttons[0].disabled, true); assert.equal(buttons[1].disabled, false); buttons[1].click(); await tick();
  assert.equal(p.requests.filter((r) => r.url === '/api/slot/activate').length, 1);
});

test('NDEF text and URI records share a generated card that can be reused and exported', async (t) => {
  const p = await page(t, { '/api/ndef/write': () => response(card), '/api/ndef/read': () => response({ records: [{ kind: 'text', value: 'hello' }] }) });
  p.input('#ndef-input', JSON.stringify(card)); p.input('#ndef-text', 'hello'); p.input('#ndef-uri', 'https://example.com');
  p.input('#ndef-language', 'zh');
  await p.click('#ndef-write-go'); assert.equal(p.$('#ndef-use-result').hidden, false);
  const body = JSON.parse(p.requests.find(r => r.url === '/api/ndef/write').body);
  assert.equal(body.text, 'hello'); assert.equal(body.uri, 'https://example.com'); assert.equal(body.language, 'zh');
  await p.click('[data-export="ndef-out"]'); assert.equal(JSON.parse(await p.downloads[0].blob.text()).id, card.id);
  await p.click('#ndef-use-result'); await p.click('#ndef-go'); assert.match(summary(p.$, '#ndef-out').textContent, /hello/);
  p.input('#ndef-input', '{'); await p.click('#ndef-write-go'); await p.click('[data-export="ndef-out"]'); assert.equal(p.downloads.length, 1);
});

test('database add/list/select/show/delete preserves the updated database and exports card separately', async (t) => {
  const database = { 'flutter.cards': [JSON.stringify(card)], unknown: 'preserved' };
  const p = await page(t, { '/api/database': (opts) => {
    const body = JSON.parse(opts.body);
    if (body.op === 'add') return response(database);
    assert.equal(body.database.unknown, 'preserved');
    if (body.op === 'list') return response([{ id: card.id, name: card.name }]);
    if (body.op === 'show') return response(card);
    return response({ database: { ...database, 'flutter.cards': [] }, removed: 1 });
  } });
  p.input('#db-card', JSON.stringify(card)); await p.click('#db-add'); assert.equal(JSON.parse(p.$('#db-json').value).unknown, 'preserved');
  await p.click('#db-list'); await p.click('#library-results button'); assert.equal(p.$('#db-id').value, card.id);
  await p.click('#db-show'); assert.equal(p.d.querySelectorAll('#library-results button').length, 1);
  await p.click('[data-export-card]'); assert.equal(JSON.parse(await p.downloads[0].blob.text()).id, card.id);
  await p.click('[data-export="db-out"]:not([data-export-card])'); assert.equal(JSON.parse(await p.downloads[1].blob.text()).unknown, 'preserved');
  await p.click('#db-delete'); assert.equal(JSON.parse(p.$('#db-json').value)['flutter.cards'].length, 0); assert.equal(p.$('#db-id').value, '');
  p.input('#db-json', '{}'); assert.equal(p.$('#db-out').dataset.ready, 'false'); assert.equal(p.$('#library-results').children.length, 0);
});

test('dictionary sources are selectable offline, downloaded keys export, and online failure is visible', async (t) => {
  let fail = false;
  const p = await page(t, { '/api/dictionary/sources': () => response([{ name: 'sample-source' }]), '/api/dictionary/download': () => fail ? response({ error: 'network unavailable' }, 502) : response({ id: 'dict', name: 'sample', keys: [[1, 2, 3, 4]], keyLength: 8 }) });
  await p.click('#dict-go'); await p.click('#dictionary-sources button'); assert.equal(p.$('#dict-name').value, 'sample-source');
  await p.click('#dict-download'); assert.match(summary(p.$, '#dict-out').textContent, /1 个密钥/);
  await p.click('[data-export="dict-out"]'); assert.equal(JSON.parse(await p.downloads[0].blob.text()).keys.length, 1);
  fail = true; await p.click('#dict-download'); assert.match(summary(p.$, '#dict-out').textContent, /network unavailable/); assert.equal(p.$('#dict-download').disabled, false);
});

test('import dispatches input invalidation and rejects oversized, primitive, or stale asynchronous imports', async (t) => {
  const p = await page(t);
  const importFile = async (value, size = 20) => {
    await p.click('[data-import="db-json"]'); const input = p.imports.at(-1);
    Object.defineProperty(input, 'files', { value: [{ name: 'test.json', size, text: async () => value }] }); input.dispatchEvent(new p.w.Event('change')); await tick();
  };
  await importFile('{"cards":[]}'); assert.equal(p.$('#db-json').value, '{"cards":[]}'); assert.equal(p.$('#db-json').closest('details').open, true);
  await importFile('null'); assert.match(p.$('#toast').textContent, /JSON 对象/);
  await importFile('{}', 33 * 1024 * 1024); assert.match(p.$('#toast').textContent, /大小限制/);
  const pending = deferred(); await p.click('[data-import="db-json"]'); const input = p.imports.at(-1);
  Object.defineProperty(input, 'files', { value: [{ name: 'slow.json', size: 20, text: () => pending.promise }] }); input.dispatchEvent(new p.w.Event('change'));
  p.input('#db-json', '{"edited":true}'); pending.resolve('{}'); await tick();
  assert.equal(p.$('#db-json').value, '{"edited":true}'); assert.match(p.$('#toast').textContent, /输入已变化/);
});

test('arranging respects existing z-order so a front window cannot cover every other title', async (t) => {
  const p = await page(t);
  for (const id of ['slots', 'read', 'cards', 'ndef', 'acl']) await p.click(open(id));
  await p.click(open('device')); await p.click('#arrange-windows');
  const wins = [...p.d.querySelectorAll('.app-window:not([hidden])')].sort((a,b) => Number(a.style.zIndex) - Number(b.style.zIndex));
  assert.ok(wins.at(-1).offsetTop > wins[0].offsetTop);
  assert.equal(wins.at(-1).id, 'device');
});

test('expired write session disables mutations and allows an explicit permissions refresh without retrying a write', async (t) => {
  const p = await page(t, { '/api/slot/load-card': () => response({ error: 'invalid token' }, 403) }, { writes: true });
  p.input('#load-card-json', JSON.stringify(card)); await p.click('#load-card-go');
  assert.equal(p.$('#load-card-go').disabled, true); assert.equal(p.$('#retry-capabilities').hidden, false);
  assert.match(summary(p.$, '#load-card-out').textContent, /invalid token/);
  await p.click('#retry-capabilities'); assert.equal(p.$('#load-card-go').disabled, false);
  assert.equal(p.requests.filter((r) => r.url === '/api/slot/load-card').length, 1);
});

test('arranging leaves distinct title rows instead of clamping the traffic controls under neighboring windows', async (t) => {
  const p = await page(t, {}, { width: 1368, height: 682 });
  for (const id of ['slots', 'read', 'cards', 'ndef', 'acl', 'vanity', 'dictionary']) await p.click(open(id));
  await p.click('#arrange-windows');
  const wins = [...p.d.querySelectorAll('.app-window')].sort((a,b) => a.offsetTop - b.offsetTop);
  for (let i=1; i<wins.length; i++) assert.ok(wins[i].offsetTop - wins[i-1].offsetTop >= 24);
});

test('device overview, HF result and firmware summaries use successful API responses', async (t) => {
  const p = await page(t, { '/api/info': () => response({ model:'Mock Ultra', firmware:{major:2,minor:2}, git_version:'v2.2-test', mode:'emulator', battery:{percent:80,millivolts:3900}, slots:[], capabilities:[] }), '/api/scan/hf': () => response({ present:true,cards:[{uid:'04112233',sak:8}] }), '/api/firmware/check': () => response({ up_to_date:true,latest:{tag:'v2.2'},notes:['checked'] }) });
  await p.click('#refresh-info'); assert.match(p.$('#info-cards').textContent, /Mock Ultra/); assert.match(p.$('#info-cards').textContent, /3900/);
  await p.click('#scan-hf'); assert.match(p.$('#scan-out').textContent, /04112233/); assert.equal(p.$('#menu-connection').classList.contains('connected'),true);
  await p.click('#fw-check'); assert.match(summary(p.$,'#fw-out').textContent,/最新构建/);
});

test('ACL includes trailer permissions and UID inference distinguishes valid defaults', async (t) => {
  const p = await page(t, { '/api/acl/decode': () => response({ conditions:[0,0,0,4],blocks:[{block:0,read:'A/B',write:'A/B'}],trailer:{key_a_write:'A',access_read:'A',access_write:'A',key_b_read:'A',key_b_write:'A'} }), '/api/vanity': (opts) => response({ vanity_sak:JSON.parse(opts.body).uid.length === 14 }) });
  await p.click('#acl-decode-go'); assert.match(summary(p.$,'#acl-out').textContent,/扇区尾块/);
  p.input('#vanity-uid','04112233'); await p.click('#vanity-go'); assert.match(summary(p.$,'#vanity-out').textContent,/默认不启用/);
  p.input('#vanity-uid','04 11 22 33 44 55 66'); await p.click('#vanity-go'); assert.match(summary(p.$,'#vanity-out').textContent,/默认启用/);
});

test('database failures preserve input and result errors remain visible for non-JSON server replies', async (t) => {
  const p = await page(t, { '/api/database': () => response({error:'record not found'},404), '/api/dictionary/sources': () => response('<html>proxy</html>',200,'text/html') });
  p.input('#db-json','{"custom":"keep"}'); p.input('#db-id','missing'); await p.click('#db-show');
  assert.equal(p.$('#db-json').value,'{"custom":"keep"}'); assert.match(summary(p.$,'#db-out').textContent,/record not found/);
  await p.click('#dict-go'); assert.match(summary(p.$,'#dict-out').textContent,/非 JSON/);
});

test('viewport shrink then expand restores the prior desktop rectangle, including a minimized window', async (t) => {
  const p = await page(t); await p.click(open('acl'));
  const win = p.$('#acl'); const rect = () => ['left','top','width','height'].map((key) => win.style[key]); const original = rect();
  p.w.innerWidth=375; p.w.innerHeight=600; p.w.dispatchEvent(new p.w.Event('resize'));
  assert.ok(win.offsetWidth <= 375);
  await p.click('#acl [data-window="minimize"]');
  p.w.innerWidth=1368; p.w.innerHeight=900; p.w.dispatchEvent(new p.w.Event('resize'));
  await p.click(open('acl')); assert.deepEqual(rect(),original);
});

test('a window minimized before shrinking also recovers its desktop rectangle after opening on mobile', async (t) => {
  const p = await page(t); await p.click(open('cards'));
  const win = p.$('#cards'); const rect = () => ['left','top','width','height'].map((key) => win.style[key]); const original = rect();
  await p.click('#cards [data-window="minimize"]');
  p.w.innerWidth=375; p.w.innerHeight=600; p.w.dispatchEvent(new p.w.Event('resize'));
  await p.click(open('cards')); assert.ok(win.offsetWidth <= 375);
  p.w.innerWidth=1368; p.w.innerHeight=900; p.w.dispatchEvent(new p.w.Event('resize'));
  assert.deepEqual(rect(),original);
});

test('HF scan uses structured fields, preserves zero values and inference, and keeps JSON collapsed', async (t) => {
  const p=await page(t,{'/api/scan/hf':()=>response({present:true,cards:[{uid:'04112233445566',atqa:68,atqa_wire:'4400',sak:0,type:'Ultralight / NTAG-compatible (exact model unknown)',type_basis:'SAK-based inference; exact chip model not verified'}]})});
  let copied=''; Object.defineProperty(p.w.navigator,'clipboard',{value:{writeText:async(value)=>{copied=value;}}});
  await p.click('#scan-hf');
  assert.equal(p.d.querySelectorAll('#scan-out .scan-card').length,1);
  assert.match(p.$('#scan-out .inspector-fields').textContent,/0x0044/); assert.match(p.$('#scan-out .inspector-fields').textContent,/0x00/);
  assert.match(p.$('#scan-out .inspector-fields').textContent,/7 字节/); assert.match(p.$('#scan-out .inference-note').textContent,/类型依据/);
  assert.equal(p.$('#scan-out details').open,false); assert.equal(p.$('#scan-out pre').closest('details').classList.contains('hardware-raw'),true);
  await p.click('#scan-out .copy-identifier'); assert.equal(copied,'04112233445566');
});

test('LF scan uses the actual API id and tag_type fields and copy failures remain recoverable',async(t)=>{
  const p=await page(t,{'/api/scan/lf':()=>response({present:true,card:{protocol:'em410x',tag_type:100,id:'1122334455'}})});
  await p.click('#scan-lf'); assert.equal(p.$('#scan-out .identifier-value').textContent,'1122334455');
  assert.match(p.$('#scan-out .inspector-fields').textContent,/EM410X/); assert.match(p.$('#scan-out .inspector-fields').textContent,/100/); assert.match(p.$('#scan-out .inspector-fields').textContent,/5 字节/);
  await p.click('#scan-out .copy-identifier'); assert.match(p.$('#toast').textContent,/手动复制/);
  assert.equal(p.$('#scan-hf').disabled,false);
});

test('device busy error offers a working read retry, clears stale data, and recovers connection state',async(t)=>{
  let attempt=0;
  const p=await page(t,{'/api/scan/hf':()=>++attempt===1?response({error:'serial port resource busy'},502):response({present:true,cards:[{uid:'04112233',sak:0,atqa:0}]})});
  await p.click('#scan-hf'); assert.match(p.$('#scan-out [role="alert"]').textContent,/其他程序占用/);
  assert.equal(p.$('#menu-connection').classList.contains('connected'),false);
  await p.click('#scan-out .hardware-state button'); assert.equal(p.d.querySelectorAll('#scan-out [role="alert"]').length,0);
  assert.equal(p.$('#menu-connection').classList.contains('connected'),true); assert.equal(attempt,2);
  assert.equal(p.requests.some((r)=>r.method==='POST'),false);
});

test('device metrics, model title and zero battery are consistent across failure and retry',async(t)=>{
  let fail=false;
  const p=await page(t,{'/api/info':()=>fail?response({error:'device unplugged'},502):response({model:'Chameleon Lite',firmware:{major:2,minor:2},mode:'emulator',battery:{percent:0,millivolts:3200},capabilities:[],slots:[{number:1,active:true}]})});
  await p.click('#refresh-info'); assert.equal(p.$('#device .device-hero h1').textContent,'Chameleon Lite');
  assert.equal(p.$('#info-cards meter').value,0); assert.match(p.$('#info-cards').textContent,/0%/); assert.match(p.$('#info-cards').textContent,/3200 mV/);
  assert.equal(p.$('#info-raw details').open,false); assert.equal(p.$('#device').classList.contains('has-device-data'),true);
  fail=true; await p.click('#refresh-info'); assert.equal(p.d.querySelectorAll('#info-cards .metric-tile').length,0); assert.equal(p.$('#device').classList.contains('has-device-data'),false);
  fail=false; await p.click('#info-cards .hardware-state button'); assert.equal(p.d.querySelectorAll('#info-cards .metric-tile').length,6);
});

test('read-only slot inspector renders channel states, active slot and no mutation controls',async(t)=>{
  const p=await page(t,{'/api/info':()=>response({model:'Mock',slots:[{number:1,active:true,hf_enabled:true,hf_name:'MIFARE Classic 1K',lf_enabled:false},{number:2,active:false,hf_enabled:false,lf_enabled:true,lf_name:'EM410X'}]})});
  await p.click('#refresh-slots'); assert.equal(p.d.querySelectorAll('#slot-list .slot-card').length,2);
  assert.match(p.$('#slot-list').textContent,/只读查看/); assert.match(p.$('#slot-list .slot-card.active').textContent,/当前/);
  assert.equal(p.d.querySelectorAll('#slot-list .slot-channel.enabled').length,2); assert.equal(p.d.querySelectorAll('#slot-list [data-slot-active]').length,0);
  assert.equal(p.$('#load-card-go').disabled,true); assert.equal(p.$('#slot-list details').open,false);
});

test('no-card response is a recoverable empty state; malformed responses are errors',async(t)=>{
  let malformed=false;
  const p=await page(t,{'/api/scan/lf':()=>response(malformed?{present:true}:{present:false})});
  await p.click('#scan-lf'); assert.equal(p.$('#scan-out .hardware-state').classList.contains('state-empty'),true);
  assert.equal(p.$('#menu-connection').classList.contains('connected'),true);
  malformed=true; await p.click('#scan-out .hardware-state button'); assert.match(p.$('#scan-out [role="alert"]').textContent,/不完整/);
  assert.equal(p.$('#menu-connection').classList.contains('connected'),false);
});

test('slot snapshot export contains the read configuration and is invalidated by a failed refresh',async(t)=>{
 let fail=false;
 const slots=[{number:1,active:true,hf_enabled:true,hf_name:'Classic',lf_enabled:false}];
 const p=await page(t,{'/api/info':()=>fail?response({error:'port busy'},502):response({model:'Mock',slots})});
 assert.equal(p.$('.slot-write-options').open,false);
 await p.click('[data-export="slot-list"]'); assert.equal(p.downloads.length,0);
 await p.click('#refresh-slots'); await p.click('[data-export="slot-list"]'); assert.deepEqual(JSON.parse(await p.downloads[0].blob.text()),{slots});
 fail=true; await p.click('#refresh-slots'); await p.click('[data-export="slot-list"]'); assert.equal(p.downloads.length,1);
});

test('ACL uses accessible permission matrices and can decode an encoded result',async(t)=>{
 const p=await page(t,{'/api/acl/encode':()=>response({access_hex:'FF0780'}),'/api/acl/decode':()=>response({conditions:[0,0,0,4],blocks:[{block:0,read:'A/B',write:'-',increment:'-',decrement:'A/B'}],trailer:{key_a_read:'-',key_a_write:'A',access_read:'A',access_write:'A',key_b_read:'A',key_b_write:'A'}})});
 await p.click('#acl-encode-go');await p.click('#acl .tool-panel>button');
 assert.equal(p.d.querySelectorAll('#acl .permission-table').length,2);assert.match(p.$('#acl .permission-table').textContent,/加值/);assert.match(p.$('#acl .permission-table').textContent,/减值/);assert.match(p.$('#acl .permission-table').textContent,/禁止/);
 assert.equal(p.$('#acl-out').closest('details').open,false);
});

test('NDEF renders record values as text, preserves empty records and never creates unsafe links',async(t)=>{
 const p=await page(t,{'/api/ndef/read':()=>response({mapping:'type2',capacity:144,records:[{kind:'text',type:'T',language:'zh',value:'<img src=x onerror=alert(1)>'},{kind:'uri',type:'U',value:'javascript:alert(1)'},{kind:'text',type:'T',value:''}]})});
 p.input('#ndef-input',JSON.stringify(card));await p.click('#ndef-go');
 assert.equal(p.d.querySelectorAll('#ndef .ndef-record').length,3);assert.match(summary(p.$,'#ndef-out').textContent,/144 字节/);assert.match(summary(p.$,'#ndef-out').textContent,/空内容/);
 assert.equal(p.d.querySelectorAll('#ndef .tool-panel img, #ndef .tool-panel a').length,0);assert.match(p.$('#ndef .record-value').textContent,/<img/);
});

test('changing inputs invalidates result exports for ACL, dictionary, UID and selected library cards',async(t)=>{
 const p=await page(t,{'/api/acl/encode':()=>response({access_hex:'FF0780'}),'/api/vanity':()=>response({uid:'04112233',vanity_sak:false}),'/api/dictionary/download':()=>response({id:'d',name:'test',keys:[[1,2,3,4]],keyLength:8}),'/api/database':()=>response(card)});
 await p.click('#acl-encode-go');p.input('#acl-cond','1,1,1,1');await p.click('[data-export="acl-out"]');
 p.input('#vanity-uid','04112233');await p.click('#vanity-go');p.input('#vanity-uid','04112233445566');await p.click('[data-export="vanity-out"]');
 await p.click('#dict-download');p.input('#dict-name','other');await p.click('[data-export="dict-out"]');
 p.input('#db-id',card.id);await p.click('#db-show');p.input('#db-id','different');await p.click('[data-export-card]');
 assert.equal(p.downloads.length,0);
});

test('read-only result retries stay in their own task group and can recover after failure',async(t)=>{
 let attempts=0;const pending=deferred();
 const p=await page(t,{'/api/acl/decode':()=>++attempts===1?response({error:'offline service unavailable'},503):pending.promise});
 await p.click('#acl-decode-go');await p.click('#acl .hardware-state button');
 assert.equal(p.$('#acl-encode-go').disabled,true);assert.equal(p.$('#scan-hf').disabled,false);
 pending.resolve(response({conditions:[0,0,0,4],blocks:[]}));await tick();
 assert.match(summary(p.$,'#acl-out').textContent,/访问权限已解码/);assert.equal(p.$('#acl-encode-go').disabled,false);assert.equal(attempts,2);
});

test('database mutations do not offer a one-click retry after an uncertain failure',async(t)=>{
 const p=await page(t,{'/api/database':()=>response({error:'connection lost'},502)});
 p.input('#db-card',JSON.stringify(card));const original=p.$('#db-json').value;await p.click('#db-add');
 assert.equal(p.$('#db-json').value,original);assert.equal(summary(p.$,'#db-out').querySelector('button'),null);
});

test('a changed NDEF source cannot publish an in-flight generated card or enable its reuse',async(t)=>{
 const pending=deferred();const p=await page(t,{'/api/ndef/write':()=>pending.promise});
 p.input('#ndef-input',JSON.stringify(card));p.input('#ndef-text','first');await p.click('#ndef-write-go');
 p.input('#ndef-text','newer');pending.resolve(response(card));await tick();
 assert.equal(p.$('#ndef-out').dataset.ready,'false');assert.equal(p.$('#ndef-use-result').hidden,true);await p.click('[data-export="ndef-out"]');assert.equal(p.downloads.length,0);
});

test('card inspectors can hand a copy to NDEF without touching hardware or changing database metadata',async(t)=>{
 const p=await page(t,{'/api/database':()=>response({...card,custom:'preserve'})});
 p.input('#db-id',card.id);await p.click('#db-show');await p.click('#cards .tool-panel>button');
 assert.equal(p.$('#ndef').hidden,false);assert.equal(JSON.parse(p.$('#ndef-input').value).custom,'preserve');assert.match(p.$('#ndef-source').textContent,/Offline sample/);
 assert.equal(p.requests.some((r)=>r.url==='/api/info'||r.url.startsWith('/api/slot/')),false);
});

test('dictionary key preview stays bounded while export contains every key',async(t)=>{
 const keys=Array.from({length:1000},(_,i)=>[0,0,(i>>8)&255,i&255]);
 const p=await page(t,{'/api/dictionary/download':()=>response({id:'many',name:'many keys',keyLength:8,keys})});
 await p.click('#dict-download');assert.equal(p.d.querySelectorAll('.key-chip').length,24);assert.match(summary(p.$,'#dict-out').textContent,/1000 个密钥/);
 await p.click('[data-export="dict-out"]');assert.equal(JSON.parse(await p.downloads[0].blob.text()).keys.length,1000);
});

test('library list pagination is bounded and a failed search clears stale rows and selected data',async(t)=>{
 let fail=false;const rows=Array.from({length:250},(_,i)=>({id:'c'+i,name:'Card '+i,frequency:'hf'}));
 const p=await page(t,{'/api/database':()=>fail?response({error:'invalid database'},422):response(rows)});
 await p.click('#db-list');assert.equal(p.d.querySelectorAll('[data-card-id]').length,100);await p.click('.library-more');assert.equal(p.d.querySelectorAll('[data-card-id]').length,200);
 fail=true;await p.click('#db-list');assert.equal(p.$('#library-results').children.length,0);assert.equal(p.$('#db-id').value,'');
});

test('minimize and close restore useful focus rather than traffic controls or closed details',async(t)=>{
 const p=await page(t);await p.click(open('acl'));p.$('#acl-hex').focus();
 p.$('#acl [data-window="minimize"]').focus();await p.click('#acl [data-window="minimize"]');await p.click(open('acl'));assert.equal(p.d.activeElement.id,'acl-hex');
 await p.click(open('ndef'));const details=p.$('#ndef-input').closest('details');details.open=true;p.$('#ndef-input').focus();details.open=false;
 await p.click('#ndef [data-window="close"]');await p.click(open('ndef'));
 assert.equal(p.d.activeElement.closest('.traffic'),null);assert.notEqual(p.d.activeElement.id,'ndef-input');
});

test('pending offline results survive minimizing and restore without leaving controls disabled',async(t)=>{
 const pending=deferred();const p=await page(t,{'/api/vanity':()=>pending.promise});
 await p.click(open('vanity'));p.input('#vanity-uid','04112233');await p.click('#vanity-go');await p.click('#vanity [data-window="minimize"]');
 pending.resolve(response({uid:'04112233',vanity_sak:false}));await tick();await p.click(open('vanity'));
 assert.match(summary(p.$,'#vanity-out').textContent,/默认不启用/);assert.equal(p.$('#vanity-go').disabled,false);assert.equal(p.$('#vanity').hasAttribute('aria-busy'),false);
});

test('database export rejects arrays even when they are syntactically valid JSON',async(t)=>{
 const p=await page(t);p.input('#db-json','[]');await p.click('[data-export="db-out"]:not([data-export-card])');assert.equal(p.downloads.length,0);
});

test('empty NDEF messages and omitted empty text values remain readable across old and current servers',async(t)=>{
 let empty=true;const p=await page(t,{'/api/ndef/read':()=>response(empty?{mapping:'type2',capacity:144,records:null}:{mapping:'type2',capacity:144,records:[{tnf:1,type:'T',kind:'text',language:'en',payload:'02656E'}]})});
 p.input('#ndef-input',JSON.stringify(card));await p.click('#ndef-go');assert.match(summary(p.$,'#ndef-out').textContent,/没有 NDEF 记录/);
 empty=false;await p.click('#ndef-go');assert.equal(p.$('#ndef .record-value').textContent,'（空内容）');
});

test('imported NDEF files show a source summary without forcing the JSON editor open',async(t)=>{
 const p=await page(t);await p.click('[data-import="ndef-input"]');const input=p.imports.at(-1);
 Object.defineProperty(input,'files',{value:[{name:'card.json',size:100,text:async()=>JSON.stringify(card)}]});input.dispatchEvent(new p.w.Event('change'));await tick();
 assert.equal(p.$('#ndef-input').closest('details').open,false);assert.match(p.$('#ndef-source').textContent,/Offline sample/);
});

test('result reveal scrolls only the active visible window and never a minimized task',async(t)=>{
 const pending=deferred();let requests=0;
 const p=await page(t,{'/api/acl/encode':()=>++requests===1?response({access_hex:'FF0780'}):pending.promise});
 let reveals=0;summary(p.$,'#acl-out').scrollIntoView=()=>{reveals++;};await p.click(open('acl'));await p.click('#acl-encode-go');assert.equal(reveals,1);
 await p.click('#acl-encode-go');await p.click('#acl [data-window="minimize"]');pending.resolve(response({access_hex:'FF0780'}));await tick();assert.equal(reveals,1);
});

test('list operations reveal selectable rows instead of scrolling past them to the status panel',async(t)=>{
 const p=await page(t,{'/api/database':()=>response([{id:'one',name:'One'}]),'/api/dictionary/sources':()=>response([{name:'source'}])});
 let cards=0,sources=0,status=0;p.$('#library-results').scrollIntoView=()=>{cards++;};p.$('#dictionary-source-options').scrollIntoView=()=>{sources++;};
 summary(p.$,'#db-out').scrollIntoView=()=>{status++;};await p.click(open('cards'));await p.click('#db-list');assert.equal(cards,1);assert.equal(status,0);
 await p.click(open('dictionary'));await p.click('#dict-go');assert.equal(sources,1);assert.equal(p.$('#dictionary-source-options').open,true);
});

test('result navigation returns only its own window to the input area',async(t)=>{
 const p=await page(t,{'/api/acl/encode':()=>response({access_hex:'FF0780'})});await p.click('#acl-encode-go');
 p.$('#acl .window-content').scrollTop=300;p.$('#cards .window-content').scrollTop=100;await p.click('#acl .result-actions button');
 assert.equal(p.$('#acl .window-content').scrollTop,0);assert.equal(p.$('#cards .window-content').scrollTop,100);
});

test('missing USB device uses a connection hint, keeps diagnostics collapsed and recovers on retry',async(t)=>{
 let missing=true;const message='open /dev/cu.usbmodem-test: no such file or directory';
 const p=await page(t,{'/api/scan/hf':()=>missing?response({error:message},502):response({present:true,cards:[{uid:'04112233',atqa:0,sak:0}]})});
 await p.click('#scan-hf');assert.match(p.$('#scan-out .hardware-state p').textContent,/检查 USB 连接/);assert.equal(p.$('#scan-out details').open,false);assert.match(p.$('#scan-out details pre').textContent,/usbmodem-test/);
 missing=false;await p.click('#scan-out .hardware-state button');assert.equal(p.d.querySelectorAll('#scan-out .scan-card').length,1);assert.equal(p.$('#menu-connection').classList.contains('connected'),true);
});


test('desktop branding remains consistent when every window is hidden', async (t) => {
  const p = await page(t);
  assert.equal(p.d.title, 'Chameleon Desk');
  assert.equal(p.$('#about-dialog h2').textContent, 'Chameleon Desk');
  assert.equal(p.$('#about-button').getAttribute('aria-label'), '关于 Chameleon Desk');
  for (const caption of p.d.querySelectorAll('.window-caption')) assert.equal(caption.textContent, 'Chameleon Desk');
  for (const sidebar of p.d.querySelectorAll('.sidebar-bottom')) assert.match(sidebar.textContent, /^CHAMELEON DESK/);
  assert.doesNotMatch(p.d.body.textContent, /OS-X-Leopard|Atomkit|Apple-Arc-OSX|Aqua|proxmark3|flipper-unleashed|flutter\.cards/i);
  await p.click('#device [data-window="close"]');
  assert.equal(p.$('#active-app').textContent, 'Chameleon Desk');
  await p.click(open('ndef')); await p.click('#desktop-button');
  assert.equal(p.$('#active-app').textContent, 'Chameleon Desk');
});

test('dictionary labels preserve source request IDs and export complete key data', async (t) => {
  const sources = [
    { name: 'proxmark3-t55xx', url: 'https://raw.githubusercontent.com/example/t55xx.dic' },
    { name: 'flipper-unleashed-classic', url: 'https://raw.githubusercontent.com/example/classic.dic' }
  ];
  const keys = [[1, 2, 3, 4, 5, 6], [6, 5, 4, 3, 2, 1]];
  const p = await page(t, {
    '/api/dictionary/sources': () => response(sources),
    '/api/dictionary/download': () => response({ id: 'dictionary-id', name: 'flipper-unleashed-classic', keys, keyLength: 12 })
  });
  assert.equal(p.$('#dict-name').selectedOptions[0].textContent, 'T55xx 默认密码');
  await p.click('#dict-go');
  assert.equal(p.$('#dict-name').value, 'proxmark3-t55xx');
  await p.click('#dictionary-sources button:last-child');
  assert.equal(p.$('#dict-name').value, 'flipper-unleashed-classic');
  assert.equal(p.$('#dict-name').selectedOptions[0].textContent, 'Classic 扩展密钥');
  await p.click('#dict-download');
  const request = p.requests.find((r) => r.url === '/api/dictionary/download');
  assert.deepEqual(JSON.parse(request.body), { name: 'flipper-unleashed-classic' });
  assert.match(summary(p.$, '#dict-out').textContent, /Classic 扩展密钥/);
  assert.doesNotMatch(p.$('#dictionary').textContent, /proxmark3|flipper-unleashed/i);
  await p.click('[data-export="dict-out"]');
  const exported = JSON.parse(await p.downloads[0].blob.text());
  assert.equal(exported.name, 'Classic 扩展密钥');
  assert.equal(exported.id, 'dictionary-id');
  assert.equal(exported.keyLength, 12);
  assert.deepEqual(exported.keys, keys);
  assert.equal(p.downloads[0].name, 'chameleon-desk-dict-out.json');
});

for (const maximizeBeforeShrink of [true, false]) test(`desktop geometry survives restoring a maximized window across a narrow viewport (${maximizeBeforeShrink})`, async (t) => {
  const p = await page(t);
  await p.click(open('acl'));
  const win = p.$('#acl');
  const rectangle = () => ['left', 'top', 'width', 'height'].map(key => win.style[key]);
  const original = rectangle();
  if (maximizeBeforeShrink) await p.click('#acl [data-window="maximize"]');
  p.w.innerWidth = 375; p.w.innerHeight = 600; p.w.dispatchEvent(new p.w.Event('resize'));
  if (!maximizeBeforeShrink) await p.click('#acl [data-window="maximize"]');
  if (maximizeBeforeShrink) await p.click('#acl [data-window="maximize"]');
  p.w.innerWidth = 1368; p.w.innerHeight = 900; p.w.dispatchEvent(new p.w.Event('resize'));
  if (!maximizeBeforeShrink) await p.click('#acl [data-window="maximize"]');
  assert.deepEqual(rectangle(), original);
});

test('latest file selection wins even when its text matches the current input', async (t) => {
  const p = await page(t), slow = deferred();
  const original = p.$('#db-json').value;
  const choose = async (name, text) => {
    await p.click('[data-import="db-json"]'); const input = p.imports.at(-1);
    Object.defineProperty(input, 'files', { value: [{ name, size: 20, text }] });
    input.dispatchEvent(new p.w.Event('change')); await tick();
  };
  await choose('older.json', () => slow.promise);
  await choose('latest.json', async () => original);
  slow.resolve('{"older":true}'); await tick();
  assert.equal(p.$('#db-json').value, original);
  assert.match(p.$('#toast').textContent, /latest.json/);
});

test('editing and reverting an input still invalidates a pending import', async (t) => {
  const p = await page(t), slow = deferred(), original = p.$('#db-json').value;
  await p.click('[data-import="db-json"]'); const input = p.imports.at(-1);
  Object.defineProperty(input, 'files', { value: [{ name: 'old.json', size: 20, text: () => slow.promise }] });
  input.dispatchEvent(new p.w.Event('change'));
  p.input('#db-json', '{"edited":true}'); p.input('#db-json', original);
  slow.resolve('{"older":true}'); await tick();
  assert.equal(p.$('#db-json').value, original);
  assert.match(p.$('#toast').textContent, /输入已变化/);
});

for (const stalledBody of [false, true]) test(`a stalled ${stalledBody ? 'response body' : 'request'} times out and an offline retry recovers`, async (t) => {
  let attempts = 0;
  const p = await page(t, { '/api/acl/decode': (opts) => {
    if (++attempts > 1) return response({ conditions: [0, 0, 0, 4], blocks: [] });
    const pending = new Promise((resolve, reject) => opts.signal?.addEventListener('abort', () => reject(new p.w.DOMException('Aborted', 'AbortError'))));
    return stalledBody ? { ...response(null), json: () => pending } : pending;
  } }, { clock: true });
  await p.click('#acl-decode-go');
  assert.equal(p.$('#acl-encode-go').disabled, true);
  await p.advance(60000);
  assert.match(summary(p.$, '#acl-out').textContent, /超时/);
  assert.doesNotMatch(summary(p.$, '#acl-out').textContent, /写入|读取状态/);
  assert.equal(p.$('#acl-encode-go').disabled, false);
  assert.equal(p.$('#acl').hasAttribute('aria-busy'), false);
  await p.click('#acl .hardware-state button');
  assert.match(summary(p.$, '#acl-out').textContent, /访问权限已解码/);
  assert.equal(attempts, 2);
  assert.equal(p.timers.size, 0);
});

test('a malformed JSON response clears stale success and permits a successful retry', async (t) => {
  let malformed = false;
  const p = await page(t, { '/api/acl/encode': () => malformed ? { ...response(null), json: async () => { throw new SyntaxError('Unexpected token <'); } } : response({ access_hex: 'FF0780' }) });
  await p.click('#acl-encode-go'); malformed = true; await p.click('#acl-encode-go');
  assert.equal(p.$('#acl-out').dataset.ready, 'false');
  assert.match(summary(p.$, '#acl-out').textContent, /无效 JSON/);
  assert.equal(p.$('#acl-encode-go').disabled, false);
  malformed = false; await p.click('#acl .hardware-state button');
  assert.equal(p.$('#acl-out').dataset.ready, 'true');
});

for (const malformed of [false, true]) test(`a forbidden write revokes UI permissions even with a ${malformed ? 'malformed JSON' : 'text'} response`, async (t) => {
  const p = await page(t, { '/api/slot/load-card': () => malformed ? { ...response(null, 403), json: async () => { throw new SyntaxError('Invalid JSON'); } } : response('Forbidden', 403, 'text/plain') }, { writes: true });
  p.input('#load-card-json', JSON.stringify(card)); await p.click('#load-card-go');
  assert.equal(p.$('#load-card-go').disabled, true);
  assert.equal(p.$('#retry-capabilities').hidden, false);
  assert.equal(p.$('#load-card-out').dataset.ready, 'false');
  assert.equal(summary(p.$, '#load-card-out').querySelector('.hardware-state button'), null);
  assert.equal(p.requests.filter(r => r.url === '/api/slot/load-card').length, 1);
});

test('initial capability failure overlapping another task leaves permission retry usable', async (t) => {
  const caps = deferred(), operation = deferred(); let attempts = 0;
  const p = await page(t, {
    '/api/capabilities': () => ++attempts === 1 ? caps.promise : response({ writes_enabled: false }),
    '/api/firmware/check': () => operation.promise
  });
  await p.click('#fw-check');
  caps.resolve(response({ error: 'temporarily unavailable' }, 503)); await tick();
  assert.equal(p.$('#retry-capabilities').hidden, false);
  assert.equal(p.$('#retry-capabilities').disabled, true);
  operation.resolve(response({ notes: ['Offline fixture'] })); await tick();
  assert.equal(p.$('#retry-capabilities').disabled, false);
  await p.click('#retry-capabilities');
  assert.equal(p.$('#retry-capabilities').hidden, true);
  assert.equal(attempts, 2);
  assert.equal(p.$('#load-card-go').disabled, true);
});

for (const field of ['#db-query', '#db-json']) test(`a superseded library search cannot repopulate rows after ${field} changes`, async (t) => {
  const pending = deferred(); let attempt = 0;
  const p = await page(t, { '/api/database': () => ++attempt === 1 ? pending.promise : response([{ id: 'fresh', name: 'Current card' }]) });
  await p.click('#db-list');
  p.input(field, field === '#db-query' ? 'current' : '{"cards":[],"setting":"current"}');
  pending.resolve(response([{ id: 'old', name: 'Stale card' }])); await tick();
  assert.equal(p.$('#library-results').children.length, 0);
  assert.equal(p.$('#db-out').dataset.ready, 'false');
  await p.click('#db-list');
  assert.equal(p.$('[data-card-id]').dataset.cardId, 'fresh');
});

test('a superseded source listing cannot reset a newer dictionary choice', async (t) => {
  const pending = deferred();
  const p = await page(t, { '/api/dictionary/sources': () => pending.promise });
  await p.click('#dict-go');
  p.input('#dict-name', 'proxmark3-t55xx');
  pending.resolve(response([{ name: 'proxmark3-classic' }])); await tick();
  assert.equal(p.$('#dict-name').value, 'proxmark3-t55xx');
  assert.equal(p.$('#dictionary-sources').children.length, 0);
  assert.equal(p.$('#dict-out').dataset.ready, 'false');
});

test('a timed-out simulated write is not retried automatically or offered a retry action', async (t) => {
  const p = await page(t, { '/api/slot/load-card': opts => new Promise((resolve, reject) => opts.signal.addEventListener('abort', () => reject(new p.w.DOMException('Aborted', 'AbortError')))) }, { clock: true, writes: true });
  p.input('#load-card-json', JSON.stringify(card)); await p.click('#load-card-go');
  await p.advance(60000);
  assert.match(summary(p.$, '#load-card-out').textContent, /先读取状态确认结果/);
  assert.equal(summary(p.$, '#load-card-out').querySelector('.hardware-state button'), null);
  assert.equal(p.requests.filter(r => r.url === '/api/slot/load-card').length, 1);
  assert.equal(p.$('#slots').hasAttribute('aria-busy'), false);
});

for (const kind of ['database', 'card', 'dictionary']) test(`successful ${kind} exports preserve a user metadata field named error`, async (t) => {
  const metadata = { message: 'User-defined metadata', keep: true };
  const p = await page(t, {
    '/api/database': () => response({ ...card, error: metadata }),
    '/api/dictionary/download': () => response({ id: 'metadata', name: 'sample', keyLength: 8, keys: [[1, 2, 3, 4]], error: metadata })
  });
  if (kind === 'database') {
    p.input('#db-json', JSON.stringify({ cards: [], error: metadata }));
    await p.click('[data-export="db-out"]:not([data-export-card])');
  } else if (kind === 'card') {
    p.input('#db-id', card.id); await p.click('#db-show'); await p.click('[data-export-card]');
  } else {
    await p.click('#dict-download'); await p.click('[data-export="dict-out"]');
  }
  assert.equal(p.downloads.length, 1);
  assert.deepEqual(JSON.parse(await p.downloads[0].blob.text()).error, metadata);
});

test('NDEF language validation and an in-flight language change cannot publish stale output', async (t) => {
  const pending = deferred();
  const p = await page(t, { '/api/ndef/write': () => pending.promise });
  p.input('#ndef-input', JSON.stringify(card)); p.input('#ndef-text', 'text');
  for (const language of ['中文', 'a'.repeat(64)]) {
    p.input('#ndef-language', language); await p.click('#ndef-write-go');
    assert.match(summary(p.$, '#ndef-out').textContent, /63.*ASCII/);
  }
  assert.equal(p.requests.filter(r => r.url === '/api/ndef/write').length, 0);
  p.input('#ndef-language', 'en'); await p.click('#ndef-write-go');
  p.input('#ndef-language', 'zh'); pending.resolve(response(card)); await tick();
  assert.equal(p.$('#ndef-out').dataset.ready, 'false');
  assert.equal(p.$('#ndef-use-result').hidden, true);
  await p.click('[data-export="ndef-out"]'); assert.equal(p.downloads.length, 0);
});

for (const persisted of [false, true]) test(`a failed simulated load retains acknowledged stages (saved=${persisted}) and only offers a state read`, async (t) => {
  const result = { plan: { slot: 2, steps: [{ name: 'simulated' }] }, completed_steps: 1, verified: true, persisted, activated: true };
  const info = { model: 'Simulated', slots: [{ number: 1, active: true }, { number: 2, active: false }] };
  const p = await page(t, { '/api/info': () => response(info), '/api/slot/load-card': () => response({ error: persisted ? 'restore original device mode: failed' : 'save failed', result }, 502) }, { writes: true });
  await p.click('#refresh-slots'); assert.equal(p.$('#slot-list').dataset.ready, 'true');
  p.input('#load-card-json', JSON.stringify(card)); p.input('#load-slot', '2');
  await p.click('#load-card-go');
  const panel = summary(p.$, '#load-card-out');
  assert.match(panel.textContent, /加载未完成/); assert.match(panel.textContent, /已通过/);
  assert.match(panel.textContent, /1 \/ 1/); assert.equal(panel.querySelector('.hardware-state button'), null);
  assert.deepEqual(JSON.parse(p.$('#load-card-out').textContent).result, result);
  assert.equal(p.$('#load-card-out').dataset.ready, 'false');
  assert.equal(p.$('#slot-list').dataset.ready, 'false');
  await p.click('[data-export="slot-list"]'); assert.equal(p.downloads.length, 0);
  await p.click('#slots .load-state-check');
  assert.equal(p.$('#slot-list').dataset.ready, 'true');
  assert.equal(p.requests.filter(r => r.url === '/api/slot/load-card').length, 1);
  assert.equal(p.requests.filter(r => r.url === '/api/info').length, 2);
});

test('an uncertain slot activation invalidates stale exports and recovers by reading without repeating the mutation', async (t) => {
  const p = await page(t, {
    '/api/info': () => response({ model: 'Simulated', slots: [{ number: 1, active: true }, { number: 2, active: false }] }),
    '/api/slot/activate': () => response({ error: 'connection lost after command' }, 502)
  }, { writes: true });
  await p.click('#refresh-slots'); await p.click('#slot-list .slot-activate:not(:disabled)');
  assert.equal(p.$('#slot-list').dataset.ready, 'false');
  assert.equal(p.$('#slot-list').dataset.exportJson, undefined);
  assert.match(p.$('#slot-list').textContent, /重新读取/);
  await p.click('[data-export="slot-list"]'); assert.equal(p.downloads.length, 0);
  await p.click('#slot-list .hardware-state button');
  assert.equal(p.$('#slot-list').dataset.ready, 'true');
  assert.equal(p.requests.filter(r => r.url === '/api/slot/activate').length, 1);
});

test('a verified load remains distinguishable from a failed follow-up refresh', async (t) => {
  const result = { plan: { slot: 2, steps: [] }, completed_steps: 0, verified: true, persisted: false, activated: false };
  const p = await page(t, { '/api/slot/load-card': () => response(result), '/api/info': () => response({ error: 'read unavailable' }, 502) }, { writes: true });
  p.input('#load-card-json', JSON.stringify(card)); await p.click('#load-card-go');
  assert.match(summary(p.$, '#load-card-out').textContent, /已回读验证/);
  assert.match(summary(p.$, '#load-card-out').textContent, /未保存到 Flash/);
  assert.equal(p.$('#load-card-out').dataset.ready, 'true');
  assert.equal(p.$('#slot-list').dataset.ready, 'false');
  assert.match(p.$('#slot-list').textContent, /read unavailable/);
  assert.equal(p.requests.filter(r => r.url === '/api/slot/load-card').length, 1);
});

test('malformed load outcomes cannot become a success or hide the original failure', async (t) => {
  let failed = false;
  const p = await page(t, {
    '/api/slot/load-card': () => failed ? response({ error: 'original failure', result: { plan: { slot: 99 } } }, 502) : response({ ok: true }),
    '/api/info': () => response({ model: 'Simulated', slots: [] })
  }, { writes: true });
  p.input('#load-card-json', JSON.stringify(card)); await p.click('#load-card-go');
  assert.equal(p.$('#load-card-out').dataset.ready, 'false');
  assert.match(summary(p.$, '#load-card-out').textContent, /结果不完整/);
  failed = true; await p.click('#load-card-go');
  assert.equal(p.$('#load-card-out').dataset.ready, 'false');
  assert.match(summary(p.$, '#load-card-out').textContent, /original failure/);
  assert.equal(summary(p.$, '#load-card-out').querySelector('.hardware-state button'), null);
});

for (const [path, button, out, budget] of [
  ['/api/firmware/check', '#fw-check', '#fw-out', 95000],
  ['/api/dictionary/download', '#dict-download', '#dict-out', 65000]
]) test(`${path} allows its server stages to finish before the client timeout`, async (t) => {
  const p = await page(t, { [path]: opts => new Promise((resolve, reject) => opts.signal.addEventListener('abort', () => reject(new p.w.DOMException('Aborted', 'AbortError')))) }, { clock: true });
  await p.click(button); await p.advance(60000);
  assert.equal(p.$(button).disabled, true, 'the server still has time to return a bounded result');
  await p.advance(budget - 60000);
  assert.equal(p.$(button).disabled, false);
  assert.match(summary(p.$, out).textContent, /超时/);
  assert.equal(p.requests.filter(r => r.url === path).length, 1);
});
