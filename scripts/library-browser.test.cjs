const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { createInterface } = require('node:readline');
const { chromium } = require('playwright');

async function serve(binary, addr = '127.0.0.1:0') {
  const child = spawn(path.resolve(binary), ['serve', '--addr', addr], { stdio: ['ignore', 'pipe', 'pipe'] });
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
