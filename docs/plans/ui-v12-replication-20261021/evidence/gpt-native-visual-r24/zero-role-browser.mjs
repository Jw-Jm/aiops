import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync,chmodSync} from 'node:fs';
import {randomBytes} from 'node:crypto';
const root='/data/ops-v12-native-visual-r24-20261026',origin='https://192.168.0.63:30471',file=root+'/private/member-account.json';
let identity=JSON.parse(readFileSync(file,'utf8'));const sensitive=[identity.password];const result={origin,checks:[],requests:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});const page=await context.newPage();page.setDefaultTimeout(15000);
page.on('response',r=>{if(new URL(r.url()).pathname.startsWith('/api/'))result.requests.push({path:new URL(r.url()).pathname,status:r.status(),requestId:r.headers()['x-request-id']});});
const assert=(ok,message)=>{if(!ok)throw new Error(message);};
async function shot(name){await page.waitForFunction(()=>[...document.querySelectorAll('.ant-modal')].every(n=>getComputedStyle(n).opacity==='1'&&getComputedStyle(n).transform==='none'));await page.screenshot({path:root+'/evidence/'+name+'.png'});}
try{
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(identity.loginName);await page.getByLabel('密码',{exact:true}).fill(identity.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!=='/login');
 if(new URL(page.url()).pathname==='/auth/first-password'){
  const d=page.getByRole('dialog',{name:'首次登录：修改密码'});await d.waitFor();const next=randomBytes(24).toString('base64url');sensitive.push(next);await d.getByLabel('当前密码',{exact:true}).fill(identity.password);await d.getByLabel('密码',{exact:true}).fill(next);await d.getByLabel('确认密码',{exact:true}).fill(next);const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change');await d.getByRole('button',{name:'保存密码',exact:true}).click();assert((await response).status()===201,'zero-role first password rejected');identity={...identity,password:next};writeFileSync(file,JSON.stringify(identity),{mode:0o600});await page.waitForURL(u=>u.pathname!=='/auth/first-password');
 }
 await page.goto(origin+'/account/security');await page.getByRole('button',{name:'启用双因素',exact:true}).waitFor();assert(await page.evaluate(async()=>(await fetch('/api/v1/auth/mfa/status')).status)===200,'zero-role MFA status unavailable');await shot('zero-role-own-security');result.checks.push('zero-role native account changes initial password and reaches own MFA settings');
 await page.goto(origin+'/settings/permissions');await page.getByText('需要平台管理员权限',{exact:true}).waitFor();assert(await page.evaluate(async()=>(await fetch('/api/v1/admin/users')).status)===403,'zero-role account gained administrator API');await shot('role-denied-admin');
 await page.getByRole('button',{name:'查看当前身份',exact:true}).click();const d=page.getByRole('dialog',{name:'我的账号',exact:true});await d.waitFor();assert(await d.getByText('未授权',{exact:true}).count()===2,'independent grants not denied');await shot('role-denied-account');await d.getByRole('button',{name:'修改密码',exact:true}).click();await page.getByRole('dialog',{name:'修改我的密码',exact:true}).waitFor();await shot('role-denied-self-password');await page.keyboard.press('Escape');
 await page.goto(origin+'/overview');await page.getByText('需要运维操作员权限',{exact:true}).waitFor();await shot('role-denied-operator');result.checks.push('both role routes denied; actual admin API403; current identity and self-password remain available without granting roles');
 await context.storageState({path:root+'/private/member-storage-state.json'});chmodSync(root+'/private/member-storage-state.json',0o600);result.status='passed';
}catch(error){result.status='failed';result.failure=sensitive.reduce((s,v)=>s.replaceAll(v,'[REDACTED]'),error.message);process.exitCode=1;}
finally{writeFileSync(root+'/evidence/zero-role-browser.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,checks:result.checks,failure:result.failure}));}
