// Actual Chrome against synthetic localhost APIs. No real accounts or service keys.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { chromium } = require(process.env.UI_PLAYWRIGHT_MODULE || path.join(os.tmpdir(), 'codearts2api-ui-verification/node_modules/playwright'));
let root = __dirname;
while (!fs.existsSync(path.join(root, 'go.mod'))) root = path.dirname(root);
const html = fs.readFileSync(path.join(root, 'internal/server/panel.html'), 'utf8')
  .replaceAll('__SERVICE_NAME__', 'codearts2api').replaceAll('__SERVICE_TITLE__', 'CodeArts2API').replaceAll('__LOGO__', 'CA').replaceAll('__ACCENT__', '#0284c7');
const output = path.join(root, '.local-test', 'token-ui-' + Date.now());
fs.mkdirSync(output, { recursive: true });
const stamp = '2026-10-24T00:00:00Z';
const malicious = '<img src=x onerror="window.__usageXSS=1">';
const overview = { service: 'codearts2api', stats: { total: 1, healthy: 1, cooling: 0, disabled: 0 },
  accounts: [{ uid: 'synthetic-account', name: 'synthetic-account', active_concurrent: 0, max_concurrent: 1, token_remaining: '59m0s' }],
  models: [{ id: 'glm-5.3' }], audit: { enabled: true, recent: 512 }, schedule: {}, effective: {} };
const totals = { requests: 5, with_usage: 4, upstream_requests: 3, estimated_requests: 1, unknown_requests: 1,
  prompt_tokens: 110, completion_tokens: 210, total_tokens: 315, prompt_known: 3, completion_known: 4, total_known: 3, overflowed: false };
const usage = { scope: 'recent_window', window: 5, disabled: false, from: stamp, to: stamp, ...totals,
  by_account: [{ key: malicious, ...totals }], by_model: [{ key: 'glm-5.3', ...totals }], accounts_truncated: 0, models_truncated: 0 };
const event = (seq, value) => ({ seq, id: 'synthetic-' + seq, ts: stamp, model: 'glm-5.3', account: 'synthetic-account',
  committed: true, status: 200, outcome: 'ok', stream: true, attempts: 1, total_ms: 123, queue_ms: 0, first_data_ms: 10,
  cause: { class: 'none' }, ...(value ? { usage: value } : {}) });
const events = [event(5, { source: 'upstream', prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }),
  event(4, { source: 'estimated', prompt_tokens: 100, completion_tokens: 200, total_tokens: 300 }),
  event(3, { source: 'upstream', prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 }),
  event(2, { source: 'upstream', prompt_tokens: null, completion_tokens: 5, total_tokens: null }), event(1)];
const state = { mode: 'normal', calls: [], hold: null, started: null, release: null };
function snapshot(url) {
  const model = url.searchParams.get('model');
  if (state.mode === 'disabled') return { events: [], disabled: true, window: 0, usage: { ...usage, disabled: true } };
  if (state.mode === 'empty') return { events: [], window: 512, usage: { ...usage, requests: 0, with_usage: 0,
    upstream_requests: 0, estimated_requests: 0, unknown_requests: 0, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0,
    prompt_known: 0, completion_known: 0, total_known: 0, from: '', to: '', by_account: [], by_model: [] } };
  if (state.mode === 'legacy') return { events: [event(1)], window: 512 };
  if (state.mode === 'invalid') return { events: [event(1, { source: malicious, prompt_tokens: 5, completion_tokens: 8, total_tokens: 13 }),
    event(2, { source: 'upstream', prompt_tokens: '7', completion_tokens: -1, total_tokens: 1.5 })], window: 512,
    usage: { ...usage, prompt_known: 0, completion_known: 0, total_known: 0, requests: 2 } };
  if (state.mode === 'warning') return { events: events.slice(0, 2), window: 512, usage: { ...usage,
    overflowed: true, accounts_truncated: 3, models_truncated: 4 }, dropped: 1, write_failures: 2 };
  if (model && model !== 'slow' && model !== 'slowlogout') return { events: [event(9, { source: 'estimated', prompt_tokens: 2, completion_tokens: 3, total_tokens: 5 })],
    window: 512, usage: { ...usage, ...totals, requests: 1, with_usage: 1, upstream_requests: 0, estimated_requests: 1, unknown_requests: 0,
      prompt_tokens: 2, completion_tokens: 3, total_tokens: 5, prompt_known: 1, completion_known: 1, total_known: 1,
      by_account: [], by_model: [{ key: model, requests: 1, with_usage: 1, upstream_requests: 0, estimated_requests: 1, unknown_requests: 0,
        prompt_tokens: 2, completion_tokens: 3, total_tokens: 5, prompt_known: 1, completion_known: 1, total_known: 1 }] } };
  return { events: url.searchParams.has('before') ? events.slice(2) : events.slice(0, 2), window: 512, usage,
    has_more: !url.searchParams.has('before'), next_before: 4, disabled: false, dropped: 0, write_failures: 0 };
}
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://fixture.invalid');
  const route = url.pathname.replace(/^\/proxy/, '');
  state.calls.push(url.pathname + url.search);
  const json = (status, data) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)); };
  if (route === '/favicon.ico') { res.writeHead(204); res.end(); return; }
  if (!route.includes('/admin/api/')) { res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' }); res.end(html); return; }
  if (req.headers.authorization !== 'Bearer synthetic-ui-key') { json(401, { error: { message: 'Synthetic key required' } }); return; }
  if (route === '/admin/api/overview') { json(200, overview); return; }
  if (route === '/admin/api/requests') {
    const data = snapshot(url);
    if (state.hold && url.searchParams.get('model') === state.hold) await new Promise(resolve => { state.release = resolve; state.started?.(); });
    if (state.mode === 'failure' || url.searchParams.get('model') === 'error') json(500, { error: { message: 'Synthetic request failure' } });
    else json(200, data);
    return;
  }
  json(200, {});
});
async function noOverflow(page, label) {
  const value = await page.evaluate(() => ({ width: document.documentElement.scrollWidth, viewport: innerWidth,
    height: document.documentElement.scrollHeight, viewportHeight: innerHeight, rootScroll: scrollY }));
  assert.ok(value.width <= value.viewport + 1 && value.height <= value.viewportHeight + 1 && value.rootScroll === 0,
    label + ': document overflow ' + JSON.stringify(value));
}
async function filter(page, value) { await page.locator('#reqModel').fill(value); await page.locator('#reqModel').press('Enter'); }
async function refresh(page) {
  const response = page.waitForResponse(res => res.url().includes('/admin/api/requests'));
  await page.locator('#btnReqRefresh').click(); await response;
  await page.waitForFunction(() => document.getElementById('requestCard').getAttribute('aria-busy') === 'false');
}
async function capture(page, name) { await page.screenshot({ path: path.join(output, name + '.png') }); }

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  const browser = await chromium.launch({ headless: true, executablePath: process.env.UI_CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe' });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1024 }, reducedMotion: 'reduce' });
  const errors = [], external = [];
  await context.route('**/*', route => {
    if (new URL(route.request().url()).origin !== origin) { external.push(route.request().url()); return route.abort(); }
    return route.continue();
  });
  const page = await context.newPage(); page.setDefaultTimeout(7000);
  page.on('pageerror', error => errors.push(error.message));
  try {
    await page.goto(origin + '/proxy/panel/#requests');
    await page.locator('#keyInput').fill('synthetic-ui-key'); await page.locator('#loginBtn').click();
    await page.waitForFunction(() => document.getElementById('reqUsageTotal').textContent === '315');
    assert.equal(await page.locator('#reqUsageCompletion').innerText(), '210', 'server totals exceed the two loaded rows');
    assert.equal(await page.locator('#reqBody tr').count(), 2);
    assert.match(await page.locator('#reqBody tr').first().innerText(), /输入 0[\s\S]*输出 0[\s\S]*总计 0[\s\S]*上游值/);
    assert.match(await page.locator('#reqBody tr').nth(1).innerText(), /估算值/);
    assert.match(await page.locator('#reqUsageTotalKnown').innerText(), /已知 3 \/ 匹配 5/);
    assert.match(await page.locator('#reqUsageScope').innerText(), /近期窗口.*已保留 5.*筛选匹配 5/);
    assert.match(await page.locator('#reqMeta').innerText(), /近期窗口 512 条/);
    assert.match(await page.locator('#reqUsageCoverage').innerText(), /上游值 3.*估算值 1.*未知 1/);
    assert.equal(await page.locator('#reqUsageAccounts img').count(), 0);
    assert.match(await page.locator('#reqUsageAccounts').innerText(), /<img src=x/);
    assert.equal(await page.evaluate(() => window.__usageXSS), undefined);
    assert.ok(state.calls.some(call => call.startsWith('/proxy/admin/api/requests')));
    await noOverflow(page, 'desktop'); await capture(page, 'tokens-desktop');
    const more = page.waitForResponse(res => res.url().includes('before='));
    await page.locator('#btnReqMore').click(); await more;
    await page.waitForFunction(() => document.getElementById('reqBody').children.length === 5);
    assert.equal(await page.locator('#reqUsageTotal').innerText(), '315', 'pagination cannot multiply totals');
    assert.match(await page.locator('#reqBody tr').nth(3).innerText(), /输入 未知[\s\S]*输出 5[\s\S]*总计 未知/);
    assert.match(await page.locator('#reqBody tr').last().innerText(), /用量未知/);
    await filter(page, 'filtered');
    await page.waitForFunction(() => document.getElementById('reqUsageTotal').textContent === '5');
    assert.match(await page.locator('#reqUsageModels').innerText(), /filtered/);
    state.mode = 'failure'; await refresh(page);
    assert.equal(await page.locator('#reqUsageTotal').innerText(), '5', 'same-query refresh error preserves its summary');
    assert.equal(await page.locator('#reqError').isVisible(), true);
    await filter(page, 'error');
    await page.waitForFunction(() => document.getElementById('reqUsageScope').textContent.includes('加载失败'));
    assert.equal(await page.locator('#reqUsageTotal').innerText(), '—', 'failed new query cannot retain old totals');
    state.mode = 'normal'; await filter(page, '');
    await page.waitForFunction(() => document.getElementById('reqUsageTotal').textContent === '315');
    state.hold = 'slow';
    const slowStarted = new Promise(resolve => { state.started = resolve; });
    await filter(page, 'slow'); await slowStarted;
    assert.equal(await page.locator('#reqUsageTotal').innerText(), '—', 'new query clears pending summary');
    await filter(page, 'filtered');
    await page.waitForFunction(() => document.getElementById('reqUsageTotal').textContent === '5');
    state.hold = null; state.release(); await page.waitForTimeout(100);
    assert.equal(await page.locator('#reqUsageTotal').innerText(), '5', 'late old-query response ignored');
    await filter(page, ''); await page.waitForFunction(() => document.getElementById('reqUsageTotal').textContent === '315');
    for (const mode of ['legacy', 'invalid', 'empty', 'disabled', 'warning']) {
      state.mode = mode; await refresh(page);
      const scope = await page.locator('#reqUsageScope').innerText();
      if (mode === 'legacy') { assert.match(scope, /未提供/); assert.match(await page.locator('#reqBody').innerText(), /用量未知/); }
      if (mode === 'invalid') { assert.equal(await page.locator('#reqUsageTotal').innerText(), '未知'); assert.doesNotMatch(await page.locator('#reqBody').innerText(), /输入 7|输出 -1|总计 1.5/); }
      if (mode === 'empty') { assert.equal(await page.locator('#reqUsageTotal').innerText(), '0'); assert.match(await page.locator('#reqBody').innerText(), /暂无匹配/); }
      if (mode === 'disabled') { assert.match(scope, /审计已关闭/); assert.equal(await page.locator('#reqUsageTotal').innerText(), '—'); assert.equal(await page.locator('#reqUsageGroups').isVisible(), false); }
      if (mode === 'warning') { assert.match(await page.locator('#reqUsageWarning').innerText(), /安全范围.*账号分组省略 3.*模型分组省略 4/); assert.equal(await page.locator('#reqLoss').isVisible(), true); }
    }
    state.mode = 'normal'; await refresh(page);
    await page.locator('#themeToggle').click(); await capture(page, 'tokens-dark');
    await page.setViewportSize({ width: 390, height: 844 });
    await page.locator('.page-content').evaluate(node => { node.scrollTop = 0; });
    await noOverflow(page, 'mobile top'); await capture(page, 'tokens-mobile');
    await page.locator('#reqUsageModels').scrollIntoViewIfNeeded();
    const horizontal = await page.locator('.usage-table').first().evaluate(table => {
      const container = table.parentElement; container.scrollLeft = 120;
      return { scroll: container.scrollLeft, width: container.clientWidth, content: container.scrollWidth };
    });
    assert.ok(horizontal.scroll > 0 && horizontal.content > horizontal.width, 'group tables scroll locally');
    await noOverflow(page, 'mobile group table'); await capture(page, 'tokens-mobile-groups');
    await page.setViewportSize({ width: 390, height: 480 }); await noOverflow(page, 'short mobile');
    await page.setViewportSize({ width: 1440, height: 1024 });
    await page.locator('nav [data-page="accounts"]').click();
    assert.match(await page.locator('#acctBody').innerText(), /凭证剩余 59m0s/);
    assert.doesNotMatch(await page.locator('#acctBody').innerText(), /Token 剩余/);
    await page.locator('nav [data-page="requests"]').click();
    state.hold = 'slowlogout';
    const logoutStarted = new Promise(resolve => { state.started = resolve; });
    await filter(page, 'slowlogout'); await logoutStarted;
    await page.locator('#btnLogout').click();
    await page.locator('#loginView').waitFor({ state: 'visible' });
    state.hold = null; state.release(); await page.waitForTimeout(100);
    assert.equal(await page.locator('#reqUsageTotal').innerText(), '—');
    assert.equal(await page.locator('#reqUsageGroups').isVisible(), false);
    assert.doesNotMatch(await page.locator('#reqUsageAccounts').innerText(), /<img|synthetic/);
    assert.equal(await page.evaluate(() => window.__usageXSS), undefined);
    assert.deepEqual(errors, []); assert.deepEqual(external, []);
    console.log(JSON.stringify({ passed: true, screenshots: output,
      checks: ['native/estimated/unknown/partial/zero rows', 'server summary and coverage', 'pagination does not multiply totals',
        'filtered summaries and query failure/reset', 'same-query error retention', 'late query/auth suppression',
        'legacy/invalid/empty/disabled/overflow/truncation/loss', 'escaped account/model groups', 'desktop/dark/mobile local scroll',
        'credential expiry wording', 'proxy prefix', 'no external requests or browser errors'] }));
  } catch (error) {
    await capture(page, 'failure').catch(() => {}); console.error(JSON.stringify({ screenshots: output, errors, external })); throw error;
  } finally { state.release?.(); await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(error => { console.error(error); server.close(); process.exitCode = 1; });
