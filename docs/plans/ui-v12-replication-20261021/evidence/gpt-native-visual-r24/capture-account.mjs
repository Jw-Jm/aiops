import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
const root='/data/ops-v12-native-visual-r24-20261026',origin='https://192.168.0.63:30471';
const identity=JSON.parse(readFileSync(root+'/private/browser-account.json','utf8'));
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});const page=await context.newPage();page.setDefaultTimeout(15000);const result={origin};
try{
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(identity.loginName);await page.getByLabel('密码',{exact:true}).fill(identity.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!=='/login');await page.goto(origin+'/settings/permissions');await page.getByText(identity.loginName,{exact:true}).waitFor();await page.getByRole('button',{name:'我的账号',exact:true}).click();const d=page.getByRole('dialog',{name:'我的账号',exact:true});await d.waitFor();
 // Let rc-motion install its animation before waiting for its final state.
 await page.evaluate(async()=>{for(let i=0;i<3;i++)await new Promise(requestAnimationFrame);});
 await page.waitForFunction(()=>{
  const nodes=[...document.querySelectorAll('.ant-modal,.ant-modal-mask')];
  return nodes.length>=2&&nodes.every(n=>!n.className.match(/ant-(zoom|fade)-(appear|enter)/)&&getComputedStyle(n).opacity==='1'&&getComputedStyle(n).visibility==='visible'&&getComputedStyle(n).transform==='none');
 });
 result.geometry=await d.evaluate(e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,headerHeight:e.querySelector('.ant-modal-header').getBoundingClientRect().height,noteHeight:e.querySelector('.v12-note').getBoundingClientRect().height,keyValueLineHeight:getComputedStyle(e.querySelector('.v12-key-values')).lineHeight};});
 await d.screenshot({path:root+'/evidence/my-account-final-foreground.png'});await page.screenshot({path:root+'/evidence/my-account-final-stable.png'});
 await d.getByRole('button',{name:'关闭',exact:true}).click();await d.waitFor({state:'hidden'});result.closePassed=true;result.status='passed';
}catch(e){result.status='failed';result.failure=e.message.replaceAll(identity.password,'[REDACTED]');process.exitCode=1;}
finally{writeFileSync(root+'/evidence/account-stable-capture.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify(result));}
