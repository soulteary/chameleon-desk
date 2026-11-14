const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');
const { IDBFactory } = require('fake-indexeddb');
const script = fs.readFileSync(path.join(__dirname, '../cmd/chameleon/webui/library-persistence.js'), 'utf8');
const tick = () => new Promise(resolve => setImmediate(resolve));
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
function library(t, { factory = new IDBFactory(), validate = async () => {}, store: supplied } = {}) {
  const dom = new JSDOM('', { url: 'http://127.0.0.1:8088', runScripts: 'outside-only' });
  const w = dom.window; w.indexedDB = factory; w.eval(script);
  let text = '{}', state;
  const store = supplied || new w.DeskLibraryStore();
  const persistence = new w.DeskLibraryPersistence({ getText: () => text, setText: value => { text = value; }, validate, render: value => { state = value; }, store, delay: 60000 });
  t.after(() => { persistence.cancelTimer(); dom.window.close(); });
  return { store, persistence, get text() { return text; }, get state() { return state; }, edit(value) { text = value; persistence.changed(); } };
}
async function rawPut(store, key, value) {
  const db = await store.open();
  try {
    await new Promise((resolve, reject) => {
      const tx = db.transaction('snapshots', 'readwrite'); tx.objectStore('snapshots').put(value, key);
      tx.oncomplete = resolve; tx.onabort = () => reject(tx.error);
    });
  } finally { db.close(); }
}

test('committed library survives a new page and preserves unknown metadata and GUI prefixes', async t => {
  const factory = new IDBFactory(), first = library(t, { factory }); await first.persistence.load();
  const text = JSON.stringify({ 'flutter.cards': [], 'flutter.dictionaries': [], custom: { unicode: '卡片', error: 'metadata' } }, null, 2);
  first.edit(text); assert(first.persistence.dirty); await first.persistence.save();
  assert(!first.persistence.dirty); assert.match(first.state.message, /已自动保存/);
  const second = library(t, { factory }); await second.persistence.load();
  assert.equal(second.text, text); assert(!second.persistence.dirty); assert.match(second.state.message, /已恢复/);
});

test('aborted IndexedDB transaction preserves both current and previous snapshots', async t => {
  const p = library(t); const a = await p.store.write(undefined, '{"version":"A"}');
  const b = await p.store.write(a, '{"version":"B"}');
  const open = p.store.open.bind(p.store);
  p.store.open = async () => {
    const db = await open(), transaction = db.transaction.bind(db);
    db.transaction = (...args) => {
      const tx = transaction(...args), objectStore = tx.objectStore.bind(tx);
      tx.objectStore = name => {
        const store = objectStore(name);
        return { get: store.get.bind(store), put(value, key) { const request = store.put(value, key); if (key === 'current') tx.abort(); return request; } };
      };
      return tx;
    };
    return db;
  };
  await assert.rejects(p.store.write(b, '{"version":"C"}'));
  p.store.open = open;
  assert.deepEqual({ ...await p.store.read() }, { current: b, previous: a });
});

test('quota failure retains draft, old snapshots and dirty state; explicit retry can succeed', async t => {
  const p = library(t); await p.persistence.load(); p.edit('{"saved":1}'); await p.persistence.save();
  const before = await p.store.read(), write = p.store.write.bind(p.store);
  p.store.write = async () => { throw Object.assign(new Error('full'), { name: 'QuotaExceededError' }); };
  p.edit('{"saved":2}'); await p.persistence.save();
  assert.equal(p.text, '{"saved":2}'); assert(p.persistence.dirty); assert.match(p.state.message, /空间不足/);
  assert.deepEqual(await p.store.read(), before);
  p.store.write = write; await p.persistence.save(); assert(!p.persistence.dirty);
  assert.equal(JSON.parse((await p.store.read()).current).text, p.text);
});

test('invalid JSON and backend schema validation never replace the committed library', async t => {
  let reject = false;
  const p = library(t, { validate: async () => { if (reject) throw new Error('invalid card collection'); } });
  await p.persistence.load(); p.edit('{"good":true}'); await p.persistence.save(); const saved = await p.store.read();
  for (const text of ['{', '[]', '{"cards":"bad"}']) {
    reject = true; p.edit(text); await p.persistence.save(); assert(p.persistence.dirty); assert(p.state.error);
    assert.equal(p.text, text); assert.deepEqual(await p.store.read(), saved);
  }
});

test('stale validation cannot save an obsolete document or clear newer unsaved edits', async t => {
  const pending = deferred(); let calls = 0;
  const p = library(t, { validate: async () => { if (++calls === 1) await pending.promise; } });
  await p.persistence.load(); p.edit('{"draft":"A"}'); const saving = p.persistence.save(); await tick();
  p.edit('{"draft":"B"}'); pending.resolve(); await saving;
  assert.equal((await p.store.read()).current, undefined); assert(p.persistence.dirty);
  await p.persistence.save(); assert.equal(JSON.parse((await p.store.read()).current).text, '{"draft":"B"}');
});

test('an edit during a final commit remains dirty until its own transaction completes', async t => {
  const p = library(t); await p.persistence.load(); const pending = deferred(), write = p.store.write.bind(p.store);
  p.store.write = async (...args) => { await pending.promise; return write(...args); };
  p.edit('{"draft":"A"}'); const saving = p.persistence.save(); await tick();
  p.edit('{"draft":"B"}'); pending.resolve(); await saving;
  assert(p.persistence.dirty); assert.equal(p.text, '{"draft":"B"}');
  assert.equal(JSON.parse((await p.store.read()).current).text, '{"draft":"A"}');
  await p.persistence.save(); assert(!p.persistence.dirty);
});

test('two tabs cannot silently overwrite one another and reload resolves the conflict explicitly', async t => {
  const factory = new IDBFactory(), a = library(t, { factory }), b = library(t, { factory });
  await a.persistence.load(); await b.persistence.load();
  a.edit('{"owner":"A"}'); await a.persistence.save(); const saved = await a.store.read();
  b.edit('{"owner":"B"}'); await b.persistence.save();
  assert(b.persistence.dirty); assert(b.persistence.blocked); assert.match(b.state.message, /另一个页面/);
  assert.equal(b.text, '{"owner":"B"}'); assert.deepEqual({ ...await b.store.read() }, { ...saved });
  await b.persistence.load(); assert.equal(b.text, '{"owner":"A"}'); assert(!b.persistence.dirty);
  b.edit('{"owner":"B"}'); await b.persistence.save(); assert(!b.persistence.dirty);
});

test('unsupported or corrupted current records are preserved and previous recovery is deliberate', async t => {
  for (const raw of ['broken-record', JSON.stringify({ format: 2, text: '{}' }), { format: 2, text: '{}' }]) {
    const p = library(t); const good = await p.store.write(undefined, '{"recover":"this"}');
    await rawPut(p.store, 'previous', good); await rawPut(p.store, 'current', raw);
    await p.persistence.load(); assert(p.persistence.blocked); assert(p.state.error); assert(p.state.previous);
    p.edit('{"new":"draft"}'); await p.persistence.save(); assert.equal(JSON.stringify((await p.store.read()).current), JSON.stringify(raw));
    await p.persistence.restorePrevious(); assert(!p.persistence.dirty); assert(!p.persistence.blocked);
    assert.equal(p.text, '{"recover":"this"}'); assert.equal((await p.store.read()).previous, good);
  }
});

test('a saved document that fails schema validation does not become the current editor', async t => {
  const p = library(t, { validate: async () => { throw new Error('broken card references'); } });
  const raw = await p.store.write(undefined, '{"cards":"invalid"}'); await p.persistence.load();
  assert(p.persistence.blocked); assert.equal(p.text, '{}'); assert.equal((await p.store.read()).current, raw);
});

test('unavailable storage leaves editing available but never acknowledges a save', async t => {
  const p = library(t, { store: { read: async () => { throw new Error('storage denied'); } } });
  await p.persistence.load(); p.edit('{"keep":"draft"}'); await p.persistence.save();
  assert.equal(p.text, '{"keep":"draft"}'); assert(p.persistence.dirty); assert(p.persistence.blocked);
  assert(!p.state.message.includes('已自动保存'));
});

test('restoring a damaged previous record fails without replacing either snapshot or draft', async t => {
  const p = library(t); await p.persistence.load(); p.edit('{"saved":true}'); await p.persistence.save();
  await rawPut(p.store, 'previous', 'bad'); const saved = await p.store.read();
  p.edit('{"draft":true}'); await p.persistence.restorePrevious();
  assert(p.persistence.dirty); assert.equal(p.text, '{"draft":true}'); assert.deepEqual(await p.store.read(), saved);
});
