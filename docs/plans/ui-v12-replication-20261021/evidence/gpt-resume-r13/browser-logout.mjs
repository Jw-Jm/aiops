import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
const root='/data/ops-ui-v12-mfa-20261022-r1/gpt-resume-r13/preview',origin='https://192.168.0.63:30467';
const fixture=JSON.parse(readFileSync(root+'/private/browser-created-account.json','utf8'));
const result={origin,scope:'resume only the interrupted logout check using the already rotated fixture account',checks:[],requests:[],pageErrors:[]};
const assert=(v,m)=>{if(!v)throw new Error(m)};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();
page.on('response',r=>{if(r.url().includes('/api/'))result.requests.push({path:new URL(r.url()).pathname,status:r.status(),requestId:r.headers()['x-request-id']})});
page.on('pageerror',e=>result.pageErrors.push(e.message));
try{
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(fixture.loginName);await page.getByLabel('密码',{exact:true}).fill(fixture.password);await page.getByRole('button',{name:/^登\s*录$/}).click();
 await page.getByText('未分配可用角色',{exact:true}).waitFor();
 const [logout]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/logout'),page.getByRole('button',{name:/^退\s*出$/}).click()]);
 assert(logout.status()===200,'logout did not revoke session');await page.waitForURL('**/login');
 const session=await context.request.get(origin+'/api/v1/auth/session');assert(session.status()===401,'logged out session still active');
 await page.reload();await page.getByRole('button',{name:/^登\s*录$/}).waitFor();
 assert(result.pageErrors.length===0,'browser page error');await page.screenshot({path:root+'/logs/logout-complete.png'});
 result.checks.push('rotated password login201 and no default authority', 'real UI logout200 then session401', 'reload stays logged out');result.status='passed';
}catch(e){result.status='failed';result.failure=e.message.replaceAll(fixture.password,'[REDACTED]');process.exitCode=1}
finally{writeFileSync(root+'/logs/logout-browser-result.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,failure:result.failure,checks:result.checks}))}
