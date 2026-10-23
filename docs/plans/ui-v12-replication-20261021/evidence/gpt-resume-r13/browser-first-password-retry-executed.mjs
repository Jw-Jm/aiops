import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {randomUUID} from 'node:crypto';
const root='/data/ops-ui-v12-mfa-20261022-r1/gpt-resume-r13/preview',origin='https://192.168.0.63:30467';
const fixture=JSON.parse(readFileSync(root+'/private/browser-created-account.json','utf8'));
const nextPassword=randomUUID()+'B8!', wrongPassword=randomUUID()+'wrong';
const sensitive=[fixture.password,nextPassword,wrongPassword];
const result={origin,scope:'existing isolated account; real first-password/error/logout paths, no pixel comparison',checks:[],requests:[],pageErrors:[]};
const assert=(v,m)=>{if(!v)throw new Error(m)};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();
page.on('response',r=>{if(r.url().includes('/api/'))result.requests.push({path:new URL(r.url()).pathname,status:r.status(),requestId:r.headers()['x-request-id']})});
page.on('pageerror',e=>result.pageErrors.push(e.message));
try {
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(fixture.loginName);await page.getByLabel('密码',{exact:true}).fill(fixture.password);await page.getByRole('button',{name:/^登\s*录$/}).click();
 await page.waitForURL('**/auth/first-password');
 const oldCookie=(await context.cookies()).find(c=>c.name==='__Host-ops-session');assert(oldCookie,'missing first-password cookie');
 const blocked=await page.evaluate(async()=>{const r=await fetch('/api/v1/admin/users');return r.status});assert(blocked===403,'first-password session accessed business API');
 await page.reload();await page.getByRole('heading',{name:'设置新的登录密码'}).waitFor();
 await page.getByLabel('当前密码',{exact:true}).fill(wrongPassword);await page.getByLabel('新密码',{exact:true}).fill(nextPassword);await page.getByLabel('确认新密码',{exact:true}).fill(nextPassword);
 const rejected=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change');await page.getByRole('button',{name:'设置密码并继续',exact:true}).click();assert((await rejected).status()===401,'wrong current password accepted');
 await page.getByText('当前密码不正确。',{exact:true}).waitFor();await page.screenshot({path:root+'/logs/first-password-error-second.png'});
 await page.getByLabel('当前密码',{exact:true}).fill(fixture.password);
 const changed=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change');await page.getByRole('button',{name:'设置密码并继续',exact:true}).click();
 assert((await changed).status()===201,'first password change did not create a rotated session');
 fixture.password=nextPassword;writeFileSync(root+'/private/browser-created-account.json',JSON.stringify(fixture),{mode:0o600});
 await page.getByText('未分配可用角色',{exact:true}).waitFor();
 const stale=await context.request.get(origin+'/api/v1/auth/session',{headers:{Cookie:oldCookie.name+'='+oldCookie.value}});assert(stale.status()===401,'old password-only cookie survived rotation');
 await page.reload();await page.getByText('未分配可用角色',{exact:true}).waitFor();await page.screenshot({path:root+'/logs/no-role-after-password-second.png'});
 const logout=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/logout');await page.getByRole('button',{name:'退出',exact:true}).click();assert((await logout).status()===200,'logout did not revoke session');await page.waitForURL('**/login');
 const session=await context.request.get(origin+'/api/v1/auth/session');assert(session.status()===401,'logout still restored a session');
 assert(result.pageErrors.length===0,'browser page error');
 result.checks.push('first-password session survives reload but business API403', 'wrong current password401 and UI error', 'real password change201 rotates cookie and old cookie401', 'no roles after change/reload; no default authority', 'logout200 then session401');result.status='passed';
}catch(e){result.status='failed';result.failure=sensitive.reduce((message,value)=>message.replaceAll(value,'[REDACTED]'),e.message);process.exitCode=1}
finally{writeFileSync(root+'/logs/first-password-browser-result-second.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,failure:result.failure,checks:result.checks}))}
