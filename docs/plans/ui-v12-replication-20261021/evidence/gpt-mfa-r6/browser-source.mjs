// Synthetic registration fixture; proves UI/auth/SQL wiring, not source qualification.
import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {randomUUID,createHmac} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import https from 'node:https';
const root='/data/ops-ui-v12-mfa-20261022-r1/gpt-r6-preview',bao='/data/ops-ui-v12-mfa-20261022-r1/gpt-r6-openbao';
const identity=JSON.parse(readFileSync(root+'/private/browser-identity.json','utf8'));
const tenant=readFileSync(root+'/private/tenant-id','utf8').trim();
const cluster=randomUUID(),source=randomUUID(),name='source-ui-'+source.slice(0,8),uid='cluster-ui-'+cluster;
const sql=q=>execFileSync('docker',['exec','-i','ops-ui-v12-mfa-pg-20261022','psql','-U','postgres','-d','ui_v12_native_identity','-X','-tAc',q],{encoding:'utf8'}).trim();
sql(`INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES('${tenant}','${cluster}','${uid}','Source UI fixture');INSERT INTO platform.source_registrations(tenant_id,source_id,source_type,instance_key,cluster_id,auth_ref) VALUES('${tenant}','${source}','victorialogs','${name}','${cluster}','openbao://isolated/qualification-unverified');`);
const result={scope:'isolated synthetic registration; source qualification/live ingestion not asserted',checks:[],requests:[],source,cluster,tenant};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const ctx=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await ctx.newPage();
page.on('response',r=>{if(r.url().includes('/api/'))result.requests.push({path:new URL(r.url()).pathname,status:r.status(),requestId:r.headers()['x-request-id']})});
const assert=(v,m)=>{if(!v)throw new Error(m)};
function request(path,body,token){return new Promise((resolve,reject)=>{const q=https.request('https://127.0.0.1:18206/v1/'+path,{method:'POST',ca:readFileSync(bao+'/tls/ca.crt'),servername:'mfa-gpt-r6.ops.local',headers:{'Content-Type':'application/json',...(token?{'X-Vault-Token':token}:{})}},r=>{let text='';r.on('data',b=>text+=b);r.on('end',()=>{if(r.statusCode!==200)return reject(new Error('task Transit oracle status'+r.statusCode));resolve(JSON.parse(text))})});q.on('error',reject);q.end(JSON.stringify(body))})}
function code(secret,step){let bits='';for(const c of secret.toUpperCase().replace(/=+$/,''))bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));const b=Buffer.alloc(8);b.writeBigUInt64BE(BigInt(step));const h=createHmac('sha1',Buffer.from(bytes)).update(b).digest(),o=h[h.length-1]&15;return String((h.readUInt32BE(o)&0x7fffffff)%1000000).padStart(6,'0')}
try{
 await page.goto('https://192.168.0.63:30466/login');await page.getByLabel('用户名',{exact:true}).fill(identity.loginName);await page.getByLabel('密码',{exact:true}).fill(identity.rotatedPassword);await page.getByRole('button',{name:/^登\s*录$/}).click();await page.waitForURL('**/access/sources');
 await page.getByRole('button',{name,exact:true}).click();
 await page.getByLabel('来源新状态',{exact:true}).click();await page.getByTitle('停用',{exact:true}).click();
 const save=page.getByRole('button',{name:'保存来源状态',exact:true});assert(await save.isDisabled(),'write enabled before operation proof');
 // Read only this fixture's encrypted envelope to generate an independent code;
 // neither the secret, ciphertext, code nor workload token enters the result.
 const factor=JSON.parse(sql(`SELECT json_build_object('id',e.enrollment_id,'envelope',e.secret_ciphertext,'step',e.last_used_step,'keyRef',e.secret_key_ref) FROM identity.mfa_enrollments e JOIN identity.accounts a USING(tenant_id,user_id) WHERE e.tenant_id='${tenant}' AND a.login_name='${identity.loginName}' AND e.state='confirmed'`));
 assert(factor.keyRef==='openbao:ui-v12-mfa','fixture factor is not encrypted by the configured task Transit key');
 const envelope=JSON.parse(factor.envelope);
 const login=await request('auth/kubernetes/login',{role:'ops-api-workload',jwt:readFileSync(bao+'/private/projected.jwt','utf8').trim()});
 const decrypted=await request('transit/decrypt/ui-v12-mfa',{ciphertext:envelope.ciphertext,associated_data:Buffer.from('ops-sensitive-object/v1|'+tenant+'|'+factor.id).toString('base64')},login.auth.client_token);
 const secret=Buffer.from(decrypted.data.plaintext,'base64').toString('utf8');const step=Math.max(Math.floor(Date.now()/30000),Number(factor.step)+1);assert(step<=Math.floor(Date.now()/30000)+1,'fixture time-step already consumed; wait for a fresh window');
 await page.getByLabel('验证密码',{exact:true}).fill(identity.rotatedPassword);await page.getByLabel('验证动态码',{exact:true}).fill(code(secret,step));await page.getByRole('button',{name:'验证本次操作',exact:true}).click();await save.waitFor();await page.waitForFunction(()=>!Array.from(document.querySelectorAll('button')).find(b=>b.textContent==='保存来源状态')?.disabled);
 const write=page.waitForResponse(r=>new URL(r.url()).pathname===`/api/v1/admin/source-registrations/${source}`&&r.request().method()==='PATCH');await save.click();assert((await write).status()===200,'verified source write failed');
 const stored=JSON.parse(sql(`SELECT json_build_object('status',status,'revision',revision) FROM platform.source_registrations WHERE tenant_id='${tenant}' AND source_id='${source}'`));assert(stored.status==='disabled'&&stored.revision===2,'source write did not persist');
 result.checks.push('actual source UI requires proof, verifies password+TOTP through real Transit factor, sends exact challenge header and persists status/revision');result.stored=stored;result.status='passed';await page.screenshot({path:root+'/logs/source-write.png'});
}catch(e){result.status='failed';result.failure=e.message;process.exitCode=1}finally{writeFileSync(root+'/logs/source-browser-result.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,failure:result.failure,checks:result.checks}))}
