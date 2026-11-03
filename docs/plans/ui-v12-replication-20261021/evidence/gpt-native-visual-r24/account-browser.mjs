import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomUUID, randomBytes } from 'node:crypto';
const root='/data/ops-v12-native-visual-r24-20261026';
const origin='https://192.168.0.63:30471';
const file=root+'/private/browser-account.json';
let identity=JSON.parse(readFileSync(file,'utf8'));
const sensitive=[identity.password];
const result={origin,checks:[],requests:[],screenshots:[],geometry:{}};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});
const page=await context.newPage();page.setDefaultTimeout(15000);
page.on('response',r=>{if(new URL(r.url()).pathname.startsWith('/api/'))result.requests.push({path:new URL(r.url()).pathname,status:r.status(),requestId:r.headers()['x-request-id']});});
const assert=(ok,message)=>{if(!ok)throw new Error(message);};
const shot=async name=>{await page.waitForFunction(()=>[...document.querySelectorAll('.ant-modal')].every(n=>getComputedStyle(n).opacity==='1' && getComputedStyle(n).transform==='none'));await page.screenshot({path:root+'/evidence/'+name+'.png'});result.screenshots.push(name);};
async function login(){
  await page.goto(origin+'/login');
  await page.getByLabel('用户名',{exact:true}).fill(identity.loginName);
  await page.getByLabel('密码',{exact:true}).fill(identity.password);
  const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/login');
  await page.getByRole('button',{name:/^登\s*录/}).click();
  assert((await response).status()===201,'native login rejected');
  await page.waitForURL(u=>u.pathname!=='/login');
}
async function password(dialog,current,next){
  await dialog.getByLabel('当前密码',{exact:true}).fill(current);
  await dialog.getByLabel('密码',{exact:true}).fill(next);
  await dialog.getByLabel('确认密码',{exact:true}).fill(next);
}
async function savePassword(dialog,next){
  const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change');
  await dialog.getByRole('button',{name:'保存密码',exact:true}).click();
  const reply=await response; assert(reply.status()===201,'password rotation rejected');
  identity={...identity,password:next};writeFileSync(file,JSON.stringify(identity),{mode:0o600});
}
try{
  await page.goto(origin+'/login');
  await page.getByLabel('用户名',{exact:true}).fill('lin.wei');
  await page.getByLabel('密码',{exact:true}).fill('DemoOps#2026');
  await shot('login-current');
  await page.getByLabel('用户名',{exact:true}).fill('r24-absent-'+randomUUID());
  let release,arrive;const gate=new Promise(r=>release=r),arrived=new Promise(r=>arrive=r);
  await context.route('**/api/v1/auth/login',async route=>{arrive();await gate;await route.continue();});
  const rejected=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/login');
  await page.getByRole('button',{name:/^登\s*录/}).click();await arrived;
  assert(await page.locator('button[type=submit]').isDisabled(),'duplicate login is enabled');await shot('login-pending');release();
  assert((await rejected).status()===401,'unknown user was not rejected');
  await page.getByText('登录名或密码不正确。',{exact:true}).waitFor();await shot('login-error');await context.unroute('**/api/v1/auth/login');
  result.checks.push('real pending login disables duplicate submit; API401 remains inline');
  await login();
  if(new URL(page.url()).pathname==='/auth/first-password'){
    let d=page.getByRole('dialog',{name:'首次登录：修改密码'});await d.waitFor();await shot('first-password');
    await page.keyboard.press('Escape');await page.waitForURL('**/login');await login();
    d=page.getByRole('dialog',{name:'首次登录：修改密码'});await d.waitFor();
    const next=randomBytes(24).toString('base64url');sensitive.push(next);
    await password(d,'wrong-current-password',next);
    const wrong=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change');
    await d.getByRole('button',{name:'保存密码',exact:true}).click();assert((await wrong).status()===401,'wrong current password accepted');
    await d.getByText('当前密码不正确。',{exact:true}).waitFor();await shot('first-password-error');
    await password(d,identity.password,next);await savePassword(d,next);await page.waitForURL(u=>u.pathname!=='/auth/first-password');
    result.checks.push('first-password Escape revokes session; relogin; wrong current401 remains; real201 rotates credentials');
  }
  await page.getByRole('button',{name:'我的账号',exact:true}).click();
  let account=page.getByRole('dialog',{name:'我的账号',exact:true});await account.waitFor();
  assert(await account.getByText(identity.loginName,{exact:true}).count()===1,'server-owned login name absent');
  await shot('my-account');await page.waitForFunction(()=>document.querySelector('.ant-modal')?.getBoundingClientRect().width===660);result.geometry.account=await account.boundingBox();assert(result.geometry.account.width===660,'account modal width differs from slice');
  await account.getByRole('button',{name:'修改密码',exact:true}).click();
  let d=page.getByRole('dialog',{name:'修改我的密码',exact:true});await d.waitFor();await shot('self-password');
  await d.getByRole('button',{name:/取\s*消/}).click();await account.waitFor();
  await account.getByRole('button',{name:'修改密码',exact:true}).click();d=page.getByRole('dialog',{name:'修改我的密码',exact:true});
  const next=randomBytes(24).toString('base64url');sensitive.push(next);
  await password(d,'wrong-current-password',next);
  const wrong=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change');
  await d.getByRole('button',{name:'保存密码',exact:true}).click();assert((await wrong).status()===401,'self wrong current accepted');
  await d.getByText('当前密码不正确。',{exact:true}).waitFor();await shot('self-password-error');
  await password(d,identity.password,next);await savePassword(d,next);
  await page.reload();await page.getByRole('button',{name:'我的账号',exact:true}).waitFor();
  result.checks.push('server-owned own account / independent grants; self cancel/error/201; rotated session survives reload');
  result.status='passed';
}catch(error){result.status='failed';result.failure=sensitive.reduce((s,v)=>s.replaceAll(v,'[REDACTED]'),error.message);process.exitCode=1;}
finally{writeFileSync(root+'/evidence/account-browser.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,checks:result.checks,failure:result.failure}));}
