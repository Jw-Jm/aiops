import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {safeShot} from './safe-capture.mjs';
import {safeErrorMessage} from './safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',run=root+'/r36',origin='https://192.168.0.63:30471';
const admin=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8'));
mkdirSync(run+'/shots',{recursive:true});
const result={origin,viewport:{width:1440,height:960},captures:[],geometry:{},requests:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:result.viewport,deviceScaleFactor:1,locale:'zh-CN',timezoneId:'Asia/Shanghai'});const page=await context.newPage();
page.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/'))result.requests.push({path,method:r.request().method(),status:r.status(),requestId:r.headers()['x-request-id']})});
async function capture(name){result.captures.push({name,...await safeShot(page,run+'/shots/'+name+'.png')});result.geometry[name]=await page.evaluate(()=>{const out={};for(const selector of ['.v12-login-main .login-card','.v12-page-head','.v12-users-page .ant-tabs-nav','.v12-users-page .v12-surface','.ant-modal','.ant-modal-header','.ant-modal-body','.ant-modal-footer','.ant-drawer-content']){const e=document.querySelector(selector);if(e){const r=e.getBoundingClientRect();out[selector]={x:r.x,y:r.y,width:r.width,height:r.height,font:getComputedStyle(e).font}}}return out})}
try{
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(admin.loginName);await page.getByLabel('密码',{exact:true}).fill(admin.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 result.menu=await page.locator('.v12-sider').innerText();
 for(const [path,name] of [['/overview','operator-overview'],['/resources','operator-resources'],['/assistant','operator-assistant']]) {
  await page.goto(origin+path);await page.locator('h1').waitFor();await page.waitForFunction(()=>!document.querySelector('.ant-spin-spinning'));
  if(await page.locator('body').innerText().then(text=>/sk-[A-Za-z0-9]{20,}/.test(text)))throw Error('capture refused: possible secret');
  result.geometry[name]={path:new URL(page.url()).pathname,title:await page.locator("h1").innerText()};
 }
 result.status='passed';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[admin.password]);process.exitCode=1}
finally{writeFileSync(run+'/operator-pages.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,menu:result.menu,captures:result.captures.map(c=>c.name),failure:result.failure}))}
