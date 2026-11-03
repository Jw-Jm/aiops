import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {safeErrorMessage} from './safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r37',origin='https://192.168.0.63:30471';
const mode=process.env.MODE;if(!['success','empty','failure','cancel','permission'].includes(mode))throw Error('explicit mode required');
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8'));const snapshot=JSON.parse(readFileSync(work+'/source-snapshot.json','utf8'));
const result={mode,at:new Date().toISOString(),origin,requests:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true});const page=await context.newPage();page.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/'))result.requests.push({path,method:r.request().method(),status:r.status(),requestId:r.headers()['x-request-id']})});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 result.outcome=await page.evaluate(async({mode,snapshot})=>{
  const assert=(ok,message)=>{if(!ok)throw Error(message)};
  let csrf;
  const call=async(method,path,body)=>{const r=await fetch(path,{method,credentials:'same-origin',cache:'no-store',headers:{...(body?{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()}: {})},body:body?JSON.stringify(body):undefined});return {status:r.status,requestId:r.headers.get('x-request-id'),body:await r.json()}};
  const session=await call('GET','/api/v1/auth/session');assert(session.status===200,'session rejected');csrf=session.body.data.csrf.token;
  const cluster=snapshot.cluster.clusterUid,base='/api/v1/observability/',params=new URLSearchParams({clusterUid:cluster});
  const initial=await call('GET',base+'capabilities?'+params);assert(initial.status===200,'resource capabilities rejected '+initial.status+' '+(initial.body.code||initial.body.error?.code));
  const selected=initial.body.data.resources.find(r=>r.uid===snapshot.podUID);assert(selected,'native source Pod not in authorized resource choices');
  params.set('canonicalId',selected.canonicalId);const qualified=await call('GET',base+'capabilities?'+params);assert(qualified.status===200&&qualified.body.data.sources.length===1,'qualified source absent');params.delete('canonicalId');
  const capability=qualified.body.data.sources[0];const resource={canonicalId:selected.canonicalId,uid:selected.uid,clusterUid:selected.clusterUid,namespaceScope:selected.namespaceScope};
  const body={schemaVersion:'ui-v12-observability-query/v1',queryKind:'logs',templateId:'logs.records',templateVersion:'logs.records/v1',resource,source:capability.source,window:{from:snapshot.window.from,to:snapshot.window.to,timezone:'Asia/Shanghai',bounds:'[from,to)'},filters:mode==='empty'?[{field:'level',operator:'eq',value:'debug'}]:[],budget:qualified.body.data.budget};
  if(mode==='permission'){
   body.resource={...body.resource,namespaceScope:{kind:'namespaced',namespace:'ops-native-v12-r35-outside',namespaceUid:'ungranted-namespace'}};
   const denied=await call('POST',base+'queries?'+params,body);assert(denied.status===403,'cross-scope request was accepted '+denied.status);return {deniedStatus:denied.status,requestId:denied.requestId,sourceId:capability.source.registrationId};
  }
  const accepted=await call('POST',base+'queries?'+params,body);assert(accepted.status===202,'create rejected '+accepted.status+' '+(accepted.body.code||accepted.body.error?.code));const queryId=accepted.body.data.queryId,events=[{state:accepted.body.data.state,revision:accepted.body.data.revision,requestId:accepted.requestId}];
  let current=accepted.body.data,cancelled=false;
  for(let i=0;i<16;i++){
   const reply=await call('GET',base+'queries/'+queryId+'?'+params);assert(reply.status===200,'status unavailable '+reply.status+' '+(reply.body.code||reply.body.error?.code));current=reply.body.data;events.push({state:current.state,revision:current.revision,requestId:reply.requestId});
   if(mode==='cancel'&&current.state==='running'&&!cancelled){const cancel=await call('POST',base+'queries/'+queryId+':cancel?'+params,{expectedRevision:current.revision,reason:'r37 task-only running query cancellation'});assert(cancel.status===200&&cancel.body.data.state==='cancel_requested'&&cancel.body.data.termination===null,'cancel did not preserve requested/confirmed distinction');cancelled=true;events.push({state:'cancel_requested',requestId:cancel.requestId});}
   if(!['accepted','running','cancel_requested'].includes(current.state))break;
   await new Promise(resolve=>setTimeout(resolve,750));
  }
  const results=await call('GET',base+'queries/'+queryId+'/results?'+params);
  const out={queryId,source:capability.source,resource,window:body.window,events,finalState:current.state,resultStatus:results.status,termination:current.termination,failure:current.failure};
  if(mode==='cancel'){assert(cancelled&&current.state==='terminated'&&results.status===409&&current.resultRef===null,'cancel published a result');assert(current.termination.sourceStopped===false,'HTTP abort was falsely called source termination');}
  else if(mode==='failure'){assert(current.state==='failed'&&results.status===409&&current.resultRef===null,'source outage published a result');}
  else{assert(current.state==='completed'&&results.status===200,'real query did not complete');const data=results.body.data;assert(data.resource.uid===snapshot.podUID&&data.source.registrationId===capability.source.registrationId&&data.coverage.complete===false,'result lost fixed identity/coverage');
   out.result={resultId:data.resultId,resultDigest:data.resultDigest,status:data.status,population:data.population,sampleCount:data.samples.returned,truncation:data.truncation,coverage:data.coverage,reauthorization:data.reauthorization};
   if(mode==='empty')assert(data.status==='no_data'&&data.population.count===0&&data.samples.returned===0,'empty sample was substituted for measured zero');else assert(data.population.count===snapshot.records&&data.samples.returned===20&&data.status==='partial','measured population and bounded samples differ');
  }
  return out;
 },{mode,snapshot});result.status='passed';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{writeFileSync(work+'/live-'+mode+'.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,mode,queryId:result.outcome?.queryId,finalState:result.outcome?.finalState,result:result.outcome?.result,failure:result.failure}))}
