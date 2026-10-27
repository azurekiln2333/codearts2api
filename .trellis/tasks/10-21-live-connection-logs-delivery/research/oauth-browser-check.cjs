// Real Chrome against the embedded panel and synthetic localhost OAuth DTOs.
// Optional UI_PANEL_SOURCE verifies the asset from an extracted source archive.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { chromium } = require(process.env.UI_PLAYWRIGHT_MODULE || path.join(os.tmpdir(), 'codearts2api-ui-verification/node_modules/playwright'));
let root = __dirname;
while (!fs.existsSync(path.join(root, 'go.mod'))) {
  const parent = path.dirname(root);
  if (parent === root) throw new Error('Project root not found');
  root = parent;
}
const panelSource = process.env.UI_PANEL_SOURCE || path.join(root, 'internal/server/panel.html');
const html = fs.readFileSync(panelSource, 'utf8').replaceAll('__SERVICE_NAME__','codearts2api').replaceAll('__SERVICE_TITLE__','CodeArts2API').replaceAll('__LOGO__','CA').replaceAll('__ACCENT__','#0284c7');
const output = process.env.UI_OUTPUT_DIR || path.join(root, '.local-test', 'oauth-ui-' + Date.now());
fs.mkdirSync(output, {recursive:true});
const state = {calls:[], sessions:new Map(), next:0, overview:0, overviewFailures:0, active:0, maxActive:0, mode:'normal', hold:null, accountName:null};
const account = {uid:'synthetic-oauth-account',name:'测试授权账号',disabled:false,cooling:false,active_concurrent:0,max_concurrent:1,err_count:0};
let origin;
const gates = new Set();
function hold(route) {
  let resolveRelease, started;
  const released = new Promise(resolve => { resolveRelease = resolve; });
  const began = new Promise(resolve => { started = resolve; });
  const gate = {route,released,started,release:() => {gates.delete(gate); resolveRelease();}};
  gates.add(gate); state.hold = gate;
  return {release:gate.release,began};
}
const server = http.createServer(async (req,res) => {
  const url = new URL(req.url,'http://synthetic.invalid');
  const route = url.pathname.replace(/^\/proxy/,'');
  const chunks = []; for await (const chunk of req) chunks.push(chunk);
  let body = {}; try { body = JSON.parse(Buffer.concat(chunks).toString() || '{}'); } catch {}
  state.calls.push({route,path:url.pathname,method:req.method,body});
  const json = (status,data) => { res.writeHead(status,{'Content-Type':'application/json'}); res.end(JSON.stringify(data)); };
  if (route === '/favicon.ico') { res.writeHead(204); res.end(); return; }
  if (!route.startsWith('/admin/api/')) { res.writeHead(200,{'Content-Type':'text/html; charset=utf-8'}); res.end(html); return; }
  if (req.headers.authorization !== 'Bearer synthetic-oauth-key') { json(401,{message:'Synthetic API key required'}); return; }
  if (route === '/admin/api/overview') {
    state.overview++;
    if (state.hold?.route === route) {
      const gate = state.hold; state.hold = null; gate.started(); await gate.released;
    }
    if (state.mode === 'overview-error') { state.overviewFailures++; json(503,{message:'Synthetic overview unavailable'}); return; }
    json(200,{service:'CodeArts2API',region:'cn',accounts:[{...account,name:state.accountName || account.name}],models:[],stats:{total:1,healthy:1,cooling:0,disabled:0},effective:{},schedule:{},audit:{enabled:true},build:{known:true,commit:'synthetic'}}); return;
  }
  if (route.startsWith('/admin/api/oauth/')) {
    state.active++; state.maxActive = Math.max(state.active,state.maxActive);
    try {
      if (state.hold?.route === route) {
        const gate = state.hold; state.hold = null; gate.started(); await gate.released;
      }
      if (route.endsWith('/start')) {
        if (state.mode === 'start-error') { json(503,{ok:false,message:'Synthetic start unavailable'}); return; }
        const id = 'synthetic-session-' + ++state.next;
        state.sessions.set(id,{deleted:false,done:false,expired:false,pollFailure:0,terminal:false});
        json(200,{ok:true,session_id:id,auth_url:origin+'/authorize?ticket_id='+id,expires_in:900}); return;
      }
      const session = state.sessions.get(body.session_id);
      if (!session || session.deleted || session.expired) { json(404,{ok:false,message:'Synthetic session missing'}); return; }
      if (route.endsWith('/poll')) {
        if (session.pollFailure-- > 0) { json(503,{ok:false,message:'Synthetic detection unavailable'}); return; }
        if (session.terminal) { json(200,{ok:false,status:'error',message:'Synthetic authorization refused'}); return; }
        json(200,{ok:true,status:session.done ? 'done' : 'pending',message:session.done ? 'Synthetic authorization complete' : 'Generic pending response'}); return;
      }
      if (route.endsWith('/import-callback')) {
        if (!String(body.callback_url).startsWith('http://127.0.0.1:7866/oauth/callback?') || String(body.callback_url).includes('reject-sensitive-code')) {
          // Reflected input must stay out of status/toasts, even on a bad server.
          json(400,{ok:false,message:'Rejected callback '+body.callback_url}); return;
        }
        if (String(body.callback_url).includes('secret=')) {
          json(200,{ok:true,status:'continue',message:'Synthetic first callback',next_url:origin+'/continue-authorize?ticket_id='+body.session_id}); return;
        }
        session.deleted = true;
        json(200,{ok:true,status:'done',message:'Synthetic authorization complete',account:{uid:account.uid}}); return;
      }
      json(404,{ok:false});
    } finally { state.active--; }
    return;
  }
  json(200,{ok:true});
});
const count = route => state.calls.filter(call => call.route === route).length;
const startRoute = '/admin/api/oauth/start', pollRoute = '/admin/api/oauth/poll', importRoute = '/admin/api/oauth/import-callback';
function currentSession() { return [...state.sessions.values()].at(-1); }
async function waitUntil(predicate) {
  const deadline = Date.now()+5000;
  while (!predicate()) { if (Date.now()>deadline) throw new Error('Fixture state did not settle'); await new Promise(resolve=>setTimeout(resolve,20)); }
}
async function noOverflow(page,label) {
  const bounds = await page.evaluate(() => ({w:document.documentElement.scrollWidth,h:document.documentElement.scrollHeight,vw:innerWidth,vh:innerHeight}));
  assert.ok(bounds.w <= bounds.vw+1 && bounds.h <= bounds.vh+1,label+': '+JSON.stringify(bounds));
}
(async () => {
  await new Promise(resolve => server.listen(0,'127.0.0.1',resolve)); origin = 'http://127.0.0.1:'+server.address().port;
  const browser = await chromium.launch({headless:true,executablePath:process.env.UI_CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe'});
  const errors = [], external = [], popups = [];
  const context = await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'});
  await context.grantPermissions(['clipboard-read','clipboard-write'],{origin});
  await context.addInitScript(() => localStorage.setItem('codearts2api_panel_api_key','synthetic-oauth-key'));
  await context.route('**/*', async route => {
    if (new URL(route.request().url()).origin !== origin) { external.push(route.request().url()); await route.abort(); }
    else await route.continue();
  });
  const page = await context.newPage(); page.on('pageerror',error => errors.push(error.message));
  context.on('page',popup => { popups.push(popup); });
  const capture = name => page.screenshot({path:path.join(output,name+'.png')});
  const status = () => page.locator('#oauthStatus').innerText();
  const open = async () => { await page.locator('#btnAddAccount').click(); await page.locator('#oauthModal').waitFor({state:'visible'}); };
  const close = async () => { if (await page.locator('#oauthModal').isVisible()) await page.locator('#oauthClose').click(); };
  const start = async () => {
    await page.locator('#oauthStart').click();
    await page.waitForFunction(() => document.getElementById('oauthStart').textContent === '提交授权');
  };
  const submit = async value => { await page.locator('#oauthCallbackUrl').fill(value); await page.locator('#oauthStart').click(); };
  const completed = async () => { await page.waitForFunction(() => document.getElementById('oauthStart').textContent === '授权完成'); };
  const forcePoll = async () => { await page.evaluate(() => pollOAuth()); };
  const code = 'http://127.0.0.1:7866/oauth/callback?code=synthetic-sensitive-code&state=synthetic-state';
  try {
    await page.goto(origin+'/panel/#accounts'); await page.locator('#appView').waitFor({state:'visible'});
    await open();
    assert.equal(await page.locator('#oauthStart').innerText(),'获取授权链接');
    assert.equal(await page.locator('#oauthCallbackUrl').isDisabled(),true);
    assert.equal(await page.locator('#oauthImport,#oauthPoll').count(),0);
    assert.equal(await page.getByRole('button',{name:'我已授权'}).count(),0);
    assert.equal(await page.getByRole('button',{name:'提交回调地址'}).count(),0);
    assert.equal(await page.locator('#oauthCopy').innerText(),'复制授权链接');
    assert.match(await status(),/复制链接或手动打开授权页/);
    assert.match(await page.locator('#oauthCallbackHelp').innerText(),/全部参数.*不是上方的授权链接/);
    await capture('oauth-idle-desktop');
    const startGate = hold(startRoute), starts = count(startRoute);
    await page.locator('#oauthStart').click(); await startGate.began;
    await page.evaluate(() => document.getElementById('oauthStart').click());
    assert.equal(count(startRoute),starts+1,'no duplicate start while in flight');
    startGate.release(); await page.waitForFunction(() => document.getElementById('oauthStart').textContent === '提交授权');
    assert.equal(popups.length,0,'start does not attempt an async popup');
    assert.equal(count('/authorize'),0,'obtaining the link does not open or fetch the authorization page');
    assert.match(await status(),/授权链接已生成.*复制链接.*打开授权页/);
    await page.locator('#oauthCopy').click();
    const link = await page.locator('#oauthUrl').innerText();
    await page.waitForFunction(async expected => await navigator.clipboard.readText() === expected,link);
    const popupWait = context.waitForEvent('page'); await page.locator('#oauthOpen').click(); const popup = await popupWait; await popup.close();
    assert.equal(popups.length,1,'authorization page opens only through explicit action');
    await page.locator('#oauthStart').click(); assert.match(await status(),/请先粘贴.*提交授权/);
    const emptyNotice = await status(); await forcePoll(); assert.equal(await status(),emptyNotice,'pending poll preserves missing-input guidance');
    await page.locator('#oauthCallbackUrl').fill(code);
    assert.match(await status(),/已填写回调地址.*提交授权/);
    assert.doesNotMatch(await status(),/请先粘贴/);
    await capture('oauth-pasted-desktop');
    const imports = count(importRoute), overview = state.overview;
    const importGate = hold(importRoute);
    await page.locator('#oauthStart').click(); await importGate.began;
    await page.evaluate(() => document.getElementById('oauthStart').click());
    assert.equal(count(importRoute),imports+1,'no duplicate callback import');
    importGate.release(); await completed();
    await waitUntil(() => state.overview === overview+1);
    await page.waitForFunction(() => document.getElementById('oauthStatus').textContent.includes('Synthetic authorization complete'));
    assert.equal(await page.locator('#oauthCallbackUrl').inputValue(),'');
    assert.match(await page.locator('#oauthUrl').innerText(),/获取后显示/);
    await capture('oauth-success-desktop');
    await page.locator('#oauthModal').waitFor({state:'hidden'});
    const pollsAfterImport = count(pollRoute); await page.waitForTimeout(2700);
    assert.equal(count(pollRoute),pollsAfterImport,'done import never polls deleted session');
    assert.equal(state.overview,overview+1,'success refreshes account data exactly once');

    await open(); await start();
    const delayedOverview = hold('/admin/api/overview'), delayedOverviewCount = state.overview;
    state.accountName = '新授权账号';
    await submit(code); await delayedOverview.began; await completed();
    await page.locator('#oauthModal').waitFor({state:'hidden',timeout:3000});
    assert.equal(state.overview,delayedOverviewCount+1,'authorization starts one overview refresh');
    assert.match(await page.locator('#acctBody').innerText(),/测试授权账号/,'dialog closes while refresh is still pending');
    delayedOverview.release();
    await page.waitForFunction(() => document.getElementById('acctBody').textContent.includes('新授权账号'));
    assert.equal(state.overview,delayedOverviewCount+1,'delayed refresh completes without a duplicate');
    state.accountName = null;

    await open(); await start();
    const failedOverview = hold('/admin/api/overview'), failedOverviewCount = state.overview;
    await submit(code); await failedOverview.began; await completed();
    await page.locator('#oauthModal').waitFor({state:'hidden',timeout:3000});
    state.mode = 'overview-error'; failedOverview.release();
    await page.waitForFunction(() => document.getElementById('toast').textContent.includes('账号列表刷新失败'));
    assert.equal(state.overview,failedOverviewCount+1,'failed refresh is attempted once');
    state.mode = 'normal';

    await open(); await start();
    const staleOverview = hold('/admin/api/overview'), failuresBeforeStale = state.overviewFailures;
    await submit(code); await staleOverview.began; await completed();
    await page.locator('#oauthModal').waitFor({state:'hidden',timeout:3000});
    await page.locator('#btnRefresh').click();
    await page.waitForFunction(() => document.getElementById('toast').textContent === '控制台已刷新');
    state.mode = 'overview-error'; staleOverview.release();
    await waitUntil(() => state.overviewFailures === failuresBeforeStale+1);
    await page.waitForTimeout(150);
    assert.equal(await page.locator('#toast').innerText(),'控制台已刷新','old refresh failure cannot overwrite newer successful refresh');
    state.mode = 'normal';

    await open(); await start();
    await page.evaluate(() => {
      const original = window.setTimeout;
      window.restoreOAuthTestTimeout = () => { window.setTimeout = original; };
      window.setTimeout = (fn,delay,...args) => original(fn,delay === 15000 ? 400 : delay,...args);
    });
    const timedOutOverview = hold('/admin/api/overview');
    await submit(code); await timedOutOverview.began; await completed();
    await page.waitForFunction(() => document.getElementById('toast').textContent.includes('账号列表刷新失败'));
    await page.locator('#oauthModal').waitFor({state:'hidden',timeout:3000});
    timedOutOverview.release();
    await page.evaluate(() => window.restoreOAuthTestTimeout());

    await open(); await start(); currentSession().done = true;
    const directImports = count(importRoute), directOverview = state.overview;
    await completed();
    await waitUntil(() => state.overview === directOverview+1);
    assert.equal(count(importRoute),directImports,'reachable direct callback completes automatically');
    await page.waitForFunction(() => document.getElementById('acctBody').textContent.includes('测试授权账号'));
    assert.equal(state.overview,directOverview+1); await close();

    await open(); await start();
    await submit('http://127.0.0.1:7866/oauth/callback?secret=synthetic-secret&redirect=synthetic-portal');
    await page.waitForFunction(() => document.getElementById('oauthCopy').textContent === '复制继续授权链接');
    const continued = await status(); assert.match(continued,/打开继续授权页.*新的完整回调地址/);
    await page.waitForTimeout(1000);
    assert.equal(await page.locator('#oauthModal').isVisible(),true,'continuation is not treated as completed authorization');
    assert.equal(await page.locator('#oauthCallbackUrl').inputValue(),'');
    assert.match(await page.locator('#oauthUrl').innerText(),/continue-authorize/);
    await forcePoll(); assert.equal(await status(),continued,'pending preserves continuation instruction');
    await page.locator('#oauthCopy').click();
    const nextLink = await page.locator('#oauthUrl').innerText();
    await page.waitForFunction(async expected => await navigator.clipboard.readText() === expected,nextLink);
    assert.equal(popups.length,1,'continue does not attempt an async popup');
    const nextPopupWait = context.waitForEvent('page'); await page.locator('#oauthOpen').click(); const nextPopup = await nextPopupWait; await nextPopup.close();
    assert.equal(popups.length,2,'continuation page opens only through explicit action');
    await capture('oauth-continue-desktop'); await submit(code); await completed(); await close();

    await open(); await start();
    const raceGate = hold(pollRoute); page.evaluate(() => { pollOAuth(); }); await raceGate.began;
    assert.equal(await page.locator('#oauthStart').isEnabled(),true,'poll does not disable submit');
    assert.equal(await page.locator('#oauthCallbackUrl').isEnabled(),true,'poll does not disable callback entry');
    const beforeQueued = count(importRoute);
    await submit(code); await page.evaluate(() => document.getElementById('oauthStart').click());
    assert.match(await status(),/等待当前检测结束/);
    assert.equal(count(importRoute),beforeQueued,'submission queued until current poll finishes');
    raceGate.release(); await completed();
    assert.equal(count(importRoute),beforeQueued+1,'queued click imports once');
    assert.equal(state.maxActive,1,'OAuth operations do not overlap'); await close();

    await open(); await start();
    const winsGate = hold(pollRoute); page.evaluate(() => { pollOAuth(); }); await winsGate.began;
    const beforeWinner = count(importRoute);
    await submit(code); currentSession().done = true; winsGate.release(); await completed();
    assert.equal(count(importRoute),beforeWinner,'direct completion wins over queued import'); await close();

    await open(); await start();
    const rejected = 'http://127.0.0.1:7866/oauth/callback?code=reject-sensitive-code';
    await submit(rejected); await page.waitForFunction(() => document.getElementById('oauthStatus').textContent.includes('回调地址无效'));
    const rejectedNotice = await status(); assert.doesNotMatch(rejectedNotice,/reject-sensitive-code|http:\/\/127/);
    assert.equal(await page.locator('#oauthCallbackUrl').inputValue(),rejected);
    await forcePoll(); assert.equal(await status(),rejectedNotice,'pending preserves validation error');
    currentSession().pollFailure = 1; await forcePoll();
    assert.match(await status(),/回调地址无效.*自动检测暂时失败/);
    await forcePoll(); assert.equal(await status(),rejectedNotice,'recovery restores actionable validation error');
    await capture('oauth-error-desktop'); await submit(code); await completed(); await close();

    await open(); await start(); currentSession().expired = true; await forcePoll();
    assert.equal(await page.locator('#oauthStart').innerText(),'获取授权链接');
    assert.match(await status(),/会话已过期/); assert.equal(await page.locator('#oauthCallbackUrl').inputValue(),'');
    assert.equal(await page.locator('#oauthCopy').isDisabled(),true); await start();
    currentSession().terminal = true; await forcePoll();
    assert.equal(await page.locator('#oauthStart').innerText(),'获取授权链接'); assert.match(await status(),/授权失败/); await close();

    state.mode = 'start-error'; await open(); await page.locator('#oauthStart').click();
    await page.waitForFunction(() => document.getElementById('oauthStatus').textContent.includes('获取授权链接失败'));
    assert.equal(await page.locator('#oauthStart').isEnabled(),true); state.mode = 'normal'; await start(); await close();

    await open(); const lateStart = hold(startRoute), lateStartOverview = state.overview;
    await page.locator('#oauthStart').click(); await lateStart.began; await close(); lateStart.release(); await page.waitForTimeout(100);
    assert.equal(state.overview,lateStartOverview); assert.equal(await page.locator('#oauthModal').isVisible(),false);
    const oldPolls = count(pollRoute); await page.waitForTimeout(900); assert.equal(count(pollRoute),oldPolls,'dismissed start cannot schedule polling');

    await open(); await start(); const lateImport = hold(importRoute), lateImportOverview = state.overview;
    await submit(code); await lateImport.began; await close(); lateImport.release(); await page.waitForTimeout(100);
    assert.equal(state.overview,lateImportOverview,'dismissed import cannot refresh account data');
    assert.equal(await page.locator('#oauthModal').isVisible(),false);

    await open(); await start(); const latePoll = hold(pollRoute), latePollOverview = state.overview;
    page.evaluate(() => { pollOAuth(); }); await latePoll.began; currentSession().done = true; await close(); latePoll.release(); await page.waitForTimeout(100);
    assert.equal(state.overview,latePollOverview,'dismissed successful poll cannot refresh account data');
    const stoppedPolls = count(pollRoute); await page.waitForTimeout(2700); assert.equal(count(pollRoute),stoppedPolls,'dismissed poll cannot restart timer');

    await open(); await start(); const logoutImport = hold(importRoute), beforeLogoutOverview = state.overview;
    await submit(code); await logoutImport.began;
    await page.evaluate(() => document.getElementById('btnLogout').click());
    await page.locator('#loginView').waitFor({state:'visible'}); logoutImport.release(); await page.waitForTimeout(150);
    assert.equal(state.overview,beforeLogoutOverview,'late import after logout starts no authenticated follow-up');
    assert.equal(await page.locator('#oauthCallbackUrl').inputValue(),'');
    await page.locator('#keyInput').fill('synthetic-oauth-key'); await page.locator('#loginBtn').click(); await page.locator('#appView').waitFor({state:'visible'});
    await page.goto(origin+'/proxy/panel/#accounts'); await page.locator('#appView').waitFor({state:'visible'});
    await open(); await start(); await submit(code); await completed();
    assert.ok(state.calls.some(call => call.path === '/proxy/admin/api/oauth/import-callback'),'OAuth preserves proxy base'); await close();

    await page.waitForFunction(() => !document.getElementById('toast').classList.contains('show'));
    await page.setViewportSize({width:390,height:844}); await open(); await noOverflow(page,'mobile idle');
    await page.locator('#oauthModal .modal').evaluate(element => {element.scrollTop=0;}); await capture('oauth-idle-mobile');
    await start(); await page.locator('#oauthCallbackUrl').fill(code); await capture('oauth-pasted-mobile');
    await submit('http://127.0.0.1:7866/oauth/callback?secret=synthetic-secret&redirect=synthetic-portal');
    await page.waitForFunction(() => document.getElementById('oauthCopy').textContent === '复制继续授权链接');
    await noOverflow(page,'mobile continue'); await capture('oauth-continue-mobile');
    await page.setViewportSize({width:390,height:480});
    await page.locator('#oauthStart').scrollIntoViewIfNeeded(); await noOverflow(page,'short mobile');
    assert.equal(await page.locator('#oauthStart').isVisible(),true); await capture('oauth-short-mobile');
    await page.locator('#oauthClose').focus(); await page.keyboard.press('Shift+Tab');
    assert.equal(await page.evaluate(() => document.activeElement.id),'oauthStart','dialog traps backward focus');
    await page.keyboard.press('Tab'); assert.equal(await page.evaluate(() => document.activeElement.id),'oauthClose','dialog traps forward focus');
    await page.keyboard.press('Escape'); await page.locator('#oauthModal').waitFor({state:'hidden'});
    assert.equal(await page.evaluate(() => document.activeElement.id),'btnAddAccount','Escape restores focus');
    assert.deepEqual(errors,[]); assert.deepEqual(external,[]);
    assert.equal(state.maxActive,1); assert.equal(state.active,0);
    console.log(JSON.stringify({passed:true,panelSource,screenshots:output,checks:['get authorization link separately and open manually','single get/submit primary and exact clipboard labels','no ambiguous import/poll buttons','real done DTO with deleted session and one refresh','close while refresh is delayed; report refresh failure','ignore old refresh failure after newer success; bound hung refresh','direct callback automatic detection','continue and second callback','queued poll-submit and direct-completion winner','duplicate suppression and serialized OAuth requests','validation/error/transport recovery/expiry/retry','no callback echo in feedback','close and logout late-response suppression','explicit popup only','proxy prefix','desktop/mobile/short scroll and keyboard focus','no external requests or page errors']}));
  } catch(error) { await capture('failure').catch(()=>{}); console.error(JSON.stringify({screenshots:output,errors,external})); throw error; }
  finally { for (const gate of gates) gate.release(); await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(error => { console.error(error); server.close(); process.exitCode=1; });
