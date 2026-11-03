import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync,chmodSync,existsSync} from 'node:fs';
import {createHmac,createHash,randomUUID} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import assert from 'node:assert/strict';
const root='/data/ops-v12-native-visual-r24-20261026',dir=root+'/r28-worker',origin='https://192.168.0.63:30471';
const who=JSON.parse(readFileSync(root+'/private/browser-account.json','utf8')),factor=JSON.parse(readFileSync(root+'/private/browser-mfa.json','utf8'));
const result={scope:'actual native admin HTTP, password+TOTP, signed policy publication/activation',requests:[],checks:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true});const page=await context.newPage();let session,lastStep=-1;
function canonical(x){if(Array.isArray(x))return x.map(canonical);if(x&&typeof x==='object')return Object.fromEntries(Object.entries(x).sort(([a],[b])=>a<b?-1:a>b?1:0).map(([k,v])=>[k,canonical(v)]));return x;}
function totp(){let bits='';for(const c of factor.secret.replace(/=+$/,''))bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));const b=Buffer.alloc(8);b.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const d=createHmac('sha1',Buffer.from(bytes)).update(b).digest();return String((d.readUInt32BE(d[d.length-1]&15)&0x7fffffff)%1000000).padStart(6,'0');}
async function api(method,path,body,extra={}){const r=await context.request.fetch(origin+path,{method,data:body,headers:{Origin:origin,'X-Tenant-ID':session.tenantId,'X-CSRF-Token':session.csrf.token,'Idempotency-Key':randomUUID(),...extra}});const payload=await r.json();result.requests.push({method,path,status:r.status(),requestId:payload.requestId});assert(r.ok(),`${method} ${path}: HTTP${r.status()} ${payload.error?.code??''}`);return payload;}
async function write(path,body){const digest='sha256:'+createHash('sha256').update(JSON.stringify(canonical(body))).digest('hex');let c=(await api('POST','/api/v1/auth/step-up-challenges',{operation:'registry_administration',target:{kind:'administrative_write',write:{method:'POST',path,bodyDigest:digest}},expectedSessionVersion:session.revocation.sessionVersion})).data;
 const id=c.challengeId;let v=(await api('POST',`/api/v1/auth/step-up-challenges/${id}:verify`,{method:'password',credential:who.password,expectedChallengeRevision:c.revision})).data;assert(!v.failureCode,'password proof rejected');
 if(Math.floor(Date.now()/30000)===lastStep)await new Promise(r=>setTimeout(r,30000-Date.now()%30000+200));lastStep=Math.floor(Date.now()/30000);
 v=(await api('POST',`/api/v1/auth/step-up-challenges/${id}:verify`,{method:'totp',credential:totp(),expectedChallengeRevision:v.revision})).data;assert(v.stepUpSession,'TOTP proof rejected');return api('POST',path,body,{'X-Ops-Step-Up-Challenge':id});}
try{assert(!existsSync(dir+'/policy-activation.json'),'policy already activated; do not rerun');
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(who.loginName);await page.getByLabel('密码',{exact:true}).fill(who.password);const login=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/login');await page.getByRole('button',{name:/^登\s*录/}).click();const reply=await login;assert(reply.status()===201);session=(await reply.json()).data;await page.waitForURL(u=>u.pathname!=='/login');
 writeFileSync(dir+'/private/actor-session.json',JSON.stringify(session),{mode:0o600});await context.storageState({path:dir+'/private/actor-storage.json'});chmodSync(dir+'/private/actor-storage.json',0o600);
 const body=JSON.parse(readFileSync(dir+'/policy-draft-body.json','utf8'));
 const draft=await write('/api/v1/admin/registry-drafts',body);writeFileSync(dir+'/policy-draft.json',JSON.stringify(draft,null,2));
 execFileSync(dir+'/material',['sign']);
 const published=await write(`/api/v1/admin/registry-drafts/${draft.data.draftId}:publish`,JSON.parse(readFileSync(dir+'/policy-publish-body.json','utf8')));writeFileSync(dir+'/policy-published.json',JSON.stringify(published,null,2));
 const activated=await write('/api/v1/admin/registry-activations',{kind:'policy',logicalName:body.logicalName,versionId:published.data.versionId,scopeType:'tenant',expectedRevision:0});writeFileSync(dir+'/policy-activation.json',JSON.stringify(activated,null,2));result.checks.push('three distinct exact-body native password/TOTP proofs; actual signed draft publication and tenant activation');result.policyVersion=published.data.versionId;result.status='passed';
}catch(error){result.status='failed';result.failure=String(error.message).replaceAll(who.password,'[REDACTED]').replaceAll(factor.secret,'[REDACTED]');process.exitCode=1;}
finally{await browser.close();writeFileSync(dir+'/policy-browser-result.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result));}
