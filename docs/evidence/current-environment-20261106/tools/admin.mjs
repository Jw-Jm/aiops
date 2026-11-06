// Deployment-host acceptance helper. Credentials remain in repository-external files.
import { chromium } from '/opt/ops-current-tools/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHmac, createHash } from 'node:crypto';
export const root=process.env.OPS_CURRENT_ROOT??'/data/ops-current';
export const origin=process.env.OPS_CURRENT_ORIGIN??'https://192.168.0.63:30471';
export const account=JSON.parse(readFileSync(root+'/private/browser-account.json'));
const factor=JSON.parse(readFileSync(root+'/private/browser-mfa.json'));
export const canonical=v=>Array.isArray(v)?v.map(canonical):v&&typeof v==='object'?Object.fromEntries(Object.keys(v).sort().map(k=>[k,canonical(v[k])])):v;
export const digest=v=>'sha256:'+createHash('sha256').update(JSON.stringify(canonical(v))).digest('hex');
function totp(){let bits='';for(const c of factor.secret.replace(/=+$/,''))bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));const counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const d=createHmac('sha1',Buffer.from(bytes)).update(counter).digest();return String((d.readUInt32BE(d[d.length-1]&15)&0x7fffffff)%1000000).padStart(6,'0')}
export async function login(){const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});try{const page=await browser.newPage({ignoreHTTPSErrors:true});await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');await page.waitForLoadState('networkidle');return {browser,page}}catch(e){await browser.close();throw e}}
export async function read(page,path){return page.evaluate(async path=>{const r=await fetch(path,{cache:'no-store'});return {status:r.status,body:await r.json()}},path)}
export async function write(page,operation,method,path,body){
 // Wait for a new counter; TOTP replay protection also covers earlier UI writes.
 await new Promise(r=>setTimeout(r,30000-Date.now()%30000+250));
 return page.evaluate(async({operation,method,path,body,password,code,bodyDigest})=>{
  const call=async(method,path,body,headers={})=>{const r=await fetch(path,{method,credentials:'same-origin',headers:{...(body?{'Content-Type':'application/json'}:{}),...headers},body:body?JSON.stringify(body):undefined});return {status:r.status,requestId:r.headers.get('x-request-id'),body:await r.json()}};
  const session=await call('GET','/api/v1/auth/session');if(session.status!==200)throw Error('session unavailable '+session.status);
  const headers={'X-CSRF-Token':session.body.data.csrf.token,'Idempotency-Key':crypto.randomUUID()};
  const c=await call('POST','/api/v1/auth/step-up-challenges',{operation,target:{kind:'administrative_write',write:{method,path,bodyDigest}},expectedSessionVersion:session.body.data.revocation.sessionVersion},headers);
  if(c.status!==201)throw Error('challenge rejected '+c.status+' '+c.body.code);
  const id=c.body.data.challengeId;let revision=c.body.data.revision;
  for(const [method,credential] of [['password',password],['totp',code]]){const v=await call('POST','/api/v1/auth/step-up-challenges/'+id+':verify',{method,credential,expectedChallengeRevision:revision},{...headers,'Idempotency-Key':crypto.randomUUID()});if(v.status!==200)throw Error('factor rejected '+v.status+' '+v.body.code);revision=v.body.data.revision;if(method==='totp'&&v.body.data.verified!==true)throw Error('TOTP did not complete challenge; do not run administrative writers concurrently')}
  return call(method,path,body,{...headers,'Idempotency-Key':crypto.randomUUID(),'X-Ops-Step-Up-Challenge':id});
 },{operation,method,path,body,password:account.password,code:totp(),bodyDigest:digest(body)})
}
export function save(name,data){writeFileSync(root+'/evidence/'+name,JSON.stringify(data,null,2)+'\n')}
