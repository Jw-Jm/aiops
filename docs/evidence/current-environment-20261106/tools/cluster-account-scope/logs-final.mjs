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
  const requestKey=(await import('node:crypto')).randomUUID();
  const request=async(path,body)=>page.evaluate(async ({path,body,csrf,key})=>{
    const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':key},body:JSON.stringify(body)});
    return {status:response.status,body:await response.json()};
  },{path,body,csrf:session.body.data.csrf.token,key:requestKey});
  const canonical=uid=>'k8s+v1://'+cluster.tenantId+'/'+cluster.clusterUid+'/core/Pod/'+uid;
  let logs;
  for(let i=0;i<6;i++){
    logs=await read(page,'/api/v1/observability/capabilities?'+scope+'&canonicalId='+encodeURIComponent(canonical(proof.nativeLogPodUID)));
    if(logs.status===200)break;
    await page.waitForTimeout(2000);
  }
  assert.equal(logs.status,200,JSON.stringify(logs.body));
  const capability=logs.body.data.sources.find(s=>s.source.collectorVersion==='v1.52.0-ops1');
  const source=capability?.source;
  const to=new Date(capability?.coverageTo);
  assert(Number.isFinite(+to),'missing log coverage watermark');
  assert.equal(source?.registrationRevision,2);
  const body=load('ui/browser-archive-logs.json').queries[0].request;
  const namespaces=load('cluster-account-scope-20261106/namespaces.json').items;
  body.resource={canonicalId:canonical(proof.nativeLogPodUID),uid:proof.nativeLogPodUID,clusterUid:cluster.clusterUid,namespaceScope:{kind:'namespaced',namespace:'kube-system',namespaceUid:namespaces.find(n=>n.metadata.name==='kube-system').metadata.uid}};
  body.source=source;
  body.window={...body.window,from:new Date(+to-300000).toISOString(),to:to.toISOString()};
  let created;
  for(let i=0;i<4;i++){
    created=await request('/api/v1/observability/queries?'+scope,body);
    if(created.status!==503)break;
    await page.waitForTimeout(1000);
  }
  assert([200,201,202].includes(created.status),JSON.stringify(created.body));
  const queryId=created.body.data.queryId;
  checks.logQuery={queryId,status:created.status};
  for(let i=0;i<12;i++){
    const result=await read(page,'/api/v1/observability/queries/'+queryId+'/results?'+scope);
    checks.lastResult={http:result.status,status:result.body.data?.status,code:result.body.code};
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
  writeFileSync(dir+'/logs-final.json',JSON.stringify(checks,null,2)+'\n');
  console.log(JSON.stringify(checks));await browser.close();
}
