import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
const dir='/data/ops-v12-native-visual-r24-20261026/r28-worker',origin='https://192.168.0.63:30471';
const completed=JSON.parse(readFileSync(dir+'/third-business-result.json','utf8'));
const result={origin,scope:'actual native admin browser audit entry, independent role, existing persisted real model/query result; no new model call',requests:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});const context=await browser.newContext({ignoreHTTPSErrors:true,storageState:dir+'/private/actor-storage.json',viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});const page=await context.newPage();page.setDefaultTimeout(20000);
page.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/v1/assistant/'))result.requests.push({path,status:r.status()});});
try{
 await page.goto(origin+'/settings/audit');await page.getByRole('button',{name:'审计问数',exact:true}).waitFor();await page.getByRole('button',{name:'审计问数',exact:true}).click();await page.waitForURL('**/settings/audit/assistant');await page.getByText('管理员审计域',{exact:true}).waitFor();
 const session=await context.request.get(origin+'/api/v1/auth/session');const s=(await session.json()).data;assert(s.authorization.grants.some(g=>g.role==='platform_admin')&&!s.authorization.grants.some(g=>g.role==='operator'),'test identity must be admin only');
 const denied=await context.request.get(origin+'/api/v1/resources?clusterUid=r28-no-operator');assert(denied.status()===403,'audit entry broadened operator access');
 await page.goto(origin+'/settings/audit/assistant?conversation='+completed.conversationId);await page.getByText(completed.answer.summary,{exact:true}).waitFor();await page.screenshot({path:dir+'/audit-ui-result.png',fullPage:true});
 assert(result.requests.some(r=>r.path.endsWith('/turns/'+completed.turnId)&&r.status===200)||result.requests.some(r=>r.path.endsWith('/messages')&&r.status===200));
 await page.reload();await page.getByText(completed.answer.summary,{exact:true}).waitFor();result.status='passed';result.checks=['audit record page links to audit assistant without operator grant','administrator audit-only session; operational resource API403','persisted real DeepSeek answer and query facts render; page reload retains result'];result.conversationId=completed.conversationId;result.turnId=completed.turnId;
}catch(error){result.status='failed';result.failure=error.message;process.exitCode=1;}
finally{await browser.close();writeFileSync(dir+'/audit-ui-result.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result));}
