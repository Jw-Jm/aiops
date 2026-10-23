import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {randomUUID} from 'node:crypto';
const root='/data/ops-ui-v12-mfa-20261022-r1/gpt-dialog-r14/preview',origin='https://192.168.0.63:30468';
const fixture=JSON.parse(readFileSync(root+'/private/browser-created-account.json','utf8'));
const nextPassword=randomUUID()+'B8!',wrongPassword=randomUUID()+'wrong';
const sensitive=[fixture.password,nextPassword,wrongPassword];
const result={origin,viewport:{width:1440,height:960},scope:'SL-005 real password-only session, logout, failure and rotation; security extensions explicitly differ from reference',checks:[],requests:[],pageErrors:[]};
const assert=(v,m)=>{if(!v)throw new Error(m)};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:result.viewport,locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();
page.on('response',r=>{if(r.url().includes('/api/'))result.requests.push({path:new URL(r.url()).pathname,method:r.request().method(),status:r.status(),requestId:r.headers()['x-request-id']})});
page.on('pageerror',e=>result.pageErrors.push(e.message));
async function login(){await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(fixture.loginName);await page.getByLabel('密码',{exact:true}).fill(fixture.password);await page.getByRole('button',{name:/^登\s*录$/}).click();await page.waitForURL('**/auth/first-password');await page.getByRole('dialog',{name:'首次登录：修改密码'}).waitFor();await page.waitForFunction(()=>{const e=document.querySelector('.ant-modal');return e&&Math.abs(e.getBoundingClientRect().width-660)<0.01&&getComputedStyle(e).opacity==='1'});}
try{
 await login();
 assert(result.requests.every(r=>r.path.startsWith('/api/v1/auth/')),'first-password page prefetched business data');
 const canceledCookie=(await context.cookies()).find(c=>c.name==='__Host-ops-session');assert(canceledCookie,'missing first-password cookie');
 await page.screenshot({path:root+'/logs/dialog-empty.png'});
 result.geometry=await page.evaluate(()=>{const pick=s=>{const e=document.querySelector(s),r=e.getBoundingClientRect(),c=getComputedStyle(e);return {x:r.x,y:r.y,width:r.width,height:r.height,padding:c.padding,borderRadius:c.borderRadius,fontSize:c.fontSize,lineHeight:c.lineHeight,color:c.color,backgroundColor:c.backgroundColor}};return Object.fromEntries(['.ant-modal','.ant-modal-header','.ant-modal-title','.ant-modal-body','.ant-modal-footer','.ant-input','.ant-form-item-label label','.ant-modal-footer .ant-btn-primary'].map(s=>[s,pick(s)]))});
 assert(result.geometry['.ant-modal'].width===660,'SL-005 dialog width mismatch');
 const [cancel]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/logout'),page.getByRole('button',{name:'取消',exact:true}).click()]);assert(cancel.status()===200,'cancel did not revoke server session');await page.waitForURL('**/login');
 const revoked=await context.request.get(origin+'/api/v1/auth/session',{headers:{Cookie:canceledCookie.name+'='+canceledCookie.value}});assert(revoked.status()===401,'cancel left old session active');result.checks.push('no business prefetch before rotation','cancel -> logout200 and old cookie401');
 await login();await page.reload();await page.getByRole('dialog',{name:'首次登录：修改密码'}).waitFor();
 const oldCookie=(await context.cookies()).find(c=>c.name==='__Host-ops-session');
 const blocked=await page.evaluate(async()=> (await fetch('/api/v1/admin/users')).status);assert(blocked===403,'password-only session accessed business API');
 await page.getByLabel('当前密码',{exact:true}).fill(wrongPassword);await page.getByLabel('密码',{exact:true}).fill(nextPassword);await page.getByLabel('确认密码',{exact:true}).fill(nextPassword+'mismatch');
 const before=result.requests.filter(r=>r.path==='/api/v1/auth/password:change').length;
 await page.getByRole('button',{name:'保存密码',exact:true}).click();await page.getByText('两次输入的新密码不一致',{exact:true}).waitFor();assert(result.requests.filter(r=>r.path==='/api/v1/auth/password:change').length===before,'mismatch submitted a password request');
 await page.getByLabel('确认密码',{exact:true}).fill(nextPassword);
 const [rejected]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change'),page.getByRole('button',{name:'保存密码',exact:true}).click()]);assert(rejected.status()===401,'wrong current password accepted');await page.getByText('当前密码不正确。',{exact:true}).waitFor();
 await page.screenshot({path:root+'/logs/dialog-current-password-error.png'});
 assert((await context.cookies()).some(c=>c.name===oldCookie.name&&c.value===oldCookie.value),'wrong password destroyed valid session');
 await page.getByLabel('当前密码',{exact:true}).fill(fixture.password);
 const [changed]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change'),page.getByRole('button',{name:'保存密码',exact:true}).click()]);assert(changed.status()===201,'real rotation did not return201');fixture.password=nextPassword;writeFileSync(root+'/private/browser-created-account.json',JSON.stringify(fixture),{mode:0o600});
 await page.getByText('未分配可用角色',{exact:true}).waitFor();assert((await context.request.get(origin+'/api/v1/auth/session',{headers:{Cookie:oldCookie.name+'='+oldCookie.value}})).status()===401,'old cookie survived rotation');
 await page.reload();await page.getByText('未分配可用角色',{exact:true}).waitFor();
 const [logout]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/logout'),page.getByRole('button',{name:/^退\s*出$/}).click()]);assert(logout.status()===200,'logout failed');await page.waitForURL('**/login');assert((await context.request.get(origin+'/api/v1/auth/session')).status()===401,'logout restored session');
 assert(!result.pageErrors.length,'browser page error');result.checks.push('reload retains forced rotation and business403','confirm mismatch makes no API request','wrong current password401 retains session and inline error','real rotation201 invalidates prior cookie; reload persists no-role identity','logout200/session401');result.status='passed';
}catch(e){result.status='failed';result.failure=sensitive.reduce((m,v)=>m.replaceAll(v,'[REDACTED]'),e.message);process.exitCode=1}
finally{writeFileSync(root+'/logs/first-password-result.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,failure:result.failure,checks:result.checks,geometry:result.geometry}))}
