import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';
import assert from 'node:assert/strict';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const base='/data/ops-v12-native-visual-r24-20261026',work=base+'/r53',origin='https://192.168.0.63:30471';
const account=JSON.parse(readFileSync(base+'/private/browser-account.json','utf8')),target=JSON.parse(readFileSync(base+'/r43/browser-topology.json','utf8')).root;
const result={at:new Date().toISOString(),requests:[],errors:[],queries:[],screenshots:false,otherBusinessWrites:0};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();page.setDefaultTimeout(20000);
 page.on('pageerror',error=>result.errors.push(safeErrorMessage(error,[account.password])));
 page.on('response',r=>{const u=new URL(r.url());if(u.pathname.startsWith('/api/v1/observability/'))result.requests.push({path:u.pathname,query:u.searchParams.toString(),method:r.request().method(),status:r.status()})});
 page.on('request',r=>{const u=new URL(r.url());if(r.method()!=='GET'&&u.pathname.startsWith('/api/v1/')&&!u.pathname.includes('/auth/')&&u.pathname!='/api/v1/observability/queries')result.otherBusinessWrites++});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 const capResponse=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/observability/capabilities');const navigation=await page.goto(origin+'/observe/logs?'+new URLSearchParams({clusterUid:target.scope,canonicalId:target.canonicalId}));result.indexSHA256=createHash('sha256').update(await navigation.body()).digest('hex');const cr=await capResponse;assert.equal(cr.status(),200);result.capabilities=(await cr.json()).data;
 const native=result.capabilities.sources.find(s=>s.source.collectorVersion==='v1.52.0-ops1');assert(native,'native source missing');assert.equal(native.source.registrationId,'01a19f70-d86a-70d7-a29d-dc148a60be6a');assert.equal(native.source.registrationRevision,1);assert.equal(result.capabilities.sources.length,2);assert(!result.capabilities.sources.some(s=>s.source.collectorVersion.includes('snapshot')));result.source=native;
 async function selectNative(){await page.getByLabel('数据来源',{exact:true}).click();await page.locator('.ant-select-item-option-content').getByText('VictoriaLogs · r49-continuous-pod-logs',{exact:true}).click();assert(await page.getByRole('button',{name:'查询',exact:true}).isEnabled())}
 const nanos=s=>BigInt(Date.parse(s))*1000000n+BigInt(((s.match(/\.(\d+)/)?.[1]||"").padEnd(9,"0").slice(3))||"0");
 async function execute(){
  const [create]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/observability/queries'&&r.request().method()==='POST'),page.getByRole('button',{name:'查询',exact:true}).click()]);assert([200,201,202].includes(create.status()),'create status '+create.status());const envelope=await create.json(),view=envelope.data;assert(view?.queryId);const request=create.request().postDataJSON();assert.equal(request.resource.uid,target.stableId);assert.equal(request.source.registrationId,native.source.registrationId);assert.equal(request.source.collectorVersion,'v1.52.0-ops1');
  await page.getByText(/来源总体 \d+ 条 · 本次脱敏样例 \d+ 条/).waitFor();const response=await context.request.get(origin+'/api/v1/observability/queries/'+view.queryId+'/results?'+new URLSearchParams({clusterUid:target.scope}));assert.equal(response.status(),200);const saved=(await response.json()).data;assert.equal(saved.population.basis,'exact');assert(saved.population.count>0);assert(saved.samples.items.every(s=>s.fields?.containerId));assert.equal(saved.status,'partial');assert(saved.samples.returned>0&&saved.samples.returned<=20);assert(saved.samples.items.every(s=>nanos(s.timestamp)>=nanos(saved.window.from)&&nanos(s.timestamp)<nanos(saved.window.to)));assert(Date.parse(saved.window.from)>Date.parse('2026-10-29T00:00:00+08:00'));result.queries.push({createStatus:create.status(),request,view,result:saved});return saved;
 }
 await selectNative();const first=await execute();
 await page.reload();await page.getByRole('button',{name:/查询历史/}).click();await page.locator('tr[data-row-key="'+first.queryId+'"]').waitFor();result.historyRecovered=true;result.status='passed_one_real_log_query_and_history';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{writeFileSync(work+'/browser-archive-logs.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,source:result.source?.source,queries:result.queries.map(q=>({queryId:q.result.queryId,returned:q.result.samples.returned,window:q.result.window})),newSamplesAfterFirstWindow:result.newSamplesAfterFirstWindow,historyRecovered:result.historyRecovered,errors:result.errors,failure:result.failure}))}
