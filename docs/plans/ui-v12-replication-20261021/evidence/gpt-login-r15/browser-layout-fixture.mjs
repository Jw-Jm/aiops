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
const login='browser.r15.'+randomUUID().replaceAll('-','').slice(0,12), password=randomUUID()+'A9!';
const sensitive=[password,identity.rotatedPassword];
const result={origin,schemaVersion:56,scope:'isolated account fixture; no source qualification or pixel acceptance',checks:[],requests:[],pageErrors:[]};
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
 await page.getByRole('button',{name:'＋ 创建用户',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'创建内置账号'});
 await dialog.getByLabel('用户名',{exact:true}).fill(login);
 await dialog.getByLabel('姓名',{exact:true}).fill('R15 layout fixture');
 await dialog.getByLabel('密码',{exact:true}).fill(password);
 await dialog.getByLabel('确认密码',{exact:true}).fill(password);
 const submit=dialog.getByRole('button',{name:'确认提交',exact:true});
 assert(await submit.isDisabled(),'creation enabled without proof');
 const factor=JSON.parse(sql(`SELECT json_build_object('id',e.enrollment_id,'envelope',e.secret_ciphertext,'step',e.last_used_step,'keyRef',e.secret_key_ref) FROM identity.mfa_enrollments e JOIN identity.accounts a USING(tenant_id,user_id) WHERE e.tenant_id='${tenant}' AND a.login_name='${identity.loginName}' AND e.state='confirmed'`));
 assert(factor.keyRef==='openbao:ui-v12-mfa','unexpected fixture factor key');
 const loginBao=await request('auth/kubernetes/login',{role:'ops-api-workload',jwt:readFileSync(bao+'/private/projected.jwt','utf8').trim()});
 const decrypted=await request('transit/decrypt/ui-v12-mfa',{ciphertext:JSON.parse(factor.envelope).ciphertext,associated_data:Buffer.from('ops-sensitive-object/v1|'+tenant+'|'+factor.id).toString('base64')},loginBao.auth.client_token);
 const secret=Buffer.from(decrypted.data.plaintext,'base64').toString('utf8');
 sensitive.push(secret);
 const step=Math.max(Math.floor(Date.now()/30000),Number(factor.step)+1);
 assert(step<=Math.floor(Date.now()/30000)+1,'wait for a real unused TOTP window');
 await dialog.getByLabel('验证密码',{exact:true}).fill(identity.rotatedPassword);
 const totp=code(secret,step); sensitive.push(totp);
 await dialog.getByLabel('验证动态码',{exact:true}).fill(totp);
 await dialog.getByRole('button',{name:'验证本次操作',exact:true}).click();
 await page.waitForFunction(()=>Array.from(document.querySelectorAll('button')).some(b=>b.textContent==='确认提交'&&!b.disabled));
 const createdResponse=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/admin/users'&&r.request().method()==='POST');
 await submit.click();
 const response=await createdResponse; assert(response.status()===201,'actual account creation failed');
 const account=(await response.json()).data;
 assert(account.mustChangePassword&&account.roleGrants.length===0,'creation bypassed password rotation or inherited roles');
 writeFileSync(root+'/private/browser-created-account.json',JSON.stringify({loginName:login,password,userId:account.userId}),{mode:0o600,flag:'wx'});
 await dialog.waitFor({state:'hidden'});
 await page.reload();
 await page.getByText(login,{exact:true}).waitFor();
 const stored=JSON.parse(sql(`SELECT json_build_object('userId',user_id,'subject',subject,'revision',account_revision,'mustChangePassword',must_change_password,'status',status) FROM identity.accounts WHERE tenant_id='${tenant}' AND user_id='${account.userId}'`));
 assert(stored.subject===account.subject&&stored.mustChangePassword&&stored.status==='active','stored account differs from response');
 result.account=stored; result.loginName=login;
 result.checks.push('native administrator login and cluster/user reads', 'create UI blocked before exact password+TOTP proof via real Transit', '201 creation persists native principal without grants and with mandatory first password', 'reload reads persisted account');
 assert(result.pageErrors.length===0,'browser page error');
 await page.screenshot({path:root+'/logs/users-created.png'});
 result.status='passed';
}catch(e){result.status='failed';result.failure=sensitive.reduce((message,value)=>value?message.replaceAll(value,'[REDACTED]'):message,e.message);process.exitCode=1}
finally {writeFileSync(root+'/logs/users-browser-result.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,failure:result.failure,checks:result.checks}))}
