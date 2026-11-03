import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const base='/data/ops-v12-native-visual-r24-20261026',work=base+'/r49',origin='https://192.168.0.63:30471';
const result=JSON.parse(readFileSync(work+'/browser-two-queries-history-locator-failed.json','utf8')),account=JSON.parse(readFileSync(base+'/private/r31-operator-account.json','utf8'));
assert.equal(result.status,'failed');assert(result.failure.includes('查询历史'));assert.equal(result.queries.length,2);assert(result.newSamplesAfterFirstWindow>0);assert(result.firstResultImmutableAfterRefresh);
const target=result.queries[1].result.resource,second=result.queries[1].result;result.initialHarnessFailure=result.failure;delete result.failure;
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();page.setDefaultTimeout(25000);
 page.on('pageerror',e=>result.errors.push(safeErrorMessage(e,[account.password])));
 page.on('request',r=>{const path=new URL(r.url()).pathname;if(r.method()!=='GET'&&path.startsWith('/api/v1/')&&!path.includes('/auth/'))throw Error('resume must not create another query')});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 await page.goto(origin+'/observe/logs?'+new URLSearchParams({clusterUid:target.clusterUid,canonicalId:target.canonicalId}));
 const [history]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/observability/queries'),page.getByRole('button',{name:/查询历史/}).click()]);assert.equal(history.status(),200);result.historyResponse=await history.json();for(const entry of result.queries)assert(result.historyResponse.data.some(q=>q.queryId===entry.result.queryId));
 const [read]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/observability/queries/'+second.queryId+'/results'),page.locator('tr[data-row-key="'+second.queryId+'"]').getByRole('button',{name:/查\s*看/}).click()]);assert.equal(read.status(),200);assert.equal((await read.json()).data.resultDigest,second.resultDigest);await page.locator('.v12-log-summary').waitFor();result.historySummary=await page.locator('.v12-log-summary').innerText();assert(result.historySummary.includes('来源总体 '+second.population.count+' 条 · 本次脱敏样例 '+second.samples.returned+' 条'));result.historyRecovered=true;
 const foreign=target.canonicalId.replace('/'+target.clusterUid+'/', '/00000000-0000-4000-8000-000000000048/');const denied=await context.request.get(origin+'/api/v1/observability/capabilities?'+new URLSearchParams({clusterUid:target.clusterUid,canonicalId:foreign}));assert.equal(denied.status(),403);result.foreignIdentityDenied={status:denied.status(),body:await denied.json()};
 assert.deepEqual(result.errors,[]);assert.equal(result.otherBusinessWrites,0);result.status='passed_continuous_archive_two_real_queries_and_history';result.resumedAt=new Date().toISOString();
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{writeFileSync(work+'/browser-archive-logs.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,queryIds:result.queries.map(q=>q.result.queryId),newSamplesAfterFirstWindow:result.newSamplesAfterFirstWindow,historyRecovered:result.historyRecovered,failure:result.failure}))}
