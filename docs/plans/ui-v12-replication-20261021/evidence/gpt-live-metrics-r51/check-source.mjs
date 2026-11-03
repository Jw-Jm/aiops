import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r51',origin='https://192.168.0.63:30471',mode=process.env.MODE;
assert(['outage','recovery','storage-recovery'].includes(mode));
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json')),saved=JSON.parse(readFileSync(work+'/browser-metrics.json')).queries[0],cluster=JSON.parse(readFileSync(work+'/source-start.json')).clusterUid;
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});let out={mode};
try{
 const page=await browser.newPage({ignoreHTTPSErrors:true});await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 out=await page.evaluate(async({cluster,saved,mode})=>{
  const params=new URLSearchParams({clusterUid:cluster,canonicalId:saved.canonicalId});const response=await fetch('/api/v1/metrics/capabilities?'+params);const caps=await response.json();if(response.status!==200)throw Error('capability owner unavailable '+response.status);
  const session=await(await fetch('/api/v1/auth/session')).json();params.delete('canonicalId');const query=await fetch('/api/v1/metrics/query?'+params,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.data.csrf.token},body:JSON.stringify({canonicalId:saved.canonicalId,intent:saved.intent})});const body=await query.json();
  return {mode,sourceCapabilities:caps.data.sourceCapabilities,queryStatus:query.status,result:body.data,code:body.code||body.error?.code};
 },{cluster,saved,mode});
 if(mode==='outage'){assert.equal(out.sourceCapabilities.length,0);assert.equal(out.queryStatus,503);assert(!out.result)}
 else{assert(out.sourceCapabilities.some(c=>c.datasetId===saved.intent.datasetId&&c.operation==='trend'&&c.proofDigest===saved.result.sourceManifest.qualificationDigest));assert.equal(out.queryStatus,200);assert(out.result.rows.length>0);assert.equal(out.result.sourceManifest.sourceId,saved.result.sourceManifest.sourceId)}
 out.status='passed';
}catch(e){out.status='failed';out.failure=safeErrorMessage(e,[account.password]);process.exitCode=1}
finally{writeFileSync(work+'/check-'+mode+'.json',JSON.stringify(out,null,2));await browser.close();console.log(JSON.stringify({mode,status:out.status,capabilities:out.sourceCapabilities?.length,queryStatus:out.queryStatus,failure:out.failure}))}
