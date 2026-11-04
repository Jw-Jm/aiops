import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r53',origin='https://192.168.0.63:30471';
const a=JSON.parse(readFileSync(root+'/private/browser-account.json'));const routes=JSON.parse(readFileSync(work+'/routes.json'));routes.push({path:'/account/security',name:'账号安全',available:true});
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});const report={at:new Date().toISOString(),routes:[],writes:[],errors:[]};
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN'});const page=await context.newPage();page.setDefaultTimeout(15000);let current=null;
 page.on('pageerror',e=>{if(current)current.errors.push(e.message.replaceAll(a.password,'[REDACTED]'))});
 page.on('response',async r=>{if(current&&new URL(r.url()).pathname.startsWith('/api/'))current.requests.push({method:r.request().method(),path:new URL(r.url()).pathname,status:r.status()})});
 page.on('request',r=>{const p=new URL(r.url()).pathname;if(!['GET','HEAD'].includes(r.method())&&p.startsWith('/api/')&&!p.includes('/auth/'))report.writes.push({method:r.method(),path:p})});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(a.loginName);await page.getByLabel('密码',{exact:true}).fill(a.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 for(const route of routes){current={...route,requests:[],errors:[]};report.routes.push(current);
  try{await page.goto(origin+route.path+'?clusterUid=79014400-9453-4cb4-9678-0fae47981d79');await page.locator('main').waitFor();await page.waitForTimeout(900);await page.waitForFunction(()=>!document.querySelector('.ant-spin-spinning'),{},{timeout:5000}).catch(()=>{});current.view=await page.locator('main').evaluate(e=>({headings:[...e.querySelectorAll('h1,h2,h3')].map(n=>n.textContent),alerts:[...e.querySelectorAll('[role="alert"]')].map(n=>n.textContent.slice(0,350)),buttons:[...e.querySelectorAll('button')].map(n=>({name:n.textContent.trim().slice(0,70),disabled:n.disabled})),rows:e.querySelectorAll('tbody tr').length,unavailable:/当前版本暂不提供/.test(e.textContent),empty:/暂无数据|暂无记录|没有.*记录/.test(e.textContent),error:/请求失败|加载失败|GRAPH_NOT_READY|SOURCE_UNAVAILABLE|重试/.test(e.textContent)}));current.currentPath=new URL(page.url()).pathname;current.status=!route.available?'unimplemented':current.errors.length||current.requests.some(r=>r.status>=400)?'blocked':'page_loaded';
  }catch(e){current.status='failed';current.failure=e.message.replaceAll(a.password,'[REDACTED]')}
  writeFileSync(work+'/smoke.json',JSON.stringify(report,null,2));console.log(JSON.stringify({path:route.path,status:current.status,failures:current.requests.filter(r=>r.status>=400),unavailable:current.view?.unavailable}));
 }
 assert.equal(report.writes.length,0);report.status='completed_navigation_smoke_only';
}catch(e){report.failure=e.message.replaceAll(a.password,'[REDACTED]');report.status='failed';process.exitCode=1}
finally{writeFileSync(work+'/smoke.json',JSON.stringify(report,null,2));await browser.close()}
