import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHmac } from 'node:crypto';
import { execFileSync } from 'node:child_process';
const root='/data/ops-ui-v12-mfa-20261022-r1/gpt-r6-preview';
const origin='https://192.168.0.63:30466';
const identity=JSON.parse(readFileSync(root+'/private/browser-identity.json','utf8'));
const result={origin,schemaVersion:54,checks:[],requests:[],pageErrors:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});
const page=await context.newPage();
page.on('response',r=>{if(r.url().includes('/api/'))result.requests.push({path:new URL(r.url()).pathname,status:r.status(),requestId:r.headers()['x-request-id']})});
page.on('pageerror',e=>result.pageErrors.push(e.message));
function code(secret,step=Math.floor(Date.now()/30000)){
 let bits='';for(const c of secret.toUpperCase().replace(/=+$/,''))bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');
 const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));
 const b=Buffer.alloc(8);b.writeBigUInt64BE(BigInt(step));const d=createHmac('sha1',Buffer.from(bytes)).update(b).digest();const n=d[d.length-1]&15;
 return String((d.readUInt32BE(n)&0x7fffffff)%1000000).padStart(6,'0');
}
const assert=(condition,message)=>{if(!condition)throw new Error(message)};
const api=path=>page.evaluate(async p=>{const r=await fetch(p);return {status:r.status,body:await r.json()}},path);
async function login(password){await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(identity.loginName);await page.getByLabel('密码',{exact:true}).fill(password);await page.getByRole('button',{name:/^登\s*录$/}).click();}
const waitText=text=>page.getByText(text,{exact:true}).waitFor();
async function setFactor(oldSecret){
 await page.goto(origin+'/account/security');
 await page.getByRole('button',{name:oldSecret?'重新绑定双因素':'启用双因素',exact:true}).click();
 await page.getByLabel('当前密码',{exact:true}).waitFor();
 const uri=await page.locator('code').filter({hasText:'otpauth://'}).textContent();
 const secret=new URL(uri.trim()).searchParams.get('secret');
 // Actual form submits: no direct API call can replace either retry assertion.
 await page.getByLabel('当前密码',{exact:true}).fill('wrong-synthetic-password');
 await page.getByRole('button',{name:'验证密码',exact:true}).click();await waitText('当前密码不正确，挑战仍待完成。');
 await page.getByLabel('当前密码',{exact:true}).fill(identity.rotatedPassword);
 await page.getByRole('button',{name:'验证密码',exact:true}).click();await waitText('密码已验证，请提交身份验证器中的 6 位动态码。');
 const valid=code(oldSecret??secret,Math.floor(Date.now()/30000)+(oldSecret?1:0));
 const wrong=String((Number(valid)+17)%1000000).padStart(6,'0');
 await page.getByLabel('动态码',{exact:true}).fill(wrong);await page.getByRole('button',{name:'验证动态码',exact:true}).click();await waitText('动态码不正确或该挑战已失效。');
 await page.getByLabel('动态码',{exact:true}).fill(valid);await page.getByRole('button',{name:'验证动态码',exact:true}).click();await page.getByLabel('新绑定动态码',{exact:true}).waitFor();
 if(oldSecret){const status=await api('/api/v1/auth/mfa/status');assert(status.body.data.state==='confirmed','old factor was revoked before replacement confirmed');}
 await page.getByLabel('新绑定动态码',{exact:true}).fill(code(secret));await page.getByRole('button',{name:'确认新绑定',exact:true}).click();await waitText('双因素已启用。恢复码只显示这一次，请离线保存。');
 const codes=await page.locator('.ant-tag').allTextContents();const recovery=codes.filter(x=>/^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(x));assert(recovery.length===10,'expected ten single-display recovery codes');
 result.checks.push(oldSecret?'actual UI rebind validates old factor and confirms new factor atomically':'actual UI initial enrollment creates challenge, retries password/TOTP and confirms');
 return {secret,recovery};
}
try{
 await login(identity.password);await page.waitForURL('**/auth/first-password');
 await page.getByLabel('当前密码',{exact:true}).fill(identity.password);await page.getByLabel('新密码',{exact:true}).fill(identity.rotatedPassword);await page.getByLabel('确认新密码',{exact:true}).fill(identity.rotatedPassword);await page.getByRole('button',{name:'设置密码并继续',exact:true}).click();await page.waitForURL('**/access/sources');
 const initial=await setFactor();const replaced=await setFactor(initial.secret);
 for(const failure of ['500','timeout','offline']){
  await page.route('**/api/v1/auth/logout',route=>failure==='500'?route.fulfill({status:500,contentType:'application/json',body:JSON.stringify({code:'INTERNAL',message:'isolated revocation failure'})}):route.abort(failure==='timeout'?'timedout':'internetdisconnected'));
  await page.getByRole('button',{name:'退出',exact:true}).click();await page.waitForURL('**/login');await waitText('上次退出的服务器会话尚未确认撤销');
  const stillLive=await api('/api/v1/auth/session');assert(stillLive.status===200,'failure fixture must retain a live server cookie');
  await page.reload();await page.getByLabel('用户名',{exact:true}).waitFor();await waitText('上次退出的服务器会话尚未确认撤销');assert(new URL(page.url()).pathname==='/login','refresh adopted pending server identity');
  await page.unroute('**/api/v1/auth/logout');await page.getByRole('button',{name:'重试撤销',exact:true}).click();await page.getByText('上次退出的服务器会话尚未确认撤销',{exact:true}).waitFor({state:'hidden'});
  assert((await api('/api/v1/auth/session')).status===401,'retry did not revoke old server cookie');
  result.checks.push(failure+' logout failure + real GET200 + reload stays signed out + controlled retry401');
  await login(identity.rotatedPassword);await page.waitForURL('**/access/sources');
 }
 await page.goto(origin+'/account/security');await page.getByRole('button',{name:'创建恢复挑战',exact:true}).click();await page.getByLabel('恢复码',{exact:true}).fill(replaced.recovery[0]);await page.getByRole('button',{name:'提交恢复码',exact:true}).click();await page.waitForURL('**/login');
 await login(identity.rotatedPassword);await page.waitForURL('**/account/security');
 const setup=await api('/api/v1/auth/session');assert(setup.body.data.mustReenrollMfa===true,'recovery restriction did not persist across login');assert(setup.body.data.authorization.grants.length===0,'restricted session returned business grants');assert((await api('/api/v1/admin/clusters')).status===403,'restricted session accessed business route');
 result.checks.push('recovery survives relogin, redirects to setup and business request403');
 await setFactor();assert((await api('/api/v1/auth/session')).body.data.mustReenrollMfa===false,'confirmed enrollment did not release setup restriction');assert((await api('/api/v1/admin/clusters')).status===200,'business authorization not restored after enrollment');
 result.checks.push('new factor confirmation restores persisted business authorization');
 // Exercise the actual Transit backend outage, with no plaintext fallback.
 execFileSync('docker',['stop','ops-ui-v12-gpt-r6-openbao'],{stdio:'pipe'});
 const failedSeal=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/mfa/enrollments' && r.request().method()==='POST');
 await page.getByRole('button',{name:'重新绑定双因素',exact:true}).click();
 assert((await failedSeal).status()===503,'unavailable Transit did not fail closed503');
 const statusDuringOutage=await api('/api/v1/auth/mfa/status');assert(statusDuringOutage.body.data.state==='confirmed','failed seal changed existing factor');
 execFileSync('docker',['start','ops-ui-v12-gpt-r6-openbao'],{stdio:'pipe'});
 execFileSync('python3',[root+'/unseal-task.py'],{stdio:'pipe'});
 const restoredSeal=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/mfa/enrollments' && r.request().method()==='POST');
 await page.getByRole('button',{name:'重新绑定双因素',exact:true}).click();
 assert((await restoredSeal).status()===201,'Transit recovery did not restore real enrollment');
 await page.getByRole('button',{name:'取消绑定',exact:true}).click();
 result.checks.push('actual Transit outage503 preserves confirmed factor; persistent Raft restart/unseal restores encryption without file fallback');
 assert(result.pageErrors.length===0,'browser page errors');result.status='passed';
 // Capture no provisioning URI, input value, recovery code or credential.
 await page.goto(origin+'/access/sources');await page.screenshot({path:root+'/logs/business-after-recovery.png'});
}catch(e){result.status='failed';result.failure=e.message;process.exitCode=1;}
finally{writeFileSync(root+'/logs/browser-result.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,checks:result.checks,failure:result.failure}));}
