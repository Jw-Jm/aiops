import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r51',origin='https://192.168.0.63:30471',mode=process.env.MODE;
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json')),saved=JSON.parse(readFileSync(work+'/browser-metrics.json')).queries[0],cluster=JSON.parse(readFileSync(work+'/source-start.json')).clusterUid;
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});let out={mode:'uid-isolation'};
try{
 const page=await browser.newPage({ignoreHTTPSErrors:true});await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 const identity=JSON.parse(readFileSync(work+'/identity-input.json'));const memory=JSON.parse(readFileSync(work+'/browser-metrics.json')).queries.find(q=>q.result.datasetId==='metrics.pod-memory');
 out=await page.evaluate(async({identity,memory,cluster})=>{
  const session=await(await fetch('/api/v1/auth/session')).json();const checks=[];
  for(const window of ['historical','current'])for(const uid of [identity.previousUID,identity.currentUID]){
   const intent={...memory.intent,timeRange:window==='historical'?identity.historicalWindow:memory.intent.timeRange};const canonicalId=memory.canonicalId.replace(/[^/]+$/,uid);
   const reply=await fetch('/api/v1/metrics/query?'+new URLSearchParams({clusterUid:cluster}),{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.data.csrf.token},body:JSON.stringify({canonicalId,intent})});const body=await reply.json();
   checks.push({window,uid,http:reply.status,result:body.data,error:body.error});
  }return {identity,checks};
 },{identity,memory,cluster});
 for(const c of out.checks){assert.equal(c.http,200);const present=(c.window==='current')===(c.uid===identity.currentUID);assert.equal(c.result.rows.length,present?3:0);assert.equal(c.result.status,present?'partial':'no_data');assert.equal(c.result.exact,false)}
 out.status='passed';
}catch(e){out.status='failed';out.failure=safeErrorMessage(e,[account.password]);process.exitCode=1}
finally{writeFileSync(work+'/identity-check.json',JSON.stringify(out,null,2));await browser.close();console.log(JSON.stringify({mode,status:out.status,capabilities:out.sourceCapabilities?.length,queryStatus:out.queryStatus,failure:out.failure}))}
