// Pure JavaScript fixture checks for the embedded panel; no browser or network.
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');

(async () => {
  let root = __dirname;
  while (!fs.existsSync(path.join(root, 'go.mod'))) {
    const parent = path.dirname(root);
    if (parent === root) throw new Error('Project root not found');
    root = parent;
  }
  const html = fs.readFileSync(path.join(root, 'internal/server/panel.html'), 'utf8');
  const source = html.match(/<script>([\s\S]*?)<\/script>/)[1];
  const elements = new Map();
  for (const match of html.matchAll(/\bid="([^"]+)"/g)) {
    elements.set(match[1], {
      textContent: '', innerHTML: '', value: '', style: {},
      classList: { add() {}, remove() {} },
      addEventListener() {}, focus() {},
    });
  }
  const overview = {
    service: 'fixture', region: 'cn', stats: { total: 1, healthy: 1 },
    accounts: [{ name: 'fixture-account', uid: 'fixture-account', nickname: 'Fixture', disabled: false, cooling: false, err_count: 4,
      active_concurrent: 1, max_concurrent: 5, last_error: 'consecutive errors' }],
    models: [{ id: 'fixture-model' }], schedule: {}, build: { known: false, commit: '', built_at: null },
    effective: { startup_timeout: '90s', account_wait_timeout: '10s', upstream_idle_timeout: '60s', stream_write_timeout: '30s', max_pending: 64, queue_max_attempts: 3, queue_retry_delay: '2s' },
    audit: { enabled: true, retention_days: 7, max_files: 8, max_file_bytes: 8388608, max_total_bytes: 67108864, recent: 512, max_queue: 256 },
  };
  const event = {
    seq: 4, id: 'fixture-request-id', ts: new Date().toISOString(), model: '<img src=x onerror=evil()>',
    account: 'fixture-account', committed: true, status: 200, outcome: 'upstream_stream_error',
    attempts: 1, queue_ms: 1, first_data_ms: null, total_ms: 10,
    cause: { class: 'capability', code: 'InferHub.4004.200', text: 'benefit not claimed or expired' },
  };
  const context = vm.createContext({
    console, URLSearchParams, location: { pathname: '/', origin: 'http://fixture.invalid' },
    document: { getElementById(id) { assert.ok(elements.has(id), 'missing real panel element: ' + id); return elements.get(id); } },
    localStorage: { getItem() { return ''; }, setItem() {}, removeItem() {} },
    fetch: async url => ({ ok: true, text: async () => JSON.stringify(url.includes('/requests') ? { events: [event], window: 512, has_more: false, next_before: 4 } : overview) }),
    setTimeout() { return 1; }, clearTimeout() {}, setInterval() { return 1; }, clearInterval() {},
    navigator: {}, window: {}, fixtureOverview: overview, fixtureEvent: event,
  });
  vm.runInContext(source, context, { filename: 'panel.html<script>' });
  await new Promise(setImmediate);
  assert.equal(elements.get('buildBadge').textContent, '未知构建');
  assert.ok(elements.get('runtimeLimits').innerHTML.includes('启动预算 90s'));
  assert.ok(elements.get('auditLimits').textContent.includes('合计 64 MiB'));
  assert.ok(elements.get('acctBody').innerHTML.includes('可调度'));
  assert.ok(elements.get('acctBody').innerHTML.includes('时间未知（旧记录）'));
  assert.ok(elements.get('acctBody').innerHTML.includes('并发 1 / 5'));
  const rows = elements.get('reqBody').innerHTML;
  assert.ok(rows.includes('&lt;img src=x onerror=evil()&gt;'));
  assert.ok(!rows.includes('<img src=x'));
  assert.ok(rows.includes('InferHub.4004.200') && rows.includes('benefit not claimed or expired'));
  assert.ok(rows.includes('upstream_stream_error') && rows.includes('200'));
  vm.runInContext('fixtureOverview.build={known:true,commit:"abcdef1234567890",built_at:null}; render(fixtureOverview);', context);
  assert.equal(elements.get('buildBadge').textContent, 'abcdef123456');
  vm.runInContext('reqState.before=0; renderRequests({events:[{...fixtureEvent,committed:false,status:0,account:null}],window:512});', context);
  assert.ok(elements.get('reqBody').innerHTML.includes('未提交'));
  vm.runInContext('renderRequests({events:[],disabled:true});', context);
  assert.ok(elements.get('reqBody').innerHTML.includes('审计已关闭'));
  assert.ok(elements.get('reqFilters').innerHTML.includes('reqModel') && elements.get('reqFilters').innerHTML.includes('reqAccount'));
  console.log(JSON.stringify({ checks: ['embedded JavaScript executes', 'real element IDs', 'unknown/known build', 'runtime and storage limits', 'schedulability versus legacy history', 'per-account concurrency', 'HTML escaping', 'provider code', 'transport status versus failure', 'absent status', 'disabled view', 'model/account filters'], passed: true }));
})().catch(error => { console.error(error); process.exitCode = 1; });
