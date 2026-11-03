import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync,writeFileSync } from 'node:fs';
import { randomBytes,createHmac } from 'node:crypto';
const root='/data/ops-v12-native-visual-r24-20261026',origin='https://192.168.0.63:30471';
const identity=JSON.parse(readFileSync(root+'/private/browser-account.json','utf8'));
const factor=JSON.parse(readFileSync(root+'/private/browser-mfa.json','utf8'));
const target={loginName:'r24.member.'+Date.now(),displayName:'验收成员',password:randomBytes(24).toString('base64url')};
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
  await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(identity.loginName);await page.getByLabel('密码',{exact:true}).fill(identity.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!=='/login');
  await page.goto(origin+'/settings/permissions');await page.getByText(identity.loginName,{exact:true}).waitFor();await shot('users-list');
  await page.getByRole('button',{name:/创建用户/}).click();let d=page.getByRole('dialog',{name:'创建内置账号'});await d.waitFor();await shot('users-create');
  await d.getByLabel('用户名',{exact:true}).fill(target.loginName);await d.getByLabel('姓名',{exact:true}).fill(target.displayName);await d.getByLabel('密码',{exact:true}).fill(target.password);await d.getByLabel('确认密码',{exact:true}).fill(target.password);
  assert(await d.getByRole('button',{name:'创建账号',exact:true}).isDisabled(),'create bypassed operation proof');
  await proof(d,'创建账号');const created=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/admin/users'&&r.request().method()==='POST');await d.getByRole('button',{name:'创建账号',exact:true}).click();const reply=await created;assert(reply.status()===201,'user creation rejected');target.userId=(await reply.json()).data.userId;writeFileSync(root+'/private/member-account.json',JSON.stringify(target),{mode:0o600});
  await d.waitFor({state:'hidden'});let row=page.getByRole('row').filter({hasText:target.loginName});await row.getByText('尚未授权',{exact:true}).waitFor();result.checks.push('real create201 with password+TOTP exact operation proof; no implicit role');
  await row.getByRole('button',{name:'详情',exact:true}).click();const drawer=page.getByRole('dialog',{name:'用户详情'});await drawer.waitFor();await page.waitForFunction(()=>{const e=document.querySelector('.ant-drawer-content-wrapper');return e&&getComputedStyle(e).transform==='none';});await shot('user-detail');result.geometry.drawer=await drawer.boundingBox();
  await drawer.getByRole('button',{name:'编辑资料',exact:true}).click();d=page.getByRole('dialog',{name:'编辑用户'});await d.waitFor();assert(await d.getByLabel('用户名',{exact:true}).inputValue()===target.loginName,'edit lost read-only login');await shot('user-edit');await page.keyboard.press('Escape');await drawer.waitFor();await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});
  row=page.getByRole('row').filter({hasText:target.loginName});await row.getByRole('button',{name:'配置授权',exact:true}).click();d=page.getByRole('dialog',{name:'配置用户授权'});await d.waitFor();await shot('user-grant-no-clusters');assert(await d.getByRole('button',{name:'确认提交',exact:true}).isDisabled(),'operator granted without eligible scope');
  await d.getByRole('combobox',{name:'角色',exact:true}).click();await page.getByTitle('平台管理员',{exact:true}).click();await d.getByLabel('操作原因',{exact:true}).fill('独立管理员授权验收');await proof(d,'确认提交');await shot('user-grant-verified');const grant=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/admin/role-bindings'&&r.request().method()==='POST');await d.getByRole('button',{name:'确认提交',exact:true}).click();assert((await grant).status()===201,'independent admin grant rejected');await d.waitFor({state:'hidden'});
  await page.getByRole('tab',{name:'角色与授权'}).click();await page.getByRole('row').filter({hasText:target.loginName}).waitFor();await shot('role-bindings');result.checks.push('detail drawer/edit readonly/cancel; no-cluster operator denied; independent administrator201 and role table');
  result.status='passed';
}catch(error){result.status='failed';result.failure=sensitive.reduce((s,v)=>s.replaceAll(v,'[REDACTED]'),error.message).replace(/\b\d{6}\b/g,'[CODE]');process.exitCode=1;}
finally{writeFileSync(root+'/evidence/users-browser.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,checks:result.checks,failure:result.failure}));}
