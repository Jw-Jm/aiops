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
const phase=process.argv[2];assertPhase();function assertPhase(){if(!['before','after'].includes(phase))throw new Error('explicit before/after phase required')}
const wanted=phase==='before'?400:201;
const sensitive=[fixture.password,identity.rotatedPassword];
const result={origin,schemaVersion:56,scope:'200-namespace NativeIdentityScopeV1 boundary on isolated r15 fixture; no source qualification',checks:[],requests:[],pageErrors:[]};
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
 for(const role of ['operator']){
 const row=page.getByRole('row').filter({has:page.getByText(login,{exact:true})});
 await row.getByRole('button',{name:'配置授权',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'配置用户授权'});
 if(role==='operator'){
  await dialog.getByLabel('集群范围',{exact:true}).click();await page.locator('.ant-select-item-option').first().click();await dialog.getByText('集群范围',{exact:true}).click();
 }else{
  await dialog.getByLabel('角色',{exact:true}).click();await page.locator('.ant-select-item-option').filter({hasText:'平台管理员'}).click();
 }
 await dialog.getByLabel('命名空间',{exact:true}).fill(Array.from({length:200},(_,i)=>'ns-'+String(i).padStart(3,'0')).join(','));
 await dialog.getByLabel('操作原因',{exact:true}).fill('用户明确要求独立账号查看V1.2全部菜单；仅任务隔离环境');
 const submit=dialog.getByRole('button',{name:'确认提交',exact:true});
 assert(await submit.isDisabled(),'grant enabled without proof');
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
 const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/admin/role-bindings'&&r.request().method()==='POST'),submit.click()]);
 assert(response.status()===wanted,'200-namespace grant status '+response.status()+' expected '+wanted);
 const body=await response.json();result.checks.push('200 namespaces via exact password+TOTP: HTTP'+response.status());if(phase==='before'){assert(body.code==='INVALID_ARGUMENT','unexpected rejection');break;}assert(body.data.scope.namespaces.length===200,'stored grant lost namespaces');result.grant=body.data;
 await dialog.waitFor({state:'hidden'});await page.reload();await page.getByText(login,{exact:true}).waitFor();
 }
 result.loginName=login;result.status='passed';
}catch(e){result.status='failed';result.failure=sensitive.reduce((message,value)=>value?message.replaceAll(value,'[REDACTED]'):message,e.message);process.exitCode=1}
finally {writeFileSync(root+'/logs/scope-'+phase+'-result.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,failure:result.failure,checks:result.checks}))}
