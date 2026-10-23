// Isolated account fixture; this is runtime wiring evidence, not HTML pixel acceptance.
import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {createHmac,randomUUID} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import https from 'node:https';
const root='/data/ops-ui-v12-mfa-20261022-r1/gpt-login-r15/preview-r2';
const bao='/data/ops-ui-v12-mfa-20261022-r1/gpt-r6-openbao';
const origin='https://192.168.0.63:30469';
const identity=JSON.parse(readFileSync(root+'/private/browser-identity.json','utf8'));
const tenant=readFileSync(root+'/private/tenant-id','utf8').trim();
const fixture=JSON.parse(readFileSync(root+'/private/browser-created-account.json','utf8'));
const login=fixture.loginName;
const resetPassword=randomUUID()+'D5!',finalPassword=randomUUID()+'E6!';
const sensitive=[fixture.password,identity.rotatedPassword,resetPassword,finalPassword];
const result={origin,schemaVersion:57,scope:'isolated r15 operator password reset/forced change, persisted scope and final footer geometry; schema57',checks:[],requests:[],pageErrors:[]};
const sql=q=>execFileSync('docker',['exec','-i','ops-ui-v12-mfa-pg-20261022','psql','-U','postgres','-d','ui_v12_native_identity','-X','-tAc',q],{encoding:'utf8'}).trim();
const assert=(v,m)=>{if(!v)throw new Error(m)};
function request(path,body,token){return new Promise((resolve,reject)=>{const q=https.request('https://127.0.0.1:18206/v1/'+path,{method:'POST',ca:readFileSync(bao+'/tls/ca.crt'),servername:'mfa-gpt-r6.ops.local',headers:{'Content-Type':'application/json',...(token?{'X-Vault-Token':token}:{})}},r=>{let raw='';r.on('data',b=>raw+=b);r.on('end',()=>{if(r.statusCode!==200)return reject(new Error('task Bao status '+r.statusCode));resolve(JSON.parse(raw))})});q.on('error',reject);q.end(JSON.stringify(body))})}
function code(secret,step){let bits='';for(const c of secret.toUpperCase().replace(/=+$/,''))bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));const b=Buffer.alloc(8);b.writeBigUInt64BE(BigInt(step));const h=createHmac('sha1',Buffer.from(bytes)).update(b).digest(),o=h[h.length-1]&15;return String((h.readUInt32BE(o)&0x7fffffff)%1000000).padStart(6,'0')}
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();
page.on('response',r=>{if(r.url().includes('/api/'))result.requests.push({path:new URL(r.url()).pathname,status:r.status(),requestId:r.headers()['x-request-id']})});
page.on('pageerror',e=>result.pageErrors.push(e.message));
try {
 await page.goto(origin+'/login');
 await page.getByLabel('用户名',{exact:true}).fill(identity.loginName);
 await page.getByLabel('密码',{exact:true}).fill(identity.rotatedPassword);
 await page.getByRole('button',{name:/^登\s*录$/}).click();
 await page.waitForURL('**/access/sources');
 await page.goto(origin+'/settings/permissions');
 const row=page.getByRole('row').filter({has:page.getByText(login,{exact:true})});await row.getByRole('button',{name:'详情',exact:true}).click();
 await page.getByRole('dialog',{name:'用户详情'}).getByRole('button',{name:'重置用户密码',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'重置用户密码'});
 await dialog.getByLabel('密码',{exact:true}).fill(resetPassword);await dialog.getByLabel('确认密码',{exact:true}).fill(resetPassword);
 const submit=dialog.getByRole('button',{name:'确认提交',exact:true});assert(await submit.isDisabled(),'reset enabled without proof');
 const factor=JSON.parse(sql(`SELECT json_build_object('id',e.enrollment_id,'envelope',e.secret_ciphertext,'step',e.last_used_step,'keyRef',e.secret_key_ref) FROM identity.mfa_enrollments e JOIN identity.accounts a USING(tenant_id,user_id) WHERE e.tenant_id='${tenant}' AND a.login_name='${identity.loginName}' AND e.state='confirmed'`));
 assert(factor.keyRef==='openbao:ui-v12-mfa','unexpected fixture factor key');
 const loginBao=await request('auth/kubernetes/login',{role:'ops-api-workload',jwt:readFileSync(bao+'/private/projected.jwt','utf8').trim()});
 const decrypted=await request('transit/decrypt/ui-v12-mfa',{ciphertext:JSON.parse(factor.envelope).ciphertext,associated_data:Buffer.from('ops-sensitive-object/v1|'+tenant+'|'+factor.id).toString('base64')},loginBao.auth.client_token);
 const secret=Buffer.from(decrypted.data.plaintext,'base64').toString('utf8');
 sensitive.push(secret);
 const step=Math.max(Math.floor(Date.now()/30000),Number(factor.step)+1);
 while(Math.floor(Date.now()/30000)<step) await new Promise(resolve=>setTimeout(resolve,Math.min(1000,step*30000-Date.now())));
 await dialog.getByLabel('验证密码',{exact:true}).fill(identity.rotatedPassword);
 const totp=code(secret,step); sensitive.push(totp);
 await dialog.getByLabel('验证动态码',{exact:true}).fill(totp);
 await dialog.getByRole('button',{name:'验证本次操作',exact:true}).click();
 await page.waitForFunction(()=>Array.from(document.querySelectorAll('button')).some(b=>b.textContent==='确认提交'&&!b.disabled));
 const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/password:reset')),submit.click()]);assert(response.status()===201,'real password reset failed');
 fixture.password=resetPassword;writeFileSync(root+'/private/browser-created-account.json',JSON.stringify(fixture),{mode:0o600});
 await dialog.waitFor({state:'hidden'});await context.close();
 const viewer=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),v=await viewer.newPage();
 await v.goto(origin+'/login');await v.getByLabel('用户名',{exact:true}).fill(login);await v.getByLabel('密码',{exact:true}).fill(fixture.password);await v.getByRole('button',{name:/^登\s*录$/}).click();await v.waitForURL('**/auth/first-password');
 await v.waitForFunction(()=>{const e=document.querySelector('.ant-modal');return e&&Math.abs(e.getBoundingClientRect().width-660)<.01&&getComputedStyle(e).opacity==='1'});
 result.footer=await v.evaluate(()=>{const e=document.querySelectorAll('.ant-modal-footer > .ant-btn');return {gap:e[1].getBoundingClientRect().left-e[0].getBoundingClientRect().right,width:document.querySelector('.ant-modal').getBoundingClientRect().width}});assert(Math.abs(result.footer.gap-10)<.1,'footer differs from source10px gap');
 await v.screenshot({path:root+'/logs/footer-final.png'});
 const f=v.getByRole('dialog',{name:'首次登录：修改密码'});await f.getByLabel('当前密码',{exact:true}).fill(fixture.password);await f.getByLabel('密码',{exact:true}).fill(finalPassword);await f.getByLabel('确认密码',{exact:true}).fill(finalPassword);
 const [rotated]=await Promise.all([v.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change'),f.getByRole('button',{name:'保存密码',exact:true}).click()]);assert(rotated.status()===201,'reset first password rotation failed');fixture.password=finalPassword;writeFileSync(root+'/private/browser-created-account.json',JSON.stringify(fixture),{mode:0o600});
 await v.waitForURL('**/overview**');await v.reload();await v.getByRole('navigation',{name:'平台功能'}).getByText('资源管理',{exact:true}).waitFor();assert(await v.getByRole('navigation',{name:'平台功能'}).getByText('接入管理',{exact:true}).count()===0,'operator inherited admin menu');
 const session=await viewer.request.get(origin+'/api/v1/auth/session');const data=(await session.json()).data;assert(data.mode==='full'&&data.authorization.grants.length===1&&data.authorization.grants[0].role==='operator'&&data.authorization.grants[0].scope.namespaces.length===200,'reset lost independent200-namespace grant');
 result.checks.push('real UI password+TOTP-bound reset201 forces first password','footer10px gap and660px width','real rotation201 and reload land operator overview','current session preserves exactly operator200 namespaces without admin');result.status='passed';
}catch(e){result.status='failed';result.failure=sensitive.reduce((message,value)=>value?message.replaceAll(value,'[REDACTED]'):message,e.message);process.exitCode=1}
finally {writeFileSync(root+'/logs/operator-reset-result.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,failure:result.failure,checks:result.checks}))}
