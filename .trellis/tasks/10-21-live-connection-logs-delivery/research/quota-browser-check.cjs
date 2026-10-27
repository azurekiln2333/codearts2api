// Exercise the embedded panel with synthetic quota snapshots and no real credentials.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { chromium } = require(process.env.UI_PLAYWRIGHT_MODULE || path.join(os.tmpdir(), 'codearts2api-ui-verification/node_modules/playwright'));
let root = __dirname;
while (!fs.existsSync(path.join(root, 'go.mod'))) root = path.dirname(root);
const html = fs.readFileSync(process.env.UI_PANEL_SOURCE || path.join(root, 'internal/server/panel.html'), 'utf8')
  .replaceAll('__SERVICE_NAME__', 'codearts2api').replaceAll('__SERVICE_TITLE__', 'CodeArts2API').replaceAll('__LOGO__', 'CA').replaceAll('__ACCENT__', '#0284c7');
const output = process.env.UI_OUTPUT_DIR || path.join(root, '.local-test', 'quota-ui-' + Date.now());
fs.mkdirSync(output, { recursive: true });
const stamp = '2026-10-27T01:00:00Z';
const ready = { state: 'ready', complete: true, total: 5000, used: 1200, remaining: 3800, known_remaining: 3800,
  ready_accounts: 2, counted_accounts: 2, oldest_success_at: stamp, refresh: { running: false } };
const state = { quota: ready, failOverview: false, failRefresh: false, refreshReads: 0, calls: [], hold: false, started: null, release: null,
  refreshReply: undefined, holdRefresh: false, refreshStarted: null, releaseRefresh: null };
const overview = () => ({ stats: { total: 2, healthy: 2, cooling: 0, disabled: 0 }, accounts: [], models: [],
  audit: { enabled: true }, effective: {}, schedule: {}, ...(state.quota === undefined ? {} : { quota: state.quota }) });
const server = http.createServer(async (req, res) => {
  const route = new URL(req.url, 'http://fixture.invalid').pathname.replace(/^\/proxy/, '');
  state.calls.push(route);
  const json = (status, data) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)); };
  if (route === '/favicon.ico') { res.writeHead(204); res.end(); return; }
  if (!route.includes('/admin/api/')) { res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' }); res.end(html); return; }
  if (req.headers.authorization !== 'Bearer synthetic-quota-key') { json(401, { error: { message: 'Synthetic key required' } }); return; }
  if (route === '/admin/api/overview') {
    if (state.hold) await new Promise(resolve => { state.started?.(); state.release = resolve; });
    if (state.failOverview) { json(503, { error: { message: 'snapshot failed' } }); return; }
    if (state.quota?.refresh?.running && ++state.refreshReads >= 2) state.quota = { ...ready, total: 6000, remaining: 4800 };
    json(200, overview()); return;
  }
  if (route === '/admin/api/quota/refresh') {
    const reply = state.refreshReply;
    if (state.holdRefresh) {
      await new Promise(resolve => { state.refreshStarted?.(); state.releaseRefresh = resolve; });
      json(202, { refresh: reply }); return;
    }
    if (state.failRefresh) { json(503, { error: { message: 'refresh failed' } }); return; }
    if (reply !== undefined) {
      if (typeof reply?.running === 'boolean') state.quota = { ...state.quota, refresh: reply };
      json(202, { refresh: reply }); return;
    }
    state.refreshReads = 0;
    state.quota = { ...state.quota, refresh: { running: true } };
    json(202, { refresh: { running: true } }); return;
  }
  json(200, {});
});

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  const browser = await chromium.launch({ headless: true, executablePath: process.env.UI_CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe' });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' });
  const page = await context.newPage(); page.setDefaultTimeout(8000);
  const errors = []; page.on('pageerror', error => errors.push(error.message));
  async function revisit() {
    await page.evaluate(() => { location.hash = '#accounts'; });
    await page.waitForFunction(() => !document.querySelector('[data-view="accounts"]').hidden);
    const response = page.waitForResponse(res => res.url().includes('/admin/api/overview'));
    await page.evaluate(() => { location.hash = '#overview'; });
    await response;
  }
  try {
    await page.goto(origin + '/proxy/panel/#overview');
    await page.locator('#keyInput').fill('synthetic-quota-key'); await page.locator('#loginBtn').click();
    await page.waitForFunction(() => document.getElementById('quotaRemaining').textContent === '3,800');
    assert.equal(await page.locator('#quotaTotal').innerText(), '5,000');
    assert.equal(await page.locator('#quotaUsed').innerText(), '1,200');
    assert.match(await page.locator('#quotaCard').innerText(), /福利模型额度另算/);
    await page.locator('#btnQuotaRefresh').click();
    await page.waitForFunction(() => document.getElementById('quotaStatus').textContent.includes('正在刷新'));
    assert.equal(await page.locator('#btnQuotaRefresh').isDisabled(), true);
    await page.waitForFunction(() => document.getElementById('quotaRemaining').textContent === '4,800');
    assert.equal(await page.locator('#btnQuotaRefresh').isEnabled(), true);

    const retryAt = new Date(Date.now() + 2600).toISOString();
    state.refreshReply = { running: false, next_at: retryAt };
    const beforeThrottle = state.calls.filter(route => route === '/admin/api/overview').length;
    await page.locator('#btnQuotaRefresh').click();
    await page.waitForFunction(() => document.getElementById('quotaStatus').textContent.includes('刷新频率受限'));
    const expectedRetry = await page.evaluate(value => new Date(value).toLocaleString(), retryAt);
    const limited = await page.locator('#quotaStatus').innerText();
    assert.ok(limited.includes(expectedRetry), 'throttle shows the actual next retry time');
    assert.doesNotMatch(limited, /正在刷新|已提交/);
    assert.doesNotMatch(await page.locator('#toast').textContent(), /已提交/);
    assert.equal(await page.locator('#btnQuotaRefresh').isEnabled(), true, 'client clock cannot lock out manual retries');
    assert.equal(state.calls.filter(route => route === '/admin/api/overview').length, beforeThrottle, 'throttled response does not trigger an immediate GET');
    await page.setViewportSize({ width: 375, height: 812 });
    await page.screenshot({ path: path.join(output, 'throttled-mobile.png') });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForFunction(() => !document.getElementById('quotaStatus').textContent.includes('刷新频率受限'), null, { timeout: 6000 });
    assert.ok(state.calls.filter(route => route === '/admin/api/overview').length > beforeThrottle, 'refresh status is rechecked after the deadline');

    state.refreshReply = undefined;
    state.quota = { ...ready, refresh: { running: false, next_at: new Date(Date.now() + 1600).toISOString() } };
    await revisit();
    await page.waitForFunction(() => document.getElementById('quotaStatus').textContent.includes('刷新频率受限'));
    await page.evaluate(() => { location.hash = '#accounts'; });
    await page.waitForFunction(() => !document.querySelector('[data-view="accounts"]').hidden);
    const offPageCalls = state.calls.filter(route => route === '/admin/api/overview').length;
    await page.waitForTimeout(1900);
    assert.equal(state.calls.filter(route => route === '/admin/api/overview').length, offPageCalls, 'navigation cancels the quota deadline poll');
    const back = page.waitForResponse(res => res.url().includes('/admin/api/overview'));
    await page.evaluate(() => { location.hash = '#overview'; }); await back;
    await page.waitForFunction(() => !document.getElementById('quotaStatus').textContent.includes('刷新频率受限'));

    state.quota = { ...ready, refresh: { running: false, next_at: new Date(Date.now() + 1600).toISOString() } };
    await revisit();
    await page.waitForFunction(() => document.getElementById('quotaStatus').textContent.includes('刷新频率受限'));
    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => true });
      document.dispatchEvent(new Event('visibilitychange'));
    });
    const hiddenCalls = state.calls.filter(route => route === '/admin/api/overview').length;
    await page.waitForTimeout(1900);
    assert.equal(state.calls.filter(route => route === '/admin/api/overview').length, hiddenCalls, 'hidden page cancels the quota deadline poll');
    const resumed = page.waitForResponse(res => res.url().includes('/admin/api/overview'));
    await page.evaluate(() => { delete document.hidden; document.dispatchEvent(new Event('visibilitychange')); }); await resumed;
    await page.waitForFunction(() => !document.getElementById('quotaStatus').textContent.includes('刷新频率受限'));

    for (const nextAt of ['not-a-date', new Date(Date.now() - 60000).toISOString(), null]) {
      state.refreshReply = { running: false, next_at: nextAt };
      await page.locator('#btnQuotaRefresh').click();
      await page.waitForFunction(() => document.getElementById('quotaStatus').textContent.includes('本次刷新未启动'));
      const text = await page.locator('#quotaStatus').innerText();
      assert.doesNotMatch(text, /刷新频率受限|正在刷新|已提交/);
      assert.equal(await page.locator('#btnQuotaRefresh').isEnabled(), true);
    }
    state.refreshReply = { running: 'yes' };
    await page.locator('#btnQuotaRefresh').click();
    await page.waitForFunction(() => document.getElementById('quotaStatus').textContent.includes('服务未返回有效刷新状态'));
    assert.equal(await page.locator('#btnQuotaRefresh').isEnabled(), true);
    state.refreshReply = undefined;

    state.quota = { state: 'partial', complete: false, total: null, used: null, remaining: null, known_remaining: 250,
      ready_accounts: 1, counted_accounts: 2, refresh: { running: false } };
    await revisit();
    await page.waitForFunction(() => document.getElementById('quotaStatus').textContent.includes('已知剩余 250'));
    assert.equal(await page.locator('#quotaTotal').innerText(), '未知');
    assert.equal(await page.locator('#quotaRemaining').innerText(), '未知');
    assert.match(await page.locator('#quotaStatus').innerText(), /1 \/ 2 个账号/);
    state.quota = { ...ready, total: 0, used: 0, remaining: 0, known_remaining: 0 };
    await revisit(); await page.waitForFunction(() => document.getElementById('quotaRemaining').textContent === '0');
    state.quota = undefined;
    await revisit(); await page.waitForFunction(() => document.getElementById('quotaTotal').textContent === '未知');
    state.failRefresh = true;
    await page.locator('#btnQuotaRefresh').click();
    await page.waitForFunction(() => document.getElementById('quotaStatus').textContent.includes('refresh failed'));
    assert.equal(await page.locator('#btnQuotaRefresh').isEnabled(), true);
    state.failRefresh = false; state.failOverview = true;
    await revisit(); await page.waitForFunction(() => document.getElementById('quotaStatus').textContent.includes('snapshot failed'));
    state.failOverview = false; state.quota = ready;

    state.holdRefresh = true;
    state.refreshReply = { running: false, next_at: new Date(Date.now() + 30000).toISOString() };
    const started = new Promise(resolve => { state.refreshStarted = resolve; });
    await page.locator('#btnQuotaRefresh').click(); await started;
    await page.locator('#btnLogout').click();
    await page.locator('#loginView').waitFor({ state: 'visible' });
    state.holdRefresh = false; state.refreshReply = undefined;
    state.quota = { ...ready, remaining: 1234 };
    await page.locator('#keyInput').fill('synthetic-quota-key'); await page.locator('#loginBtn').click();
    await page.waitForFunction(() => document.getElementById('quotaRemaining').textContent === '1,234');
    state.releaseRefresh(); state.releaseRefresh = null;
    await page.waitForTimeout(150);
    assert.doesNotMatch(await page.locator('#quotaStatus').innerText(), /刷新频率受限|正在刷新/);
    assert.equal(await page.locator('#quotaRemaining').innerText(), '1,234', 'old login cannot publish a delayed refresh');
    state.quota = ready;

    await page.setViewportSize({ width: 375, height: 812 }); await revisit();
    await page.waitForFunction(() => document.getElementById('quotaRemaining').textContent === '3,800');
    const layout = await page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, width: innerWidth,
      scrollHeight: document.documentElement.scrollHeight, height: innerHeight }));
    assert.ok(layout.scrollWidth <= layout.width + 1 && layout.scrollHeight <= layout.height + 1, JSON.stringify(layout));
    await page.screenshot({ path: path.join(output, 'overview-mobile.png') });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.screenshot({ path: path.join(output, 'overview-desktop.png') });
    assert.deepEqual(errors, []);
    assert.ok(state.calls.includes('/admin/api/quota/refresh'));
    console.log(JSON.stringify({ passed: true, checks: ['complete and zero balances', 'partial and unknown balances', 'running and throttled refresh', 'retry deadline, navigation and visibility timers', 'invalid and expired retry times', 'snapshot and refresh errors', 'logout during refresh', 'proxy prefix', 'desktop and mobile layout'], screenshots: output }));
  } finally {
    state.releaseRefresh?.(); state.release?.();
    await browser.close(); await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
