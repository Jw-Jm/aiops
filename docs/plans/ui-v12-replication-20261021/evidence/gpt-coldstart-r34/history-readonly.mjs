import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {safeShot} from './safe-capture.mjs';
import {safeErrorMessage} from './safe-error.mjs';
const root='/data/ops-v12-native-cold-r34-20261027',origin='https://192.168.0.63:30472';
const account=JSON.parse(readFileSync(root+'/private/r34-hist-account.json','utf8'));
const fixture=JSON.parse(readFileSync(root+'/evidence/history-fixture.json','utf8'));
const result={origin,checks:[],eventsRequested:0,requests:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const c=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'});const p=await c.newPage();
 p.on('request',r=>{if(new URL(r.url()).pathname.endsWith('/events'))result.eventsRequested++});
 p.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/'))result.requests.push({path,status:r.status(),requestId:r.headers()['x-request-id']})});
 await p.goto(origin+'/login');await p.getByLabel('用户名',{exact:true}).fill(account.loginName);await p.getByLabel('密码',{exact:true}).fill(account.password);await p.getByRole('button',{name:/^登\s*录/}).click();await p.waitForURL(u=>u.pathname!='/login');
 await p.goto(origin+'/settings/audit/assistant?conversation='+fixture.conversationId);await p.getByText('历史会话只读',{exact:true}).waitFor();
 for(const name of [/^发\s*送$/,/^归\s*档$/])if(!await p.getByRole('button',{name}).isDisabled())throw Error('historical mutation control enabled');
 if(!await p.getByRole('textbox',{name:'问题',exact:true}).isDisabled() || result.eventsRequested!==0)throw Error('historical write or event stream enabled');
 result.capture=await safeShot(p,root+'/evidence/history-read-only.png');result.checks.push('post-restart mapped history renders read-only with zero owner SSE requests');result.status='passed';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{writeFileSync(root+'/evidence/history-readonly.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,eventsRequested:result.eventsRequested,failure:result.failure}))}
