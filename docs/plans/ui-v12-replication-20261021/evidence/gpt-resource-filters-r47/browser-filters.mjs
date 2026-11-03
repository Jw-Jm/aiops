import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';
import assert from 'node:assert/strict';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r47',origin='https://192.168.0.63:30471';
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8')),node=JSON.parse(readFileSync(root+'/r43/browser-topology.json','utf8')).root;
const result={at:new Date().toISOString(),requests:[],errors:[],businessWrites:0,startupReads:[],screenshots:false};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();page.setDefaultTimeout(15000);
 page.on('pageerror',e=>result.errors.push(safeErrorMessage(e,[account.password])));
 page.on('response',r=>{const u=new URL(r.url());if(u.pathname==='/api/v1/resources')result.requests.push({query:u.searchParams.toString(),status:r.status()})});
 page.on('request',r=>{const u=new URL(r.url());if(r.method()!=='GET'&&u.pathname.startsWith('/api/v1/')&&!u.pathname.includes('/auth/'))result.businessWrites++});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 async function read(params){const response=await context.request.get(origin+'/api/v1/resources?'+new URLSearchParams({clusterUid:node.scope,...params}));return {status:response.status(),body:await response.json()}}
 let full;
 for(let i=0;i<12;i++){const r=await read({limit:'200'});result.startupReads.push({status:r.status,code:r.body.code});if(r.status===200){full=r.body;break}assert.equal(r.status,503);await delay(750)}
 assert(full);assert(!full.meta.nextCursor,'oracle must have whole authorized inventory');assert(full.data.length>1);result.inventory=full;
 const expected={kinds:[...new Set(full.data.map(n=>n.kind))].sort(),namespaces:[...new Set(full.data.map(n=>n.namespace).filter(Boolean))].sort(),clusterScoped:full.data.some(n=>!n.namespace)};
 assert(!('filterOptions' in full.meta.summary),'legacy response acquired field');
 const scoped=await read({limit:'1',includeFilterOptions:'true',q:'no-such-r47-resource',namespace:'ops-native-v12-r35-scope'});assert.equal(scoped.status,200);assert.equal(scoped.body.data.length,0);assert.equal(scoped.body.meta.summary.total,0);assert.deepEqual(scoped.body.meta.summary.filterOptions,expected);result.emptyFiltered=scoped.body;result.expected=expected;
 const clusterOnly=await read({namespace:'__cluster__',includeFilterOptions:'true',limit:'50'});assert.equal(clusterOnly.status,200);assert(clusterOnly.body.data.every(n=>!n.namespace));assert.deepEqual(clusterOnly.body.meta.summary.filterOptions,expected);result.clusterOnly=clusterOnly.body;
 const invalid=await read({includeFilterOptions:'yes'});assert.equal(invalid.status,400);result.invalidFlag=invalid;
 const nav=await page.goto(origin+'/resources?'+new URLSearchParams({clusterUid:node.scope,q:'no-such-r47-resource',namespace:node.namespace,kind:'Pod',health:'normal',label:'r47=none',sort:'kind',order:'desc',limit:'20'}));result.indexSHA256=createHash('sha256').update(await nav.body()).digest('hex');
 await page.getByText('当前筛选范围没有正式数据',{exact:true}).waitFor();
 await page.getByLabel('资源类型',{exact:true}).click();const typeOptions=await page.locator('.ant-select-dropdown').filter({has:page.locator('#resource-kind_list')}).locator('.ant-select-item-option-content').allTextContents();assert.deepEqual(typeOptions,expected.kinds);result.kindOptions=typeOptions;await page.keyboard.press('Escape');
 await page.getByLabel('命名空间',{exact:true}).click();const namespaceOptions=await page.locator('.ant-select-dropdown').filter({has:page.locator('#resource-namespace_list')}).locator('.ant-select-item-option-content').allTextContents();assert.deepEqual(namespaceOptions,[...expected.namespaces,...(expected.clusterScoped?['集群级资源']:[])]);result.namespaceOptions=namespaceOptions;await page.keyboard.press('Escape');
 const [resetRead]=await Promise.all([page.waitForResponse(r=>{const u=new URL(r.url());return u.pathname==='/api/v1/resources'&&u.searchParams.get('includeFilterOptions')==='true'&&!u.searchParams.has('q')&&!u.searchParams.has('kind')}),page.getByRole('button',{name:/^重\s*置$/}).click()]);assert.equal(resetRead.status(),200);result.reset=await resetRead.json();assert.equal(result.reset.meta.summary.total,full.meta.summary.total);assert.equal(await page.getByLabel('搜索资源',{exact:true}).inputValue(),'');assert.equal(new URL(page.url()).search,'?'+new URLSearchParams({clusterUid:node.scope,limit:'20'}));result.resetPreservesScopeAndSize=true;
 await page.getByRole('columnheader',{name:'类型 / 领域',exact:true}).waitFor();await page.reload();await page.getByLabel('资源类型',{exact:true}).waitFor();assert.equal(await page.getByLabel('资源每页条数',{exact:true}).inputValue(),'20');result.reloadPreservesScopeAndSize=true;
 await page.getByLabel('资源类型',{exact:true}).click();const [kindRead]=await Promise.all([page.waitForResponse(r=>{const u=new URL(r.url());return u.pathname==='/api/v1/resources'&&u.searchParams.get('kind')==='Pod'}),page.locator('.ant-select-dropdown').filter({has:page.locator('#resource-kind_list')}).locator('.ant-select-item-option').filter({hasText:/^Pod$/}).click()]);assert.equal(kindRead.status(),200);result.selectedKind=await kindRead.json();assert(result.selectedKind.data.length>0);assert(result.selectedKind.data.every(n=>n.kind==='Pod'));assert.deepEqual(result.selectedKind.meta.summary.filterOptions,expected);
 assert.equal(result.businessWrites,0);assert.deepEqual(result.errors,[]);result.status='passed_scoped_resource_candidates_and_reset';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{writeFileSync(work+'/browser-filters.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,expected:result.expected,businessWrites:result.businessWrites,errors:result.errors,failure:result.failure}))}
