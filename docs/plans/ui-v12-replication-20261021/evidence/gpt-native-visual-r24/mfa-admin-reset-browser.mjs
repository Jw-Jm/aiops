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
  async function login(p,who){await p.goto(origin+'/login');await p.getByLabel('用户名',{exact:true}).fill(who.loginName);await p.getByLabel('密码',{exact:true}).fill(who.password);const res=p.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/login');await p.getByRole('button',{name:/^登\s*录/}).click();const reply=await res;assert(reply.status()===201,'fresh login rejected');await p.waitForURL(u=>u.pathname!=='/login');return (await reply.json()).data;}
  const actor=await login(page,identity);result.actorSessionRef=actor.sessionRef;
  const memberContext=await browser.newContext({ignoreHTTPSErrors:true});const memberPage=await memberContext.newPage();await login(memberPage,target);assert((await memberContext.request.get(origin+'/api/v1/auth/session')).status()===200,'member session not active before reset');const beforeMFA=(await (await memberContext.request.get(origin+'/api/v1/auth/mfa/status')).json()).data;assert(beforeMFA.state==='confirmed','target factor not confirmed before reset');result.before={factorState:beforeMFA.state,recoveryCodesRemaining:beforeMFA.recoveryCodesRemaining};
  await page.goto(origin+'/account/security');const picker=page.getByRole('combobox',{name:'目标账号（本页）',exact:true});await picker.click();await page.getByTitle(target.displayName+' / '+target.loginName,{exact:true}).click();assert(await page.getByLabel('目标账号 revision').count()===0,'raw revision input still exposed');await page.getByLabel('确认登录名',{exact:true}).fill(target.loginName);await page.getByLabel('原因',{exact:true}).fill('独立验收账号恢复与撤销验证');await page.getByLabel('我的当前密码',{exact:true}).fill(identity.password);await page.getByLabel('我的动态码',{exact:true}).fill(code());await shot('mfa-admin-reset-target');
  const reset=page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/mfa:reset'));await page.getByRole('button',{name:'执行管理员重置',exact:true}).click();const reply=await reset;assert(reply.status()===200,'administrator MFA reset rejected');result.reset=(await reply.json()).data;await page.getByText(/重置完成：撤销/).waitFor();await shot('mfa-admin-reset-result');assert((await memberContext.request.get(origin+'/api/v1/auth/session')).status()===401,'previous target cookie survived reset');result.checks.push('selected actual account; server-owned account and credential versions; password+TOTP proof; MFA reset200 revokes an active cookie401');
  const restored=await login(memberPage,target);assert(restored.mustReenrollMfa===true,'reset did not require re-enrollment');await memberPage.waitForURL('**/account/security');const after=(await (await memberContext.request.get(origin+'/api/v1/auth/mfa/status')).json()).data;assert(after.state==='reset_required'&&after.recoveryCodesRemaining===0,'old factor/recovery codes remained available');assert((await memberContext.request.get(origin+'/api/v1/admin/users')).status()===403,'reset widened target roles');result.after={factorState:after.state,recoveryCodesRemaining:after.recoveryCodesRemaining,mustReenrollMfa:restored.mustReenrollMfa};await memberPage.screenshot({path:root+'/evidence/mfa-reset-required-member.png',mask:[memberPage.locator('input[autocomplete=one-time-code]')]});result.checks.push('relogin requires re-enrollment; old recovery count0; zero-grant target still receives admin403');await memberContext.close();
  await context.storageState({path:root+'/private/admin-storage-state-fresh.json'});chmodSync(root+'/private/admin-storage-state-fresh.json',0o600);result.status='passed';
}catch(error){result.status='failed';result.failure=sensitive.reduce((s,v)=>s.replaceAll(v,'[REDACTED]'),error.message).replace(/\b\d{6}\b/g,'[CODE]');process.exitCode=1;}
finally{writeFileSync(root+'/evidence/mfa-admin-reset-browser.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,checks:result.checks,failure:result.failure}));}
