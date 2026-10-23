// Browser transport faults only; all successful responses come from the real API.
import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {randomUUID} from 'node:crypto';
const root='/data/ops-ui-v12-mfa-20261022-r1/gpt-login-r15/preview-r2',origin='https://192.168.0.63:30469';
const fixture=JSON.parse(readFileSync(root+'/private/browser-created-account.json','utf8')),nextPassword=randomUUID()+'C7!';
const sensitive=[fixture.password,nextPassword];
const result={origin,runtime:'r15 backdrop Web; unchanged r13 API; PG15453/schema56',scope:'SL-005 close/Escape/transport-failed logout/reload/retry and pending true password rotation',checks:[],requests:[],phase:'start'};
const assert=(v,m)=>{if(!v)throw new Error(m)};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();
page.on('requestfailed',r=>{result.transportFailures??=[];result.transportFailures.push({path:new URL(r.url()).pathname,error:r.failure()?.errorText})});
page.on('response',r=>{if(r.url().includes('/api/'))result.requests.push({path:new URL(r.url()).pathname,method:r.request().method(),status:r.status(),requestId:r.headers()['x-request-id']})});
const phase=value=>{result.phase=value;writeFileSync(root+'/logs/browser-states-progress.json',JSON.stringify({phase:value,checks:result.checks}))};
async function login(){await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(fixture.loginName);await page.getByLabel('密码',{exact:true}).fill(fixture.password);await page.getByRole('button',{name:/^登\s*录$/}).click();await page.waitForURL('**/auth/first-password');await page.waitForFunction(()=>{const e=document.querySelector('.ant-modal');return e&&Math.abs(e.getBoundingClientRect().width-660)<.01&&getComputedStyle(e).opacity==='1'});}
async function captureGeometry(){
 result.geometry=await page.evaluate(()=>{
  const label=document.querySelector('.v12-first-password-modal .ant-form-item-label label'),input=document.querySelector('.v12-first-password-modal .ant-input'),hint=document.querySelector('.v12-first-password-modal .ant-form-item-extra');
  const debug=Array.from(document.querySelectorAll('.v12-first-password-modal .ant-form-item-label,.v12-first-password-modal .ant-form-item-label label,.v12-first-password-modal .ant-form-item-row,.v12-first-password-modal .ant-form-item-control')).slice(0,4).map(e=>{const c=getComputedStyle(e),r=e.getBoundingClientRect();return {className:e.className,top:r.top,height:r.height,display:c.display,lineHeight:c.lineHeight,padding:c.padding,margin:c.margin,gap:c.gap,fontSize:c.fontSize}});
  return {debug,dialogWidth:document.querySelector('.ant-modal').getBoundingClientRect().width,labelInputGap:input.getBoundingClientRect().top-label.getBoundingClientRect().bottom,inputHeight:input.getBoundingClientRect().height,hintLineHeight:getComputedStyle(hint).lineHeight,backdropInert:document.querySelector('.v12-first-password-page [inert]')!==null,backdropInputs:Array.from(document.querySelectorAll('.v12-first-password-page .v12-login input')).map(e=>({disabled:e.disabled,empty:e.value===''}))};
 });
 assert(Math.abs(result.geometry.labelInputGap-6)<.1,'label input gap differs from source6px');assert(result.geometry.hintLineHeight==='18.7px','helper line height differs from source11/1.7');assert(result.geometry.backdropInert&&result.geometry.backdropInputs.every(e=>e.disabled&&e.empty),'backdrop is interactive or contains credentials');
 await page.screenshot({path:root+'/logs/first-password-layout.png'});
}
const cookie=async()=>{const c=(await context.cookies()).find(c=>c.name==='__Host-ops-session');assert(c,'missing cookie');return c};
const oldStatus=async c=>(await context.request.get(origin+'/api/v1/auth/session',{headers:{Cookie:c.name+'='+c.value}})).status();
try{
 phase('logout failure');await login();await captureGeometry();assert(result.requests.every(r=>r.path.startsWith('/api/v1/auth/')),'background fetched business data');const failedCookie=await cookie();
 const failLogout=route=>route.abort('failed');await context.route('**/api/v1/auth/logout',failLogout);
 await page.getByRole('button',{name:'取消',exact:true}).click();await page.waitForURL('**/login');
 await page.getByText('上次退出的服务器会话尚未确认撤销',{exact:true}).waitFor();assert(await oldStatus(failedCookie)===200,'transport-failed logout unexpectedly revoked server session');
 await page.reload();await page.getByText('上次退出的服务器会话尚未确认撤销',{exact:true}).waitFor();assert(new URL(page.url()).pathname==='/login','unconfirmed logout restored identity');
 await page.screenshot({path:root+'/logs/logout-unconfirmed-reload.png'});result.checks.push('aborted logout clears UI identity, server cookie still200; reload keeps warning/login');
 phase('logout retry');await context.unroute('**/api/v1/auth/logout',failLogout);
 const [retried]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/logout'),page.getByRole('button',{name:'重试撤销',exact:true}).click()]);assert(retried.status()===200,'retry did not revoke');
 await page.getByText('上次退出的服务器会话尚未确认撤销',{exact:true}).waitFor({state:'hidden'});assert(await oldStatus(failedCookie)===401,'retried cookie still valid');result.checks.push('retry authenticates exact old session then logout200; old cookie401; warning cleared');
 for(const action of ['close','escape']){
  phase(action);await login();const old=await cookie();
  const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/logout'),action==='close'?page.getByRole('button',{name:'关闭',exact:true}).click():page.keyboard.press('Escape')]);assert(response.status()===200,action+' failed logout');await page.waitForURL('**/login');assert(await oldStatus(old)===401,action+' left session valid');result.checks.push(action+' -> real logout200/old cookie401');
 }
 phase('pending password');await login();const initialCookie=await cookie();
 await page.getByLabel('当前密码',{exact:true}).fill(fixture.password);await page.getByLabel('密码',{exact:true}).fill(nextPassword);await page.getByLabel('确认密码',{exact:true}).fill(nextPassword);
 let release,entered;const gate=new Promise(resolve=>release=resolve),arrival=new Promise(resolve=>entered=resolve);let intercepted=0;
 const pause=async route=>{intercepted++;entered();await gate;await route.continue()};
 await context.route('**/api/v1/auth/password:change',pause);
 const observePending=(async()=>{
  try{
   await Promise.race([arrival,new Promise((_,reject)=>setTimeout(()=>reject(new Error('password request was not intercepted')),15000))]);
   assert(await page.getByRole('button',{name:'取消',exact:true}).isDisabled(),'pending cancel enabled');assert(await page.getByRole('button',{name:/保存密码/}).isDisabled(),'pending save enabled');assert(await page.getByRole('button',{name:'关闭',exact:true}).isDisabled(),'pending close enabled');
   for(const label of ['当前密码','密码','确认密码'])assert(await page.getByLabel(label,{exact:true}).isDisabled(),'pending field enabled');
   await page.keyboard.press('Escape');assert(new URL(page.url()).pathname==='/auth/first-password','pending Escape dismissed form');await page.screenshot({path:root+'/logs/first-password-pending.png'});
  }catch(e){result.pendingFailure=e.message;throw e}finally{release()}
 })();
 const [changed]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change'),page.getByRole('button',{name:'保存密码',exact:true}).click(),observePending]);
 assert(changed.status()===201,'real password rotation failed');fixture.password=nextPassword;writeFileSync(root+'/private/browser-created-account.json',JSON.stringify(fixture),{mode:0o600});
 assert(intercepted===1,'duplicate password request');await context.unroute('**/api/v1/auth/password:change',pause);await page.getByText('未分配可用角色',{exact:true}).waitFor();assert(await oldStatus(initialCookie)===401,'old password-only cookie valid');
 result.checks.push('pending actual request disables all fields/save/cancel/close and ignores Escape; exactly one request then real201/old cookie401');
 const [out]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/logout'),page.getByRole('button',{name:/^退\s*出$/}).click()]);assert(out.status()===200,'final logout failed');result.status='passed';phase('complete');
}catch(e){await page.screenshot({path:root+'/logs/browser-failure.png'});result.failureView=await page.evaluate(()=>({path:location.pathname,dialog:!!document.querySelector('[role=dialog]'),notice:document.body.innerText.includes('上次退出的服务器会话尚未确认撤销'),closeDisabled:document.querySelector('.ant-modal-close')?.disabled}));result.status='failed';result.failure=sensitive.reduce((m,v)=>m.replaceAll(v,'[REDACTED]'),e.message);process.exitCode=1}
finally{writeFileSync(root+'/logs/browser-states-result.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,phase:result.phase,failure:result.failure,checks:result.checks}))}
