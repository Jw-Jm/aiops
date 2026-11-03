import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
const dir='/data/ops-v12-native-visual-r24-20261026/r29-login/attempt-3',origin='https://192.168.0.63:30471';
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});const page=await context.newPage();page.setDefaultTimeout(15000);
const result={origin,scope:'actual native login rejection; held real401 response for pending; network abort; fixed1440x960; reused existing reference',checks:[],apiRequests:[]};
page.on('request',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/v1/'))result.apiRequests.push({method:r.method(),path});});
async function stable(){await page.evaluate(async()=>{await document.fonts.ready;for(let i=0;i<3;i++)await new Promise(requestAnimationFrame);});}
try{
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).waitFor();await stable();await page.screenshot({path:dir+'/login-empty.png'});
 // The known prototype username is absent from this isolated two-account tenant.
 await page.getByLabel('用户名',{exact:true}).fill('lin.wei');await page.getByLabel('密码',{exact:true}).fill('wrong-password-value');
 let entered,release;const seen=new Promise(r=>entered=r),held=new Promise(r=>release=r);
 await page.route('**/api/v1/auth/login',async route=>{const response=await route.fetch();result.rejection={status:response.status(),requestId:response.headers()['x-request-id']??null};entered();await held;await route.fulfill({response});});
 const button=page.locator('.login-card button[type=submit]');await button.click();await seen;assert(await button.isDisabled());assert(await page.getByLabel('用户名',{exact:true}).isDisabled());assert(await page.getByLabel('密码',{exact:true}).isDisabled());await stable();await page.screenshot({path:dir+'/login-pending.png'});result.checks.push('held actual401: all login inputs and submit disabled; no duplicate submit');
 release();assert(result.rejection.status===401);const error=page.getByRole('alert');await error.waitFor();await page.waitForFunction(()=>document.querySelector('.v12-login-error')?.textContent==='用户名或密码错误，或账号不可用。');assert.equal(await error.locator('svg').count(),0);await stable();result.geometry=await error.evaluate(e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return{x:r.x,y:r.y,width:r.width,height:r.height,color:s.color,background:s.backgroundColor,font:s.fontSize,lineHeight:s.lineHeight,padding:s.padding,borderRadius:s.borderRadius};});await page.screenshot({path:dir+'/login-error.png'});result.checks.push('real401 constant non-enumerating error; source-matching no-icon form strip; retry controls enabled');assert(await button.isEnabled());
 await page.unroute('**/api/v1/auth/login');await page.route('**/api/v1/auth/login',route=>route.abort('failed'));await button.click();await page.waitForFunction(()=>document.querySelector('.v12-login-error')?.textContent.includes('接口连接中断'));assert(await button.isEnabled());await page.screenshot({path:dir+'/login-network-error.png'});result.checks.push('network failure shown separately; retry re-enabled; no fabricated success');
 assert(result.apiRequests.every(r=>r.path.startsWith('/api/v1/auth/')),'login prefetched business data');result.status='passed';
}catch(error){result.status='failed';result.failure=error.message;process.exitCode=1;}
finally{await browser.close();writeFileSync(dir+'/login-browser.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result));}
