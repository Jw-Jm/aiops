import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {randomBytes} from 'node:crypto';
import {safeShot} from './safe-capture.mjs';
import {safeErrorMessage} from './safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',run=root+'/r36',origin='https://192.168.0.63:30471';
const account=JSON.parse(readFileSync(root+'/private/r36-visual-account.json','utf8')),next=randomBytes(24).toString('base64url');
const result={checks:[],requests:[],captures:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},deviceScaleFactor:1,locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();
page.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/'))result.requests.push({path,method:r.request().method(),status:r.status(),requestId:r.headers()['x-request-id']})});
async function shot(name){result.captures.push({name,...await safeShot(page,run+'/shots/'+name+'.png')})}
try{
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname==='/auth/first-password');await page.getByRole('dialog',{name:'首次登录：修改密码'}).waitFor();await shot('sl005-first-password');
 await page.getByLabel('当前密码',{exact:true}).fill(account.password);await page.getByLabel('密码',{exact:true}).fill(next);await page.getByLabel('确认密码',{exact:true}).fill(next+'x');await page.getByRole('button',{name:'保存密码',exact:true}).click();await page.getByText('两次输入的新密码不一致',{exact:true}).waitFor();await shot('sl005-first-password-mismatch');
 if(result.requests.some(r=>r.path==='/api/v1/auth/password:change'))throw Error('mismatch reached server');result.checks.push('confirmation mismatch rejected before mutation');
 await page.getByLabel('确认密码',{exact:true}).fill(next);const changed=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change');await page.getByRole('button',{name:'保存密码',exact:true}).click();if((await changed).status()!==201)throw Error('password change failed');writeFileSync(root+'/private/r36-visual-account.json',JSON.stringify({...account,password:next}),{mode:0o600});await page.waitForURL(u=>u.pathname!='/auth/first-password');
 await page.goto(origin+'/account/security');await page.getByText('双因素状态',{exact:true}).waitFor();await page.waitForFunction(()=>!document.querySelector('.ant-spin-spinning'));await shot('extension-mfa-not-enrolled');result.checks.push('new zero-role account forced password change 201; MFA empty status real persisted API');
 const logout=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/logout');await page.getByRole('button',{name:/退出/}).click();if((await logout).status()!==200)throw Error('logout not confirmed');await page.waitForURL(u=>u.pathname==='/login');await shot('sl001-after-logout');result.checks.push('server confirmed logout and login view restored');result.status='passed';
}catch(e){result.status='failed';result.failure=safeErrorMessage(e,[account.password,next]);process.exitCode=1}
finally{writeFileSync(run+'/visual-first-password.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,checks:result.checks,failure:result.failure}))}
