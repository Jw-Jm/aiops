import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
import {safeShot} from '../r30-auth/safe-capture.mjs';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r54',origin='https://192.168.0.63:30471';
const account=JSON.parse(readFileSync(root+'/private/browser-account.json')),source=JSON.parse(readFileSync(work+'/source-start.json')),registration=JSON.parse(readFileSync(work+'/source-registration.json')).outcome;
const uid=JSON.parse(readFileSync(work+'/current-seaweed.json')).stableId,canonical=`k8s+v1://${source.tenantId}/${source.clusterUid}/core/Pod/${uid}`;
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});const result={at:new Date().toISOString(),queries:[],errors:[],requests:[],businessWrites:0};
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();page.setDefaultTimeout(30000);
 page.on('pageerror',e=>result.errors.push(safeErrorMessage(e,[account.password])));
 page.on('request',r=>{const p=new URL(r.url()).pathname;if(r.method()!=='GET'&&p.startsWith('/api/')&&!p.includes('/auth/')&&p!=='/api/v1/metrics/query')result.businessWrites++});
 page.on('response',r=>{const p=new URL(r.url()).pathname;if(p.startsWith('/api/v1/metrics/'))result.requests.push({path:p,status:r.status(),requestId:r.headers()['x-request-id']})});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 await page.goto(origin+'/observe/metrics?'+new URLSearchParams({clusterUid:source.clusterUid,canonicalId:canonical}));
 await page.getByText('真实指标来源已就绪',{exact:true}).waitFor();
 const kinds=[['metrics.pod-cpu','CPU 使用量'],['metrics.pod-memory','内存工作集'],['metrics.pod-restarts','容器重启增量'],['metrics.pod-phase','Pod 状态数量']];
 for(const [id,name]of kinds){
  if(id!==kinds[0][0]){await page.getByLabel('指标类型',{exact:true}).click();await page.locator('.ant-select-item-option-content').getByText(name,{exact:true}).click()}
  await page.waitForFunction(()=>[...document.querySelectorAll('button')].some(b=>b.textContent.includes('查询指标')&&!b.disabled));
  const [reply]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/metrics/query'),page.getByRole('button',{name:'查询指标',exact:true}).click()]);
  const body=await reply.json();assert.equal(reply.status(),200,JSON.stringify(body));const data=body.data;assert.equal(data.datasetId,id);assert.equal(data.status,'partial');assert.equal(data.exact,false);assert.equal(data.rows.length,3);assert.equal(data.sourceManifest.sourceId,registration.sourceId);assert(data.rows.every(r=>Number.isFinite(r.value)&&r.value>=0));
  await page.getByRole('img',{name:'指标时间趋势'}).waitFor();assert((await page.getByRole('status').innerText()).includes('3 个指标计算点'));
  result.queries.push({intent:reply.request().postDataJSON().intent,canonicalId:canonical,result:data});
 }
 assert(result.queries.find(q=>q.result.datasetId==='metrics.pod-memory').result.rows.every(r=>r.value>0));assert(result.queries.find(q=>q.result.datasetId==='metrics.pod-phase').result.rows.every(r=>r.value===1));
 result.guards=await page.evaluate(async({cluster,canonical,intent})=>{
  const session=await(await fetch('/api/v1/auth/session')).json();const csrf=session.data.csrf.token,path='/api/v1/metrics/query?'+new URLSearchParams({clusterUid:cluster});
  const post=async(body,token=csrf)=>{const r=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json',...(token?{'X-CSRF-Token':token}:{})},body:JSON.stringify(body)});return {status:r.status,body:await r.json()}};
  const foreign=await post({canonicalId:canonical.replace('/'+cluster+'/', '/00000000-0000-4000-8000-000000000051/'),intent});
  const raw=await post({canonicalId:canonical,intent,query:'arbitrary prometheus expression'});
  const missing=await post({canonicalId:canonical.replace(/[^/]+$/,'00000000-0000-4000-8000-000000000051'),intent});
  const noCSRF=await post({canonicalId:canonical,intent},'');
  return {foreignStatus:foreign.status,rawQueryStatus:raw.status,missing:{status:missing.status,resultStatus:missing.body.data?.status,rows:missing.body.data?.rows,exact:missing.body.data?.exact},csrfStatus:noCSRF.status};
 },{cluster:source.clusterUid,canonical,intent:result.queries[0].intent});
 assert.equal(result.guards.foreignStatus,403);assert.equal(result.guards.rawQueryStatus,400);assert.equal(result.guards.csrfStatus,403);assert.equal(result.guards.missing.status,200);assert.equal(result.guards.missing.resultStatus,'no_data');assert.deepEqual(result.guards.missing.rows,[]);assert.equal(result.guards.missing.exact,false);
 try{result.capture=await safeShot(page,work+'/metrics-phase.png')}catch(e){if(!String(e.message).includes('recovery-code'))throw e;result.capture={status:'refused',reason:'recovery-code probe matched public UUID text; no screenshot retained'}}
 assert.equal(result.businessWrites,0);assert.deepEqual(result.errors,[]);result.status='passed_four_native_metric_trends_and_guards';
}catch(e){result.status='failed';result.failure=safeErrorMessage(e,[account.password]);process.exitCode=1}
finally{writeFileSync(work+'/browser-metrics.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,datasets:result.queries.map(q=>q.result.datasetId),guards:result.guards,errors:result.errors,failure:result.failure}))}
