import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync,writeFileSync,chmodSync } from 'node:fs';
import { randomBytes,createHmac } from 'node:crypto';
const root='/data/ops-v12-native-visual-r24-20261026',origin='https://192.168.0.63:30471';
const identity=JSON.parse(readFileSync(root+'/private/browser-account.json','utf8'));
const factor=JSON.parse(readFileSync(root+'/private/browser-mfa.json','utf8'));
const target=JSON.parse(readFileSync(root+'/private/member-account.json','utf8'));
const sensitive=[identity.password,factor.secret,...factor.recovery,target.password];
const result={origin,checks:[],requests:[],screenshots:[],geometry:{}};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});
const page=await context.newPage();page.setDefaultTimeout(15000);
page.on('response',r=>{if(new URL(r.url()).pathname.startsWith('/api/'))result.requests.push({path:new URL(r.url()).pathname,status:r.status(),requestId:r.headers()['x-request-id']});});
const assert=(ok,message)=>{if(!ok)throw new Error(message);};
async function shot(name){await page.waitForFunction(()=>[...document.querySelectorAll('.ant-modal')].every(n=>getComputedStyle(n).opacity==='1'&&getComputedStyle(n).transform==='none'));await page.screenshot({path:root+'/evidence/'+name+'.png',mask:[page.locator('input[autocomplete=one-time-code]')]});result.screenshots.push(name);}
function code(){let bits='';for(const c of factor.secret.replace(/=+$/,''))bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));const b=Buffer.alloc(8);b.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const d=createHmac('sha1',Buffer.from(bytes)).update(b).digest();return String((d.readUInt32BE(d[d.length-1]&15)&0x7fffffff)%1000000).padStart(6,'0');}
let lastStep=-1;
async function proof(d,submit){
  const now=Math.floor(Date.now()/30000);if(now===lastStep)await new Promise(r=>setTimeout(r,30000-Date.now()%30000+200));lastStep=Math.floor(Date.now()/30000);
  await d.getByLabel('验证密码',{exact:true}).fill(identity.password);
  await d.getByLabel('验证动态码',{exact:true}).fill(code());
  await d.getByRole('button',{name:'验证本次操作',exact:true}).click();
  await page.waitForFunction(({name})=>[...document.querySelectorAll('.ant-modal button')].some(b=>b.textContent.trim()===name&&!b.disabled),{name:submit});
}
try{
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(identity.loginName);await page.getByLabel('密码',{exact:true}).fill(identity.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!=='/login');await page.goto(origin+'/settings/permissions');
 const row=page.getByRole('row').filter({hasText:target.loginName});await row.getByRole('button',{name:'详情',exact:true}).click();const drawer=page.getByRole('dialog',{name:'用户详情'});await drawer.getByRole('button',{name:'重置密码',exact:true}).click();const d=page.getByRole('dialog',{name:'重置用户密码'});const next=randomBytes(24).toString('base64url');sensitive.push(next);await d.getByLabel('密码',{exact:true}).fill(next);await d.getByLabel('确认密码',{exact:true}).fill(next);await proof(d,'保存密码');await shot('user-reset-verified');const response=page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/password:reset'));await d.getByRole('button',{name:'保存密码',exact:true}).click();assert((await response).status()===200,'real password reset rejected');target.password=next;writeFileSync(root+'/private/member-account.json',JSON.stringify(target),{mode:0o600});await d.waitFor({state:'hidden'});
 const old=await browser.newContext({ignoreHTTPSErrors:true,storageState:root+'/private/member-storage-state.json'});const stale=await old.request.get(origin+'/api/v1/auth/session');assert(stale.status()===401,'reset failed to revoke existing target session');await old.close();result.checks.push('actual password+TOTP operation after projected rotation; password reset200 and old user cookie401');
 await context.storageState({path:root+'/private/admin-storage-state.json'});chmodSync(root+'/private/admin-storage-state.json',0o600);const cookie=(await context.cookies()).find(c=>c.name==='__Host-ops-session');assert(cookie,'native cookie unavailable');writeFileSync(root+'/private/cli-session-token',cookie.value,{mode:0o600});result.status='passed';
}catch(error){result.status='failed';result.failure=sensitive.reduce((s,v)=>s.replaceAll(v,'[REDACTED]'),error.message).replace(/\b\d{6}\b/g,'[CODE]');process.exitCode=1;}
finally{writeFileSync(root+'/evidence/user-reset-browser.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,checks:result.checks,failure:result.failure}));}
