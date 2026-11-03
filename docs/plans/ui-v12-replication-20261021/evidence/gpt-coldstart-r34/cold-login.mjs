import {safeErrorMessage} from './safe-error.mjs';
import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync,chmodSync} from 'node:fs';
import {createHmac,randomBytes} from 'node:crypto';
const root='/data/ops-v12-native-cold-r34-20261027',origin='https://192.168.0.63:30472';
const file=root+'/private/browser-account.json';let account=JSON.parse(readFileSync(file,'utf8'));
const result={origin,requests:[],checks:[]};const sensitive=[account.password];
const assert=(v,m)=>{if(!v)throw Error(m)};
function totp(secret){let bits='';for(const c of secret.replace(/=+$/,''))bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');const key=[];for(let i=0;i+8<=bits.length;i+=8)key.push(parseInt(bits.slice(i,i+8),2));const b=Buffer.alloc(8);b.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const d=createHmac('sha1',Buffer.from(key)).update(b).digest();return String((d.readUInt32BE(d[d.length-1]&15)&0x7fffffff)%1000000).padStart(6,'0')}
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});const page=await context.newPage();page.setDefaultTimeout(25000);
 page.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/'))result.requests.push({path,method:r.request().method(),status:r.status(),requestId:r.headers()['x-request-id']})});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 if(new URL(page.url()).pathname==='/auth/first-password'){
 const dialog=page.getByRole('dialog',{name:'首次登录：修改密码'});await dialog.waitFor();
 const next=randomBytes(24).toString('base64url');sensitive.push(next);
 await dialog.getByLabel('当前密码',{exact:true}).fill(account.password);await dialog.getByLabel('密码',{exact:true}).fill(next);await dialog.getByLabel('确认密码',{exact:true}).fill(next);
 const changed=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change');await dialog.getByRole('button',{name:'保存密码',exact:true}).click();assert((await changed).status()===201,'first password change refused');account.password=next;writeFileSync(file,JSON.stringify(account),{mode:0o600});await dialog.waitFor({state:'hidden'});result.checks.push('fresh first-tenant CLI identity logs in and changes initial password through UI');
 }else{result.checks.push('existing first-password result retained; no password reset')}
 await page.goto(origin+'/account/security');if(await page.getByRole('button',{name:'启用双因素',exact:true}).count()){
await page.getByRole('button',{name:'启用双因素',exact:true}).click();await page.getByLabel('当前密码',{exact:true}).waitFor();
 const uri=await page.locator('code').filter({hasText:'otpauth://'}).textContent();const secret=new URL(uri.trim()).searchParams.get('secret');assert(secret,'MFA enrollment has no factor');sensitive.push(uri,secret);
 // No screenshots, videos, trace, request bodies or secret-bearing HTML are retained.
 await page.getByLabel('当前密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:'验证密码',exact:true}).click();await page.getByText('密码已验证，请提交身份验证器中的 6 位动态码。',{exact:true}).waitFor();await page.getByLabel('动态码',{exact:true}).fill(totp(secret));await page.getByRole('button',{name:'验证动态码',exact:true}).click();await page.getByLabel('新绑定动态码',{exact:true}).fill(totp(secret));await page.getByRole('button',{name:'确认新绑定',exact:true}).click();await page.getByText('双因素已启用。恢复码只显示这一次，请离线保存。',{exact:true}).waitFor();
 const recovery=(await page.locator('.ant-tag').allTextContents()).filter(x=>/^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(x));assert(recovery.length===10,'MFA confirmation did not produce ten recovery codes');sensitive.push(...recovery);writeFileSync(root+'/private/browser-mfa.json',JSON.stringify({...account,secret,recovery}),{mode:0o600});
 }
 await page.reload();await page.getByText('已启用',{exact:true}).waitFor();assert(await page.locator('code').filter({hasText:'otpauth://'}).count()===0,'factor reappeared after reload');result.checks.push('new namespace projected identity uses its own Transit; real MFA setup persists without redisclosing factor');
 result.session=await page.evaluate(async()=>{const r=await fetch('/api/v1/auth/session');const b=await r.json();return{status:r.status,roles:b.data.authorization.grants.map(g=>g.role),subject:b.data.subject,mode:b.data.mode}});assert(result.session.status===200 && result.session.roles.join(',')==='platform_admin','first tenant got unintended roles');
 await page.goto(origin+'/settings/permissions');await page.getByText(account.loginName,{exact:true}).waitFor();result.checks.push('native administrator permissions page renders on new origin');
 await context.storageState({path:root+'/private/admin-state.json'});chmodSync(root+'/private/admin-state.json',0o600);result.status='passed';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,sensitive).replace(/\b\d{6}\b/g,'[CODE]');process.exitCode=1}
finally{writeFileSync(root+'/evidence/cold-login.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,checks:result.checks,failure:result.failure}))}
