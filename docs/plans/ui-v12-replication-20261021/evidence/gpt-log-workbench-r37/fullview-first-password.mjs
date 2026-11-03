import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {randomBytes} from 'node:crypto';
import {safeErrorMessage} from './safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r37',origin='https://192.168.0.63:30471';
const file=root+'/private/fullview-account.json',account=JSON.parse(readFileSync(file,'utf8')),next=randomBytes(18).toString('base64url');
const result={origin,loginName:account.loginName,pages:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true});const page=await context.newPage();
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname==='/auth/first-password');
 await page.getByLabel('当前密码',{exact:true}).fill(account.password);await page.getByLabel('密码',{exact:true}).fill(next);await page.getByLabel('确认密码',{exact:true}).fill(next);const changed=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/password:change');await page.getByRole('button',{name:'保存密码',exact:true}).click();if((await changed).status()!==201)throw Error('mandatory password change rejected');
 writeFileSync(file,JSON.stringify({...account,password:next}),{mode:0o600});await page.waitForURL(u=>u.pathname!='/auth/first-password');
 result.roles=await page.evaluate(async()=>{const r=await fetch('/api/v1/auth/session',{credentials:'same-origin'});if(r.status!==200)throw Error('session not active');const body=await r.json();return body.data.authorization.grants.filter(g=>!g.revokedAt).map(g=>g.role).sort()});
 if(JSON.stringify(result.roles)!==JSON.stringify(['operator','platform_admin']))throw Error('independent roles not both active');
 result.menu=await page.locator('.v12-sider').innerText();
 for(const path of ['/overview','/resources','/assistant','/settings/permissions']){await page.goto(origin+path);await page.locator('h1').waitFor();result.pages.push({path,title:await page.locator('h1').innerText()})}
 result.status='passed';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[account.password,next]);process.exitCode=1}
finally{writeFileSync(work+'/fullview-access.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify(result))}
