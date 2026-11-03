import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r49',origin='https://192.168.0.63:30471';
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json'));
const mode=process.env.MODE||'history',saved=JSON.parse(readFileSync(work+(mode==='deleted-resource'?'/live-multi-container.json':'/live-success.json'))).outcome;
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});let out={mode};
try{
 const page=await browser.newPage({ignoreHTTPSErrors:true});await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 out=await page.evaluate(async({saved,mode})=>{
  const assert=(ok,msg)=>{if(!ok)throw Error(msg)},params=new URLSearchParams({clusterUid:saved.resource.clusterUid,canonicalId:saved.resource.canonicalId}),base='/api/v1/observability/';
  if(mode==='deleted-resource'){params.delete('canonicalId');const reply=await fetch(base+'queries/'+saved.queryId+'/results?'+params);assert(reply.status===403,'deleted Pod result was not denied: '+reply.status);return {mode,status:'passed',queryId:saved.queryId,resultReadStatus:reply.status}}
  const cap=await fetch(base+'capabilities?'+params);assert(cap.status===200,'capabilities unavailable '+cap.status);const caps=(await cap.json()).data;
  const source=caps.sources.find(s=>s.source.registrationId===saved.source.registrationId);
  params.delete('canonicalId');const reply=await fetch(base+'queries/'+saved.queryId+'/results?'+params);const result=await reply.json();
  if(mode==='outage'){assert(!source,'outage kept source qualified');assert(reply.status!==200,'outage returned qualified result');return {mode,status:'passed',archiveSelectable:false,resultReadStatus:reply.status,nativeSourcePresent:caps.sources.some(s=>s.source.collectorVersion==='kubernetes-pod-logs/v1')}}
  assert(source,'archive did not recover');assert(source.source.qualificationDigest===saved.source.qualificationDigest,'renewal changed immutable source proof');assert(reply.status===200&&result.data.resultDigest===saved.result.resultDigest,'saved result changed or unavailable');
  return {mode,status:'passed',queryId:saved.queryId,resultDigest:result.data.resultDigest,qualificationDigest:source.source.qualificationDigest,currentWatermark:source.coverageTo,savedWindowEnd:saved.window.to,resultReadStatus:reply.status};
 },{saved,mode});
}catch(e){out={mode,status:'failed',failure:safeErrorMessage(e,[account.password])};process.exitCode=1}
finally{writeFileSync(work+'/check-'+mode+'.json',JSON.stringify(out,null,2));await browser.close();console.log(JSON.stringify(out))}
