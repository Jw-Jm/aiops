// Execute on mssc-direct; no credentials or browser storage enter the receipt.
import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {safeErrorMessage} from './safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026';
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8'));
const origin='https://192.168.0.63:30471';
const result={at:new Date().toISOString(),requests:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try {
 const state=root+'/private/r35-cross-state.json';
 const context=await browser.newContext({ignoreHTTPSErrors:true,...(process.env.REUSE_STATE?{storageState:state}:{})});const page=await context.newPage();
 if(!process.env.REUSE_STATE){
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');}
 if(process.env.SAVE_STATE){await context.storageState({path:state});(await import('node:fs')).chmodSync(state,0o600)}
 for(let i=0;i<Number(process.env.COUNT||1);i++){
  const query=new URLSearchParams({clusterUid:'79014400-9453-4cb4-9678-0fae47981d79',kind:'ConfigMap',name:process.env.NAME||'r35-credential-proof'});if(process.env.NAMESPACE)query.set('namespace',process.env.NAMESPACE);
  const path=process.env.EVIDENCE_ID?'/api/v1/evidence/'+process.env.EVIDENCE_ID:process.env.CANONICAL_ID?'/api/v1/resources/by-canonical-id?canonicalId='+encodeURIComponent(process.env.CANONICAL_ID):'/api/v1/resources?'+query;
  const response=await context.request.get(origin+path);const body=await response.json();
  result.requests.push({at:new Date().toISOString(),status:response.status(),requestId:response.headers()['x-request-id'],errorCode:body.error?.code||body.code,message:body.message,...(process.env.EVIDENCE_ID?{evidence:{id:body.data?.evidenceId,canonicalId:body.data?.resourceCanonicalId,sourceId:body.data?.sourceRegistrationId,sourceRevision:body.data?.sourceRevision,state:body.data?.replayState,contentDigest:body.data?.contentDigest,uid:body.data?.factSlice?.metadata?.uid,rv:body.data?.factSlice?.metadata?.resourceVersion,namespace:body.data?.factSlice?.metadata?.namespace,name:body.data?.factSlice?.metadata?.name},meta:body.meta}:{}),rows:Array.isArray(body.data)?body.data.map(r=>({canonicalId:r.canonicalId,namespace:r.namespace,name:r.name,labels:r.labels,sourceRefs:r.sourceRefs})):[]});
  if(i+1<Number(process.env.COUNT||1))await new Promise(r=>setTimeout(r,1000));
 }
 result.status='recorded';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{writeFileSync(process.env.OUTPUT,JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,statuses:result.requests.map(r=>r.status),failure:result.failure}))}
