// Real Chrome against the actual embedded panel and synthetic localhost APIs only.
// UI_PANEL_SOURCE and UI_OUTPUT_DIR permit verification of an extracted release.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { createHash } = require('node:crypto');
const { chromium } = require(process.env.UI_PLAYWRIGHT_MODULE || path.join(os.tmpdir(), 'codearts2api-ui-verification/node_modules/playwright'));
let root = __dirname;
while (!fs.existsSync(path.join(root, 'go.mod'))) {
  const parent = path.dirname(root); if (parent === root) throw new Error('Project root not found'); root = parent;
}
const panelSource = process.env.UI_PANEL_SOURCE || path.join(root, 'internal/server/panel.html');
const original = fs.readFileSync(panelSource, 'utf8');
const panelSha256 = createHash('sha256').update(original).digest('hex');
const html = original.replaceAll('__SERVICE_NAME__','codearts2api').replaceAll('__SERVICE_TITLE__','CodeArts2API').replaceAll('__LOGO__','CA').replaceAll('__ACCENT__','#3169df');
const output = process.env.UI_OUTPUT_DIR || path.join(root,'.local-test','cnb-ui-'+Date.now());
fs.mkdirSync(output,{recursive:true});
const stamp = new Date().toISOString();
const overview = {service:'codearts2api', region:'cn', stats:{total:1,healthy:1,cooling:0,disabled:0}, accounts:[{uid:'synthetic-account', name:'本地测试账号', nickname:'验证账号', disabled:false, cooling:false, active_concurrent:0,max_concurrent:1,err_count:0,token_remaining:'59m0s'}],models:[{id:'deepseek-v4.1-flash'},{id:'glm-5.3'}],build:{known:true,commit:'synthetic-visual-revision',built_at:stamp},audit:{enabled:true,recent:512,retention_days:7},schedule:{watch:{enabled:true,poll_minutes:30}},effective:{max_pending:64,max_concurrent:1}};
const state = {mode:'normal', proxy:false, calls:[], held:[]};
const server = http.createServer(async (req,res) => {
  const url=new URL(req.url,'http://fixture.invalid'), route=url.pathname.replace(/^\/proxy/,'');
  const json=(status,value)=>{if(res.destroyed)return;res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
  if(route==='/favicon.ico'){res.writeHead(204);res.end();return;}
  if(!route.startsWith('/admin/api/')){res.writeHead(200,{'Content-Type':'text/html; charset=utf-8'});res.end(html);return;}
  state.calls.push({path:url.pathname,auth:req.headers.authorization,method:req.method});
  if(route==='/admin/api/overview'){
    if(state.mode==='hold') await new Promise(resolve=>state.held.push(resolve));
    if(state.mode==='error'){json(503,{error:{message:'Synthetic connection unavailable'}});return;}
    if(req.headers.authorization!=='Bearer synthetic-visual-key'&&!state.proxy){json(401,{error:{message:'Invalid fixture key'}});return;}
    json(200,overview);return;
  }
  if(req.headers.authorization!=='Bearer synthetic-visual-key'&&!state.proxy){json(401,{error:{message:'Invalid fixture key'}});return;}
  if(route==='/admin/api/requests'){json(200,{events:[],has_more:false,window:512,usage:{scope:'recent_window',window:0,requests:0,with_usage:0,unknown_requests:0,upstream_requests:0,estimated_requests:0,prompt_tokens:0,completion_tokens:0,total_tokens:0,prompt_known:0,completion_known:0,total_known:0,by_account:[],by_model:[]}});return;}
  if(route==='/admin/api/models'){json(200,{models:overview.models.map(model=>({...model,origin:'discovered',catalog_accounts:1})),accounts:[],exclusions:[],refresh:{state:'idle'},truncated:{}});return;}
  if(route==='/admin/api/connections'){json(200,{active:[],recent:[],disabled:false,capacity:512,overflow:0,trace_truncated:0});return;}
  json(200,{ok:true});
});
const release=()=>{for(const done of state.held.splice(0))done();};
let browser;
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const origin='http://127.0.0.1:'+server.address().port;
  browser=await chromium.launch({headless:true,executablePath:process.env.UI_CHROME_PATH||'C:/Program Files/Google/Chrome/Application/chrome.exe'});
  const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:'light',reducedMotion:'reduce'});
  const errors=[],external=[];
  await context.route('**/*',async route=>{if(new URL(route.request().url()).origin!==origin){external.push(route.request().url());await route.abort();}else await route.continue();});
  const page=await context.newPage(); page.setDefaultTimeout(10000);page.on('pageerror',error=>errors.push(error.message));
  const visible=selector=>page.locator(selector).waitFor({state:'visible'});
  const shot=name=>page.screenshot({path:path.join(output,name+'.png')});
  const geometry=async label=>{
    const measured=await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,height:innerHeight,scrollHeight:document.documentElement.scrollHeight}));
    assert.ok(measured.scroll<=measured.width+1,label+': horizontal overflow '+JSON.stringify(measured));
    assert.ok(measured.scrollHeight<=measured.height+1,label+': outer document scroll '+JSON.stringify(measured));
  };
  const login=async()=>{await page.locator('#keyInput').fill('synthetic-visual-key');await page.locator('#loginBtn').click();await visible('#appView');};
  try {
    // Initial connection really waits on overview, and keeps authenticated content hidden.
    state.mode='hold';await page.goto(origin+'/panel/');await visible('#bootView');
    assert.match(await page.locator('#bootView').innerText(),/正在连接控制台/);
    assert.equal(await page.locator('#bootView .spin').evaluate(element=>getComputedStyle(element).animationName),'none','reduced motion stops spinner animation');
    assert.equal(await page.locator('#appView').evaluate(element=>[...element.querySelectorAll('a[href],button,input,select,[tabindex="0"]')].filter(node=>node.getClientRects().length).length),0,'pre-auth controls cannot enter visible focus order');
    assert.equal(await page.locator('#appView').isVisible(),false);assert.equal(await page.locator('#loginView').isVisible(),false);
    await shot('boot-desktop');await geometry('desktop boot');state.mode='normal';release();await visible('#loginView');
    assert.ok(state.calls.some(call=>call.path==='/admin/api/overview'&&!call.auth),'startup tries proxy auth without stored key');
    assert.equal(await page.locator('#loginError').isVisible(),false,'first blank login does not accuse operator of an invalid key');
    await shot('login-desktop');await geometry('desktop login');
    const loginLayout=await page.locator('#loginForm').boundingBox();assert.ok(loginLayout.width<=400,'compact reference login form');
    assert.equal(await page.locator('#keyInput').getAttribute('type'),'password');
    await page.locator('#keyInput').fill('wrong-key');await page.locator('#loginBtn').click();await visible('#loginError');assert.match(await page.locator('#loginError').innerText(),/API Key/);
    assert.equal(await page.locator('#appView').isVisible(),false);
    // Submitted login uses the same true connecting stage; no arbitrary spinner.
    state.mode='hold';await page.locator('#keyInput').fill('synthetic-visual-key');await page.locator('#loginBtn').click();await visible('#bootView');await shot('boot-submitted');assert.equal(await page.locator('#loginView').isVisible(),false);
    state.mode='normal';release();await visible('#appView');
    assert.equal(await page.evaluate(()=>localStorage.getItem('codearts2api_panel_api_key')),'synthetic-visual-key');
    await page.waitForFunction(()=>document.getElementById('sTotal').textContent==='1');
    const shell=await page.evaluate(()=>({header:document.getElementById('appHeader').getBoundingClientRect().height,sidebar:document.getElementById('sidebar').getBoundingClientRect().width}));
    assert.ok(Math.abs(shell.header-52)<=1,'reference header height: '+shell.header);assert.ok(Math.abs(shell.sidebar-180)<=1,'reference sidebar width: '+shell.sidebar);
    assert.equal(await page.locator('.sidebar-footer,#sidebarBuild,#subTitle,#btnAddAccount2').count(),0,'removed redundant chrome stays removed');
    for(const view of ['overview','accounts','requests','connections','models','settings']){
      await page.locator('nav [data-page="'+view+'"]').click();await visible('[data-view="'+view+'"]');
      assert.equal(new URL(page.url()).hash,'#'+view);await geometry('desktop '+view);await shot(view+'-desktop');
      assert.equal(await page.locator('[data-view]:not([hidden])').count(),1,'only active content is exposed');
      const visibleHiddenControls=await page.locator('[data-view][hidden]').evaluateAll(views=>views.flatMap(view=>[...view.querySelectorAll('a[href],button,input,select,[tabindex="0"]')]).filter(node=>node.getClientRects().length).length);
      assert.equal(visibleHiddenControls,0,'hidden page controls cannot enter visible focus order');
    }
    await page.locator('#sidebarCollapse').click();
    const collapsed=await page.locator('#sidebar').boundingBox();assert.ok(Math.abs(collapsed.width-64)<=1,'reference collapsed sidebar width: '+collapsed.width);await shot('shell-collapsed');
    await page.locator('#sidebarCollapse').click();
    await page.locator('#themeToggle').click();assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');await shot('shell-dark');
    await page.reload();await visible('#appView');assert.equal(await page.locator('html').getAttribute('data-theme'),'dark','theme survives saved-key restore');
    await page.locator('#btnLogout').click();await visible('#loginView');await shot('login-dark');assert.equal(await page.evaluate(()=>localStorage.getItem('codearts2api_panel_api_key')),null);
    // Transport and timeout paths must return to an actionable form.
    state.mode='error';await page.locator('#keyInput').fill('synthetic-visual-key');await page.locator('#loginBtn').click();await visible('#loginError');assert.match(await page.locator('#loginError').innerText(),/连接/);assert.equal(await page.locator('#loginBtn').isEnabled(),true);await shot('login-connection-error');
    state.mode='hold';await page.locator('#keyInput').fill('synthetic-visual-key');await page.locator('#loginBtn').click();await visible('#bootView');
    await page.locator('#loginView').waitFor({state:'visible',timeout:25000});assert.match(await page.locator('#loginError').innerText(),/超时|timeout|abort/i);assert.equal(await page.locator('#loginBtn').isEnabled(),true);assert.equal(await page.locator('#appView').isVisible(),false);
    await shot('login-timeout');state.mode='normal';release();await page.waitForTimeout(100);assert.equal(await page.locator('#appView').isVisible(),false,'late timeout response cannot authorize');
    await login();await page.locator('#themeToggle').click();await page.setViewportSize({width:390,height:844});
    await page.locator('#sidebarToggle').click();assert.equal(await page.locator('#sidebarToggle').getAttribute('aria-expanded'),'true');await shot('navigation-mobile');await page.keyboard.press('Escape');assert.equal(await page.locator('#sidebarToggle').getAttribute('aria-expanded'),'false');
    for(const view of ['overview','accounts','requests','connections','models','settings']){
      await page.locator('#sidebarToggle').click();await page.locator('nav [data-page="'+view+'"]').click();await visible('[data-view="'+view+'"]');assert.equal(await page.locator('#sidebarToggle').getAttribute('aria-expanded'),'false');await geometry('mobile '+view);await shot(view+'-mobile');
    }
    await page.locator('#btnLogout').click();await visible('#loginView');await geometry('mobile login');await shot('login-mobile');await page.setViewportSize({width:390,height:480});await geometry('short mobile login');await shot('login-short-mobile');
    // Prefix routing and a proxy that supplies authentication work without local key.
    state.proxy=true;await page.goto(origin+'/proxy/panel/#models');await visible('#appView');await visible('[data-view="models"]');assert.ok(state.calls.some(call=>call.path==='/proxy/admin/api/overview'&&!call.auth));
    await page.locator('#sidebarToggle').click();await page.keyboard.press('Escape');await geometry('proxy mobile');
    assert.deepEqual(errors,[],'no uncaught browser errors');assert.deepEqual(external,[],'no external requests');
    const result={passed:true,panelSource,panelSha256,screenshots:output,checks:['real initial/submitted connecting stage and no pre-auth disclosure','blank initial 401 login versus explicit bad-key error','reduced-motion spinner and hidden-view focus isolation','split compact login and exact reference shell geometry','wrong-key, connection failure, bounded timeout and late response recovery','saved key and proxy-injected authentication with prefix routing','six operational views desktop/mobile','64px collapsed sidebar','dark theme persistence and login styling','mobile drawer Escape/navigation dismissal','short mobile layout','removed redundant chrome','no external requests or uncaught errors']};
    fs.writeFileSync(path.join(output,'result.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result));
  }catch(error){await shot('failure').catch(()=>{});console.error(JSON.stringify({errors,external,lastCalls:state.calls.slice(-10),failureScreenshot:path.join(output,'failure.png')}));throw error;}
  finally {release();await context.close();}
})().catch(error=>{console.error(error);process.exitCode=1;}).finally(async()=>{release();await browser?.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));});
