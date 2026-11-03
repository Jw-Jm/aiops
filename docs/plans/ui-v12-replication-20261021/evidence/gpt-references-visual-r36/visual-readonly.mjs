import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {safeShot} from './safe-capture.mjs';
import {safeErrorMessage} from './safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',run=root+'/r36',origin='https://192.168.0.63:30471';
const admin=JSON.parse(readFileSync(root+'/private/browser-account.json','utf8'));
mkdirSync(run+'/shots',{recursive:true});
const result={origin,viewport:{width:1440,height:960},captures:[],geometry:{},requests:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:result.viewport,deviceScaleFactor:1,locale:'zh-CN',timezoneId:'Asia/Shanghai'});const page=await context.newPage();
page.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/'))result.requests.push({path,method:r.request().method(),status:r.status(),requestId:r.headers()['x-request-id']})});
async function capture(name){result.captures.push({name,...await safeShot(page,run+'/shots/'+name+'.png')});result.geometry[name]=await page.evaluate(()=>{const out={};for(const selector of ['.v12-login-main .login-card','.v12-page-head','.v12-users-page .ant-tabs-nav','.v12-users-page .v12-surface','.ant-modal','.ant-modal-header','.ant-modal-body','.ant-modal-footer','.ant-drawer-content']){const e=document.querySelector(selector);if(e){const r=e.getBoundingClientRect();out[selector]={x:r.x,y:r.y,width:r.width,height:r.height,font:getComputedStyle(e).font}}}return out})}
try{
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).waitFor();await capture('sl001-login-empty');
 await page.getByLabel('用户名',{exact:true}).fill('r36.absent.visual');await page.getByLabel('密码',{exact:true}).fill('invalid-task-password');const failed=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/login'&&r.request().method()==='POST');await page.getByRole('button',{name:/^登\s*录/}).click();if((await failed).status()!==401)throw Error('real invalid login did not refuse');await page.locator('.v12-login-error[role=alert]').waitFor();await capture('sl002-login-error');
 await page.getByLabel('用户名',{exact:true}).fill(admin.loginName);await page.getByLabel('密码',{exact:true}).fill(admin.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 await page.getByRole('button',{name:'我的账号',exact:true}).click();let dialog=page.getByRole('dialog',{name:'我的账号',exact:true});await dialog.waitFor();await capture('sl003-my-account');await dialog.getByRole('button',{name:'修改密码',exact:true}).click();dialog=page.getByRole('dialog',{name:'修改我的密码',exact:true});await dialog.waitFor();await capture('sl004-change-password');await dialog.getByRole('button',{name:'关闭',exact:true}).click();
 await page.goto(origin+'/account/security');await page.getByText('双因素状态',{exact:true}).waitFor();await page.waitForFunction(()=>!document.querySelector('.ant-spin-spinning'));await capture('extension-mfa-safe-state');
 await page.goto(origin+'/settings/permissions');await page.getByRole('button',{name:/创建用户/}).waitFor();await page.getByText(admin.loginName,{exact:true}).waitFor();await capture('sl023-users');result.tableStyle=await page.locator('th').first().evaluate(e=>({table:e.closest('table').parentElement.className,cell:e.className,padding:getComputedStyle(e).padding,font:getComputedStyle(e).font}));
 await page.getByRole('button',{name:/创建用户/}).click();dialog=page.getByRole('dialog',{name:'创建内置账号',exact:true});await dialog.waitFor();await capture('sl180-create-user');await dialog.getByRole('button',{name:'关闭',exact:true}).click();
 const row=page.getByRole('row').filter({hasText:admin.loginName});await row.getByRole('button',{name:'详情',exact:true}).click();await page.getByRole('dialog').waitFor();await capture('sl181-user-detail');await page.getByRole('button',{name:'关闭',exact:true}).last().click();
 await row.getByRole('button',{name:'配置授权',exact:true}).click();dialog=page.getByRole('dialog',{name:'配置用户授权',exact:true});await dialog.waitFor();await capture('sl182-grant-user');await dialog.getByRole('button',{name:'关闭',exact:true}).click();
 await page.getByRole('tab',{name:'角色与授权',exact:true}).click();await capture('sl093-grants');result.status='passed';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[admin.password]);process.exitCode=1}
finally{writeFileSync(run+'/visual-readonly.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,captures:result.captures.map(c=>c.name),failure:result.failure}))}
