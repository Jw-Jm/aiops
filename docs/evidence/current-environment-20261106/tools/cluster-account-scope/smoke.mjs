import assert from 'node:assert/strict';
import {readFileSync,writeFileSync} from 'node:fs';
import {account,read,root,origin} from '/data/ops-current/current/acceptance/admin.mjs';

const dir=root+'/evidence/cluster-account-scope-20261106';
const load=(path)=>JSON.parse(readFileSync(root+'/evidence/'+path));
const proof=load('cluster-account-scope-20261106/qualification.json');
const cluster=load('cluster-registration.json').body.data;
const scope='clusterUid='+cluster.clusterUid;
const {chromium}=await import('/opt/ops-current-tools/playwright-core/index.mjs');
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const page=await browser.newPage({ignoreHTTPSErrors:true});
await page.goto(origin+'/login',{waitUntil:'domcontentloaded'});
await page.getByLabel('用户名',{exact:true}).fill(account.loginName);
await page.getByLabel('密码',{exact:true}).fill(account.password);
await page.getByRole('button',{name:/^登\s*录/}).click();
await page.waitForURL(u=>u.pathname!='/login',{waitUntil:'domcontentloaded'});
const checks={at:new Date().toISOString(),origin,resources:[],metrics:[]};
try{
  const session=await read(page,'/api/v1/auth/session');
  assert.equal(session.status,200);
  const grants=session.body.data.authorization.grants;
  assert(grants.some(g=>g.role==='platform_admin'));
  assert(grants.some(g=>g.role==='operator'&&g.scope.clusterUids.includes(cluster.clusterUid)&&g.scope.namespaces.length===0));
  checks.roles=grants.map(g=>({role:g.role,scope:g.scope}));
  await page.goto(origin+'/settings/permissions?'+scope,{waitUntil:'domcontentloaded'});
  await page.getByRole('button',{name:'配置授权'}).first().click({timeout:20000}).catch(async e=>{
    checks.uiDiagnostic={url:page.url(),text:(await page.locator('main').innerText({timeout:2000}).catch(()=>'' )).slice(0,500)};
    throw e;
  });
  const dialog=page.getByRole('dialog');
  assert.equal(await dialog.getByLabel('命名空间',{exact:true}).count(),0);
  await dialog.getByRole('checkbox',{name:'高级：限制命名空间'}).check();
  await dialog.getByLabel('命名空间',{exact:true}).waitFor();
  await dialog.getByRole('combobox',{name:'角色'}).click();
  await page.locator('.ant-select-item-option-content').getByText('管理员兼运维',{exact:true}).waitFor();
  checks.permissionsUI=true;
  writeFileSync(dir+'/ui.json',JSON.stringify({at:new Date().toISOString(),status:'passed',defaultWholeCluster:true,advancedNamespaceOption:true,options:await page.locator('.ant-select-item-option-content').allTextContents()},null,2)+'\n');
  for (const [namespace,kind,name] of [
    ['kube-system','Pod',proof.nativePodName],['ops-current','Pod','ops-seaweedfs-0'],
    ['__cluster__','Node','mssc'],['__cluster__','PersistentVolume','ops-current-acceptance'],
  ]){
    const query=new URLSearchParams({namespace,kind,name,clusterUid:cluster.clusterUid});
    const response=await read(page,'/api/v1/resources?'+query);
    assert.equal(response.status,200,JSON.stringify(response.body));
    const items=Array.isArray(response.body.data)?response.body.data:response.body.data.items;
    assert(items?.length,JSON.stringify({namespace,kind,name,body:response.body}).slice(0,1000));
    checks.resources.push({namespace,kind,name,count:items.length});
  }
  assert.equal((await read(page,'/api/v1/resources?clusterUid=00000000-0000-4000-8000-000000000063')).status,403);
  checks.foreignCluster=403;
  const cap=await read(page,'/api/v1/capabilities?'+scope);
  assert.equal(cap.status,200);
  assert.equal(cap.body.data.commandExecution.status,'disabled');
  checks.capabilities=cap.body.data;
  for(const route of ['findings','incidents','admin/execution-profiles']){
    const result=await read(page,'/api/v1/'+route+(route.startsWith('admin')?'':'?'+scope));
    assert.equal(result.status,200,route);
  }
  const request=async(path,body)=>page.evaluate(async ({path,body,csrf})=>{
    const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify(body)});
    return {status:response.status,body:await response.json()};
  },{path,body,csrf:session.body.data.csrf.token});
  const canonical=uid=>'k8s+v1://'+cluster.tenantId+'/'+cluster.clusterUid+'/core/Pod/'+uid;
  const metricCap=await read(page,'/api/v1/metrics/capabilities?'+scope+'&canonicalId='+encodeURIComponent(canonical(proof.nativePodUID)));
  assert.equal(metricCap.status,200,JSON.stringify(metricCap.body));
  const qualified=metricCap.body.data.sourceCapabilities;
  assert(qualified.length>0, 'no qualified metrics capability');
  const to=new Date(Math.floor(Math.min(...qualified.map(q=>Date.parse(q.coverageTo)))/1000)*1000);
  const from=new Date(+to-180000);
  const intents=load('ui/browser-metrics.json').queries.map(q=>q.intent);
  for(const intent of intents){
    intent.timeRange={from:from.toISOString(),to:to.toISOString(),timezone:'Asia/Shanghai'};
    const result=await request('/api/v1/metrics/query?'+scope,{canonicalId:canonical(proof.nativePodUID),intent});
    assert.equal(result.status,200,JSON.stringify(result.body));
    const data=result.body.data;
    assert(data.rows?.length>0,JSON.stringify({datasetId:intent.datasetId,status:data.status,reason:data.reasons}));
    checks.metrics.push({datasetId:intent.datasetId,rows:data.rows.length,status:data.status,exact:data.exact,requestId:result.body.requestId});
  }
  const logs=await read(page,'/api/v1/observability/capabilities?'+scope);
  assert.equal(logs.status,200);
  const source=logs.body.data.sources.find(s=>s.source.collectorVersion==='v1.52.0-ops1')?.source;
  assert.equal(source?.registrationRevision,2);
  const body=load('ui/browser-archive-logs.json').queries[0].request;
  const namespaces=load('cluster-account-scope-20261106/namespaces.json').items;
  body.resource={canonicalId:canonical(proof.nativeLogPodUID),uid:proof.nativeLogPodUID,clusterUid:cluster.clusterUid,namespaceScope:{kind:'namespaced',namespace:'kube-system',namespaceUid:namespaces.find(n=>n.metadata.name==='kube-system').metadata.uid}};
  body.source=source;
  body.window={...body.window,from:new Date(+to-300000).toISOString(),to:to.toISOString()};
  const created=await request('/api/v1/observability/queries?'+scope,body);
  assert([200,201,202].includes(created.status),JSON.stringify(created.body));
  const queryId=created.body.data.queryId;
  for(let i=0;i<12;i++){
    const result=await read(page,'/api/v1/observability/queries/'+queryId+'/results?'+scope);
    if(result.status===200&&result.body.data?.samples?.returned>0){
      checks.logs={queryId,returned:result.body.data.samples.returned,status:result.body.data.status};
      break;
    }
    await page.waitForTimeout(1000);
  }
  assert(checks.logs,'no saved log samples');
  checks.status='passed';
}catch(error){checks.status='failed';checks.failure=String(error.message).slice(0,1500);process.exitCode=1;}
finally{
  writeFileSync(dir+'/smoke.json',JSON.stringify(checks,null,2)+'\n');
  console.log(JSON.stringify(checks));await browser.close();
}
