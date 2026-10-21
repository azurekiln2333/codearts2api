// Real-browser checks against synthetic localhost APIs; never contacts production.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { chromium } = require(process.env.UI_PLAYWRIGHT_MODULE || path.join(os.tmpdir(), 'codearts2api-ui-verification/node_modules/playwright'));

let root = __dirname;
while (!fs.existsSync(path.join(root, 'go.mod'))) {
  const parent = path.dirname(root);
  if (parent === root) throw new Error('Project root not found');
  root = parent;
}
const baseline = process.argv.includes('--baseline');
const original = baseline
  ? execFileSync('git', ['show', '3fefda8:internal/server/panel.html'], { cwd: root, encoding: 'utf8' })
  : fs.readFileSync(path.join(root, 'internal/server/panel.html'), 'utf8');
const html = original.replaceAll('__SERVICE_NAME__', 'codearts2api').replaceAll('__SERVICE_TITLE__', 'CodeArts2API').replaceAll('__LOGO__', 'CA').replaceAll('__ACCENT__', '#0284c7');
const output = path.join(__dirname, 'screenshots');
fs.mkdirSync(output, { recursive: true });
const stamp = new Date().toISOString();
const malicious = '<img src=x onerror="window.__fixtureXSS=1">';
const preciseCopyModel = 'fixture`model';
const accounts = [
  { uid: 'fixture-account-01', name: 'development', nickname: '开发主账号', disabled: false, cooling: false, active_concurrent: 2, max_concurrent: 5, err_count: 0, checkin: { ok: true, message: '每日签到已领取 · 新用户福利已领取', checked_at: stamp } },
  { uid: 'fixture-account-02', name: 'backup', nickname: '备用账号', disabled: false, cooling: true, active_concurrent: 0, max_concurrent: 3, err_count: 3, reason: 'consecutive errors', until: new Date(Date.now() + 600000).toISOString(), last_failure_class: 'upstream', last_failure_text: 'upstream service error', last_failure_at: stamp },
  { uid: 'fixture-account-03', name: 'maintenance', nickname: '维护账号', disabled: true, cooling: false, active_concurrent: 0, max_concurrent: 2, err_count: 0, reason: 'manually disabled', checkin: { ok: false, message: '尚未查询', checked_at: stamp } },
  { uid: 'fixture-account-04', name: 'legacy', nickname: '兼容旧状态记录', disabled: false, cooling: false, active_concurrent: 1, max_concurrent: 5, err_count: 2, last_error: 'consecutive errors' },
];
const models = ['deepseek-v4.1-flash', 'deepseek-v4.1', 'qwen3-coder', 'glm-5-flash', 'qwen3-235b', 'openpangu-large', 'deepseek-r1', 'qwen3-32b', 'glm-4.7', 'qwen3-coder-plus', 'fixture-model-with-a-very-long-identifier-for-overflow-verification-0123456789', preciseCopyModel, malicious].map(id => ({ id }));
const overview = {
  service: 'CodeArts2API', region: 'cn-north-4', accounts, models,
  stats: { total: 4, healthy: 2, cooling: 1, disabled: 1 },
  build: { known: true, commit: '3fefda8-fixture-build', built_at: stamp },
  schedule: { watch: { enabled: true, credits_auto_claim: true, poll_minutes: 30 }, checkin_hours: [8, 20], keepalive_hours: [7, 19] },
  effective: { startup_timeout: '90s', account_wait_timeout: '10s', upstream_idle_timeout: '60s', stream_write_timeout: '30s', max_pending: 64, queue_max_attempts: 3, queue_retry_delay: '2s' },
  audit: { enabled: true, retention_days: 7, max_files: 8, max_file_bytes: 8388608, max_total_bytes: 67108864, recent: 512, max_queue: 256 },
};
const events = [
  { seq: 6, id: 'fixture-request-0006', model: 'deepseek-v4.1-flash', account: 'fixture-account-01', committed: true, status: 200, outcome: 'ok', stream: true, attempts: 1, queue_ms: 0, first_data_ms: 326, total_ms: 1824, cause: { class: 'none', text: '' } },
  { seq: 5, id: 'fixture-request-0005', model: 'glm-5-flash', account: 'fixture-account-02', committed: true, status: 200, outcome: 'upstream_stream_error', stream: true, attempts: 2, queue_ms: 18, first_data_ms: 842, total_ms: 2130, cause: { class: 'capability', code: 'InferHub.4004.200', text: 'benefit not claimed or expired' } },
  { seq: 4, id: 'fixture-request-0004', model: 'deepseek-v4.1', account: 'fixture-account-04', committed: true, status: 504, outcome: 'timeout', attempts: 1, queue_ms: 5, first_data_ms: null, total_ms: 90000, cause: { class: 'startup_timeout', text: 'request startup deadline exceeded' } },
  { seq: 3, id: 'fixture-request-0003', model: 'qwen3-coder', account: null, committed: true, status: 429, outcome: 'queue_wait_timeout', attempts: 0, queue_ms: 10000, first_data_ms: null, total_ms: 10001, cause: { class: 'queue_wait_timeout', text: 'account wait deadline exceeded' } },
  { seq: 2, id: 'fixture-request-0002', model: 'qwen3-32b', account: null, committed: false, outcome: 'canceled', attempts: 0, queue_ms: 0, first_data_ms: null, total_ms: 23, cause: { class: 'client_canceled', text: 'client canceled request' } },
  { seq: 1, id: 'fixture-request-0001', model: malicious, account: 'fixture-account-01', committed: true, status: 404, outcome: 'model_unavailable', attempts: 4, queue_ms: 0, first_data_ms: null, total_ms: 1054, cause: { class: 'capability', code: 'InferHub.002002009.404', text: 'model not registered' } },
].map((event, index) => ({ ...event, requested_model: event.model, ts: new Date(Date.now() - index * 45000).toISOString() }));
const state = { mode: 'normal', calls: [], oauthDone: false, releaseOverview: null, overviewStarted: null };
let origin;

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://fixture.invalid');
  const route = url.pathname.replace(/^\/proxy/, '');
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  let body = {};
  try { body = JSON.parse(Buffer.concat(chunks).toString() || '{}'); } catch { }
  state.calls.push({ method: req.method, path: url.pathname, query: url.search, body });
  const json = (status, value) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(value)); };
  if (route === '/favicon.ico') { res.writeHead(204); res.end(); return; }
  if (!route.includes('/admin/api/')) { res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' }); res.end(html); return; }
  if (req.headers.authorization !== 'Bearer fixture-ui-key') { json(401, { error: { message: 'Fixture API key required' } }); return; }
  if (route === '/admin/api/overview') {
    if (state.mode === 'overview-delay') await new Promise(resolve => {
      state.releaseOverview = () => { state.releaseOverview = null; resolve(); };
      state.overviewStarted?.();
    });
    if (state.mode === 'error') { json(503, { error: { message: 'Fixture temporary overview error' } }); return; }
    const data = structuredClone(overview);
    if (state.mode === 'empty') {
      data.accounts = []; data.models = []; data.stats = { total: 0, healthy: 0, cooling: 0, disabled: 0 };
      data.build = { known: false, commit: '', built_at: null }; data.audit.enabled = false;
    }
    json(200, data); return;
  }
  if (route === '/admin/api/requests') {
    if (state.mode === 'audit-error') { json(503, { error: { message: 'Fixture temporary audit error' } }); return; }
    if (state.mode === 'empty') { json(200, { events: [], disabled: true, has_more: false, window: 512 }); return; }
    const query = url.searchParams;
    const filtered = events.filter(e => (!query.get('before') || e.seq < Number(query.get('before'))) && (!query.get('outcome') || e.outcome === query.get('outcome')) && (!query.get('model') || e.model.toLowerCase().includes(query.get('model').toLowerCase())) && (!query.get('account') || String(e.account || '').includes(query.get('account'))) && (!query.get('status') || e.status === Number(query.get('status'))));
    const page = filtered.slice(0, 2);
    json(200, { events: page, has_more: filtered.length > page.length, next_before: page.at(-1)?.seq, window: 512, dropped: 0, write_failures: 0 }); return;
  }
  if (route === '/admin/api/oauth/start') { json(200, { ok: true, session_id: 'fixture-oauth-session', auth_url: origin + '/fixture-authorize' }); return; }
  if (route === '/admin/api/oauth/poll') { json(200, state.oauthDone ? { ok: true, status: 'done', message: 'Fixture authorization complete' } : { ok: true, status: 'pending', message: 'Fixture authorization pending' }); return; }
  if (route === '/admin/api/oauth/import-callback') { state.oauthDone = true; json(200, { ok: true, message: 'Fixture callback received' }); return; }
  json(200, { ok: true, message: 'Fixture operation complete' });
});

async function visible(page, selector) { await page.locator(selector).first().waitFor({ state: 'visible', timeout: 8000 }); }
async function screenshot(page, name) { await page.screenshot({ path: path.join(output, name + '.png'), fullPage: true }); }
async function expectClipboard(page, value) { await page.waitForFunction(async expected => await navigator.clipboard.readText() === expected, value); }
async function noOverflow(page, label) {
  const size = await page.evaluate(() => ({ page: document.documentElement.scrollWidth, viewport: innerWidth }));
  assert.ok(size.page <= size.viewport + 1, `${label}: document overflow ${JSON.stringify(size)}`);
}
async function navigate(page, view) {
  const mobile = page.viewportSize().width < 768;
  if (mobile && await page.locator('#sidebarToggle').getAttribute('aria-expanded') !== 'true') await page.locator('#sidebarToggle').click();
  await page.locator(`nav [data-page="${view}"]`).first().click();
  await visible(page, `[data-view="${view}"]`);
  await page.waitForFunction(expected => location.hash === '#' + expected, view);
  if (mobile) assert.equal(await page.locator('#sidebarToggle').getAttribute('aria-expanded'), 'false', 'navigation dismisses mobile drawer');
}

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  origin = 'http://127.0.0.1:' + server.address().port;
  const browser = await chromium.launch({ headless: true, executablePath: process.env.UI_CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe' });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1024 }, colorScheme: 'light', reducedMotion: 'reduce' });
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin });
  const errors = [];
  const external = [];
  await context.route('**/*', async route => {
    if (new URL(route.request().url()).origin !== origin) { external.push(route.request().url()); await route.abort(); }
    else await route.continue();
  });
  const page = await context.newPage();
  page.setDefaultTimeout(8000);
  page.on('pageerror', e => errors.push(e.message));
  try {
    await page.goto(origin + '/panel/');
    await visible(page, '#loginView');
    await screenshot(page, baseline ? 'before-login' : 'login-desktop');
    await page.locator('#keyInput').fill('fixture-ui-key');
    await page.locator('#loginBtn').click();
    await visible(page, '#appView');
    await page.waitForFunction(() => document.getElementById('sTotal')?.textContent === '4');
    if (baseline) { await screenshot(page, 'before-desktop'); console.log(JSON.stringify({ passed: true, baseline: true })); return; }

    await visible(page, '[data-view="overview"]');
    await screenshot(page, 'overview-desktop');
    await noOverflow(page, 'desktop overview');
    for (const view of ['accounts', 'requests', 'models', 'settings']) {
      await navigate(page, view); await screenshot(page, view + '-desktop'); await noOverflow(page, 'desktop ' + view);
    }
    await navigate(page, 'models');
    await page.locator('#modelSearch').fill('deepseek');
    assert.equal(await page.locator('#modelChips .model-item').count(), 3);
    await page.locator('[data-copy-model="deepseek-v4.1-flash"]').click();
    await expectClipboard(page, 'deepseek-v4.1-flash');
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'deepseek-v4.1-flash');
    await page.locator('#modelSearch').fill(preciseCopyModel);
    await page.locator('#modelChips [data-copy-model]').click();
    await expectClipboard(page, preciseCopyModel);
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), preciseCopyModel, 'attribute escaping preserves exact model identifier');
    await page.locator('#modelSearch').fill('no-such-fixture-model');
    assert.match(await page.locator('#modelChips').innerText(), /没有匹配的模型/);
    await page.locator('#modelSearch').fill('');
    await navigate(page, 'accounts'); await navigate(page, 'requests');
    await page.goBack(); await visible(page, '[data-view="accounts"]');
    await page.goForward(); await visible(page, '[data-view="requests"]');
    await navigate(page, 'accounts');
    await page.locator('#acctSearch').fill('备用');
    assert.equal(await page.locator('#acctBody tr').count(), 1);
    assert.match(await page.locator('#acctBody').innerText(), /备用账号/);
    await page.locator('#acctSearch').fill('');
    await page.locator('#acctStatus').selectOption('ready');
    assert.equal(await page.locator('#acctBody tr').count(), 2);
    await page.locator('#acctStatus').selectOption('disabled');
    assert.equal(await page.locator('#acctBody tr').count(), 1);
    assert.match(await page.locator('#acctBody').innerText(), /维护账号/);
    await page.locator('#acctStatus').selectOption('');
    assert.match(await page.locator('#acctBody').innerText(), /并发|[23]\s*\/\s*5/);
    assert.match(await page.locator('#acctBody').innerText(), /旧记录|时间未知/);
    await page.locator('#acctBody [data-act="disable"]').first().click();
    await page.waitForFunction(() => document.getElementById('toast')?.textContent.includes('Fixture operation'));
    assert.ok(state.calls.some(call => call.path.endsWith('/accounts/disable') && call.body.uid === 'fixture-account-01'));
    for (const id of ['btnCredits', 'btnCheckin', 'btnKeepalive', 'btnReload']) { await page.locator('#' + id).click(); }
    assert.ok(state.calls.some(call => call.path.endsWith('/checkin')), 'check-in remains check-in');
    const add = page.locator('#btnAddAccount');
    await add.click(); await visible(page, '#oauthModal');
    await page.locator('#oauthClose').focus();
    await page.keyboard.press('Shift+Tab');
    assert.equal(await page.locator('#oauthModal').evaluate(el => el.contains(document.activeElement)), true, 'dialog wraps backward focus');
    await page.keyboard.press('Tab');
    assert.equal(await page.evaluate(() => document.activeElement?.id), 'oauthClose', 'dialog wraps forward focus');
    await page.keyboard.press('Escape');
    await page.locator('#oauthModal').waitFor({ state: 'hidden' });
    assert.equal(await page.evaluate(() => document.activeElement?.id), 'btnAddAccount');
    await add.click();
    context.once('page', popup => popup.close());
    await page.locator('#oauthStart').click();
    await page.locator('#oauthCallbackUrl').fill('http://127.0.0.1:7866/oauth/callback?code=fixture-code&state=fixture-state');
    await page.locator('#oauthImport').click();
    await page.waitForFunction(() => document.getElementById('oauthStatus')?.textContent.includes('Fixture callback') || document.getElementById('oauthStatus')?.textContent.includes('Fixture authorization complete'));
    assert.ok(state.calls.some(call => call.path.endsWith('/oauth/import-callback') && call.body.session_id === 'fixture-oauth-session'));
    await page.locator('#oauthClose').click();

    await navigate(page, 'requests');
    await visible(page, '#reqBody tr');
    assert.match(await page.locator('#reqBody').innerText(), /200/);
    assert.match(await page.locator('#reqBody').innerText(), /InferHub\.4004\.200/);
    await page.locator('#btnReqMore').click();
    await page.waitForFunction(() => document.getElementById('reqBody')?.textContent.includes('504'));
    await page.locator('#btnReqMore').click();
    await page.waitForFunction(() => document.getElementById('reqBody')?.textContent.includes('未提交'));
    assert.equal(await page.locator('#reqBody img').count(), 0);
    assert.equal(await page.evaluate(() => window.__fixtureXSS), undefined);
    let filteredResponse = page.waitForResponse(response => response.url().includes('/admin/api/requests') && new URL(response.url()).searchParams.get('model') === 'deepseek');
    await page.locator('#reqModel').fill('deepseek'); await page.locator('#reqModel').press('Enter');
    await filteredResponse;
    assert.ok(state.calls.some(call => call.path.endsWith('/requests') && call.query.includes('model=deepseek')));
    filteredResponse = page.waitForResponse(response => response.url().includes('/admin/api/requests') && !new URL(response.url()).searchParams.get('model'));
    await page.locator('#reqModel').fill(''); await page.locator('#reqModel').press('Enter'); await filteredResponse;
    filteredResponse = page.waitForResponse(response => response.url().includes('/admin/api/requests') && new URL(response.url()).searchParams.get('account') === 'fixture-account-01');
    await page.locator('#reqAccount').fill('fixture-account-01'); await page.locator('#reqAccount').press('Enter');
    await filteredResponse;
    assert.ok(state.calls.some(call => call.path.endsWith('/requests') && call.query.includes('account=fixture-account-01')));
    filteredResponse = page.waitForResponse(response => response.url().includes('/admin/api/requests') && !new URL(response.url()).searchParams.get('account'));
    await page.locator('#reqAccount').fill(''); await page.locator('#reqAccount').press('Enter'); await filteredResponse;
    filteredResponse = page.waitForResponse(response => response.url().includes('/admin/api/requests') && new URL(response.url()).searchParams.get('outcome') === 'timeout');
    await page.locator('#reqOutcome').selectOption('timeout'); await filteredResponse;
    await page.waitForFunction(() => document.getElementById('reqBody')?.textContent.includes('504'));
    filteredResponse = page.waitForResponse(response => response.url().includes('/admin/api/requests') && !new URL(response.url()).searchParams.get('outcome'));
    await page.locator('#reqOutcome').selectOption(''); await filteredResponse;
    filteredResponse = page.waitForResponse(response => response.url().includes('/admin/api/requests') && new URL(response.url()).searchParams.get('status') === '504');
    await page.locator('#reqStatus').fill('504'); await page.locator('#reqStatus').press('Enter'); await filteredResponse;
    await page.waitForFunction(() => document.getElementById('reqBody')?.textContent.includes('fixture-request-0004'));
    assert.equal(await page.locator('#reqBody tr').count(), 1);
    filteredResponse = page.waitForResponse(response => response.url().includes('/admin/api/requests') && !new URL(response.url()).searchParams.get('status'));
    await page.locator('#reqStatus').fill(''); await page.locator('#reqStatus').press('Enter'); await filteredResponse;

    await visible(page, '#btnReqMore');
    state.mode = 'audit-error';
    const failedFilter = page.waitForResponse(response => response.url().includes('/admin/api/requests') && new URL(response.url()).searchParams.get('model') === 'qwen3' && response.status() === 503);
    await page.locator('#reqModel').fill('qwen3'); await page.locator('#reqModel').press('Enter'); await failedFilter;
    await visible(page, '#reqError');
    assert.doesNotMatch(await page.locator('#reqBody').innerText(), /fixture-request-000[56]/, 'failed new query clears rows from the previous filter');
    assert.equal(await page.locator('#btnReqMore').isVisible(), false, 'failed new query cannot paginate with an old cursor');
    state.mode = 'normal';
    const recoveredFilter = page.waitForResponse(response => response.url().includes('/admin/api/requests') && new URL(response.url()).searchParams.get('model') === 'qwen3' && !new URL(response.url()).searchParams.has('before'));
    await page.locator('#btnReqRefresh').click(); await recoveredFilter;
    await page.waitForFunction(() => document.getElementById('reqBody')?.textContent.includes('fixture-request-0003'));
    assert.doesNotMatch(await page.locator('#reqBody').innerText(), /fixture-request-000[56]/);
    filteredResponse = page.waitForResponse(response => response.url().includes('/admin/api/requests') && !new URL(response.url()).searchParams.has('model'));
    await page.locator('#reqModel').fill(''); await page.locator('#reqModel').press('Enter'); await filteredResponse;
    await page.waitForFunction(() => document.getElementById('reqBody')?.textContent.includes('fixture-request-0006'));

    state.mode = 'audit-error';
    const failedAudit = page.waitForResponse(response => response.url().includes('/admin/api/requests') && response.status() === 503);
    await page.locator('#btnReqRefresh').click(); await failedAudit;
    await page.waitForFunction(() => document.body.textContent.includes('Fixture temporary audit error'));
    assert.match(await page.locator('#reqBody').innerText(), /fixture-request-0006/, 'same-query refresh failure preserves its rows');
    await screenshot(page, 'requests-error');
    state.mode = 'normal';
    await page.locator('#btnReqRefresh').click();

    await navigate(page, 'settings');
    assert.match(await page.locator('[data-view="settings"]').innerText(), /90s/);
    await page.locator('[data-copy-from="settingsBaseUrl"]').click();
    await expectClipboard(page, origin + '/v1');
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), origin + '/v1');
    state.mode = 'error';
    const failedOverview = page.waitForResponse(response => response.url().includes('/admin/api/overview') && response.status() === 503);
    await page.locator('#btnRefresh').click(); await failedOverview;
    await page.waitForFunction(() => document.body.textContent.includes('Fixture temporary overview error'));
    assert.equal(await page.locator('#sTotal').textContent(), '4', 'failed refresh preserves displayed data');
    state.mode = 'normal';
    await page.locator('#themeToggle').click();
    await screenshot(page, 'settings-dark');
    await page.reload();
    await visible(page, '[data-view="settings"]');
    await screenshot(page, 'settings-dark-reloaded');
    await page.locator('#themeToggle').click();

    await page.setViewportSize({ width: 390, height: 844 });
    for (const view of ['overview', 'accounts', 'requests', 'models', 'settings']) {
      await navigate(page, view); await screenshot(page, view + '-mobile'); await noOverflow(page, 'mobile ' + view);
    }
    await navigate(page, 'overview'); await navigate(page, 'overview');
    await page.locator('#sidebarToggle').click(); await screenshot(page, 'navigation-mobile');
    await page.keyboard.press('Escape');
    await noOverflow(page, 'mobile navigation closed');

    state.mode = 'empty';
    await page.setViewportSize({ width: 1440, height: 1024 });
    await page.reload(); await visible(page, '#appView');
    await page.waitForFunction(() => document.getElementById('sTotal')?.textContent === '0');
    for (const view of ['overview', 'accounts', 'requests', 'models', 'settings']) { await navigate(page, view); await noOverflow(page, 'empty ' + view); }
    assert.match(await page.locator('#buildBadge').innerText(), /未知/);
    await screenshot(page, 'settings-empty');
    await navigate(page, 'requests'); assert.match(await page.locator('#reqBody').innerText(), /审计已关闭/);
    state.mode = 'normal';
    await page.goto(origin + '/proxy/panel/#accounts');
    await visible(page, '[data-view="accounts"]');
    assert.ok(state.calls.some(call => call.path === '/proxy/admin/api/overview'));
    await page.goto(origin + '/admin#unknown');
    await visible(page, '[data-view="overview"]');
    await navigate(page, 'requests');
    await page.waitForFunction(() => document.getElementById('reqBody')?.textContent.includes('fixture-request-0006'));
    await navigate(page, 'overview');
    state.mode = 'overview-delay';
    const overviewStarted = new Promise(resolve => { state.overviewStarted = resolve; });
    const delayedOverview = page.waitForResponse(response => response.url().includes('/admin/api/overview'));
    await page.locator('#btnRefresh').click(); await overviewStarted;
    await page.locator('#btnLogout').click(); await visible(page, '#loginView');
    const logoutCallCount = state.calls.length;
    state.mode = 'normal'; state.releaseOverview(); await delayedOverview;
    await page.waitForFunction(() => !document.getElementById('btnRefresh').disabled);
    assert.ok(state.calls.slice(logoutCallCount).every(call => !call.path.endsWith('/requests')), 'completed pre-logout refresh does not start an audit request');
    assert.equal(await page.locator('#toast').textContent(), '已退出', 'stale refresh does not overwrite logout feedback');
    assert.equal(await page.evaluate(() => localStorage.getItem('codearts2api_panel_api_key')), null);
    assert.deepEqual(errors, [], 'no uncaught browser errors');
    assert.deepEqual(external, [], 'no external assets or requests');
    console.log(JSON.stringify({ passed: true, checks: ['real Chrome login/logout and in-flight refresh suppression', 'five hash-routed views', 'desktop/mobile document overflow', 'account search/status/actions and legacy history', 'model search and exact real clipboard copy', 'OAuth start/import/Escape/focus trapping and return', 'request cursor/model/account/outcome/status filters', 'failed filter resets cursor and preserves only same-query rows', 'committed 200 failure and absent status', 'HTML escaping', 'light/dark persistence', 'mobile navigation', 'empty/unknown/disabled states', 'proxy base path and admin alias', 'no external assets or uncaught errors'], screenshots: output }));
  } catch (error) {
    await screenshot(page, 'failure').catch(() => {});
    console.error(JSON.stringify({ browserErrors: errors, externalRequests: external, lastCalls: state.calls.slice(-8), failureScreenshot: path.join(output, 'failure.png') }));
    throw error;
  } finally {
    state.releaseOverview?.();
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); server.close(); process.exitCode = 1; });
