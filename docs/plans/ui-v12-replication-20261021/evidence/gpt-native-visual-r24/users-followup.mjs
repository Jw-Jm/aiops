import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync,writeFileSync } from 'node:fs';
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
  await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(identity.loginName);await page.getByLabel('密码',{exact:true}).fill(identity.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!=='/login');
  await page.goto(origin+'/settings/permissions');await page.getByText(identity.loginName,{exact:true}).waitFor();
  await page.getByRole('tab',{name:'角色与授权'}).click();let row=page.getByRole('row').filter({hasText:target.loginName});await row.getByRole('button',{name:'撤销授权',exact:true}).click();let d=page.getByRole('dialog',{name:'撤销角色授权'});await d.getByLabel('操作原因',{exact:true}).fill('独立管理员撤销验收');await proof(d,'确认提交');await shot('user-revoke-verified');const revoked=page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/status')&&r.request().method()==='PATCH');await d.getByRole('button',{name:'确认提交',exact:true}).click();assert((await revoked).status()===200,'role revocation rejected');await d.waitFor({state:'hidden'});await row.waitFor({state:'hidden'});result.checks.push('real administrator grant revoked200; survives refreshed role projection');
  await page.getByRole('tab',{name:'用户管理'}).click();row=page.getByRole('row').filter({hasText:target.loginName});await row.getByText('尚未授权',{exact:true}).waitFor();await row.getByRole('button',{name:'停用',exact:true}).click();d=page.getByRole('dialog',{name:'更改用户登录状态'});await proof(d,'确认提交');
  let release,arrived;const gate=new Promise(r=>release=r),committed=new Promise(r=>arrived=r);const pattern='**/api/v1/admin/users/'+target.userId;
  await context.route(pattern,async route=>{if(route.request().method()!=='PATCH'){await route.continue();return;}const response=await route.fetch();result.committedStatus=response.status();arrived();await gate;try{await route.fulfill({response});}catch{}});
  await d.getByRole('button',{name:'确认提交',exact:true}).click();await committed;assert(result.committedStatus===200,'held real write did not commit');await shot('user-status-pending');await page.keyboard.press('Escape');await d.waitFor({state:'hidden'});await page.getByText('操作窗口已关闭；已提交请求可能仍在服务端完成，请刷新确认最终状态。',{exact:true}).waitFor();release();await context.unroute(pattern);await row.getByText('停用',{exact:true}).waitFor();await shot('user-status-uncertain');await page.reload();row=page.getByRole('row').filter({hasText:target.loginName});await row.getByRole('button',{name:'恢复',exact:true}).waitFor();result.checks.push('actual status write committed200 while response held; Escape abort retains uncertain warning; reload confirms disabled persistence');
  await row.getByRole('button',{name:'恢复',exact:true}).click();d=page.getByRole('dialog',{name:'更改用户登录状态'});await proof(d,'确认提交');const restored=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/admin/users/'+target.userId&&r.request().method()==='PATCH');await d.getByRole('button',{name:'确认提交',exact:true}).click();assert((await restored).status()===200,'account restore rejected');await d.waitFor({state:'hidden'});await page.reload();row=page.getByRole('row').filter({hasText:target.loginName});await row.getByRole('button',{name:'停用',exact:true}).waitFor();await row.getByText('尚未授权',{exact:true}).waitFor();result.checks.push('restore200 persists without reviving the revoked administrator grant');
  result.status='passed';
}catch(error){result.status='failed';result.failure=sensitive.reduce((s,v)=>s.replaceAll(v,'[REDACTED]'),error.message).replace(/\b\d{6}\b/g,'[CODE]');process.exitCode=1;}
finally{writeFileSync(root+'/evidence/users-followup.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,checks:result.checks,failure:result.failure}));}
