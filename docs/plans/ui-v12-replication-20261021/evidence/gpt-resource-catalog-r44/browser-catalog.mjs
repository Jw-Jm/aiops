import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r44',origin='https://192.168.0.63:30471';
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8')),node=JSON.parse(readFileSync(root+'/r43/browser-topology.json','utf8')).root;
const result={at:new Date().toISOString(),requests:[],errors:[],writes:0,startupReads:[],screenshots:false};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();page.setDefaultTimeout(15000);
 page.on('pageerror',e=>result.errors.push(safeErrorMessage(e,[account.password])));
 page.on('response',r=>{const u=new URL(r.url());if(u.pathname.startsWith('/api/v1/resources'))result.requests.push({path:u.pathname,query:u.searchParams.toString(),status:r.status()})});
 page.on('request',r=>{const u=new URL(r.url());if(r.method()!=='GET'&&u.pathname.startsWith('/api/v1/')&&!u.pathname.includes('/auth/'))result.writes++});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 const params={clusterUid:node.scope,namespace:node.namespace,kind:'Pod',q:node.stableId.toUpperCase(),health:'normal',limit:'10'};
 let exact;
 for(let attempt=0;attempt<12;attempt++){
  const response=await context.request.get(origin+'/api/v1/resources?'+new URLSearchParams(params));const body=await response.json();result.startupReads.push({status:response.status(),requestId:body.requestId,code:body.error?.code});
  if(response.status()===200){exact=body;break}if(response.status()!==503)throw Error('unexpected exact read '+response.status());await delay(750);
 }
 if(!exact)throw Error('graph did not recover within bounded startup reads');
 if(exact.data.length!==1||exact.meta.summary.total!==1||exact.data[0].canonicalId!==node.canonicalId||exact.data[0].attributes.health!=='normal'||exact.data[0].attributes.resourceDomain!=='kubernetes')throw Error('UID search/health/domain mismatch');result.exact=exact;
 const nav=await page.goto(origin+'/resources?'+new URLSearchParams({clusterUid:node.scope,namespace:node.namespace,kind:'Pod'}));result.indexSHA256=createHash('sha256').update(await nav.body()).digest('hex');
 await page.getByRole('heading',{name:'资源目录',exact:true}).waitFor();await page.getByRole('columnheader',{name:'来源 / 最近观测',exact:true}).waitFor();
 const search=page.getByLabel('搜索资源',{exact:true});await search.fill(node.stableId.toUpperCase());
 const [read]=await Promise.all([page.waitForResponse(r=>{const u=new URL(r.url());return u.pathname==='/api/v1/resources'&&u.searchParams.get('q')===node.stableId.toUpperCase()}),search.press('Enter')]);if(read.status()!==200)throw Error('UI search failed '+read.status());
 await page.getByRole('link',{name:node.name,exact:true}).waitFor();await page.getByText('本页 1 条 · 当前授权匹配 1 条',{exact:true}).waitFor();
 if(new URL(page.url()).searchParams.get('q')!==node.stableId.toUpperCase())throw Error('q not in URL');result.searchRequest=await read.json();
 await page.getByRole('button',{name:'速览',exact:true}).click();const drawer=page.getByRole('dialog',{name:node.name,exact:true});await drawer.waitFor();const detailLink=drawer.getByRole('link',{name:'进入资源详情',exact:true});await detailLink.waitFor();result.detailLink=await detailLink.getAttribute('href');
 if(!result.detailLink.includes(encodeURIComponent(node.canonicalId)))throw Error('quick view lost UID');await drawer.locator('.ant-drawer-close').click();
 await page.getByRole('combobox',{name:'资源每页条数',exact:true}).selectOption('20');await page.waitForURL(u=>u.searchParams.get('limit')==='20');
 await page.getByText('本页 1 条 · 当前授权匹配 1 条',{exact:true}).waitFor();await page.reload();await page.getByRole('columnheader',{name:'来源 / 最近观测',exact:true}).waitFor();
 if(await page.getByLabel('资源每页条数',{exact:true}).inputValue()!=='20'||await page.getByLabel('搜索资源',{exact:true}).inputValue()!==node.stableId.toUpperCase())throw Error('URL reload lost search/page size');
 const normal=await (await context.request.get(origin+'/api/v1/resources?'+new URLSearchParams({...params,q:'pOd',limit:'50'}))).json();if(!normal.data.every(n=>n.kind==='Pod'&&n.namespace===node.namespace&&n.attributes.health==='normal'))throw Error('kind/health filtering mismatch');result.normalPods=normal;
 const unseen=await (await context.request.get(origin+'/api/v1/resources?'+new URLSearchParams({...params,namespace:'ops-native-v12-r35-scope'}))).json();if(unseen.data?.length||unseen.meta?.summary?.total!==0)throw Error('unauthorized namespace affected search summary');result.unauthorizedSearch=unseen;
 const oversized=await context.request.get(origin+'/api/v1/resources?'+new URLSearchParams({...params,q:'字'.repeat(257)}));if(oversized.status()!==400)throw Error('oversized q not rejected');result.oversized={status:oversized.status(),body:await oversized.json()};
 await page.goto(origin+'/resources/topology?'+new URLSearchParams({clusterUid:node.scope,canonicalId:node.canonicalId,graphView:'list'}));
 await page.getByRole('columnheader',{name:'关系说明',exact:true}).waitFor();const rootRow=page.getByRole('button',{name:node.name,exact:true}).locator('xpath=ancestor::tr');result.topologyRowText=await rootRow.innerText();const topology=await (await context.request.get(origin+'/api/v1/resources/neighbors?'+new URLSearchParams({canonicalId:node.canonicalId,depth:'2',maxNodes:'200',maxEdges:'400'}))).json();result.topologyRoot=topology.data?.nodes?.find(n=>n.canonicalId===node.canonicalId);if(!await rootRow.isVisible()||!result.topologyRowText.includes('正常')||result.topologyRoot?.attributes?.health!=='normal')throw Error('topology health differs from list');result.topologyHealthVisible=true;
 const gate=await (await context.request.get(origin+'/api/v1/assistant/capabilities?clusterUid='+node.scope)).json();result.modelTasks=gate.data?.taskKinds;
 if(JSON.stringify(result.modelTasks)!==JSON.stringify(['data_query']))throw Error('model task scope changed');if(result.errors.length||result.writes)throw Error('page error or unexpected business write');
 result.status='passed_scoped_catalog_search_health_quickview';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{await browser.close();writeFileSync(work+'/browser-catalog.json',JSON.stringify(result,null,2));console.log(JSON.stringify({status:result.status,startupReads:result.startupReads,writes:result.writes,errors:result.errors,failure:result.failure}))}
