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
const output = process.env.UI_OUTPUT_DIR || path.join(root, '.local-test', 'oauth-full-ui-' + Date.now());
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
const state = { mode: 'normal', calls: [], oauthDone: false, oauthSessionValid: false, releaseOverview: null, overviewStarted: null, refresh: null, refreshReads: 0, refreshStarts: 0, pulled: false, holdRoute: null, holdMethod: null, releaseSnapshot: null, snapshotStarted: null };
function modelSnapshot() {
  const empty = state.mode === 'empty';
  const partial = state.mode === 'models-partial';
  const disabled = state.mode === 'models-benefit-disabled';
  const business = ['models-business', 'models-business-unsafe'].includes(state.mode);
  const httpError = state.mode === 'models-http-error';
  const sourceFailed = partial || business || httpError;
  const sourceError = partial ? { kind: 'timeout' } : business ? { kind: 'business', provider_code: state.mode === 'models-business-unsafe' ? malicious : '4000' } : httpError ? { kind: 'http', http_status: 503 } : null;
  const list = empty ? [] : [...models, ...(state.pulled ? [{ id: 'fixture-new-model-after-refresh' }] : [])];
  return {
    models: list.map(({ id }) => ({ id, origin: 'discovered', catalog_accounts: 2, benefit_accounts: !disabled && id.includes('flash') ? 1 : 0, blocked_accounts: id === 'deepseek-v4.1' ? 1 : 0 })),
    accounts: empty ? [] : [{ account: 'fixture-account-01', eligible: true, last_success_at: stamp, sources: [
      { source: 'agent', status: 'ok', last_attempt_at: stamp, last_success_at: stamp, count: 8, retained: 0 },
      { source: 'builtin', status: 'ok', last_attempt_at: stamp, last_success_at: stamp, count: 7, retained: 0 },
      { source: 'benefit', status: disabled ? 'disabled' : sourceFailed ? 'failed' : 'ok', last_attempt_at: stamp, last_success_at: stamp, retry_at: sourceFailed ? new Date(Date.now() + 300000).toISOString() : null, count: disabled || business || httpError ? 0 : 3, retained: partial ? 3 : 0, ...(sourceError ? { error: sourceError } : {}) },
    ], claim: disabled ? { enabled: true, attempted: false, skipped_reason: 'channel_disabled' } : partial ? { enabled: true, attempted: true, attempted_at: stamp, error: { kind: 'timeout' } } : { enabled: true, attempted: false } }],
    exclusions: empty ? [] : [{ account: 'fixture-account-02', model: 'deepseek-v4.1', reason: 'not_registered', recorded_at: stamp, expires_at: new Date(Date.now() + 1800000).toISOString() }],
    refresh: state.refresh || { id: '', state: 'idle', total: 0, completed: 0, failed: 0, skipped: 0, negative_reset_count: 0 },
    truncated: { accounts: 0, models: 0, exclusions: 0 },
  };
}
function connectionSnapshot() {
  if (state.mode === 'empty') return { active: [], recent: [], disabled: true, capacity: 512, overflow: 0, trace_truncated: 0 };
  return {
    active: [
      { id: 'fixture-live-headers', ts: stamp, model: 'deepseek-v4.1-flash', model_canonical: 'deepseek-v4.1-flash', stream: true, account: 'fixture-account-01', attempt: 1, phase: 'wait_headers', phase_age_ms: 15000, total_ms: 15120, last_upstream_progress_age_ms: null, first_sse_data_ms: null, timeline: [{ phase: 'wait_account', at_ms: 0, attempt: 0 }, { phase: 'wait_headers', at_ms: 120, attempt: 1, account: 'fixture-account-01' }], trace_truncated: false },
      { id: 'fixture-live-stream', ts: stamp, model: malicious, model_canonical: malicious, stream: true, account: 'fixture-account-02', attempt: 2, phase: 'stream', phase_age_ms: 180000, total_ms: 181000, last_upstream_progress_age_ms: 120, first_sse_data_ms: 1000, timeline: [{ phase: 'wait_headers', at_ms: 100, attempt: 1 }, { phase: 'wait_first_sse', at_ms: 800, attempt: 2 }, { phase: 'stream', at_ms: 1000, attempt: 2 }], trace_truncated: false },
    ],
    recent: [{ ...events[0], trace: { timeline: [{ phase: 'wait_account', at_ms: 0, attempt: 0 }, { phase: 'wait_first_sse', at_ms: 200, attempt: 1 }, { phase: 'stream', at_ms: 326, attempt: 1 }, { phase: 'cleanup', at_ms: 1823, attempt: 1 }, { phase: 'finished', at_ms: 1824, attempt: 1 }], first_sse_data_ms: 326, last_upstream_progress_ms: 1800, truncated: false } }, events[1]],
    disabled: false, capacity: 512, overflow: 0, trace_truncated: 0,
  };
}
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
  if (route === state.holdRoute && req.method === state.holdMethod) await new Promise(resolve => {
    state.releaseSnapshot = () => { state.releaseSnapshot = null; resolve(); };
    state.snapshotStarted?.();
  });
  if (route === '/admin/api/models/refresh' && req.method === 'POST') {
    state.refreshStarts++;
    state.refreshReads = 0;
    state.refresh = { id: 'fixture-pull-' + state.refreshStarts, state: 'running', started_at: new Date().toISOString(), finished_at: null, retry_at: null, total: 1, completed: 0, failed: 0, skipped: 0, negative_reset_count: 0 };
    json(202, { refresh: state.refresh }); return;
  }
  if (route === '/admin/api/models') {
    if (state.mode === 'models-error') { json(503, { error: { message: 'Fixture temporary model source error' } }); return; }
    if (state.refresh?.state === 'running' && ++state.refreshReads >= 2) {
      state.pulled = true;
      state.refresh = { ...state.refresh, state: state.mode === 'models-partial' ? 'partial' : 'completed', completed: 1, failed: state.mode === 'models-partial' ? 1 : 0, finished_at: new Date().toISOString(), negative_reset_count: 1 };
    }
    json(200, modelSnapshot()); return;
  }
  if (route === '/admin/api/connections') {
    if (state.mode === 'connections-error') { json(503, { error: { message: 'Fixture temporary connection log error' } }); return; }
    json(200, connectionSnapshot()); return;
  }
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
  if (route === '/admin/api/oauth/start') { state.oauthSessionValid = true; state.oauthDone = false; json(200, { ok: true, session_id: 'fixture-oauth-session', auth_url: origin + '/fixture-authorize' }); return; }
  if (route === '/admin/api/oauth/poll') { json(state.oauthSessionValid ? 200 : 404, state.oauthSessionValid ? (state.oauthDone ? { ok: true, status: 'done', message: 'Fixture authorization complete' } : { ok: true, status: 'pending', message: 'Fixture authorization pending' }) : { ok: false, message: 'Fixture session no longer exists' }); return; }
  if (route === '/admin/api/oauth/import-callback') { state.oauthSessionValid = false; json(200, { ok: true, status: 'done', message: 'Fixture callback complete' }); return; }
  json(200, { ok: true, message: 'Fixture operation complete' });
});

async function visible(page, selector) { await page.locator(selector).first().waitFor({ state: 'visible', timeout: 8000 }); }
// This console owns viewport-sized shells with internal scroll containers.
// Chrome fullPage capture includes clipped body overflow as blank canvas.
async function screenshot(page, name) { await page.screenshot({ path: path.join(output, name + '.png'), fullPage: false }); }
async function expectClipboard(page, value) { await page.waitForFunction(async expected => await navigator.clipboard.readText() === expected, value); }
async function noOverflow(page, label) {
  const size = await page.evaluate(() => {
    const before = { page: document.documentElement.scrollWidth, viewport: innerWidth, pageHeight: document.documentElement.scrollHeight, viewportHeight: innerHeight };
    window.scrollTo(0, document.documentElement.scrollHeight);
    before.rootScroll = window.scrollY;
    window.scrollTo(0,0);
    return before;
  });
  assert.ok(size.page <= size.viewport + 1, `${label}: horizontal document overflow ${JSON.stringify(size)}`);
  assert.ok(size.pageHeight <= size.viewportHeight + 1 && size.rootScroll === 0, `${label}: vertical document overflow outside the panel scroller ${JSON.stringify(size)}`);
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
    assert.equal(await page.locator('.sidebar-footer, #sidebarBuild, #subTitle, #btnAddAccount2').count(), 0, 'redundant sidebar/header/shortcut nodes are removed');
    assert.doesNotMatch(await page.locator('[data-view="overview"]').innerText(), /工作台快捷入口/);
    const radii = await page.evaluate(() => ['.workspace', '.stat', '.nav-item', '.btn', '.login-box', '.modal'].map(selector => ({ selector, radius: parseFloat(getComputedStyle(document.querySelector(selector)).borderTopLeftRadius) })));
    for (const { selector, radius } of radii) assert.ok(radius <= 16, `${selector}: compact radius (${radius}px)`);
    await screenshot(page, 'overview-desktop');
    await noOverflow(page, 'desktop overview');
    for (const view of ['accounts', 'requests', 'models', 'connections', 'settings']) {
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
    const beforePull = state.refreshStarts;
    const pulledResponse = page.waitForResponse(response => response.url().endsWith('/admin/api/models/refresh') && response.request().method() === 'POST');
    await page.locator('#btnModelsRefresh').click(); await pulledResponse;
    await page.locator('#btnModelsRefresh').evaluate(button => { button.click(); button.click(); });
    await page.waitForFunction(() => document.getElementById('modelChips')?.textContent.includes('fixture-new-model-after-refresh'));
    assert.equal(state.refreshStarts, beforePull + 1, 'duplicate clicks cannot start concurrent pulls');
    assert.match(await page.locator('#modelSourceDetails').innerText(), /benefit|福利/);
    assert.match(await page.locator('#modelExclusionBody').innerText(), /deepseek-v4\.1/);
    await screenshot(page, 'model-pull-completed');
    await page.locator('#modelSourceDetails').scrollIntoViewIfNeeded();
    await screenshot(page, 'model-source-results');
    await navigate(page, 'accounts');
    const stoppedCatalogCalls = state.calls.filter(call => call.path.endsWith('/admin/api/models')).length;
    await page.waitForTimeout(2300);
    assert.equal(state.calls.filter(call => call.path.endsWith('/admin/api/models')).length, stoppedCatalogCalls, 'catalog polling stops off-view');
    state.mode = 'models-error';
    await navigate(page, 'models'); await visible(page, '#modelCatalogError');
    assert.match(await page.locator('#modelChips').innerText(), /fixture-new-model-after-refresh/, 'a failed snapshot retains existing catalog with visible error');
    state.mode = 'models-partial';
    await navigate(page, 'accounts'); await navigate(page, 'models');
    await visible(page, '#btnModelsRefresh');
    await page.locator('#btnModelsRefresh').click();
    await page.waitForFunction(() => document.getElementById('modelSourceDetails')?.textContent.includes('timeout') || document.getElementById('modelSourceDetails')?.textContent.includes('超时'));
    await visible(page, '#modelCatalogWarning');
    assert.match(await page.locator('#modelChips').innerText(), /deepseek-v4\.1-flash/);
    assert.match(await page.locator('#modelSourceDetails').innerText(), /本次领取失败/, 'a failed claim omits succeeded=false in the backend JSON and must remain a known failure');
    await page.waitForFunction(() => document.getElementById('modelRefreshStatus')?.textContent.includes('拉取部分失败'));
    await screenshot(page, 'model-partial-failure');

    state.mode = 'models-benefit-disabled';
    await navigate(page, 'accounts'); await navigate(page, 'models');
    await page.waitForFunction(() => document.getElementById('modelSourceDetails')?.textContent.includes('本次跳过领取'));
    await page.locator('#btnModelsRefresh').click();
    await page.waitForFunction(() => document.getElementById('modelRefreshStatus')?.textContent.includes('拉取完成'));
    const benefitSource = page.locator('#modelSourceDetails td:nth-child(2) .source-entry').filter({ hasText: '福利目录' });
    assert.match(await benefitSource.innerText(), /福利通道已关闭/);
    assert.match(await benefitSource.innerText(), /使用 Agent \/ 内置目录/);
    assert.match(await benefitSource.innerText(), /当前 0 条 · 保留 0 条/);
    assert.doesNotMatch(await benefitSource.innerText(), /拉取失败|重试|HTTP|业务错误|保留旧数据/);
    assert.equal(await benefitSource.locator('.badge.warn').count(), 0, 'a confirmed closed channel is not a failing source');
    assert.equal(await page.locator('#modelCatalogWarning').isHidden(), true, 'a closed benefit channel does not warn that the pull failed');
    assert.equal(await page.locator('#modelCatalogError').isHidden(), true);
    const benefitTime = page.locator('#modelSourceDetails td:nth-child(3) .source-entry').filter({ hasText: '福利目录' });
    assert.match(await benefitTime.innerText(), /确认关闭/);
    assert.doesNotMatch(await benefitTime.innerText(), /成功/, 'a disabled source confirms closure rather than claiming a model listing succeeded');
    const claimResult = page.locator('#modelSourceDetails td:nth-child(3) .source-entry').last();
    assert.match(await claimResult.innerText(), /自动领取已开启/);
    assert.match(await claimResult.innerText(), /福利通道已关闭，本次跳过领取/);
    assert.doesNotMatch(await claimResult.innerText(), /领取失败|结果未知|领取成功/);
    assert.match(await page.locator('#modelChips').innerText(), /deepseek-v4\.1-flash/);
    await page.locator('#modelSourceDetails').scrollIntoViewIfNeeded();
    await screenshot(page, 'model-benefit-disabled');

    state.mode = 'models-business';
    await navigate(page, 'accounts'); await navigate(page, 'models');
    await page.waitForFunction(() => document.getElementById('modelSourceDetails')?.textContent.includes('通道无效或分组不存在'));
    assert.match(await benefitSource.innerText(), /上游业务错误 · 4000（通道无效或分组不存在）/);
    assert.doesNotMatch(await benefitSource.innerText(), /HTTP|福利通道已关闭|账号无权限/);
    assert.match(await benefitSource.innerText(), /下次重试/);
    await visible(page, '#modelCatalogWarning');
    await page.locator('#modelSourceDetails').scrollIntoViewIfNeeded();
    await screenshot(page, 'model-benefit-business-error');

    state.mode = 'models-business-unsafe';
    await navigate(page, 'accounts'); await navigate(page, 'models');
    await page.waitForFunction(() => document.getElementById('modelSourceDetails')?.textContent.includes('<img'));
    assert.match(await benefitSource.innerText(), /<img/);
    assert.equal(await page.locator('#modelSourceDetails img').count(), 0, 'provider codes are rendered as escaped text');
    assert.equal(await page.evaluate(() => window.__fixtureXSS), undefined);

    state.mode = 'models-http-error';
    await navigate(page, 'accounts'); await navigate(page, 'models');
    await page.waitForFunction(() => document.getElementById('modelSourceDetails')?.textContent.includes('HTTP 503'));
    assert.match(await benefitSource.innerText(), /上游 HTTP 错误 · HTTP 503/);
    assert.doesNotMatch(await benefitSource.innerText(), /业务错误|通道无效|福利通道已关闭/);
    await visible(page, '#modelCatalogWarning');
    await page.locator('#modelSourceDetails').scrollIntoViewIfNeeded();
    await screenshot(page, 'model-benefit-http-error');
    state.mode = 'normal';
    await navigate(page, 'connections'); await visible(page, '#connActiveBody tr');
    assert.match(await page.locator('#connActiveBody').innerText(), /fixture-live-headers/);
    assert.match(await page.locator('#connActiveBody').innerText(), /fixture-live-stream/);
    assert.equal(await page.locator('#connActiveBody img').count(), 0);
    assert.match(await page.locator('#connRecentBody').innerText(), /fixture-request-0006/);
    await page.locator('#connActiveBody .connection-trace summary').first().click();
    assert.match(await page.locator('#connActiveBody .connection-trace[open]').innerText(), /等待上游响应头/);
    await screenshot(page, 'connections-active');
    const connectionCalls = state.calls.filter(call => call.path.endsWith('/admin/api/connections')).length;
    await page.waitForFunction(() => document.getElementById('connActiveBody')?.textContent.includes('fixture-live-stream'));
    await page.waitForTimeout(2300);
    assert.ok(state.calls.filter(call => call.path.endsWith('/admin/api/connections')).length > connectionCalls, 'active connection view polls live state');
    assert.equal(await page.locator('#connActiveBody .connection-trace[open]').count(), 1, 'expanded trace survives polling');
    // Exercise the actual visibility listener with a controlled browser property.
    // Headless Chrome does not consistently hide an unfocused tab.
    await page.evaluate(() => { Object.defineProperty(document, 'hidden', { configurable: true, get: () => true }); document.dispatchEvent(new Event('visibilitychange')); });
    const hiddenConnectionCalls = state.calls.filter(call => call.path.endsWith('/admin/api/connections')).length;
    await page.waitForTimeout(2300);
    assert.equal(state.calls.filter(call => call.path.endsWith('/admin/api/connections')).length, hiddenConnectionCalls, 'hidden connection view stops polling');
    await page.evaluate(() => { delete document.hidden; document.dispatchEvent(new Event('visibilitychange')); });
    await page.waitForResponse(response => response.url().endsWith('/admin/api/connections'));
    state.mode = 'connections-error';
    await visible(page, '#connError');
    state.mode = 'normal';
    await navigate(page, 'accounts');
    const stoppedConnectionCalls = state.calls.filter(call => call.path.endsWith('/admin/api/connections')).length;
    await page.waitForTimeout(2300);
    assert.equal(state.calls.filter(call => call.path.endsWith('/admin/api/connections')).length, stoppedConnectionCalls, 'connection polling stops off-view');
    state.refresh = { ...state.refresh, state: 'running' }; state.refreshReads = -100;
    await navigate(page, 'models');
    await page.waitForFunction(() => document.getElementById('btnModelsRefresh').disabled);
    await page.evaluate(() => { Object.defineProperty(document, 'hidden', { configurable: true, get: () => true }); document.dispatchEvent(new Event('visibilitychange')); });
    const hiddenCatalogCalls = state.calls.filter(call => call.path.endsWith('/admin/api/models')).length;
    await page.waitForTimeout(2300);
    assert.equal(state.calls.filter(call => call.path.endsWith('/admin/api/models')).length, hiddenCatalogCalls, 'hidden model view stops a running job poll');
    await page.evaluate(() => { delete document.hidden; document.dispatchEvent(new Event('visibilitychange')); });
    await page.waitForResponse(response => response.url().endsWith('/admin/api/models'));
    state.refresh = { ...state.refresh, state: 'completed' };
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
    await page.locator('#oauthStart').click();
    await page.waitForFunction(() => document.getElementById('oauthStart')?.textContent === '提交授权');
    assert.equal(await page.locator('#oauthCopy').innerText(), '复制授权链接');
    assert.equal(await page.locator('#oauthImport,#oauthPoll').count(), 0);
    await page.locator('#oauthCallbackUrl').fill('http://127.0.0.1:7866/oauth/callback?code=fixture-code&state=fixture-state');
    await page.locator('#oauthStart').click();
    await page.waitForFunction(() => document.getElementById('oauthStatus')?.textContent.includes('Fixture callback') || document.getElementById('oauthStatus')?.textContent.includes('Fixture authorization complete'));
    assert.ok(state.calls.some(call => call.path.endsWith('/oauth/import-callback') && call.body.session_id === 'fixture-oauth-session'));
    await page.locator('#oauthModal').waitFor({ state: 'hidden' });

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
    for (const view of ['overview', 'accounts', 'requests', 'models', 'connections', 'settings']) {
      await navigate(page, view); await screenshot(page, view + '-mobile'); await noOverflow(page, 'mobile ' + view);
    }
    await navigate(page, 'models');
    await page.locator('#modelSourceDetails').scrollIntoViewIfNeeded();
    const modelScroll = await page.evaluate(() => {
      const sources = document.getElementById('modelSourceDetails'), tableScroll = sources.closest('.table-scroll');
      const before = { mainScroll:document.querySelector('.page-content').scrollTop, rootScroll:window.scrollY, sourceTop:sources.getBoundingClientRect().top };
      tableScroll.scrollLeft = tableScroll.scrollWidth;
      before.tableScroll = tableScroll.scrollLeft;
      tableScroll.scrollLeft = 0;
      return before;
    });
    assert.ok(modelScroll.mainScroll > 0 && modelScroll.tableScroll > 0 && modelScroll.rootScroll === 0 && modelScroll.sourceTop < 844, 'mobile source details and table remain scrollable inside the fixed viewport');
    await noOverflow(page, 'mobile source scrolled');
    await screenshot(page, 'model-source-mobile');
    state.mode = 'models-benefit-disabled';
    await navigate(page, 'accounts'); await navigate(page, 'models');
    await page.waitForFunction(() => document.getElementById('modelSourceDetails')?.textContent.includes('本次跳过领取'));
    await page.locator('#modelSourceDetails').scrollIntoViewIfNeeded();
    await noOverflow(page, 'mobile disabled benefit source');
    await screenshot(page, 'model-benefit-disabled-mobile');
    state.mode = 'normal';
    await navigate(page, 'overview'); await navigate(page, 'overview');
    await page.locator('#sidebarToggle').click(); await screenshot(page, 'navigation-mobile');
    await page.keyboard.press('Escape');
    await noOverflow(page, 'mobile navigation closed');

    state.mode = 'empty';
    await page.setViewportSize({ width: 1440, height: 1024 });
    await page.reload(); await visible(page, '#appView');
    await page.waitForFunction(() => document.getElementById('sTotal')?.textContent === '0');
    for (const view of ['overview', 'accounts', 'requests', 'models', 'connections', 'settings']) { await navigate(page, view); await noOverflow(page, 'empty ' + view); }
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
    for (const test of [
      { view: 'connections', method: 'GET', route: '/admin/api/connections' },
      { view: 'models', method: 'GET', route: '/admin/api/models' },
      { view: 'models', method: 'POST', route: '/admin/api/models/refresh' },
    ]) {
      await page.locator('#keyInput').fill('fixture-ui-key'); await page.locator('#loginBtn').click(); await visible(page, '#appView');
      await navigate(page, 'accounts');
      state.holdRoute = test.route; state.holdMethod = test.method;
      const started = new Promise(resolve => { state.snapshotStarted = resolve; });
      if (test.method === 'POST') {
        await navigate(page, test.view);
        await page.waitForFunction(() => !document.getElementById('btnModelsRefresh').disabled);
        await page.locator('#btnModelsRefresh').click();
      } else await navigate(page, test.view);
      await started;
      await page.locator('#btnLogout').click(); await visible(page, '#loginView');
      const afterLogout = state.calls.length;
      state.holdRoute = state.holdMethod = null; state.releaseSnapshot();
      await page.waitForTimeout(2300);
      assert.equal(state.calls.length, afterLogout, test.route + ': logout cancels polling and late follow-ups');
      assert.equal(await page.locator('#toast').textContent(), '已退出');
      assert.doesNotMatch(await page.locator('#connActiveBody').innerText(), /fixture-live/);
      assert.doesNotMatch(await page.locator('#modelSourceDetails').innerText(), /fixture-account/);
      state.refresh = { ...state.refresh, state: 'completed' };
    }
    assert.deepEqual(errors, [], 'no uncaught browser errors');
    assert.deepEqual(external, [], 'no external assets or requests');
    console.log(JSON.stringify({ passed: true, checks: ['real Chrome login/logout and in-flight overview/model/connection suppression', 'six hash-routed views', 'desktop/mobile document overflow and compact corners', 'removed sidebar/header/shortcut content', 'real model-pull POST with duplicate suppression and partial/error retention', 'model source/exclusion diagnostics', 'disabled benefit source without failure, retry or claim attempt', 'business code 4000 distinct from HTTP 503 and provider-code escaping', 'live connection polling, phase expansion and legacy completed traces', 'off-view/hidden/logout polling stops', 'account search/status/actions and legacy history', 'model search and exact real clipboard copy', 'OAuth start/import/Escape/focus trapping and return', 'request cursor/model/account/outcome/status filters', 'failed filter resets cursor and preserves only same-query rows', 'committed 200 failure and absent status', 'HTML escaping', 'light/dark persistence', 'mobile navigation', 'empty/unknown/disabled states', 'proxy base path and admin alias', 'no external assets or uncaught errors'], screenshots: output }));
  } catch (error) {
    await screenshot(page, 'failure').catch(() => {});
    console.error(JSON.stringify({ browserErrors: errors, externalRequests: external, lastCalls: state.calls.slice(-8), failureScreenshot: path.join(output, 'failure.png') }));
    throw error;
  } finally {
    state.releaseOverview?.();
    state.releaseSnapshot?.();
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); server.close(); process.exitCode = 1; });
