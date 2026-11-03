import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync,chmodSync} from 'node:fs';
import {createHash} from 'node:crypto';
const phase=process.argv[2];if(!['before','after'].includes(phase))throw new Error('explicit before/after required');
const root='/data/ops-v12-native-visual-r24-20261026',origin='https://192.168.0.63:30471';
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const state=root+'/private/admin-storage-state-fresh.json';
const context=await browser.newContext({ignoreHTTPSErrors:true,storageState:state,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});
const page=await context.newPage();page.setDefaultTimeout(15000);
const result={phase,origin,checks:[],requests:[]};
page.on('response',r=>{if(new URL(r.url()).pathname.startsWith('/api/'))result.requests.push({path:new URL(r.url()).pathname,status:r.status(),requestId:r.headers()['x-request-id']});});
const assert=(ok,message)=>{if(!ok)throw new Error(message);};
async function api(path){const r=await context.request.get(origin+path);result.requests.push({path,status:r.status(),requestId:r.headers()['x-request-id']});return r;}
try{
 const session=await api('/api/v1/auth/session');assert(session.status()===200,'saved native cookie not accepted');const view=(await session.json()).data;result.session={sessionRef:view.sessionRef,subject:view.subject,mode:view.mode,roles:view.authorization.grants.map(g=>g.role)};assert(view.mode==='full'&&result.session.roles.includes('platform_admin')&&!result.session.roles.includes('operator'),'independent role projection changed');
 const cookie=(await context.cookies()).find(c=>c.name==='__Host-ops-session');assert(cookie,'session cookie missing');result.cookieSHA256=createHash('sha256').update(cookie.value).digest('hex');
 assert((await api('/api/v1/admin/clusters?limit=100')).status()===200,'administrator cluster read rejected');const mfa=await api('/api/v1/auth/mfa/status');assert(mfa.status()===200&&(await mfa.json()).data.state==='confirmed','persisted MFA state changed');
 await page.goto(origin+'/settings/permissions');await page.getByRole('heading',{name:'用户与权限',exact:true}).waitFor();await page.getByText(view.loginName,{exact:true}).waitFor();result.checks.push('saved native cookie and independent admin role restore; real clusters200; persisted confirmed MFA200');
 if(phase==='before'){await context.storageState({path:state});chmodSync(state,0o600);}
 else{
  const before=JSON.parse(readFileSync(root+'/evidence/restart-browser-before.json','utf8'));assert(before.cookieSHA256===result.cookieSHA256&&before.session.sessionRef===result.session.sessionRef,'restart silently replaced cookie/session');
  await page.getByRole('button',{name:'我的账号',exact:true}).click();const d=page.getByRole('dialog',{name:'我的账号',exact:true});await d.waitFor();await page.waitForFunction(()=>{const e=document.querySelector('.ant-modal');return e&&getComputedStyle(e).transform==='none';});result.geometry=await d.evaluate(e=>({width:e.getBoundingClientRect().width,headerHeight:e.querySelector('.ant-modal-header').getBoundingClientRect().height,keyValueLineHeight:getComputedStyle(e.querySelector('.v12-key-values')).lineHeight}));assert(result.geometry.width===660&&result.geometry.headerHeight===76,'frozen foreground geometry differs');await page.screenshot({path:root+'/evidence/my-account-final.png'});
  const logout=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/logout');await d.getByRole('button',{name:'退出登录',exact:true}).click();assert((await logout).status()===200,'logout rejected');await page.waitForURL('**/login');const old=await browser.newContext({ignoreHTTPSErrors:true,storageState:state});const denied=await old.request.get(origin+'/api/v1/auth/session');result.oldCookieStatus=denied.status();result.oldCookieRequestId=denied.headers()['x-request-id'];assert(denied.status()===401,'old cookie remained usable after logout');await old.close();
  result.checks.push('same session/cookie survives API and Worker restart/forward rollout; final modal660/header76; logout200 persists as old-cookie401');
 }
 result.status='passed';
}catch(e){result.status='failed';result.failure=e.message;process.exitCode=1;}
finally{writeFileSync(root+'/evidence/restart-browser-'+phase+'.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({phase,status:result.status,checks:result.checks,failure:result.failure}));}
