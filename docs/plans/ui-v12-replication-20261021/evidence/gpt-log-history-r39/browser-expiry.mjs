import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r39',origin='https://192.168.0.63:30471',account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8')),fixture=JSON.parse(readFileSync(work+'/expiry-fixture.json','utf8')),snapshot=JSON.parse(readFileSync(root+'/r37/source-snapshot.json','utf8'));
const receipt={at:new Date().toISOString(),kind:fixture.kind,origin};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true}),page=await context.newPage();await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 receipt.outcome=await page.evaluate(async({fixture,snapshot})=>{
  const assert=(ok,m)=>{if(!ok)throw Error(m)},session=await(await fetch('/api/v1/auth/session')).json(),params=new URLSearchParams({clusterUid:snapshot.cluster.clusterUid}),path='/api/v1/observability/queries/'+fixture.queryId;
  const call=async(suffix,body)=>{const r=await fetch(path+suffix+'?'+params,{method:body?'POST':'GET',credentials:'same-origin',cache:'no-store',headers:body?{'Content-Type':'application/json','X-CSRF-Token':session.data.csrf.token}:undefined,body:body?JSON.stringify(body):undefined});return{status:r.status,body:await r.json(),requestId:r.headers.get('x-request-id')}};
  const state=await call('');assert(state.status===200&&state.body.data.state==='expired'&&state.body.data.resultRef===null&&state.body.data.failure.code==='RESULT_EXPIRED','expired metadata rejected: '+state.status+' '+(state.body.code||state.body.data?.state));
  const result=await call('/results');assert(result.status===410&&result.body.code==='RESULT_EXPIRED'&&!result.body.data,'expired result body disclosed');
  const cancel=await call(':cancel',{expectedRevision:state.body.data.revision,reason:'controlled expired query must not cancel'});assert(cancel.status===410&&cancel.body.code==='RESULT_EXPIRED','expired cancellation wrote state');
  return{queryId:fixture.queryId,state:{status:state.status,projection:state.body.data.state,requestId:state.requestId},result:{status:result.status,code:result.body.code,requestId:result.requestId},cancel:{status:cancel.status,code:cancel.body.code,requestId:cancel.requestId}};
 },{fixture,snapshot});receipt.status='passed';
}catch(error){receipt.status='failed';receipt.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{await browser.close();writeFileSync(work+'/browser-expiry.json',JSON.stringify(receipt,null,2));console.log(JSON.stringify(receipt))}
