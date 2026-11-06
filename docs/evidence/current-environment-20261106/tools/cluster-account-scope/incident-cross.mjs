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
const checks={at:new Date().toISOString(),results:[]};
try{
  const incident='01a1ae6b-7939-7010-845d-59eb01ca3a27';
  for (const suffix of ['', '/timeline', '/evidence', '/rca']) {
    const response=await read(page,'/api/v1/incidents/'+incident+suffix+'?'+scope);
    assert.equal(response.status,200,JSON.stringify(response.body).slice(0,400));
    const data=response.body.data;
    checks.results.push({suffix,status:response.status,count:Array.isArray(data)?data.length:undefined,state:data.state});
    if(!suffix)assert.equal(data.state,'open');
    if(suffix==='/evidence') assert(data.length>0);
  }
  checks.status='passed';
} catch(error){
  checks.status='failed';
  checks.failure=String(error.message).slice(0,600);
  process.exitCode=1;
} finally{
  writeFileSync(dir+'/incident-cross-namespace.json',JSON.stringify(checks,null,2)+'\n');
  console.log(JSON.stringify(checks));
  await browser.close();
}
